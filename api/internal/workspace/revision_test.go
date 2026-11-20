package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	dto "github.com/prometheus/client_model/go"

	"spm-platform/api/internal/metrics"
)

func revisionTestContext(t *testing.T, target string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, target, nil)
	return c, recorder
}

func TestRevisionFromRequestParsesTokens(t *testing.T) {
	body := func(value int64) *int64 { return &value }
	tests := []struct {
		name       string
		header     string
		target     string
		body       *int64
		want       int64
		wantAny    bool
		wantStatus int
		wantCode   string
	}{
		{
			name:       "missing token is precondition required",
			wantStatus: http.StatusPreconditionRequired,
			wantCode:   "REVISION_REQUIRED",
		},
		{
			name:   "quoted If-Match",
			header: `"7"`,
			want:   7,
		},
		{
			name:   "bare If-Match",
			header: "7",
			want:   7,
		},
		{
			name:   "weak If-Match",
			header: `W/"9"`,
			want:   9,
		},
		{
			name:    "wildcard If-Match",
			header:  "*",
			wantAny: true,
		},
		{
			name:       "multiple If-Match values are rejected",
			header:     `"1", "2"`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_REVISION",
		},
		{
			name:       "zero If-Match is rejected",
			header:     `"0"`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_REVISION",
		},
		{
			name:       "non-numeric If-Match is rejected",
			header:     "latest",
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_REVISION",
		},
		{
			name:   "header takes precedence over query and body",
			header: `"3"`,
			target: "/?expected_version=4",
			body:   body(5),
			want:   3,
		},
		{
			name:   "query is used when the header is absent",
			target: "/?expected_version=4",
			body:   body(5),
			want:   4,
		},
		{
			name:       "invalid query is rejected",
			target:     "/?expected_version=0",
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_REVISION",
		},
		{
			name: "body is used when the header and query are absent",
			body: body(6),
			want: 6,
		},
		{
			name:       "invalid body value is rejected",
			body:       body(0),
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_REVISION",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := tt.target
			if target == "" {
				target = "/"
			}
			c, recorder := revisionTestContext(t, target)
			if tt.header != "" {
				c.Request.Header.Set("If-Match", tt.header)
			}

			precondition, ok := revisionFromRequest(c, tt.body)
			if tt.wantStatus != 0 {
				if ok {
					t.Fatal("revisionFromRequest accepted an invalid token")
				}
				if recorder.Code != tt.wantStatus {
					t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
				}
				var response errorResponse
				if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
					t.Fatalf("decode error body: %v", err)
				}
				if response.Error.Code != tt.wantCode {
					t.Fatalf("error code = %q, want %q", response.Error.Code, tt.wantCode)
				}
				return
			}

			if !ok {
				t.Fatalf("revisionFromRequest rejected a valid token: %s", recorder.Body.String())
			}
			if precondition.Expected != tt.want {
				t.Errorf("expected revision = %d, want %d", precondition.Expected, tt.want)
			}
			if precondition.Any != tt.wantAny {
				t.Errorf("wildcard = %v, want %v", precondition.Any, tt.wantAny)
			}
			if recorder.Code != http.StatusOK {
				t.Errorf("parsing a valid token wrote %d, want no response", recorder.Code)
			}
		})
	}
}

func TestRevisionClauseUsesOneArgument(t *testing.T) {
	for _, argument := range []int{3, 9, 22} {
		want := " AND ($" + strconv.Itoa(argument) + "::bigint = 0 OR revision = $" + strconv.Itoa(argument) + "::bigint)"
		if got := revisionClause(argument); got != want {
			t.Errorf("revisionClause(%d) = %q, want %q", argument, got, want)
		}
	}
}

func TestRevisionValueUsesZeroForWildcard(t *testing.T) {
	if got := revisionValue(revisionPrecondition{Expected: 12}); got != 12 {
		t.Fatalf("revisionValue(expected) = %d, want 12", got)
	}
	if got := revisionValue(revisionPrecondition{Any: true}); got != 0 {
		t.Fatalf("revisionValue(wildcard) = %d, want 0 sentinel", got)
	}
}

