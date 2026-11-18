package gitengine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capturedRequest holds what the fake Gitea server received.
type capturedRequest struct {
	method string
	path   string
	body   map[string]interface{}
}

// newFakeGitea builds an httptest.Server that returns the given status code
// for every request and records the most recent request.
func newFakeGitea(statusCode int, responseBody string) (*httptest.Server, *capturedRequest) {
	captured := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path

		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			captured.body = body
		}

		w.WriteHeader(statusCode)
		if responseBody != "" {
			w.Write([]byte(responseBody))
		}
	}))
	return srv, captured
}

// ---------- slugify ----------

func TestSlugify(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"My Project", "my-project"},
		{"hello world", "hello-world"},
		{"SPM  Platform!", "spm--platform"},
		{"already-slug", "already-slug"},
		{"Special @#$ Chars", "special--chars"},
		{"UPPER", "upper"},
		{"123 numbers", "123-numbers"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, slugify(tt.input))
		})
	}
}

// ---------- New / NewWithConfig ----------

func TestNew_DefaultOrg(t *testing.T) {
	t.Setenv("GITEA_ORG", "")
	t.Setenv("GITEA_BASE_URL", "http://localhost:3000")
	t.Setenv("GITEA_API_TOKEN", "tok")

	c := New()
	assert.Equal(t, "spm", c.org)
	assert.Equal(t, "http://localhost:3000", c.baseURL)
	assert.Equal(t, "tok", c.token)
}

func TestNew_CustomOrg(t *testing.T) {
	t.Setenv("GITEA_ORG", "myorg")
	t.Setenv("GITEA_BASE_URL", "http://gitea.local")
	t.Setenv("GITEA_API_TOKEN", "secret")

	c := New()
	assert.Equal(t, "myorg", c.org)
}

func TestNewWithConfig_TrimsTrailingSlash(t *testing.T) {
	c := NewWithConfig("http://localhost:3000/", "tok", "spm")
	assert.Equal(t, "http://localhost:3000", c.baseURL)
}

// ---------- ProvisionRepo ----------

func TestProvisionRepo_Success201(t *testing.T) {
	srv, captured := newFakeGitea(http.StatusCreated, `{}`)
	defer srv.Close()

	c := NewWithConfig(srv.URL, "tok", "testorg")
	err := c.ProvisionRepo(context.Background(), "proj-uuid-1", "My Project")
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, captured.method)
	assert.Equal(t, "/api/v1/orgs/testorg/repos", captured.path)
	assert.Equal(t, "my-project", captured.body["name"])
	assert.Equal(t, "Repository for project proj-uuid-1", captured.body["description"])
	assert.Equal(t, false, captured.body["private"])
	assert.Equal(t, true, captured.body["auto_init"])
}

func TestProvisionRepo_Idempotent409(t *testing.T) {
	srv, _ := newFakeGitea(http.StatusConflict, `{"message":"repo already exists"}`)
	defer srv.Close()

	c := NewWithConfig(srv.URL, "tok", "testorg")
	err := c.ProvisionRepo(context.Background(), "proj-uuid-2", "Existing Repo")
	require.NoError(t, err, "409 should be treated as idempotent success")
}

func TestProvisionRepo_ErrorOnOtherStatus(t *testing.T) {
	srv, _ := newFakeGitea(http.StatusUnauthorized, `{"message":"bad token"}`)
	defer srv.Close()

	c := NewWithConfig(srv.URL, "tok", "testorg")
	err := c.ProvisionRepo(context.Background(), "proj-uuid-3", "Some Repo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 401")
}

func TestProvisionRepo_SetsAuthHeader(t *testing.T) {
	var receivedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := NewWithConfig(srv.URL, "mytoken", "org")
	require.NoError(t, c.ProvisionRepo(context.Background(), "id", "name"))
	assert.Equal(t, "token mytoken", receivedAuth)
}

func TestProvisionRepo_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// deliberately hang — context should fire first
		select {}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	c := NewWithConfig(srv.URL, "tok", "org")
	err := c.ProvisionRepo(ctx, "id", "name")
	require.Error(t, err)
}

// ---------- CreateBranch ----------

func TestCreateBranch_Success201(t *testing.T) {
	srv, captured := newFakeGitea(http.StatusCreated, `{}`)
	defer srv.Close()

	c := NewWithConfig(srv.URL, "tok", "myorg")
	err := c.CreateBranch(context.Background(), "my-repo", "feature/abc-123", "main")
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, captured.method)
	assert.Equal(t, "/api/v1/repos/myorg/my-repo/branches", captured.path)
	assert.Equal(t, "feature/abc-123", captured.body["new_branch_name"])
	assert.Equal(t, "main", captured.body["old_branch_name"])
}

func TestCreateBranch_404ReturnsErrSourceBranchNotFound(t *testing.T) {
	srv, _ := newFakeGitea(http.StatusNotFound, `{"message":"source branch not found"}`)
	defer srv.Close()

	c := NewWithConfig(srv.URL, "tok", "org")
	err := c.CreateBranch(context.Background(), "repo", "feature/x", "nonexistent")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSourceBranchNotFound),
		"expected ErrSourceBranchNotFound, got: %v", err)
}

