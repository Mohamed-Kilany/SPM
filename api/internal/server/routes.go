// Package server owns the HTTP route surface. Registration lives here so tests
// and the OpenAPI generator can enumerate the real router instead of relying on
// a second, drifting path list.
package server

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"spm-platform/api/internal/audit"
	"spm-platform/api/internal/auth"
	"spm-platform/api/internal/events"
	"spm-platform/api/internal/observability"
	"spm-platform/api/internal/project"
	"spm-platform/api/internal/search"
	"spm-platform/api/internal/sse"
	"spm-platform/api/internal/ticket"
	"spm-platform/api/internal/webhook"
	"spm-platform/api/internal/workspace"
)

// BuildInfo is the release metadata embedded at link time.
type BuildInfo struct {
	Version string
	Commit  string
	BuiltAt string
}

func (b BuildInfo) fields() gin.H {
	version, commit, builtAt := b.Version, b.Commit, b.BuiltAt
	if version == "" {
		version = "dev"
	}
	if commit == "" {
		commit = "none"
	}
	if builtAt == "" {
		builtAt = "unknown"
	}
	return gin.H{"version": version, "commit": commit, "built_at": builtAt}
}

// Handlers carries the handler set needed to register the HTTP surface.
type Handlers struct {
	Events           *events.Client
	Auth             *auth.Handler
	Project          *project.Handler
	Ticket           *ticket.Handler
	Workspace        *workspace.Handler
	Search           *search.Handler
	GiteaWebhook     *webhook.GiteaHandler
	ArgoWebhook      *webhook.ArgoCDHandler
	GenericWebhook   *webhook.GenericHandler
	SSEManager       *sse.Manager
	TicketStore      *auth.TicketStore
	Audit            *audit.Handler
	Pool             *pgxpool.Pool
	UserInvalidation nats.KeyValue
	BuildInfo        BuildInfo
}

