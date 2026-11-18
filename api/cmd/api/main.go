package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"spm-platform/api/internal/ai"
	"spm-platform/api/internal/auth"
	"spm-platform/api/internal/cdengine"
	"spm-platform/api/internal/events"
	"spm-platform/api/internal/gitengine"
	"spm-platform/api/internal/migrate"
	"spm-platform/api/internal/observability"
	"spm-platform/api/internal/project"
	"spm-platform/api/internal/search"
	"spm-platform/api/internal/snapshot"
	"spm-platform/api/internal/sse"
	"spm-platform/api/internal/ticket"
	"spm-platform/api/internal/webhook"
	"spm-platform/api/internal/worker"
	"spm-platform/api/internal/workspace"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	databaseURL := mustEnv("DATABASE_URL")
	natsURL := envOr("NATS_URL", "nats://localhost:4222")
	retentionDays := envInt("NATS_EVENT_RETENTION_DAYS", 90)
	port := envOr("PORT", "8080")

	if envOr("GIN_MODE", "release") == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	poolCfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		log.Fatalf("postgres: invalid DATABASE_URL: %v", err)
	}
	poolCfg.MaxConns = int32(envInt("DATABASE_MAX_CONNS", 20))
	poolCfg.MinConns = int32(envInt("DATABASE_MIN_CONNS", 2))
	poolCfg.MaxConnLifetime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer pool.Close()

	pingCtx, cancelPing := context.WithTimeout(ctx, 15*time.Second)
	err = pool.Ping(pingCtx)
	cancelPing()
	if err != nil {
		log.Fatalf("postgres: ping failed: %v", err)
	}

	if err := migrate.Run(ctx, pool); err != nil {
		log.Fatalf("migrations: %v", err)
	}

	eventsClient, err := events.New(natsURL, retentionDays)
	if err != nil {
		log.Fatalf("nats: %v", err)
	}
	defer eventsClient.Close()

	snapRepo := snapshot.New(pool)
	gitEngineClient := gitengine.New()

	ticketStore := auth.NewTicketStore(ctx, 10*time.Second)
	authHandler := auth.NewHandler(pool, ticketStore, eventsClient)
	projectHandler := project.NewHandler(pool, snapRepo, eventsClient, gitEngineClient)
	ticketHandler := ticket.NewHandler(snapRepo, eventsClient, gitEngineClient, pool)
	workspaceHandler := workspace.NewHandler(pool, eventsClient)
	workspaceHandler.SetAIClient(ai.New())
	workspaceHandler.SetGitClient(gitEngineClient)
	searchHandler := search.NewHandler(pool)

	sseManager := sse.NewManager(eventsClient.Conn(), pool)
	if err := sseManager.Start(ctx.Done()); err != nil {
		log.Fatalf("sse: %v", err)
	}
	defer sseManager.Close()

	streamAuth := sse.StreamAuthMiddleware(ticketStore, eventsClient.UserInvalidation())

	cdManager := cdengine.New(eventsClient)
	watchdog := cdengine.NewWatchdog(eventsClient)
	argoClient := cdengine.NewArgoClient()
	webhookStore := webhook.NewStore(pool)
	giteaWebhook := webhook.NewGiteaHandler(pool, webhookStore, eventsClient, cdManager, watchdog)
	argoWebhook := webhook.NewArgoCDHandler(pool, webhookStore, eventsClient, watchdog)
	genericWebhook := webhook.NewGenericHandler(pool, eventsClient)
	deliverySyncer := webhook.NewDeliverySyncer(pool, eventsClient, gitEngineClient, argoClient)

	w := worker.New(eventsClient, pool, snapRepo)
	if err := w.Start(ctx); err != nil {
		log.Fatalf("worker: %v", err)
	}
	watchdog.Start(ctx)
	deliverySyncer.Start(ctx)

	r := gin.New()
	r.Use(observability.RequestID(), observability.AccessLog(), observability.Recovery(),
		observability.SecurityHeaders(), observability.RateLimit(600, time.Minute))
	_ = r.SetTrustedProxies(nil)

	health := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
	ready := func(c *gin.Context) {
		checkCtx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		if err := pool.Ping(checkCtx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded", "postgres": err.Error()})
			return
		}
		if !eventsClient.Conn().IsConnected() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded", "nats": "disconnected"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	}
	r.GET("/health", ready)
	r.GET("/health/live", health)
	r.GET("/health/ready", ready)

	r.POST("/api/v1/auth/login", observability.RateLimit(30, time.Minute), authHandler.Login)
	r.POST("/api/v1/webhooks/gitea", giteaWebhook.Handle)
	r.POST("/api/v1/webhooks/argocd", argoWebhook.Handle)
	r.POST("/api/v1/webhooks/git/:provider", genericWebhook.HandleGit)
	r.POST("/api/v1/webhooks/ci/:provider", genericWebhook.HandleCI)
	r.POST("/api/v1/webhooks/cd/:provider", genericWebhook.HandleCD)

	jwt := auth.Middleware(eventsClient.UserInvalidation())
	v1 := r.Group("/api/v1", jwt)
	{
		v1.GET("/auth/me", authHandler.Me)
		v1.PATCH("/auth/me", authHandler.UpdateMe)
		v1.POST("/auth/password", authHandler.ChangePassword)
		v1.POST("/auth/stream-ticket", authHandler.StreamTicket)
		v1.GET("/users", authHandler.Directory)
		v1.GET("/search", searchHandler.Search)

		admin := v1.Group("/admin", auth.RequireRole(auth.RoleAdmin))
		{
			admin.GET("/users", authHandler.AdminListUsers)
			admin.POST("/users", authHandler.AdminCreateUser)
			admin.PATCH("/users/:userId", authHandler.AdminUpdateUser)
		}

		v1.GET("/projects", projectHandler.List)
		v1.POST("/projects", auth.RequireRole(auth.RoleAdmin, auth.RoleMember), projectHandler.Create)
		v1.GET("/projects/:id", auth.RequireProjectReadDB(pool), projectHandler.Get)
		v1.GET("/projects/:id/workspace", auth.RequireProjectReadDB(pool), workspaceHandler.GetWorkspace)
		v1.GET("/projects/:id/activity", auth.RequireProjectReadDB(pool), workspaceHandler.GetActivity)
		v1.GET("/projects/:id/traceability", auth.RequireProjectReadDB(pool), workspaceHandler.GetTraceability)
		v1.GET("/projects/:id/reports", auth.RequireProjectReadDB(pool), workspaceHandler.GetReports)
		v1.GET("/projects/:id/reports/export", auth.RequireProjectReadDB(pool), workspaceHandler.ExportReport)
		v1.GET("/projects/:id/release-readiness", auth.RequireProjectReadDB(pool), workspaceHandler.GetReleaseReadiness)
		v1.GET("/projects/:id/ai/search", auth.RequireProjectReadDB(pool), workspaceHandler.AISearch)
		v1.POST("/projects/:id/ai/ask", auth.RequireProjectReadDB(pool), workspaceHandler.AIAsk)
		v1.GET("/projects/:id/ai/insights", auth.RequireProjectReadDB(pool), workspaceHandler.AIInsights)
		v1.GET("/projects/:id/settings", auth.RequireProjectReadDB(pool), workspaceHandler.GetSettings)
		v1.GET("/projects/:id/members", auth.RequireProjectReadDB(pool), workspaceHandler.ListMembers)
		v1.GET("/projects/:id/repositories/:repoId/tree", auth.RequireProjectReadDB(pool), workspaceHandler.GetRepositoryTree)
		v1.GET("/projects/:id/repositories/:repoId/blob", auth.RequireProjectReadDB(pool), workspaceHandler.GetRepositoryFile)
		v1.GET("/projects/:id/repositories/:repoId/commits", auth.RequireProjectReadDB(pool), workspaceHandler.GetRepositoryCommits)
		v1.GET("/projects/:id/repositories/:repoId/branches", auth.RequireProjectReadDB(pool), workspaceHandler.GetRepositoryBranches)
		v1.GET("/projects/:id/pull-requests/:prId/files", auth.RequireProjectReadDB(pool), workspaceHandler.GetPullRequestFiles)

		projectAdmin := v1.Group("/projects/:id", auth.RequireProjectAdminDB(pool))
		{
			projectAdmin.PATCH("", workspaceHandler.UpdateProject)
			projectAdmin.PUT("/settings", workspaceHandler.UpdateSettings)
			projectAdmin.POST("/members", workspaceHandler.AddMember)
			projectAdmin.PATCH("/members/:userId", workspaceHandler.UpdateMember)
			projectAdmin.DELETE("/members/:userId", workspaceHandler.RemoveMember)
		}

		writes := v1.Group("/projects/:id", auth.RequireProjectWriteDB(pool))
		{
			writes.POST("/tickets", ticketHandler.Create)
			writes.PUT("/tickets/:tid", ticketHandler.Update)
			writes.PATCH("/tickets/:tid/state", ticketHandler.ChangeState)
			writes.DELETE("/tickets/:tid", ticketHandler.Delete)

			// Planning
			writes.POST("/prds", workspaceHandler.CreatePRD)
			writes.PUT("/prds/:prdId", workspaceHandler.UpdatePRD)
			writes.DELETE("/prds/:prdId", workspaceHandler.DeletePRD)
			writes.POST("/epics", workspaceHandler.CreateEpic)
			writes.PUT("/epics/:epicId", workspaceHandler.UpdateEpic)
			writes.DELETE("/epics/:epicId", workspaceHandler.DeleteEpic)
			writes.POST("/features", workspaceHandler.CreateFeature)
			writes.PUT("/features/:featureId", workspaceHandler.UpdateFeature)
			writes.DELETE("/features/:featureId", workspaceHandler.DeleteFeature)
			writes.POST("/work-items", workspaceHandler.CreateWorkItem)
			writes.PUT("/work-items/:itemId", workspaceHandler.UpdateWorkItem)
			writes.PATCH("/work-items/:itemId/status", workspaceHandler.ChangeWorkItemStatus)
			writes.DELETE("/work-items/:itemId", workspaceHandler.DeleteWorkItem)
			writes.POST("/sprints", workspaceHandler.CreateSprint)
			writes.PUT("/sprints/:sprintId", workspaceHandler.UpdateSprint)
			writes.POST("/releases", workspaceHandler.CreateRelease)
			writes.PUT("/releases/:releaseId", workspaceHandler.UpdateRelease)

			// Test management
			writes.POST("/test-cases", workspaceHandler.CreateTestCase)
			writes.PUT("/test-cases/:caseId", workspaceHandler.UpdateTestCase)
			writes.DELETE("/test-cases/:caseId", workspaceHandler.DeleteTestCase)
			writes.POST("/test-runs", workspaceHandler.CreateTestRun)
			writes.POST("/test-runs/import", workspaceHandler.ImportTestRun)
			writes.PATCH("/test-runs/:runId/results/:resultId", workspaceHandler.PatchTestResult)
			writes.PATCH("/test-runs/:runId/results/:resultId/steps/:stepResultId", workspaceHandler.PatchTestStepResult)
			writes.POST("/test-runs/:runId/results/:resultId/bug", workspaceHandler.CreateBugFromResult)

			// Git and delivery
			writes.POST("/repositories", workspaceHandler.CreateRepository)
			writes.POST("/repositories/:repoId/provision", workspaceHandler.ProvisionRepository)
			writes.POST("/branches", workspaceHandler.CreateBranch)
			writes.POST("/pull-requests", workspaceHandler.CreatePullRequest)
			writes.PUT("/pull-requests/:prId", workspaceHandler.UpdatePullRequest)
			writes.POST("/pull-requests/:prId/merge", workspaceHandler.MergePullRequest)
			writes.POST("/environments", workspaceHandler.CreateEnvironment)
			// Builds, deployments, health checks and rollbacks are read-only
			// projections of Gitea Actions and Argo CD. They are never edited
			// through the SPM API.
			writes.POST("/links", workspaceHandler.CreateLink)
			writes.DELETE("/links/:linkId", workspaceHandler.DeleteLink)
		}
	}

	stream := r.Group("/api/v1", streamAuth, auth.RequireProjectReadDB(pool))
	{
		stream.GET("/projects/:id/stream", sseManager.Stream)
	}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// WriteTimeout is intentionally zero: SSE connections are long-lived.
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()
	log.Printf("SPM API running on :%s", port)

	<-ctx.Done()
	log.Println("Shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown: %v", err)
	}
	sseManager.Close()
	eventsClient.Close()
	log.Println("Shutdown complete")
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
