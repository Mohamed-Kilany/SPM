package cdengine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// ArgoApplication is the subset of an Argo CD Application that SPM reconciles
// into deployments and health evidence. Values are read directly from the
// Argo CD API; they are never supplied by an SPM user.
type ArgoApplication struct {
	Metadata struct {
		Name      string            `json:"name"`
		Namespace string            `json:"namespace"`
		Labels    map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		Project     string `json:"project"`
		Destination struct {
			Server    string `json:"server"`
			Namespace string `json:"namespace"`
			Name      string `json:"name"`
		} `json:"destination"`
		Source struct {
			RepoURL        string `json:"repoURL"`
			Path           string `json:"path"`
			TargetRevision string `json:"targetRevision"`
		} `json:"source"`
	} `json:"spec"`
	Status struct {
		Sync struct {
			Status   string `json:"status"`
			Revision string `json:"revision"`
		} `json:"sync"`
		Health struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"health"`
		OperationState struct {
			Phase      string `json:"phase"`
			Message    string `json:"message"`
			StartedAt  string `json:"startedAt"`
			FinishedAt string `json:"finishedAt"`
			SyncResult struct {
				Revision string `json:"revision"`
			} `json:"syncResult"`
		} `json:"operationState"`
		Summary struct {
			Images []string `json:"images"`
		} `json:"summary"`
	} `json:"status"`
}

// ArgoClient is a small read-only Argo CD API client used for periodic
// reconciliation. It does not trigger or mutate deployments.
type ArgoClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewArgoClient creates a read-only Argo CD client. ARGOCD_BASE_URL defaults
// to the compose service; ARGOCD_TOKEN is optional when the local server is
// configured with --disable-auth.
func NewArgoClient() *ArgoClient {
	return &ArgoClient{
		baseURL: strings.TrimRight(envOr("ARGOCD_BASE_URL", "http://k3s:30080"), "/"),
		token:   os.Getenv("ARGOCD_TOKEN"),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Enabled reports whether an Argo CD endpoint is configured. CI stacks can
// disable reconciliation with ARGOCD_ENABLED=false when no cluster is present.
func (c *ArgoClient) Enabled() bool {
	if c == nil || c.baseURL == "" {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("ARGOCD_ENABLED")), "false")
}

func (c *ArgoClient) get(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("argocd: build request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("argocd: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("argocd: API rejected credentials (status %d); set ARGOCD_TOKEN or enable local --disable-auth", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("argocd: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("argocd: decode response: %w", err)
	}
	return nil
}

// ListApplications returns every Argo CD Application visible to the service
// token. An empty list is valid when no application is registered yet.
func (c *ArgoClient) ListApplications(ctx context.Context) ([]ArgoApplication, error) {
	var payload struct {
		Items []ArgoApplication `json:"items"`
	}
	if err := c.get(ctx, "/api/v1/applications", &payload); err != nil {
		return nil, err
	}
	return payload.Items, nil
}

// Ping checks that the Argo CD API is reachable. It is used to distinguish
// "no applications" from "Argo CD is unavailable" in the delivery console.
func (c *ArgoClient) Ping(ctx context.Context) error {
	var payload map[string]interface{}
	return c.get(ctx, "/api/version", &payload)
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
