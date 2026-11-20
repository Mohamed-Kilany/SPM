package server

import (
	"net/http"

	"spm-platform/api/internal/auth"
)

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
	Method     string
	Path       string
	Auth       AuthLevel
	Capability auth.Capability
}

func authLevelForCapability(capability auth.Capability) AuthLevel {
	switch capability {
	case auth.CapProjectRead, auth.CapCodeRead, auth.CapDeliveryRead:
		return AuthProjectRead
	case auth.CapSettingsWrite, auth.CapMemberWrite:
		return AuthProjectAdmin
	default:
		return AuthProjectWrite
	}
}

// Routes returns the canonical route inventory. Register and this table are
// checked against each other by routes_test.go, so a new route cannot ship
// without appearing in the contract pipeline. Every project-scoped route
// declares the capability its middleware enforces.
func Routes() []Route {
	r := func(method, path string, authLevel AuthLevel) Route {
		return Route{Method: method, Path: path, Auth: authLevel}
	}
	// Project route with an explicit capability.
	p := func(method, path string, capability auth.Capability) Route {
		return Route{Method: method, Path: path, Auth: authLevelForCapability(capability), Capability: capability}
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
		r(http.MethodGet, "/api/v1/admin/audit/events", AuthAdmin),
		r(http.MethodGet, "/api/v1/admin/audit/events/:auditId", AuthAdmin),
		r(http.MethodGet, "/api/v1/admin/audit/export", AuthAdmin),
		r(http.MethodGet, "/api/v1/admin/audit/verify", AuthAdmin),

		// Projects.
		r(http.MethodGet, "/api/v1/projects", AuthBearer),
		r(http.MethodPost, "/api/v1/projects", AuthMember),
		p(http.MethodGet, "/api/v1/projects/:id", auth.CapProjectRead),
		p(http.MethodPatch, "/api/v1/projects/:id", auth.CapSettingsWrite),
		p(http.MethodGet, "/api/v1/projects/:id/workspace", auth.CapProjectRead),
		p(http.MethodGet, "/api/v1/projects/:id/activity", auth.CapProjectRead),
		p(http.MethodGet, "/api/v1/projects/:id/traceability", auth.CapProjectRead),
		p(http.MethodGet, "/api/v1/projects/:id/reports", auth.CapProjectRead),
		p(http.MethodGet, "/api/v1/projects/:id/reports/export", auth.CapProjectRead),
		p(http.MethodGet, "/api/v1/projects/:id/release-readiness", auth.CapDeliveryRead),
		p(http.MethodGet, "/api/v1/projects/:id/settings", auth.CapProjectRead),
		p(http.MethodPut, "/api/v1/projects/:id/settings", auth.CapSettingsWrite),
		p(http.MethodGet, "/api/v1/projects/:id/members", auth.CapProjectRead),
		p(http.MethodPost, "/api/v1/projects/:id/members", auth.CapMemberWrite),
		p(http.MethodPatch, "/api/v1/projects/:id/members/:userId", auth.CapMemberWrite),
		p(http.MethodDelete, "/api/v1/projects/:id/members/:userId", auth.CapMemberWrite),
		{
			Method:     http.MethodGet,
			Path:       "/api/v1/projects/:id/stream",
			Auth:       AuthSSETicket,
			Capability: auth.CapProjectRead,
		},

		// AI.
		p(http.MethodGet, "/api/v1/projects/:id/ai/search", auth.CapProjectRead),
		p(http.MethodPost, "/api/v1/projects/:id/ai/ask", auth.CapProjectRead),
		p(http.MethodGet, "/api/v1/projects/:id/ai/insights", auth.CapProjectRead),

		// Repository browsing and pull requests.
		p(http.MethodGet, "/api/v1/projects/:id/repositories/:repoId/tree", auth.CapCodeRead),
		p(http.MethodGet, "/api/v1/projects/:id/repositories/:repoId/blob", auth.CapCodeRead),
		p(http.MethodGet, "/api/v1/projects/:id/repositories/:repoId/commits", auth.CapCodeRead),
		p(http.MethodGet, "/api/v1/projects/:id/repositories/:repoId/branches", auth.CapCodeRead),
		p(http.MethodGet, "/api/v1/projects/:id/pull-requests/:prId/files", auth.CapCodeRead),
		p(http.MethodPost, "/api/v1/projects/:id/repositories", auth.CapCodeWrite),
		p(http.MethodPost, "/api/v1/projects/:id/repositories/:repoId/provision", auth.CapCodeWrite),
		p(http.MethodPost, "/api/v1/projects/:id/branches", auth.CapCodeWrite),
		p(http.MethodPost, "/api/v1/projects/:id/pull-requests", auth.CapCodeWrite),
		p(http.MethodPut, "/api/v1/projects/:id/pull-requests/:prId", auth.CapCodeWrite),
		p(http.MethodPost, "/api/v1/projects/:id/pull-requests/:prId/merge", auth.CapPRMerge),

		// Tickets.
		p(http.MethodPost, "/api/v1/projects/:id/tickets", auth.CapWorkItemWrite),
		p(http.MethodPut, "/api/v1/projects/:id/tickets/:tid", auth.CapWorkItemWrite),
		p(http.MethodPatch, "/api/v1/projects/:id/tickets/:tid/state", auth.CapWorkItemWrite),
		p(http.MethodDelete, "/api/v1/projects/:id/tickets/:tid", auth.CapWorkItemWrite),

		// Planning.
		p(http.MethodPost, "/api/v1/projects/:id/prds", auth.CapPlanningWrite),
		p(http.MethodPut, "/api/v1/projects/:id/prds/:prdId", auth.CapPlanningWrite),
		p(http.MethodDelete, "/api/v1/projects/:id/prds/:prdId", auth.CapPlanningWrite),
		p(http.MethodPost, "/api/v1/projects/:id/epics", auth.CapPlanningWrite),
		p(http.MethodPut, "/api/v1/projects/:id/epics/:epicId", auth.CapPlanningWrite),
		p(http.MethodDelete, "/api/v1/projects/:id/epics/:epicId", auth.CapPlanningWrite),
		p(http.MethodPost, "/api/v1/projects/:id/features", auth.CapPlanningWrite),
		p(http.MethodPut, "/api/v1/projects/:id/features/:featureId", auth.CapPlanningWrite),
		p(http.MethodDelete, "/api/v1/projects/:id/features/:featureId", auth.CapPlanningWrite),
		p(http.MethodPost, "/api/v1/projects/:id/work-items", auth.CapWorkItemWrite),
		p(http.MethodGet, "/api/v1/projects/:id/work-items/:itemId", auth.CapProjectRead),
		p(http.MethodPut, "/api/v1/projects/:id/work-items/:itemId", auth.CapWorkItemWrite),
		p(http.MethodPatch, "/api/v1/projects/:id/work-items/:itemId/status", auth.CapWorkItemWrite),
		p(http.MethodDelete, "/api/v1/projects/:id/work-items/:itemId", auth.CapWorkItemWrite),
		p(http.MethodPost, "/api/v1/projects/:id/work-items/:itemId/comments", auth.CapWorkItemWrite),
		p(http.MethodPatch, "/api/v1/projects/:id/comments/:commentId", auth.CapWorkItemWrite),
		p(http.MethodDelete, "/api/v1/projects/:id/comments/:commentId", auth.CapWorkItemWrite),
		p(http.MethodPost, "/api/v1/projects/:id/work-items/:itemId/watch", auth.CapProjectRead),
		p(http.MethodDelete, "/api/v1/projects/:id/work-items/:itemId/watch", auth.CapProjectRead),
		p(http.MethodPost, "/api/v1/projects/:id/work-items/:itemId/attachments", auth.CapWorkItemWrite),
		p(http.MethodGet, "/api/v1/projects/:id/attachments/:attachmentId/download", auth.CapProjectRead),
		p(http.MethodDelete, "/api/v1/projects/:id/attachments/:attachmentId", auth.CapWorkItemWrite),
		p(http.MethodPost, "/api/v1/projects/:id/work-items/:itemId/links", auth.CapWorkItemWrite),
		p(http.MethodDelete, "/api/v1/projects/:id/work-items/:itemId/links/:linkId", auth.CapWorkItemWrite),
		p(http.MethodPost, "/api/v1/projects/:id/sprints", auth.CapPlanningWrite),
		p(http.MethodPut, "/api/v1/projects/:id/sprints/:sprintId", auth.CapPlanningWrite),
		p(http.MethodPost, "/api/v1/projects/:id/releases", auth.CapPlanningWrite),
		p(http.MethodPut, "/api/v1/projects/:id/releases/:releaseId", auth.CapPlanningWrite),

		// Test management.
		p(http.MethodPost, "/api/v1/projects/:id/test-cases", auth.CapTestWrite),
		p(http.MethodPut, "/api/v1/projects/:id/test-cases/:caseId", auth.CapTestWrite),
		p(http.MethodDelete, "/api/v1/projects/:id/test-cases/:caseId", auth.CapTestWrite),
		p(http.MethodPost, "/api/v1/projects/:id/test-runs", auth.CapTestWrite),
		p(http.MethodPost, "/api/v1/projects/:id/test-runs/import", auth.CapTestWrite),
		p(http.MethodPatch, "/api/v1/projects/:id/test-runs/:runId/results/:resultId", auth.CapTestWrite),
		p(http.MethodPatch, "/api/v1/projects/:id/test-runs/:runId/results/:resultId/steps/:stepResultId", auth.CapTestWrite),
		p(http.MethodPost, "/api/v1/projects/:id/test-runs/:runId/results/:resultId/bug", auth.CapTestWrite),

		// Delivery configuration and links.
		p(http.MethodPost, "/api/v1/projects/:id/environments", auth.CapDeliveryDeploy),
		p(http.MethodPost, "/api/v1/projects/:id/links", auth.CapPlanningWrite),
		p(http.MethodDelete, "/api/v1/projects/:id/links/:linkId", auth.CapPlanningWrite),

		// Inbound webhooks authenticate by signature or token.
		r(http.MethodPost, "/api/v1/webhooks/gitea", AuthPublic),
		r(http.MethodPost, "/api/v1/webhooks/argocd", AuthPublic),
		r(http.MethodPost, "/api/v1/webhooks/git/:provider", AuthPublic),
		r(http.MethodPost, "/api/v1/webhooks/ci/:provider", AuthPublic),
		r(http.MethodPost, "/api/v1/webhooks/cd/:provider", AuthPublic),
	}
}
