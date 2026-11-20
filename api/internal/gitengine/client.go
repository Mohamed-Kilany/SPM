package gitengine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"spm-platform/api/internal/metrics"
)

// Client is a Gitea HTTP API client that implements Provisioner and BranchCreator.
type Client struct {
	baseURL    string
	token      string
	org        string
	httpClient *http.Client
}

// New creates a Client reading GITEA_BASE_URL, GITEA_API_TOKEN, and GITEA_ORG
// from environment variables. GITEA_ORG defaults to "spm" if not set.
func New() *Client {
	baseURL := os.Getenv("GITEA_BASE_URL")
	token := os.Getenv("GITEA_API_TOKEN")
	org := os.Getenv("GITEA_ORG")
	if org == "" {
		org = "spm"
	}
	return NewWithConfig(baseURL, token, org)
}

// NewWithConfig creates a Client with explicit configuration (useful in tests).
func NewWithConfig(baseURL, token, org string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		org:     org,
		httpClient: &http.Client{
			Timeout:   5 * time.Second,
			Transport: metrics.InstrumentTransport("gitea", nil),
		},
	}
}

// slugify converts a human-readable name into a URL-safe repo slug:
// lowercase, spaces replaced with hyphens, non-alphanumeric/hyphen chars removed.
var nonSlugRe = regexp.MustCompile(`[^a-z0-9-]`)

func slugify(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "-")
	s = nonSlugRe.ReplaceAllString(s, "")
	return s
}

