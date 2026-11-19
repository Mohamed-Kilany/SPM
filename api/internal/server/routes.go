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
	Pool             *pgxpool.Pool
	UserInvalidation nats.KeyValue
}

// Register binds every HTTP route to the supplied handlers.
func Register(r *gin.Engine, h Handlers) {
	health := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
	ready := func(c *gin.Context) {
		checkCtx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		if err := h.Pool.Ping(checkCtx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded", "postgres": err.Error()})
			return
		}
		if !h.Events.Conn().IsConnected() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded", "nats": "disconnected"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
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
		}

		v1.GET("/projects", h.Project.List)
		v1.POST("/projects", auth.RequireRole(auth.RoleAdmin, auth.RoleMember), h.Project.Create)
		v1.GET("/projects/:id", auth.RequireProjectReadDB(h.Pool), h.Project.Get)
		v1.GET("/projects/:id/workspace", auth.RequireProjectReadDB(h.Pool), h.Workspace.GetWorkspace)
		v1.GET("/projects/:id/activity", auth.RequireProjectReadDB(h.Pool), h.Workspace.GetActivity)
		v1.GET("/projects/:id/traceability", auth.RequireProjectReadDB(h.Pool), h.Workspace.GetTraceability)
		v1.GET("/projects/:id/reports", auth.RequireProjectReadDB(h.Pool), h.Workspace.GetReports)
		v1.GET("/projects/:id/reports/export", auth.RequireProjectReadDB(h.Pool), h.Workspace.ExportReport)
		v1.GET("/projects/:id/release-readiness", auth.RequireProjectReadDB(h.Pool), h.Workspace.GetReleaseReadiness)
		v1.GET("/projects/:id/ai/search", auth.RequireProjectReadDB(h.Pool), h.Workspace.AISearch)
		v1.POST("/projects/:id/ai/ask", auth.RequireProjectReadDB(h.Pool), h.Workspace.AIAsk)
		v1.GET("/projects/:id/ai/insights", auth.RequireProjectReadDB(h.Pool), h.Workspace.AIInsights)
		v1.GET("/projects/:id/settings", auth.RequireProjectReadDB(h.Pool), h.Workspace.GetSettings)
		v1.GET("/projects/:id/members", auth.RequireProjectReadDB(h.Pool), h.Workspace.ListMembers)
		v1.GET("/projects/:id/repositories/:repoId/tree", auth.RequireProjectReadDB(h.Pool), h.Workspace.GetRepositoryTree)
		v1.GET("/projects/:id/repositories/:repoId/blob", auth.RequireProjectReadDB(h.Pool), h.Workspace.GetRepositoryFile)
		v1.GET("/projects/:id/repositories/:repoId/commits", auth.RequireProjectReadDB(h.Pool), h.Workspace.GetRepositoryCommits)
		v1.GET("/projects/:id/repositories/:repoId/branches", auth.RequireProjectReadDB(h.Pool), h.Workspace.GetRepositoryBranches)
		v1.GET("/projects/:id/pull-requests/:prId/files", auth.RequireProjectReadDB(h.Pool), h.Workspace.GetPullRequestFiles)

		projectAdmin := v1.Group("/projects/:id", auth.RequireProjectAdminDB(h.Pool))
		{
			projectAdmin.PATCH("", h.Workspace.UpdateProject)
			projectAdmin.PUT("/settings", h.Workspace.UpdateSettings)
			projectAdmin.POST("/members", h.Workspace.AddMember)
			projectAdmin.PATCH("/members/:userId", h.Workspace.UpdateMember)
			projectAdmin.DELETE("/members/:userId", h.Workspace.RemoveMember)
		}

		writes := v1.Group("/projects/:id", auth.RequireProjectWriteDB(h.Pool))
		{
			writes.POST("/tickets", h.Ticket.Create)
			writes.PUT("/tickets/:tid", h.Ticket.Update)
			writes.PATCH("/tickets/:tid/state", h.Ticket.ChangeState)
			writes.DELETE("/tickets/:tid", h.Ticket.Delete)

			// Planning
			writes.POST("/prds", h.Workspace.CreatePRD)
			writes.PUT("/prds/:prdId", h.Workspace.UpdatePRD)
			writes.DELETE("/prds/:prdId", h.Workspace.DeletePRD)
			writes.POST("/epics", h.Workspace.CreateEpic)
			writes.PUT("/epics/:epicId", h.Workspace.UpdateEpic)
			writes.DELETE("/epics/:epicId", h.Workspace.DeleteEpic)
			writes.POST("/features", h.Workspace.CreateFeature)
			writes.PUT("/features/:featureId", h.Workspace.UpdateFeature)
			writes.DELETE("/features/:featureId", h.Workspace.DeleteFeature)
			writes.POST("/work-items", h.Workspace.CreateWorkItem)
			writes.PUT("/work-items/:itemId", h.Workspace.UpdateWorkItem)
			writes.PATCH("/work-items/:itemId/status", h.Workspace.ChangeWorkItemStatus)
			writes.DELETE("/work-items/:itemId", h.Workspace.DeleteWorkItem)
			writes.POST("/sprints", h.Workspace.CreateSprint)
			writes.PUT("/sprints/:sprintId", h.Workspace.UpdateSprint)
			writes.POST("/releases", h.Workspace.CreateRelease)
			writes.PUT("/releases/:releaseId", h.Workspace.UpdateRelease)

			// Test management
			writes.POST("/test-cases", h.Workspace.CreateTestCase)
			writes.PUT("/test-cases/:caseId", h.Workspace.UpdateTestCase)
			writes.DELETE("/test-cases/:caseId", h.Workspace.DeleteTestCase)
			writes.POST("/test-runs", h.Workspace.CreateTestRun)
			writes.POST("/test-runs/import", h.Workspace.ImportTestRun)
			writes.PATCH("/test-runs/:runId/results/:resultId", h.Workspace.PatchTestResult)
			writes.PATCH("/test-runs/:runId/results/:resultId/steps/:stepResultId", h.Workspace.PatchTestStepResult)
			writes.POST("/test-runs/:runId/results/:resultId/bug", h.Workspace.CreateBugFromResult)

			// Git and delivery
			writes.POST("/repositories", h.Workspace.CreateRepository)
			writes.POST("/repositories/:repoId/provision", h.Workspace.ProvisionRepository)
			writes.POST("/branches", h.Workspace.CreateBranch)
			writes.POST("/pull-requests", h.Workspace.CreatePullRequest)
			writes.PUT("/pull-requests/:prId", h.Workspace.UpdatePullRequest)
			writes.POST("/pull-requests/:prId/merge", h.Workspace.MergePullRequest)
			writes.POST("/environments", h.Workspace.CreateEnvironment)
			// Builds, deployments, health checks and rollbacks are read-only
			// projections of Gitea Actions and Argo CD. They are never edited
			// through the SPM API.
			writes.POST("/links", h.Workspace.CreateLink)
			writes.DELETE("/links/:linkId", h.Workspace.DeleteLink)
		}
	}

	streamAuth := sse.StreamAuthMiddleware(h.TicketStore, h.UserInvalidation)
	stream := r.Group("/api/v1", streamAuth, auth.RequireProjectReadDB(h.Pool))
	{
		stream.GET("/projects/:id/stream", h.SSEManager.Stream)
	}
}
