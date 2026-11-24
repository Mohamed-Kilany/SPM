//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const testUserPassword = "Passw0rd!2345"

type ownershipTransfer struct {
	ID         string `json:"id"`
	ProjectID  string `json:"project_id"`
	FromUserID string `json:"from_user_id"`
	ToUserID   string `json:"to_user_id"`
	Status     string `json:"status"`
	Reason     string `json:"reason"`
}

type projectMemberRow struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

func uniqueUsername(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// createTestUser creates an active member account and returns its ID.
func createTestUser(t *testing.T, adminToken, username, password string) string {
	t.Helper()
	resp := do(t, http.MethodPost, "/api/v1/admin/users", adminToken, map[string]string{
		"username":     username,
		"email":        username + "@example.com",
		"display_name": username,
		"role":         "member",
		"password":     password,
	})
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	require.NotEmpty(t, created.ID)
	return created.ID
}

func addProjectMember(t *testing.T, token, projectID, userID, role string) {
	t.Helper()
	resp := do(t, http.MethodPost, "/api/v1/projects/"+projectID+"/members", token, map[string]string{
		"user_id": userID,
		"role":    role,
	})
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
}

func requestOwnershipTransfer(t *testing.T, token, projectID, toUserID string) ownershipTransfer {
	t.Helper()
	resp := do(t, http.MethodPost, "/api/v1/projects/"+projectID+"/ownership-transfers", token, map[string]string{
		"to_user_id": toUserID,
		"reason":     "integration handover",
	})
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var transfer ownershipTransfer
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&transfer))
	require.NotEmpty(t, transfer.ID)
	require.Equal(t, "pending", transfer.Status)
	return transfer
}

func listProjectMembers(t *testing.T, token, projectID string) []projectMemberRow {
	t.Helper()
	resp := do(t, http.MethodGet, "/api/v1/projects/"+projectID+"/members", token, nil)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var members []projectMemberRow
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&members))
	return members
}

func memberRole(members []projectMemberRow, userID string) string {
	for _, member := range members {
		if member.UserID == userID {
			return member.Role
		}
	}
	return ""
}

func currentUserID(t *testing.T, token string) string {
	t.Helper()
	resp := do(t, http.MethodGet, "/api/v1/auth/me", token, nil)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var user struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&user))
	require.NotEmpty(t, user.ID)
	return user.ID
}