// ProvisionRepo creates a Git repository in the Gitea org for the given project.
// The repository is named after a slugified version of name.
// The call enforces a 5-second timeout via the embedded http.Client.
// Returns nil if the repository already exists (idempotent).
func (c *Client) ProvisionRepo(ctx context.Context, projectID, name string) error {
	type createRepoRequest struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Private     bool   `json:"private"`
		AutoInit    bool   `json:"auto_init"`
	}

	payload := createRepoRequest{
		Name:        slugify(name),
		Description: fmt.Sprintf("Repository for project %s", projectID),
		Private:     false,
		AutoInit:    true,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("gitengine: marshal provision request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/orgs/%s/repos", c.baseURL, c.org)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("gitengine: build provision request: %w", err)
	}
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("gitengine: provision repo %q: %w", payload.Name, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusCreated:
		return nil
	case http.StatusConflict: // 409 — repo already exists, treat as success
		return nil
	default:
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("gitengine: provision repo %q: unexpected status %d: %s",
			payload.Name, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
}

// EnsureRepoWebhook registers a Gitea webhook that posts push, pull request
// and workflow events to SPM. Duplicate registration is treated as success.
func (c *Client) EnsureRepoWebhook(ctx context.Context, repoName, webhookURL, secret string) error {
	if webhookURL == "" {
		return nil
	}
	type hookConfig struct {
		URL         string `json:"url"`
		ContentType string `json:"content_type"`
		Secret      string `json:"secret"`
	}
	type hookRequest struct {
		Type   string     `json:"type"`
		Active bool       `json:"active"`
		Events []string   `json:"events"`
		Config hookConfig `json:"config"`
	}
	payload := hookRequest{
		Type: "gitea", Active: true,
		Events: []string{"push", "pull_request", "pull_request_review", "workflow_run"},
		Config: hookConfig{URL: webhookURL, ContentType: "json", Secret: secret},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("gitengine: marshal webhook request: %w", err)
	}
	endpoint := c.repoAPIPath(repoName, "/hooks")
	var hooks []struct {
		ID     int64  `json:"id"`
		Type   string `json:"type"`
		Config struct {
			URL string `json:"url"`
		} `json:"config"`
	}
	if err := c.getJSON(ctx, endpoint, &hooks); err != nil {
		return fmt.Errorf("gitengine: list webhooks for %q: %w", repoName, err)
	}
	var hookID int64
	for _, hook := range hooks {
		if hook.Type != "gitea" || !strings.Contains(hook.Config.URL, "/api/v1/webhooks/gitea") {
			continue
		}
		if hookID == 0 {
			hookID = hook.ID
			continue
		}
		// Remove duplicate SPM webhooks left by older provisioning runs.
		delReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.repoAPIPath(repoName, fmt.Sprintf("/hooks/%d", hook.ID)), nil)
		if err != nil {
			return fmt.Errorf("gitengine: build webhook delete request: %w", err)
		}
		delReq.Header.Set("Authorization", "token "+c.token)
		delResp, err := c.httpClient.Do(delReq)
		if err != nil {
			return fmt.Errorf("gitengine: delete duplicate webhook for %q: %w", repoName, err)
		}
		delResp.Body.Close()
		if delResp.StatusCode != http.StatusNoContent && delResp.StatusCode != http.StatusOK && delResp.StatusCode != http.StatusNotFound {
			return fmt.Errorf("gitengine: delete duplicate webhook for %q: unexpected status %d", repoName, delResp.StatusCode)
		}
	}
	method := http.MethodPost
	if hookID != 0 {
		// Migrate existing hooks to the current internal receiver URL and
		// refresh their event list instead of creating a duplicate hook.
		method = http.MethodPatch
		endpoint = c.repoAPIPath(repoName, fmt.Sprintf("/hooks/%d", hookID))
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("gitengine: build webhook request: %w", err)
	}
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("gitengine: register webhook for %q: %w", repoName, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK, http.StatusNoContent, http.StatusConflict, http.StatusUnprocessableEntity:
		return nil
	default:
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("gitengine: register webhook for %q: unexpected status %d: %s", repoName, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
}

// CreateBranch creates branchName from sourceBranch in the given repository.
// Returns ErrSourceBranchNotFound if the source branch does not exist (404).
// Returns ErrBranchExists if the branch already exists (409 or 422).
func (c *Client) CreateBranch(ctx context.Context, repoName, branchName, sourceBranch string) error {
	type createBranchRequest struct {
		NewBranchName string `json:"new_branch_name"`
		OldBranchName string `json:"old_branch_name"`
	}

	payload := createBranchRequest{
		NewBranchName: branchName,
		OldBranchName: sourceBranch,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("gitengine: marshal create-branch request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/branches", c.baseURL, c.org, repoName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("gitengine: build create-branch request: %w", err)
	}
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("gitengine: create branch %q in %q: %w", branchName, repoName, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusCreated:
		return nil
	case http.StatusNotFound: // 404 - source branch does not exist
		return fmt.Errorf("gitengine: create branch %q: source branch %q not found: %w",
			branchName, sourceBranch, ErrSourceBranchNotFound)
	case http.StatusConflict, http.StatusUnprocessableEntity: // 409 / 422 - branch already exists
		return fmt.Errorf("gitengine: create branch %q in %q: %w", branchName, repoName, ErrBranchExists)
	default:
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("gitengine: create branch %q in %q: unexpected status %d: %s",
			branchName, repoName, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
}

// PullRequestResult is the subset of a Gitea pull request returned after create.
type PullRequestResult struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	State   string `json:"state"`
	HTMLURL string `json:"html_url"`
}

// CreatePullRequest opens a pull request in Gitea and returns its number.
func (c *Client) CreatePullRequest(ctx context.Context, repoName, title, head, base, description string) (PullRequestResult, error) {
	var result PullRequestResult
	payload := map[string]interface{}{
		"title": title,
		"head":  head,
		"base":  base,
		"body":  description,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return result, fmt.Errorf("gitengine: marshal pull request: %w", err)
	}
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/pulls", c.baseURL, c.org, repoName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return result, fmt.Errorf("gitengine: build pull request: %w", err)
	}
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return result, fmt.Errorf("gitengine: create pull request in %q: %w", repoName, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return result, fmt.Errorf("gitengine: create pull request in %q: unexpected status %d: %s",
			repoName, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return result, fmt.Errorf("gitengine: decode pull request: %w", err)
	}
	return result, nil
}

// MergePullRequest merges an open pull request in Gitea.
func (c *Client) MergePullRequest(ctx context.Context, repoName string, number int) error {
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/pulls/%d/merge", c.baseURL, c.org, repoName, number)
	body, _ := json.Marshal(map[string]string{"Do": "merge", "MergeTitleField": ""})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("gitengine: build merge request: %w", err)
	}
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("gitengine: merge pull request #%d in %q: %w", number, repoName, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return nil
	default:
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("gitengine: merge pull request #%d in %q: unexpected status %d: %s",
			number, repoName, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
}

// GetRepositoryBranch returns a branch's latest commit SHA from Gitea.
func (c *Client) GetRepositoryBranch(ctx context.Context, repoName, branchName string) (string, error) {
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/branches/%s", c.baseURL, c.org, repoName, branchName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("gitengine: build branch lookup: %w", err)
	}
	req.Header.Set("Authorization", "token "+c.token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gitengine: lookup branch %q in %q: %w", branchName, repoName, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("gitengine: lookup branch %q in %q: status %d: %s",
			branchName, repoName, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var payload struct {
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("gitengine: decode branch: %w", err)
	}
	return payload.Commit.ID, nil
}

// RepositoryContentEntry describes one directory entry returned by Gitea's
// contents API. SPM renders this in its own repository browser; users never
// need to open the Gitea UI.
type RepositoryContentEntry struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Path string `json:"path"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}

// RepositoryFile is a text-safe representation of a repository file.
type RepositoryFile struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	SHA       string `json:"sha"`
	Size      int64  `json:"size"`
	Content   string `json:"content"`
	Encoding  string `json:"encoding"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
}

// RepositoryCommit is the commit summary shown in the SPM commit history.
type RepositoryCommit struct {
	SHA         string    `json:"sha"`
	Message     string    `json:"message"`
	AuthorName  string    `json:"author_name"`
	AuthorEmail string    `json:"author_email"`
	CommittedAt time.Time `json:"committed_at"`
}

// RepositoryBranch is a remote branch summary from Gitea.
type RepositoryBranch struct {
	Name          string    `json:"name"`
	CommitSHA     string    `json:"commit_sha"`
	CommitMessage string    `json:"commit_message"`
	Protected     bool      `json:"protected"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// PullRequestFile is one changed file in a pull request review.
type PullRequestFile struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Changes   int    `json:"changes"`
	Patch     string `json:"patch"`
}

func (c *Client) repoAPIPath(repoName string, suffix string) string {
	return fmt.Sprintf("%s/api/v1/repos/%s/%s%s", c.baseURL, c.org, url.PathEscape(repoName), suffix)
}

func (c *Client) getJSON(ctx context.Context, endpoint string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("gitengine: build request: %w", err)
	}
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("gitengine: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("gitengine: Gitea rejected GITEA_API_TOKEN (status %d); re-run start-stack.ps1 to provision a valid service token", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("gitengine: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("gitengine: decode response: %w", err)
	}
	return nil
}

func escapeContentPath(value string) string {
	value = strings.Trim(value, "/")
	if value == "" {
		return ""
	}
	parts := strings.Split(value, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

// ListRepositoryContents lists a directory at the given ref. The returned
// entries are sorted directories-first for a stable browser experience.
func (c *Client) ListRepositoryContents(ctx context.Context, repoName, ref, dirPath string) ([]RepositoryContentEntry, error) {
	suffix := "/contents"
	if escaped := escapeContentPath(dirPath); escaped != "" {
		suffix += "/" + escaped
	}
	if ref != "" {
		suffix += "?ref=" + url.QueryEscape(ref)
	}
	var entries []RepositoryContentEntry
	if err := c.getJSON(ctx, c.repoAPIPath(repoName, suffix), &entries); err != nil {
		return nil, err
	}
	sortRepositoryEntries(entries)
	return entries, nil
}

// GetRepositoryFile reads one file from Gitea and decodes its contents. Binary
// files are returned without content so the browser can render a safe notice.
func (c *Client) GetRepositoryFile(ctx context.Context, repoName, ref, filePath string) (RepositoryFile, error) {
	var file RepositoryFile
	suffix := "/contents/" + escapeContentPath(filePath)
	if ref != "" {
		suffix += "?ref=" + url.QueryEscape(ref)
	}
	if err := c.getJSON(ctx, c.repoAPIPath(repoName, suffix), &file); err != nil {
		return file, err
	}
	if file.Encoding == "base64" && file.Content != "" {
		raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
		if err != nil {
			return file, fmt.Errorf("gitengine: decode file %q: %w", filePath, err)
		}
		if len(raw) > 512*1024 {
			raw = raw[:512*1024]
			file.Truncated = true
		}
		if bytes.IndexByte(raw, 0) >= 0 || !utf8.Valid(raw) {
			file.Binary = true
			file.Content = ""
		} else {
			file.Content = string(raw)
		}
	}
	return file, nil
}

// ListRepositoryCommits returns the newest commits for a ref.
func (c *Client) ListRepositoryCommits(ctx context.Context, repoName, ref string, page, limit int) ([]RepositoryCommit, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 30
	}
	suffix := fmt.Sprintf("/commits?page=%d&limit=%d&stat=false", page, limit)
	if ref != "" {
		suffix += "&sha=" + url.QueryEscape(ref)
	}
	var payload []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
			Author  struct {
				Name  string    `json:"name"`
				Email string    `json:"email"`
				Date  time.Time `json:"date"`
			} `json:"author"`
		} `json:"commit"`
	}
	if err := c.getJSON(ctx, c.repoAPIPath(repoName, suffix), &payload); err != nil {
		return nil, err
	}
	commits := make([]RepositoryCommit, 0, len(payload))
	for _, entry := range payload {
		commits = append(commits, RepositoryCommit{
			SHA: entry.SHA, Message: entry.Commit.Message,
			AuthorName: entry.Commit.Author.Name, AuthorEmail: entry.Commit.Author.Email,
			CommittedAt: entry.Commit.Author.Date,
		})
	}
	return commits, nil
}

// ListRepositoryBranches returns remote branches for the SPM branch browser.
func (c *Client) ListRepositoryBranches(ctx context.Context, repoName string) ([]RepositoryBranch, error) {
	var payload []struct {
		Name      string `json:"name"`
		Protected bool   `json:"protected"`
		Commit    struct {
			ID        string    `json:"id"`
			Message   string    `json:"message"`
			Timestamp time.Time `json:"timestamp"`
		} `json:"commit"`
	}
	if err := c.getJSON(ctx, c.repoAPIPath(repoName, "/branches?limit=100"), &payload); err != nil {
		return nil, err
	}
	branches := make([]RepositoryBranch, 0, len(payload))
	for _, entry := range payload {
		branches = append(branches, RepositoryBranch{
			Name: entry.Name, CommitSHA: entry.Commit.ID, CommitMessage: entry.Commit.Message,
			Protected: entry.Protected, UpdatedAt: entry.Commit.Timestamp,
		})
	}
	return branches, nil
}

// GetPullRequestFiles returns changed files and patches for an internal review.
func (c *Client) GetPullRequestFiles(ctx context.Context, repoName string, number int) ([]PullRequestFile, error) {
	var files []PullRequestFile
	if err := c.getJSON(ctx, c.repoAPIPath(repoName, fmt.Sprintf("/pulls/%d/files", number)), &files); err != nil {
		return nil, err
	}
	return files, nil
}

// WorkflowRun is a Gitea Actions run. It is the authoritative source for CI
// status; SPM never lets a user type a build result.
type WorkflowRun struct {
	ID           int64      `json:"id"`
	RunNumber    int        `json:"run_number"`
	Name         string     `json:"name"`
	DisplayTitle string     `json:"display_title"`
	Status       string     `json:"status"`
	Conclusion   string     `json:"conclusion"`
	HeadSHA      string     `json:"head_sha"`
	HeadBranch   string     `json:"head_branch"`
	Event        string     `json:"event"`
	URL          string     `json:"url"`
	StartedAt    *time.Time `json:"run_started_at"`
	CompletedAt  *time.Time `json:"completed_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

// ListWorkflowRuns reads the Gitea Actions task API (Gitea 1.24+). The
// returned runs are newest-first and are reconciled into SPM builds. Gitea
// exposes workflow runs through /actions/tasks; /actions/runs is not a public
// API route.
func (c *Client) ListWorkflowRuns(ctx context.Context, repoName string, limit int) ([]WorkflowRun, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	var payload struct {
		WorkflowRuns []WorkflowRun `json:"workflow_runs"`
	}
	endpoint := c.repoAPIPath(repoName, fmt.Sprintf("/actions/tasks?page=1&limit=%d", limit))
	if err := c.getJSON(ctx, endpoint, &payload); err != nil {
		return nil, err
	}
	runs := payload.WorkflowRuns
	for i := 1; i < len(runs); i++ {
		for j := i; j > 0; j-- {
			left, right := runs[j-1], runs[j]
			if workflowRunTime(left).After(workflowRunTime(right)) || workflowRunTime(left).Equal(workflowRunTime(right)) {
				break
			}
			runs[j-1], runs[j] = runs[j], runs[j-1]
		}
	}
	return runs, nil
}

func workflowRunTime(run WorkflowRun) time.Time {
	if run.StartedAt != nil && !run.StartedAt.IsZero() {
		return *run.StartedAt
	}
	if run.CompletedAt != nil && !run.CompletedAt.IsZero() {
		return *run.CompletedAt
	}
	if !run.UpdatedAt.IsZero() {
		return run.UpdatedAt
	}
	return run.CreatedAt
}

func sortRepositoryEntries(entries []RepositoryContentEntry) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0; j-- {
			left, right := entries[j-1], entries[j]
			if (left.Type == "dir" && right.Type != "dir") ||
				(left.Type == right.Type && strings.ToLower(left.Name) <= strings.ToLower(right.Name)) {
				break
			}
			entries[j-1], entries[j] = entries[j], entries[j-1]
		}
	}
}
