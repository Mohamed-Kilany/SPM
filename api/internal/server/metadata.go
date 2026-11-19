package server

import "net/http"

// AuthLevel describes the authorization contract of a route. It is used by the
// OpenAPI generator and by the contract test; it does not enforce anything by
// itself. Enforcement lives in the middleware passed to Register.
type AuthLevel string

const (
	AuthPublic       AuthLevel = "public"
	AuthBearer       AuthLevel = "bearer"
	AuthMember       AuthLevel = "member"
	AuthAdmin        AuthLevel = "admin"
	AuthProjectRead  AuthLevel = "project-read"
	AuthProjectWrite AuthLevel = "project-write"
	AuthProjectAdmin AuthLevel = "project-admin"
	AuthSSETicket    AuthLevel = "sse-ticket"
)

// Route is one method/path pair on the HTTP surface.
type Route struct {
	Method string
	Path   string
	Auth   AuthLevel
}

// Routes returns the canonical route inventory. Register and this table are
// checked against each other by routes_test.go, so a new route cannot ship
// without appearing in the contract pipeline.
func Routes() []Route {
	r := func(method, path string, auth AuthLevel) Route {
		return Route{Method: method, Path: path, Auth: auth}
	}

	return []Route{
		// Probes.
		r(http.MethodGet, "/health", AuthPublic),
		r(http.MethodGet, "/health/live", AuthPublic),
		r(http.MethodGet, "/health/ready", AuthPublic),

		// Authentication and session.
		r(http.MethodPost, "/api/v1/auth/login", AuthPublic),
		r(http.MethodGet, "/api/v1/auth/me", AuthBearer),
		r(http.MethodPatch, "/api/v1/auth/me", AuthBearer),
		r(http.MethodPost, "/api/v1/auth/password", AuthBearer),
		r(http.MethodPost, "/api/v1/auth/stream-ticket", AuthBearer),
		r(http.MethodGet, "/api/v1/users", AuthBearer),
		r(http.MethodGet, "/api/v1/search", AuthBearer),

		// Administration.
		r(http.MethodGet, "/api/v1/admin/users", AuthAdmin),
		r(http.MethodPost, "/api/v1/admin/users", AuthAdmin),
		r(http.MethodPatch, "/api/v1/admin/users/:userId", AuthAdmin),

		// Projects.
		r(http.MethodGet, "/api/v1/projects", AuthBearer),
		r(http.MethodPost, "/api/v1/projects", AuthMember),
		r(http.MethodGet, "/api/v1/projects/:id", AuthProjectRead),
		r(http.MethodPatch, "/api/v1/projects/:id", AuthProjectAdmin),
		r(http.MethodGet, "/api/v1/projects/:id/workspace", AuthProjectRead),
		r(http.MethodGet, "/api/v1/projects/:id/activity", AuthProjectRead),
		r(http.MethodGet, "/api/v1/projects/:id/traceability", AuthProjectRead),
		r(http.MethodGet, "/api/v1/projects/:id/reports", AuthProjectRead),
		r(http.MethodGet, "/api/v1/projects/:id/reports/export", AuthProjectRead),
		r(http.MethodGet, "/api/v1/projects/:id/release-readiness", AuthProjectRead),
		r(http.MethodGet, "/api/v1/projects/:id/settings", AuthProjectRead),
		r(http.MethodPut, "/api/v1/projects/:id/settings", AuthProjectAdmin),
		r(http.MethodGet, "/api/v1/projects/:id/members", AuthProjectRead),
		r(http.MethodPost, "/api/v1/projects/:id/members", AuthProjectAdmin),
		r(http.MethodPatch, "/api/v1/projects/:id/members/:userId", AuthProjectAdmin),
		r(http.MethodDelete, "/api/v1/projects/:id/members/:userId", AuthProjectAdmin),
		r(http.MethodGet, "/api/v1/projects/:id/stream", AuthSSETicket),

		// AI.
		r(http.MethodGet, "/api/v1/projects/:id/ai/search", AuthProjectRead),
		r(http.MethodPost, "/api/v1/projects/:id/ai/ask", AuthProjectRead),
		r(http.MethodGet, "/api/v1/projects/:id/ai/insights", AuthProjectRead),

		// Repository browsing and pull requests.
		r(http.MethodGet, "/api/v1/projects/:id/repositories/:repoId/tree", AuthProjectRead),
		r(http.MethodGet, "/api/v1/projects/:id/repositories/:repoId/blob", AuthProjectRead),
		r(http.MethodGet, "/api/v1/projects/:id/repositories/:repoId/commits", AuthProjectRead),
		r(http.MethodGet, "/api/v1/projects/:id/repositories/:repoId/branches", AuthProjectRead),
		r(http.MethodGet, "/api/v1/projects/:id/pull-requests/:prId/files", AuthProjectRead),
		r(http.MethodPost, "/api/v1/projects/:id/repositories", AuthProjectWrite),
		r(http.MethodPost, "/api/v1/projects/:id/repositories/:repoId/provision", AuthProjectWrite),
		r(http.MethodPost, "/api/v1/projects/:id/branches", AuthProjectWrite),
		r(http.MethodPost, "/api/v1/projects/:id/pull-requests", AuthProjectWrite),
		r(http.MethodPut, "/api/v1/projects/:id/pull-requests/:prId", AuthProjectWrite),
		r(http.MethodPost, "/api/v1/projects/:id/pull-requests/:prId/merge", AuthProjectWrite),

		// Tickets.
		r(http.MethodPost, "/api/v1/projects/:id/tickets", AuthProjectWrite),
		r(http.MethodPut, "/api/v1/projects/:id/tickets/:tid", AuthProjectWrite),
		r(http.MethodPatch, "/api/v1/projects/:id/tickets/:tid/state", AuthProjectWrite),
		r(http.MethodDelete, "/api/v1/projects/:id/tickets/:tid", AuthProjectWrite),

		// Planning.
		r(http.MethodPost, "/api/v1/projects/:id/prds", AuthProjectWrite),
		r(http.MethodPut, "/api/v1/projects/:id/prds/:prdId", AuthProjectWrite),
		r(http.MethodDelete, "/api/v1/projects/:id/prds/:prdId", AuthProjectWrite),
		r(http.MethodPost, "/api/v1/projects/:id/epics", AuthProjectWrite),
		r(http.MethodPut, "/api/v1/projects/:id/epics/:epicId", AuthProjectWrite),
		r(http.MethodDelete, "/api/v1/projects/:id/epics/:epicId", AuthProjectWrite),
		r(http.MethodPost, "/api/v1/projects/:id/features", AuthProjectWrite),
		r(http.MethodPut, "/api/v1/projects/:id/features/:featureId", AuthProjectWrite),
		r(http.MethodDelete, "/api/v1/projects/:id/features/:featureId", AuthProjectWrite),
		r(http.MethodPost, "/api/v1/projects/:id/work-items", AuthProjectWrite),
		r(http.MethodPut, "/api/v1/projects/:id/work-items/:itemId", AuthProjectWrite),
		r(http.MethodPatch, "/api/v1/projects/:id/work-items/:itemId/status", AuthProjectWrite),
		r(http.MethodDelete, "/api/v1/projects/:id/work-items/:itemId", AuthProjectWrite),
		r(http.MethodPost, "/api/v1/projects/:id/sprints", AuthProjectWrite),
		r(http.MethodPut, "/api/v1/projects/:id/sprints/:sprintId", AuthProjectWrite),
		r(http.MethodPost, "/api/v1/projects/:id/releases", AuthProjectWrite),
		r(http.MethodPut, "/api/v1/projects/:id/releases/:releaseId", AuthProjectWrite),

		// Test management.
		r(http.MethodPost, "/api/v1/projects/:id/test-cases", AuthProjectWrite),
		r(http.MethodPut, "/api/v1/projects/:id/test-cases/:caseId", AuthProjectWrite),
		r(http.MethodDelete, "/api/v1/projects/:id/test-cases/:caseId", AuthProjectWrite),
		r(http.MethodPost, "/api/v1/projects/:id/test-runs", AuthProjectWrite),
		r(http.MethodPost, "/api/v1/projects/:id/test-runs/import", AuthProjectWrite),
		r(http.MethodPatch, "/api/v1/projects/:id/test-runs/:runId/results/:resultId", AuthProjectWrite),
		r(http.MethodPatch, "/api/v1/projects/:id/test-runs/:runId/results/:resultId/steps/:stepResultId", AuthProjectWrite),
		r(http.MethodPost, "/api/v1/projects/:id/test-runs/:runId/results/:resultId/bug", AuthProjectWrite),

		// Delivery configuration and links.
		r(http.MethodPost, "/api/v1/projects/:id/environments", AuthProjectWrite),
		r(http.MethodPost, "/api/v1/projects/:id/links", AuthProjectWrite),
		r(http.MethodDelete, "/api/v1/projects/:id/links/:linkId", AuthProjectWrite),

		// Inbound webhooks authenticate by signature or token.
		r(http.MethodPost, "/api/v1/webhooks/gitea", AuthPublic),
		r(http.MethodPost, "/api/v1/webhooks/argocd", AuthPublic),
		r(http.MethodPost, "/api/v1/webhooks/git/:provider", AuthPublic),
		r(http.MethodPost, "/api/v1/webhooks/ci/:provider", AuthPublic),
		r(http.MethodPost, "/api/v1/webhooks/cd/:provider", AuthPublic),
	}
}
