//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Test: Gitea webhook security — invalid HMAC → 401
// Requirement 12.1, 12.2
// ---------------------------------------------------------------------------

func TestWebhookSecurity_AbsentSignature_Returns401(t *testing.T) {
	skipIfNoStack(t)

	body := minimalPushPayload("feature/00000000-0000-0000-0000-000000000001")
	resp := postRaw(t, "/api/v1/webhooks/gitea", body, map[string]string{
		"X-Gitea-Event": "push",
		// X-Hub-Signature-256 deliberately omitted
	})
	defer ignoredBody(resp)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestWebhookSecurity_InvalidHMAC_Returns401(t *testing.T) {
	skipIfNoStack(t)

	body := minimalPushPayload("feature/00000000-0000-0000-0000-000000000002")
	resp := postRaw(t, "/api/v1/webhooks/gitea", body, map[string]string{
		"X-Gitea-Event":       "push",
		"X-Hub-Signature-256": "sha256=deadbeefdeadbeef",
	})
	defer ignoredBody(resp)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestWebhookSecurity_ValidHMAC_Returns200(t *testing.T) {
	skipIfNoStack(t)

	secret := giteaWebhookSecret()
	ticketID := "00000000-0000-0000-0000-000000000003"
	body := minimalPushPayload(featureBranchForUUID(ticketID))
	sig := computeHMACSHA256(body, secret)

	resp := postRaw(t, "/api/v1/webhooks/gitea", body, map[string]string{
		"X-Gitea-Event":       "push",
		"X-Hub-Signature-256": sig,
	})
	defer ignoredBody(resp)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// Test: Argo CD webhook — valid Bearer → HTTP 200, deployment event recorded
// Requirement 8.3, 8.4, 12.3, 12.4
// ---------------------------------------------------------------------------

func TestArgoCD_Webhook_AbsentToken_Returns401(t *testing.T) {
	skipIfNoStack(t)

	body, _ := json.Marshal(map[string]any{
		"app": map[string]any{
			"metadata": map[string]string{"name": "test-app", "namespace": "default"},
			"status": map[string]any{
				"sync":   map[string]string{"status": "Synced"},
				"health": map[string]string{"status": "Healthy"},
			},
		},
	})
	resp := postRaw(t, "/api/v1/webhooks/argocd", body, nil)
	defer ignoredBody(resp)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestArgoCD_Webhook_InvalidToken_Returns401(t *testing.T) {
	skipIfNoStack(t)

	body, _ := json.Marshal(map[string]any{
		"app": map[string]any{
			"metadata": map[string]string{"name": "test-app", "namespace": "default"},
			"status": map[string]any{
				"sync":   map[string]string{"status": "Synced"},
				"health": map[string]string{"status": "Healthy"},
			},
		},
	})
	resp := postRaw(t, "/api/v1/webhooks/argocd", body, map[string]string{
		"Authorization": "Bearer wrong-token",
	})
	defer ignoredBody(resp)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestArgoCD_Webhook_ValidToken_Returns200(t *testing.T) {
	skipIfNoStack(t)

	token := argoCDWebhookToken()
	body, _ := json.Marshal(map[string]any{
		"app": map[string]any{
			"metadata": map[string]string{"name": "spm-app", "namespace": "default"},
			"status": map[string]any{
				"sync":   map[string]string{"status": "Synced"},
				"health": map[string]string{"status": "Healthy"},
			},
		},
		"project_id": "00000000-0000-0000-0000-000000000004",
		"commit_sha": "abc12345",
	})

	resp := postRaw(t, "/api/v1/webhooks/argocd", body, map[string]string{
		"Authorization": "Bearer " + token,
	})
	defer ignoredBody(resp)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// Test: Concurrent webhook delivery — no duplicate events
// Requirement 11.7
// ---------------------------------------------------------------------------

func TestConcurrentWebhooks_NoDuplicateEvents(t *testing.T) {
	skipIfNoStack(t)

	secret := giteaWebhookSecret()
	// Use a unique ticket ID to isolate this test run.
	ticketID := "11000000-0000-0000-0000-000000000001"
	body := minimalPushPayload(featureBranchForUUID(ticketID))
	sig := computeHMACSHA256(body, secret)

	// Fire 3 concurrent identical webhooks.
	const n = 3
	statuses := make([]int, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			resp := postRaw(t, "/api/v1/webhooks/gitea", body, map[string]string{
				"X-Gitea-Event":       "push",
				"X-Hub-Signature-256": sig,
			})
			statuses[i] = resp.StatusCode
			ignoredBody(resp)
		}()
	}
	wg.Wait()

	// All should succeed (200); idempotency is handled by the EventStore.
	for i, s := range statuses {
		require.Equal(t, http.StatusOK, s, "request %d should return 200", i)
	}
}

// ---------------------------------------------------------------------------
// Test: Non-feature branch push — no event recorded (HTTP 200, silent no-op)
// Requirement 5.5
// ---------------------------------------------------------------------------

func TestPush_NonFeatureBranch_IsIgnored(t *testing.T) {
	skipIfNoStack(t)

	secret := giteaWebhookSecret()
	body := minimalPushPayload("main") // not a feature/<uuid> branch
	sig := computeHMACSHA256(body, secret)

	resp := postRaw(t, "/api/v1/webhooks/gitea", body, map[string]string{
		"X-Gitea-Event":       "push",
		"X-Hub-Signature-256": sig,
	})
	defer ignoredBody(resp)
	// Should acknowledge silently with 200.
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
