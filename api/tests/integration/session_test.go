//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// sessionLogin is the subset of the login/refresh response the session tests
// need.
type sessionLogin struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	SessionID    string `json:"session_id"`
	ExpiresIn    int    `json:"expires_in"`
}

// loginSession signs in and requires the server to return a persistent session.
func loginSession(t *testing.T, username, password string) sessionLogin {
	t.Helper()
	resp := do(t, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": username,
		"password": password,
	})
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result sessionLogin
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.NotEmpty(t, result.Token)
	require.NotEmpty(t, result.RefreshToken, "login must return a refresh token")
	require.NotEmpty(t, result.SessionID, "login must return a session ID")
	require.Positive(t, result.ExpiresIn)
	return result
}

func refreshSession(t *testing.T, refreshToken string) sessionLogin {
	t.Helper()
	resp := do(t, http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{
		"refresh_token": refreshToken,
	})
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result sessionLogin
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.NotEmpty(t, result.Token)
	require.NotEmpty(t, result.RefreshToken)
	require.NotEmpty(t, result.SessionID)
	return result
}

func TestSessionListAndRevocationBlockRequests(t *testing.T) {
	skipIfNoStack(t)
	username, password := adminCreds()

	session := loginSession(t, username, password)

	resp := do(t, http.MethodGet, "/api/v1/auth/sessions", session.Token, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var list struct {
		Sessions []struct {
			ID       string `json:"id"`
			Device   string `json:"device"`
			IP       string `json:"ip"`
			LastSeen string `json:"last_seen_at"`
			Current  bool   `json:"current"`
		} `json:"sessions"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))
	resp.Body.Close()

	found := false
	for _, item := range list.Sessions {
		if item.ID == session.SessionID {
			found = true
			require.True(t, item.Current)
			require.NotEmpty(t, item.Device)
			require.NotEmpty(t, item.LastSeen)
		}
	}
	require.True(t, found, "the current session must appear in the list")

	resp = do(t, http.MethodDelete, "/api/v1/auth/sessions/"+session.SessionID, session.Token, nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	ignoredBody(resp)

	resp = do(t, http.MethodGet, "/api/v1/auth/me", session.Token, nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	var failure struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&failure))
	resp.Body.Close()
	require.Equal(t, "SESSION_REVOKED", failure.Error.Code)
}

func TestRefreshRotationDetectsReuse(t *testing.T) {
	skipIfNoStack(t)
	username, password := adminCreds()

	first := loginSession(t, username, password)
	second := refreshSession(t, first.RefreshToken)

	require.Equal(t, first.SessionID, second.SessionID, "rotation keeps the session family")
	require.NotEqual(t, first.RefreshToken, second.RefreshToken, "refresh tokens rotate")
	require.NotEqual(t, first.Token, second.Token, "access tokens are reissued")

	// The new access token works.
	resp := do(t, http.MethodGet, "/api/v1/auth/me", second.Token, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	ignoredBody(resp)

	// Presenting the already rotated token is treated as theft and revokes
	// the family.
	resp = do(t, http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{
		"refresh_token": first.RefreshToken,
	})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	var reuse struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&reuse))
	resp.Body.Close()
	require.Equal(t, "REFRESH_TOKEN_REUSE", reuse.Error.Code)

	// The whole family is revoked, including the access token issued before
	// the reuse attempt.
	resp = do(t, http.MethodGet, "/api/v1/auth/me", second.Token, nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	ignoredBody(resp)
}

func TestRevokeOtherSessionsKeepsCurrent(t *testing.T) {
	skipIfNoStack(t)
	username, password := adminCreds()

	older := loginSession(t, username, password)
	current := loginSession(t, username, password)

	resp := do(t, http.MethodPost, "/api/v1/auth/sessions/revoke-others", current.Token, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result struct {
		Revoked int `json:"revoked"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	resp.Body.Close()
	require.Positive(t, result.Revoked)

	resp = do(t, http.MethodGet, "/api/v1/auth/me", current.Token, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	ignoredBody(resp)

	resp = do(t, http.MethodGet, "/api/v1/auth/me", older.Token, nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	ignoredBody(resp)
}

func TestLogoutRevokesCurrentSession(t *testing.T) {
	skipIfNoStack(t)
	username, password := adminCreds()

	session := loginSession(t, username, password)

	resp := do(t, http.MethodPost, "/api/v1/auth/logout", session.Token, nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	ignoredBody(resp)

	resp = do(t, http.MethodGet, "/api/v1/auth/me", session.Token, nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	ignoredBody(resp)
}
