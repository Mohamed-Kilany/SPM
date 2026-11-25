//go:build integration

// Package integration contains end-to-end integration tests for the SPM
// platform. Tests in this package require a fully running stack (PostgreSQL,
// NATS, Gitea, Argo CD, and the API backend). Run with:
//
//	go test -tags integration ./tests/integration/... -v
//
// Required env vars:
//   SPM_BASE_URL          — API base URL (default: http://localhost:8080)
//   DATABASE_URL          — PostgreSQL DSN (skip tests if absent)
//   NATS_URL              — NATS URL       (skip tests if absent)
//   GITEA_WEBHOOK_SECRET  — shared HMAC secret for Gitea webhooks
//   ARGOCD_WEBHOOK_TOKEN  — Bearer token for Argo CD webhooks
//   TEST_ADMIN_USER       — admin username  (default: admin)
//   TEST_ADMIN_PASS       — admin password  (default: Admin@1234)

package integration

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func baseURL() string {
	if v := os.Getenv("SPM_BASE_URL"); v != "" {
		return v
	}
	return "http://localhost:8080"
}

func skipIfNoStack(t *testing.T) {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("NATS_URL") == "" {
		t.Skip("DATABASE_URL and NATS_URL must be set to run integration tests")
	}
}

// newTestClient returns an http.Client with a 15-second timeout.
func newTestClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second}
}

// authToken calls POST /api/v1/auth/login and returns the JWT string.
func authToken(t *testing.T, username, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	resp, err := http.Post(
		baseURL()+"/api/v1/auth/login",
		"application/json",
		bytes.NewReader(body),
	)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "login should succeed")

	var result struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.NotEmpty(t, result.Token)
	return result.Token
}

// invalidationWait covers the server's second-precision token invalidation
// boundary. A membership or ownership change records the invalidation second
// plus one, so a token issued in the same wall-clock second as the change is
// rejected. Wait for the next second before signing in again.
const invalidationWait = 1100 * time.Millisecond

// waitPastInvalidation waits until a fresh sign-in succeeds after an action
// invalidated the user's tokens.
func waitPastInvalidation() {
	time.Sleep(invalidationWait)
}

// do performs an authenticated HTTP request and returns the response.
func do(t *testing.T, method, path, token string, body any) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, baseURL()+path, bodyReader)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := newTestClient().Do(req)
	require.NoError(t, err)
	return resp
}

// computeHMACSHA256 computes the X-Hub-Signature-256 value for a body+secret pair.
func computeHMACSHA256(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// waitFor polls fn every 100ms until it returns true or the deadline is exceeded.
func waitFor(t *testing.T, timeout time.Duration, fn func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// adminCreds returns the test admin credentials from env, falling back to defaults.
func adminCreds() (string, string) {
	user := os.Getenv("TEST_ADMIN_USER")
	pass := os.Getenv("TEST_ADMIN_PASS")
	if user == "" {
		user = "admin"
	}
	if pass == "" {
		pass = "Admin@1234"
	}
	return user, pass
}

// createProject creates a project via the REST API and returns its UUID string.
func createProject(t *testing.T, token, name string) string {
	t.Helper()
	resp := do(t, "POST", "/api/v1/projects", token, map[string]string{"name": name})
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var result struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.NotEmpty(t, result.ID)
	return result.ID
}

// readSSEEvents opens an SSE connection and returns a channel of raw data lines.
// The connection is closed when ctx is cancelled.
func readSSEEvents(ctx context.Context, t *testing.T, projectID, token string) <-chan string {
	t.Helper()
	ch := make(chan string, 32)
	go func() {
		defer close(ch)
		req, err := http.NewRequestWithContext(ctx, "GET",
			baseURL()+"/api/v1/projects/"+projectID+"/stream", nil)
		if err != nil {
			return
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "text/event-stream")

		resp, err := (&http.Client{Timeout: 0}).Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			select {
			case ch <- line:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

// giteaWebhookSecret returns the configured HMAC secret or a test default.
func giteaWebhookSecret() string {
	if v := os.Getenv("GITEA_WEBHOOK_SECRET"); v != "" {
		return v
	}
	return "test-webhook-secret"
}

// argoCDWebhookToken returns the configured Bearer token or a test default.
func argoCDWebhookToken() string {
	if v := os.Getenv("ARGOCD_WEBHOOK_TOKEN"); v != "" {
		return v
	}
	return "test-argocd-token"
}

// postJSON posts raw JSON bytes to path with the given headers and returns the response.
func postRaw(t *testing.T, path string, body []byte, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", baseURL()+path, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := newTestClient().Do(req)
	require.NoError(t, err)
	return resp
}

// ignoredBody discards the response body.
func ignoredBody(resp *http.Response) {
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// minimalPushPayload returns a minimal Gitea push webhook payload for the given branch.
func minimalPushPayload(branch string) []byte {
	payload, _ := json.Marshal(map[string]any{
		"ref":        "refs/heads/" + branch,
		"repository": map[string]string{"name": "test-repo"},
		"head_commit": map[string]any{
			"id":      "abc1234567890",
			"message": "test commit",
			"author":  map[string]string{"name": "dev"},
		},
		"commits": []map[string]string{{"id": "abc1234567890"}},
	})
	return payload
}

// featureBranchForUUID returns a valid feature branch name for a UUID-like string.
func featureBranchForUUID(id string) string {
	return fmt.Sprintf("feature/%s", id)
}