// Register binds every HTTP route to the supplied handlers.
func Register(r *gin.Engine, h Handlers) {
	build := h.BuildInfo.fields()
	health := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "build": build})
	}
	ready := func(c *gin.Context) {
		checkCtx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		if err := h.Pool.Ping(checkCtx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded", "postgres": err.Error(), "build": build})
			return
		}
		if !h.Events.Conn().IsConnected() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded", "nats": "disconnected", "build": build})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready", "build": build})
	}
	r.GET("/health", ready)
	r.GET("/health/live", health)
	r.GET("/health/ready", ready)

	r.POST("/api/v1/auth/login", observability.RateLimit(30, time.Minute), h.Auth.Login)
	r.POST("/api/v1/webhooks/gitea", h.GiteaWebhook.Handle)
	r.POST("/api/v1/webhooks/argocd", h.ArgoWebhook.Handle)
	r.POST("/api/v1/webhooks/git/:provider", h.GenericWebhook.HandleGit)
	r.POST("/api/v1/webhooks/ci/:provider", h.GenericWebhook.HandleCI)
	r.POST("/api/v1/webhooks/cd/:provider", h.GenericWebhook.HandleCD)

	jwt := auth.Middleware(h.UserInvalidation)
	v1 := r.Group("/api/v1", jwt)
	{
		v1.GET("/auth/me", h.Auth.Me)
		v1.PATCH("/auth/me", h.Auth.UpdateMe)
		v1.POST("/auth/password", h.Auth.ChangePassword)
		v1.POST("/auth/stream-ticket", h.Auth.StreamTicket)
		v1.GET("/users", h.Auth.Directory)
		v1.GET("/search", h.Search.Search)

		admin := v1.Group("/admin", auth.RequireRole(auth.RoleAdmin))
		{
			admin.GET("/users", h.Auth.AdminListUsers)
			admin.POST("/users", h.Auth.AdminCreateUser)
			admin.PATCH("/users/:userId", h.Auth.AdminUpdateUser)
			admin.GET("/audit/events", h.Audit.List)
			admin.GET("/audit/events/:auditId", h.Audit.Get)
			admin.GET("/audit/export", h.Audit.Export)
			admin.GET("/audit/verify", h.Audit.Verify)
		}

		v1.GET("/projects", h.Project.List)
		v1.POST("/projects", auth.RequireRole(auth.RoleAdmin, auth.RoleMember), h.Project.Create)

		// Project routes declare one capability each. RequireProjectCapability
		// resolves the membership from PostgreSQL on every request, so a role
		// change or removal is effective immediately.
		project := func(method, suffix string, capability auth.Capability, handler gin.HandlerFunc) {
			v1.Handle(method, "/projects/:id"+suffix, auth.RequireProjectCapability(h.Pool, capability), handler)
		}

		// Reads.
		project(http.MethodGet, "", auth.CapProjectRead, h.Project.Get)
		project(http.MethodGet, "/workspace", auth.CapProjectRead, h.Workspace.GetWorkspace)
		project(http.MethodGet, "/activity", auth.CapProjectRead, h.Workspace.GetActivity)
		project(http.MethodGet, "/traceability", auth.CapProjectRead, h.Workspace.GetTraceability)
		project(http.MethodGet, "/reports", auth.CapProjectRead, h.Workspace.GetReports)
		project(http.MethodGet, "/reports/export", auth.CapProjectRead, h.Workspace.ExportReport)
		project(http.MethodGet, "/release-readiness", auth.CapDeliveryRead, h.Workspace.GetReleaseReadiness)
		project(http.MethodGet, "/ai/search", auth.CapProjectRead, h.Workspace.AISearch)
		project(http.MethodPost, "/ai/ask", auth.CapProjectRead, h.Workspace.AIAsk)
		project(http.MethodGet, "/ai/insights", auth.CapProjectRead, h.Workspace.AIInsights)
		project(http.MethodGet, "/settings", auth.CapProjectRead, h.Workspace.GetSettings)
		project(http.MethodGet, "/members", auth.CapProjectRead, h.Workspace.ListMembers)
		project(http.MethodGet, "/repositories/:repoId/tree", auth.CapCodeRead, h.Workspace.GetRepositoryTree)
		project(http.MethodGet, "/repositories/:repoId/blob", auth.CapCodeRead, h.Workspace.GetRepositoryFile)
		project(http.MethodGet, "/repositories/:repoId/commits", auth.CapCodeRead, h.Workspace.GetRepositoryCommits)
		project(http.MethodGet, "/repositories/:repoId/branches", auth.CapCodeRead, h.Workspace.GetRepositoryBranches)
		project(http.MethodGet, "/pull-requests/:prId/files", auth.CapCodeRead, h.Workspace.GetPullRequestFiles)

		// Project settings and membership.
		project(http.MethodPatch, "", auth.CapSettingsWrite, h.Workspace.UpdateProject)
		project(http.MethodPut, "/settings", auth.CapSettingsWrite, h.Workspace.UpdateSettings)
		project(http.MethodPost, "/members", auth.CapMemberWrite, h.Workspace.AddMember)
		project(http.MethodPatch, "/members/:userId", auth.CapMemberWrite, h.Workspace.UpdateMember)
		project(http.MethodDelete, "/members/:userId", auth.CapMemberWrite, h.Workspace.RemoveMember)

		// Work items. Testers hold work_item.write for defects and status
		// changes; the ticket and work-item handlers enforce that restriction.
		project(http.MethodPost, "/tickets", auth.CapWorkItemWrite, h.Ticket.Create)
		project(http.MethodPut, "/tickets/:tid", auth.CapWorkItemWrite, h.Ticket.Update)
		project(http.MethodPatch, "/tickets/:tid/state", auth.CapWorkItemWrite, h.Ticket.ChangeState)
		project(http.MethodDelete, "/tickets/:tid", auth.CapWorkItemWrite, h.Ticket.Delete)
		project(http.MethodPost, "/work-items", auth.CapWorkItemWrite, h.Workspace.CreateWorkItem)
		project(http.MethodPut, "/work-items/:itemId", auth.CapWorkItemWrite, h.Workspace.UpdateWorkItem)
		project(http.MethodPatch, "/work-items/:itemId/status", auth.CapWorkItemWrite, h.Workspace.ChangeWorkItemStatus)
		project(http.MethodDelete, "/work-items/:itemId", auth.CapWorkItemWrite, h.Workspace.DeleteWorkItem)

		// Planning.
		project(http.MethodPost, "/prds", auth.CapPlanningWrite, h.Workspace.CreatePRD)
		project(http.MethodPut, "/prds/:prdId", auth.CapPlanningWrite, h.Workspace.UpdatePRD)
		project(http.MethodDelete, "/prds/:prdId", auth.CapPlanningWrite, h.Workspace.DeletePRD)
		project(http.MethodPost, "/epics", auth.CapPlanningWrite, h.Workspace.CreateEpic)
		project(http.MethodPut, "/epics/:epicId", auth.CapPlanningWrite, h.Workspace.UpdateEpic)
		project(http.MethodDelete, "/epics/:epicId", auth.CapPlanningWrite, h.Workspace.DeleteEpic)
		project(http.MethodPost, "/features", auth.CapPlanningWrite, h.Workspace.CreateFeature)
		project(http.MethodPut, "/features/:featureId", auth.CapPlanningWrite, h.Workspace.UpdateFeature)
		project(http.MethodDelete, "/features/:featureId", auth.CapPlanningWrite, h.Workspace.DeleteFeature)
		project(http.MethodPost, "/sprints", auth.CapPlanningWrite, h.Workspace.CreateSprint)
		project(http.MethodPut, "/sprints/:sprintId", auth.CapPlanningWrite, h.Workspace.UpdateSprint)
		project(http.MethodPost, "/releases", auth.CapPlanningWrite, h.Workspace.CreateRelease)
		project(http.MethodPut, "/releases/:releaseId", auth.CapPlanningWrite, h.Workspace.UpdateRelease)
		project(http.MethodPost, "/links", auth.CapPlanningWrite, h.Workspace.CreateLink)
		project(http.MethodDelete, "/links/:linkId", auth.CapPlanningWrite, h.Workspace.DeleteLink)

		// Test management.
		project(http.MethodPost, "/test-cases", auth.CapTestWrite, h.Workspace.CreateTestCase)
		project(http.MethodPut, "/test-cases/:caseId", auth.CapTestWrite, h.Workspace.UpdateTestCase)
		project(http.MethodDelete, "/test-cases/:caseId", auth.CapTestWrite, h.Workspace.DeleteTestCase)
		project(http.MethodPost, "/test-runs", auth.CapTestWrite, h.Workspace.CreateTestRun)
		project(http.MethodPost, "/test-runs/import", auth.CapTestWrite, h.Workspace.ImportTestRun)
		project(http.MethodPatch, "/test-runs/:runId/results/:resultId", auth.CapTestWrite, h.Workspace.PatchTestResult)
		project(http.MethodPatch, "/test-runs/:runId/results/:resultId/steps/:stepResultId", auth.CapTestWrite, h.Workspace.PatchTestStepResult)
		project(http.MethodPost, "/test-runs/:runId/results/:resultId/bug", auth.CapTestWrite, h.Workspace.CreateBugFromResult)

		// Git and delivery. Builds, deployments, health checks and rollbacks
		// are read-only projections of Gitea Actions and Argo CD; they are
		// never edited through the SPM API.
		project(http.MethodPost, "/repositories", auth.CapCodeWrite, h.Workspace.CreateRepository)
		project(http.MethodPost, "/repositories/:repoId/provision", auth.CapCodeWrite, h.Workspace.ProvisionRepository)
		project(http.MethodPost, "/branches", auth.CapCodeWrite, h.Workspace.CreateBranch)
		project(http.MethodPost, "/pull-requests", auth.CapCodeWrite, h.Workspace.CreatePullRequest)
		project(http.MethodPut, "/pull-requests/:prId", auth.CapCodeWrite, h.Workspace.UpdatePullRequest)
		project(http.MethodPost, "/pull-requests/:prId/merge", auth.CapPRMerge, h.Workspace.MergePullRequest)
		project(http.MethodPost, "/environments", auth.CapDeliveryDeploy, h.Workspace.CreateEnvironment)
	}

	streamAuth := sse.StreamAuthMiddleware(h.TicketStore, h.UserInvalidation)
	stream := r.Group("/api/v1", streamAuth, auth.RequireProjectCapability(h.Pool, auth.CapProjectRead))
	{
		stream.GET("/projects/:id/stream", h.SSEManager.Stream)
	}
}
