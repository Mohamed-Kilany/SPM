// Package cdengine handles Continuous Delivery operations for the SPM platform.
// It is responsible for committing updated image tags to the ManifestRepo via
// the Gitea API when a pull request is merged and CI status is success.
package cdengine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"

	"spm-platform/api/internal/events"
	"spm-platform/api/internal/models"
)

// defaults for optional env vars.
const (
	defaultManifestRepo = "spm-manifests"
	defaultImageTagPath = "deploy/image-tag.txt"
	defaultOrg          = "spm"
)

// Manager handles CD-related operations including ManifestRepo commits.
type Manager struct {
	baseURL      string // GITEA_BASE_URL
	token        string // GITEA_API_TOKEN
	org          string // GITEA_ORG (default: "spm")
	manifestRepo string // MANIFEST_REPO_PATH env (default: "spm-manifests")
	imageTagPath string // MANIFEST_IMAGE_TAG_PATH env (default: "deploy/image-tag.txt")
	eventsClient *events.Client
	httpClient   *http.Client
}

// New creates a Manager reading config from env vars.
//
// Required env vars:
//   - GITEA_BASE_URL: base URL of the Gitea instance (e.g. "http://gitea:3000")
//   - GITEA_API_TOKEN: bearer token for Gitea API authentication
//
// Optional env vars (have defaults):
//   - GITEA_ORG: organisation/owner name (default "spm")
//   - MANIFEST_REPO_PATH: manifest repo name (default "spm-manifests")
//   - MANIFEST_IMAGE_TAG_PATH: path to image tag file inside the repo (default "deploy/image-tag.txt")
func New(eventsClient *events.Client) *Manager {
	baseURL := os.Getenv("GITEA_BASE_URL")
	token := os.Getenv("GITEA_API_TOKEN")

	org := os.Getenv("GITEA_ORG")
	if org == "" {
		org = defaultOrg
	}

	manifestRepo := os.Getenv("MANIFEST_REPO_PATH")
	if manifestRepo == "" {
		manifestRepo = defaultManifestRepo
	}

	imageTagPath := os.Getenv("MANIFEST_IMAGE_TAG_PATH")
	if imageTagPath == "" {
		imageTagPath = defaultImageTagPath
	}

	return &Manager{
		baseURL:      baseURL,
		token:        token,
		org:          org,
		manifestRepo: manifestRepo,
		imageTagPath: imageTagPath,
		eventsClient: eventsClient,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// giteaFileContent is the response body returned by the Gitea
// GET /api/v1/repos/{owner}/{repo}/contents/{filepath} endpoint.
type giteaFileContent struct {
	SHA string `json:"sha"` // current blob SHA — required for the update PUT
}

// giteaUpdateRequest is the request body for the Gitea
// PUT /api/v1/repos/{owner}/{repo}/contents/{filepath} endpoint.
type giteaUpdateRequest struct {
	Message string `json:"message"`
	Content string `json:"content"` // base64-encoded file content
	SHA     string `json:"sha"`     // current blob SHA
	Branch  string `json:"branch"`
}

// CommitManifest commits a new image tag to the manifest repository.
// It is called when a PR is merged and the associated CI Pipeline status is success.
//
//   - projectID  is the project UUID string (used as the NATS aggregate ID for failure events).
//   - commitSHA  is the merged commit SHA; the first 8 characters are used as the tag suffix.
//   - repoName   is the application repository name; used as the image name prefix.
//
// On success nil is returned.
// On failure a deployment.manifest_commit_failed event is published to the EventStore
// and the underlying error is returned.
func (m *Manager) CommitManifest(ctx context.Context, projectID, commitSHA, repoName string) error {
	// Build the new image tag: "<repoName>:<commitSHA[:8]>"
	shortSHA := commitSHA
	if len(shortSHA) > 8 {
		shortSHA = shortSHA[:8]
	}
	imageTag := fmt.Sprintf("%s:%s", repoName, shortSHA)

	// Step 1 — GET current file to obtain its blob SHA (required for the update).
	currentSHA, err := m.getFileSHA(ctx)
	if err != nil {
		return m.failCommit(ctx, projectID, imageTag, fmt.Errorf("cdengine: failed to get current file SHA: %w", err))
	}

	// Step 2 — PUT updated content.
	if err := m.putFileContent(ctx, imageTag, currentSHA); err != nil {
		return m.failCommit(ctx, projectID, imageTag, fmt.Errorf("cdengine: failed to update manifest file: %w", err))
	}

	return nil
}

// getFileSHA calls GET on the Gitea contents API and returns the blob SHA of
// the current image tag file. The SHA is required by the subsequent PUT.
func (m *Manager) getFileSHA(ctx context.Context) (string, error) {
	url := fmt.Sprintf(
		"%s/api/v1/repos/%s/%s/contents/%s",
		m.baseURL, m.org, m.manifestRepo, m.imageTagPath,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "token "+m.token)
	req.Header.Set("Accept", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var fc giteaFileContent
	if err := json.Unmarshal(body, &fc); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	return fc.SHA, nil
}

// putFileContent calls PUT on the Gitea contents API to commit the new image
// tag value.  imageTag is the full tag string (e.g. "my-app:abc12345").
// currentSHA is the blob SHA returned by the preceding GET.
func (m *Manager) putFileContent(ctx context.Context, imageTag, currentSHA string) error {
	url := fmt.Sprintf(
		"%s/api/v1/repos/%s/%s/contents/%s",
		m.baseURL, m.org, m.manifestRepo, m.imageTagPath,
	)

	commitMsg := fmt.Sprintf("chore: update image tag to %s [skip ci]", imageTag)
	encodedContent := base64.StdEncoding.EncodeToString([]byte(imageTag))

	payload := giteaUpdateRequest{
		Message: commitMsg,
		Content: encodedContent,
		SHA:     currentSHA,
		Branch:  "main",
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "token "+m.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http put: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	// Gitea returns 200 OK or 201 Created on success.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// failCommit publishes a deployment.manifest_commit_failed event to the
// EventStore and returns the original error so callers can propagate it.
func (m *Manager) failCommit(ctx context.Context, projectID, imageTag string, cause error) error {
	projectUUID, parseErr := uuid.Parse(projectID)
	if parseErr != nil {
		// If the UUID is unparseable we still want to return the original cause.
		return cause
	}

	payload, marshalErr := json.Marshal(map[string]string{
		"reason":    cause.Error(),
		"image_tag": imageTag,
	})
	if marshalErr != nil {
		return cause
	}

	envelope := models.EventEnvelope{
		EventType:   "deployment.manifest_commit_failed",
		AggregateID: projectUUID,
		Payload:     payload,
		CreatedAt:   time.Now().UTC(),
	}

	// Publish with no optimistic concurrency check (expectedSeq=0) — this is a
	// one-shot failure notification and sequence ordering is not critical here.
	_, _ = m.eventsClient.Publish(ctx, envelope, 0)

	return cause
}