func TestClassifyRevisionFailure(t *testing.T) {
	tests := []struct {
		name        string
		lookup      func() (int64, error)
		wantKind    revisionFailureKind
		wantCurrent int64
	}{
		{
			name:        "row still exists means conflict",
			lookup:      func() (int64, error) { return 8, nil },
			wantKind:    revisionFailureConflict,
			wantCurrent: 8,
		},
		{
			name:     "deleted row means not found",
			lookup:   func() (int64, error) { return 0, pgx.ErrNoRows },
			wantKind: revisionFailureNotFound,
		},
		{
			name:     "wrapped not found still means deleted",
			lookup:   func() (int64, error) { return 0, errors.Join(pgx.ErrNoRows, errors.New("wrapped")) },
			wantKind: revisionFailureNotFound,
		},
		{
			name:     "lookup error is an internal failure",
			lookup:   func() (int64, error) { return 0, errors.New("connection reset") },
			wantKind: revisionFailureLookup,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, current := classifyRevisionFailure(tt.lookup)
			if kind != tt.wantKind || current != tt.wantCurrent {
				t.Fatalf("classifyRevisionFailure() = (%d, %d), want (%d, %d)",
					kind, current, tt.wantKind, tt.wantCurrent)
			}
		})
	}
}

func TestWriteRevisionConflictSetsContract(t *testing.T) {
	registry := metrics.New(metrics.BuildInfo{})
	previous := metrics.Default()
	metrics.SetDefault(registry)
	t.Cleanup(func() { metrics.SetDefault(previous) })

	c, recorder := revisionTestContext(t, "/")
	writeRevisionConflict(c, "PRD", 12)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", recorder.Code)
	}
	if got := recorder.Header().Get("ETag"); got != `"12"` {
		t.Fatalf("ETag = %q, want weak-validator-free revision 12", got)
	}
	var body revisionConflictResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode conflict body: %v", err)
	}
	if body.Error.Code != "REVISION_CONFLICT" {
		t.Errorf("error code = %q, want REVISION_CONFLICT", body.Error.Code)
	}
	if body.CurrentVersion != 12 {
		t.Errorf("current_version = %d, want 12", body.CurrentVersion)
	}
	if !strings.Contains(body.Error.Message, "PRD") {
		t.Errorf("message = %q, want the resource name", body.Error.Message)
	}

	conflicts := metricFamily(t, registry, "spm_planning_conflicts_total")
	if len(conflicts.GetMetric()) != 1 {
		t.Fatalf("conflict series = %d, want 1", len(conflicts.GetMetric()))
	}
	if got := labelValue(conflicts.GetMetric()[0], "resource"); got != "prd" {
		t.Errorf("resource label = %q, want lowercase prd", got)
	}
	if got := conflicts.GetMetric()[0].GetCounter().GetValue(); got != 1 {
		t.Errorf("conflict counter = %v, want 1", got)
	}
}

func metricFamily(t *testing.T, registry *metrics.Registry, name string) *dto.MetricFamily {
	t.Helper()
	families, err := registry.Gatherer().Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, candidate := range families {
		if candidate.GetName() == name {
			return candidate
		}
	}
	t.Fatalf("metric family %q not found", name)
	return nil
}

func labelValue(metric *dto.Metric, name string) string {
	for _, label := range metric.GetLabel() {
		if label.GetName() == name {
			return label.GetValue()
		}
	}
	return ""
}

func TestCurrentRevisionRejectsUnknownTable(t *testing.T) {
	handler := &Handler{}
	if _, err := handler.currentRevision(context.Background(), "projects", uuid.New(), uuid.New()); err == nil {
		t.Fatal("currentRevision accepted a table outside the allowlist")
	} else if !strings.Contains(err.Error(), "not a revisioned table") {
		t.Fatalf("error = %q, want the allowlist failure", err)
	}
}