func TestCreateBranch_409ReturnsErrBranchExists(t *testing.T) {
	srv, _ := newFakeGitea(http.StatusConflict, `{"message":"branch already exists"}`)
	defer srv.Close()

	c := NewWithConfig(srv.URL, "tok", "org")
	err := c.CreateBranch(context.Background(), "repo", "feature/x", "main")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBranchExists),
		"expected ErrBranchExists for 409, got: %v", err)
}

func TestCreateBranch_422ReturnsErrBranchExists(t *testing.T) {
	srv, _ := newFakeGitea(http.StatusUnprocessableEntity, `{"message":"branch name already exists"}`)
	defer srv.Close()

	c := NewWithConfig(srv.URL, "tok", "org")
	err := c.CreateBranch(context.Background(), "repo", "feature/x", "main")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBranchExists),
		"expected ErrBranchExists for 422, got: %v", err)
}

func TestCreateBranch_ErrorOnOtherStatus(t *testing.T) {
	srv, _ := newFakeGitea(http.StatusInternalServerError, `{"message":"server error"}`)
	defer srv.Close()

	c := NewWithConfig(srv.URL, "tok", "org")
	err := c.CreateBranch(context.Background(), "repo", "feature/x", "main")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 500")
}

func TestCreateBranch_SetsAuthHeader(t *testing.T) {
	var receivedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := NewWithConfig(srv.URL, "branchtoken", "org")
	require.NoError(t, c.CreateBranch(context.Background(), "repo", "feature/x", "main"))
	assert.Equal(t, "token branchtoken", receivedAuth)
}

func TestCreateBranch_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := NewWithConfig(srv.URL, "tok", "org")
	err := c.CreateBranch(ctx, "repo", "feature/x", "main")
	require.Error(t, err)
}

// ---------- Pull requests and merges ----------

func TestCreatePullRequest_PostsTraceablePayload(t *testing.T) {
	srv, captured := newFakeGitea(http.StatusCreated, `{"number":42,"title":"Retry checkout","state":"open","html_url":"http://gitea/spm/repo/pulls/42"}`)
	defer srv.Close()

	c := NewWithConfig(srv.URL, "tok", "myorg")
	result, err := c.CreatePullRequest(context.Background(), "my-repo", "Retry checkout", "feature/spm-1", "main", "Fixes SPM-1")
	require.NoError(t, err)
	assert.Equal(t, 42, result.Number)
	assert.Equal(t, "http://gitea/spm/repo/pulls/42", result.HTMLURL)
	assert.Equal(t, http.MethodPost, captured.method)
	assert.Equal(t, "/api/v1/repos/myorg/my-repo/pulls", captured.path)
	assert.Equal(t, "feature/spm-1", captured.body["head"])
	assert.Equal(t, "main", captured.body["base"])
	assert.Equal(t, "Retry checkout", captured.body["title"])
}

func TestMergePullRequest_UsesMergeEndpoint(t *testing.T) {
	srv, captured := newFakeGitea(http.StatusOK, `{}`)
	defer srv.Close()

	c := NewWithConfig(srv.URL, "tok", "myorg")
	require.NoError(t, c.MergePullRequest(context.Background(), "my-repo", 42))
	assert.Equal(t, http.MethodPost, captured.method)
	assert.Equal(t, "/api/v1/repos/myorg/my-repo/pulls/42/merge", captured.path)
}

func TestMergePullRequest_ReportsFailure(t *testing.T) {
	srv, _ := newFakeGitea(http.StatusUnprocessableEntity, `{"message":"merge conflict"}`)
	defer srv.Close()

	c := NewWithConfig(srv.URL, "tok", "myorg")
	err := c.MergePullRequest(context.Background(), "my-repo", 42)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 422")
}

func TestMergePullRequest_ReportsConflict(t *testing.T) {
	srv, _ := newFakeGitea(http.StatusConflict, `{"message":"merge conflict"}`)
	defer srv.Close()

	c := NewWithConfig(srv.URL, "tok", "myorg")
	err := c.MergePullRequest(context.Background(), "my-repo", 42)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 409")
}

func TestGetRepositoryBranch_ReturnsHeadSHA(t *testing.T) {
	srv, captured := newFakeGitea(http.StatusOK, `{"name":"feature/spm-1","commit":{"id":"abc123def456"}}`)
	defer srv.Close()

	c := NewWithConfig(srv.URL, "tok", "myorg")
	sha, err := c.GetRepositoryBranch(context.Background(), "my-repo", "feature/spm-1")
	require.NoError(t, err)
	assert.Equal(t, "abc123def456", sha)
	assert.Equal(t, http.MethodGet, captured.method)
	assert.Equal(t, "/api/v1/repos/myorg/my-repo/branches/feature/spm-1", captured.path)
}

// ---------- interface compliance ----------

func TestClientImplementsInterfaces(t *testing.T) {
	c := NewWithConfig("http://localhost", "tok", "org")
	var _ Provisioner = c
	var _ BranchCreator = c
}