func TestOwnershipTransferRequiresTargetAcceptance(t *testing.T) {
	skipIfNoStack(t)
	adminUser, adminPass := adminCreds()
	adminToken := authToken(t, adminUser, adminPass)
	projectID := createProject(t, adminToken, "Ownership acceptance")
	username := uniqueUsername("transfer-target")
	userID := createTestUser(t, adminToken, username, testUserPassword)
	addProjectMember(t, adminToken, projectID, userID, "developer")

	// Adding a member invalidates that user's tokens; wait for the
	// second-precision boundary before signing in as the target.
	waitPastInvalidation()
	targetSession := loginSession(t, username, testUserPassword)
	transfer := requestOwnershipTransfer(t, adminToken, projectID, userID)

	// The inbox is available without project access and lists the request.
	resp := do(t, http.MethodGet, "/api/v1/auth/ownership-transfers", targetSession.Token, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var inbox struct {
		Incoming []ownershipTransfer `json:"incoming"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&inbox))
	resp.Body.Close()
	require.Len(t, inbox.Incoming, 1)
	require.Equal(t, transfer.ID, inbox.Incoming[0].ID)

	// The current owner cannot accept their own request.
	resp = do(t, http.MethodPost,
		"/api/v1/projects/"+projectID+"/ownership-transfers/"+transfer.ID+"/accept", adminToken, nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	ignoredBody(resp)

	resp = do(t, http.MethodPost,
		"/api/v1/projects/"+projectID+"/ownership-transfers/"+transfer.ID+"/accept", targetSession.Token, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var resolved ownershipTransfer
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&resolved))
	resp.Body.Close()
	require.Equal(t, "accepted", resolved.Status)

	// Accepting the transfer invalidates both parties' tokens because their
	// claims still describe the previous owner.
	resp = do(t, http.MethodGet, "/api/v1/projects/"+projectID+"/members", targetSession.Token, nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	ignoredBody(resp)

	waitPastInvalidation()
	targetToken := authToken(t, username, testUserPassword)
	adminToken = authToken(t, adminUser, adminPass)

	members := listProjectMembers(t, targetToken, projectID)
	require.Equal(t, "owner", memberRole(members, userID), "the target becomes the owner")
	adminID := currentUserID(t, adminToken)
	require.Equal(t, "maintainer", memberRole(members, adminID), "the previous owner is demoted")
}

func TestOwnershipTransferAdminCanConfirm(t *testing.T) {
	skipIfNoStack(t)
	adminUser, adminPass := adminCreds()
	adminToken := authToken(t, adminUser, adminPass)
	projectID := createProject(t, adminToken, "Ownership confirmation")
	username := uniqueUsername("confirm-target")
	userID := createTestUser(t, adminToken, username, testUserPassword)
	addProjectMember(t, adminToken, projectID, userID, "developer")

	transfer := requestOwnershipTransfer(t, adminToken, projectID, userID)

	// A project maintainer cannot confirm on the target's behalf; only a global
	// admin can. The requester is the admin here, so the positive case follows.
	resp := do(t, http.MethodPost,
		"/api/v1/projects/"+projectID+"/ownership-transfers/"+transfer.ID+"/confirm", adminToken, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var resolved ownershipTransfer
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&resolved))
	resp.Body.Close()
	require.Equal(t, "confirmed", resolved.Status)

	// Confirming invalidates the previous owner's tokens as well.
	waitPastInvalidation()
	adminToken = authToken(t, adminUser, adminPass)

	members := listProjectMembers(t, adminToken, projectID)
	require.Equal(t, "owner", memberRole(members, userID))
}

func TestOffboardingRequiresReassignmentAndRevokesAccess(t *testing.T) {
	skipIfNoStack(t)
	adminUser, adminPass := adminCreds()
	adminToken := authToken(t, adminUser, adminPass)
	adminID := currentUserID(t, adminToken)
	projectID := createProject(t, adminToken, "Offboarding reassignment")
	username := uniqueUsername("departing-owner")
	userID := createTestUser(t, adminToken, username, testUserPassword)
	addProjectMember(t, adminToken, projectID, userID, "developer")

	// Adding a member invalidates that user's tokens; wait for the
	// second-precision boundary before signing in as the target.
	waitPastInvalidation()
	targetSession := loginSession(t, username, testUserPassword)
	transfer := requestOwnershipTransfer(t, adminToken, projectID, userID)
	resp := do(t, http.MethodPost,
		"/api/v1/projects/"+projectID+"/ownership-transfers/"+transfer.ID+"/accept", targetSession.Token, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	ignoredBody(resp)

	// Accepting invalidates both parties' tokens, so the admin signs in again
	// before previewing the offboarding.
	waitPastInvalidation()
	adminToken = authToken(t, adminUser, adminPass)

	// The preview reports the project that must be reassigned.
	resp = do(t, http.MethodGet, "/api/v1/admin/users/"+userID+"/offboarding", adminToken, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var impact struct {
		UserID        string `json:"user_id"`
		IsActive      bool   `json:"is_active"`
		OwnedProjects []struct {
			ID string `json:"id"`
		} `json:"owned_projects"`
		RequiresReassignment bool `json:"requires_reassignment"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&impact))
	resp.Body.Close()
	require.True(t, impact.IsActive)
	require.True(t, impact.RequiresReassignment)
	require.Len(t, impact.OwnedProjects, 1)
	require.Equal(t, projectID, impact.OwnedProjects[0].ID)

	// Generic deactivation is blocked until work is reassigned.
	resp = do(t, http.MethodPatch, "/api/v1/admin/users/"+userID, adminToken, map[string]any{
		"is_active": false,
	})
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	var blocked struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&blocked))
	resp.Body.Close()
	require.Equal(t, "REASSIGNMENT_REQUIRED", blocked.Error.Code)

	// Offboarding reassigns the project and revokes the user's sessions.
	resp = do(t, http.MethodPost, "/api/v1/admin/users/"+userID+"/offboarding", adminToken, map[string]string{
		"reason":      "left the company",
		"reassign_to": adminID,
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result struct {
		ReassignedProjects int `json:"reassigned_projects"`
		RevokedSessions    int `json:"revoked_sessions"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	resp.Body.Close()
	require.GreaterOrEqual(t, result.ReassignedProjects, 1)
	require.GreaterOrEqual(t, result.RevokedSessions, 1)

	// The deactivated user cannot sign in or rotate the old refresh token.
	resp = do(t, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": username,
		"password": testUserPassword,
	})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	ignoredBody(resp)

	resp = do(t, http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{
		"refresh_token": targetSession.RefreshToken,
	})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	ignoredBody(resp)

	// Ownership moved back and the departing membership is gone.
	members := listProjectMembers(t, adminToken, projectID)
	require.Equal(t, "owner", memberRole(members, adminID))
	require.Empty(t, memberRole(members, userID))
}
