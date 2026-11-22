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
	"spm-platform/api/internal/attachments"
	"spm-platform/api/internal/audit"
	"spm-platform/api/internal/auth"
	"spm-platform/api/internal/cdengine"
	"spm-platform/api/internal/events"
	"spm-platform/api/internal/gitengine"
	"spm-platform/api/internal/metrics"
	"spm-platform/api/internal/migrate"
	"spm-platform/api/internal/observability"
	"spm-platform/api/internal/project"
	"spm-platform/api/internal/search"
	"spm-platform/api/internal/server"
	"spm-platform/api/internal/snapshot"
	"spm-platform/api/internal/sse"
	"spm-platform/api/internal/ticket"
	"spm-platform/api/internal/webhook"
	"spm-platform/api/internal/worker"
	"spm-platform/api/internal/workspace"
)

// Build metadata is injected with -ldflags at release time. The defaults keep
// local and test builds honest instead of reporting a fake release version.
var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
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

	metricsRegistry := metrics.New(metrics.BuildInfo{Version: version, Commit: commit, BuiltAt: buildDate})
	metrics.SetDefault(metricsRegistry)

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
	metricsRegistry.RegisterPool(pool)

	auditLogger := audit.NewLogger(pool)
	if err := auditLogger.EnsurePartitions(ctx, 3); err != nil {
		log.Fatalf("audit: %v", err)
	}
	auditRetentionDays := audit.RetentionDaysFromEnv(os.Getenv("AUDIT_RETENTION_DAYS"))
	if dropped, err := auditLogger.Prune(ctx, auditRetentionDays); err != nil {
		log.Printf("audit: initial retention pass failed: %v", err)
	} else if dropped > 0 {
		log.Printf("audit: dropped %d expired partition(s)", dropped)
	}
	go auditLogger.RunMaintenance(ctx, auditRetentionDays)

	eventsClient, err := events.New(natsURL, retentionDays)
	if err != nil {
		log.Fatalf("nats: %v", err)
	}
	defer eventsClient.Close()
	metricsRegistry.RegisterNATS(eventsClient.Conn())

	snapRepo := snapshot.New(pool)
	gitEngineClient := gitengine.New()

	ticketStore := auth.NewTicketStore(ctx, 10*time.Second)
	authHandler := auth.NewHandler(pool, ticketStore, eventsClient)
	authHandler.SetAuditLogger(auditLogger)
	sessionStore := auth.NewSessionStore(pool, auth.SessionConfigFromEnv())
	authHandler.SetSessions(sessionStore, eventsClient)
	go sessionStore.RunMaintenance(ctx)
	metricsRegistry.RegisterAuthSessions(func() int64 {
		count, err := sessionStore.ActiveCount(context.Background())
		if err != nil {
			return 0
		}
		return count
	})
	projectHandler := project.NewHandler(pool, snapRepo, eventsClient, gitEngineClient)
	projectHandler.SetAuditLogger(auditLogger)
	ticketHandler := ticket.NewHandler(snapRepo, eventsClient, gitEngineClient, pool)
	attachmentStore, err := attachments.New(
		envOr("ATTACHMENTS_DIR", "./data/attachments"),
		int64(envInt("ATTACHMENT_MAX_BYTES", int(attachments.DefaultMaxBytes))),
		os.Getenv("CLAMAV_ADDR"),
	)
	if err != nil {
		log.Fatalf("attachments: %v", err)
	}
	workspaceHandler := workspace.NewHandler(pool, eventsClient)
	workspaceHandler.SetAIClient(ai.New())
	workspaceHandler.SetGitClient(gitEngineClient)
	workspaceHandler.SetAuditLogger(auditLogger)
	workspaceHandler.SetAttachmentStore(attachmentStore)
	searchHandler := search.NewHandler(pool)

	sseManager := sse.NewManager(eventsClient.Conn(), pool)
	sseManager.SetSessionValidator(sessionStore)
	if err := sseManager.Start(ctx.Done()); err != nil {
		log.Fatalf("sse: %v", err)
	}
	defer sseManager.Close()
	metricsRegistry.RegisterSSE(sseManager.ActiveConnections)

	cdManager := cdengine.New(eventsClient)
	watchdog := cdengine.NewWatchdog(eventsClient)
	argoClient := cdengine.NewArgoClient()
	webhookStore := webhook.NewStore(pool)
	giteaWebhook := webhook.NewGiteaHandler(pool, webhookStore, eventsClient, cdManager, watchdog)
	argoWebhook := webhook.NewArgoCDHandler(pool, webhookStore, eventsClient, watchdog)
	argoWebhook.SetAuditLogger(auditLogger)
	genericWebhook := webhook.NewGenericHandler(pool, eventsClient)
	deliverySyncer := webhook.NewDeliverySyncer(pool, eventsClient, gitEngineClient, argoClient)

	w := worker.New(eventsClient, pool, snapRepo)
	if err := w.Start(ctx); err != nil {
		log.Fatalf("worker: %v", err)
	}
	watchdog.Start(ctx)
	deliverySyncer.Start(ctx)

	r := gin.New()
	r.Use(metricsRegistry.HTTPMiddleware(), observability.RequestID(), observability.AccessLog(),
		observability.Recovery(), observability.SecurityHeaders(), observability.RateLimit(600, time.Minute))
	_ = r.SetTrustedProxies(nil)

	deps := server.Handlers{
		Events:           eventsClient,
		Auth:             authHandler,
		Project:          projectHandler,
		Ticket:           ticketHandler,
		Workspace:        workspaceHandler,
		Search:           searchHandler,
		GiteaWebhook:     giteaWebhook,
		ArgoWebhook:      argoWebhook,
		GenericWebhook:   genericWebhook,
		SSEManager:       sseManager,
		TicketStore:      ticketStore,
		Sessions:         sessionStore,
		Audit:            audit.NewHandler(auditLogger),
		Pool:             pool,
		UserInvalidation: eventsClient.UserInvalidation(),
		BuildInfo: server.BuildInfo{
			Version: version,
			Commit:  commit,
			BuiltAt: buildDate,
		},
	}
	server.Register(r, deps)

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// WriteTimeout is intentionally zero: SSE connections are long-lived.
	}

	// Metrics are served on a separate listener. The public API port never
	// exposes /metrics; compose keeps the port on the internal network and
	// METRICS_TOKEN optionally adds bearer authentication.
	var metricsSrv *http.Server
	if metricsAddr := envOr("METRICS_ADDR", ""); metricsAddr != "" {
		metricsSrv = &http.Server{
			Addr:              metricsAddr,
			Handler:           metricsRegistry.Handler(os.Getenv("METRICS_TOKEN")),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      30 * time.Second,
		}
		go func() {
			if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("metrics server: %v", err)
			}
		}()
		log.Printf("SPM metrics on %s/metrics", metricsAddr)
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
	if metricsSrv != nil {
		if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
			log.Printf("metrics shutdown: %v", err)
		}
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
