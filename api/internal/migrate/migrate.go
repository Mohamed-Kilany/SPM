// Package migrate applies the embedded, idempotent PostgreSQL schema at API
// startup. It is safe to run against databases created by docker-entrypoint
// initdb as well as existing production databases.
package migrate

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"spm-platform/api/internal/metrics"
)

//go:embed sql/*.sql
var migrationFS embed.FS

const (
	defaultLockTimeout      = 5 * time.Second
	defaultStatementTimeout = 2 * time.Minute
	noTransactionDirective  = "-- migrate:no-transaction"
)

// Config controls migration safety. Production deployments should set
// MIGRATION_REQUIRE_BACKUP=true and point MIGRATION_BACKUP_PATH at a verified
// pre-migration backup.
type Config struct {
	LockTimeout      time.Duration
	StatementTimeout time.Duration
	BackupPath       string
	BackupSHA256     string
	RequireBackup    bool
}

// ConfigFromEnv reads migration safety settings from the environment.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		LockTimeout:      defaultLockTimeout,
		StatementTimeout: defaultStatementTimeout,
		BackupPath:       strings.TrimSpace(os.Getenv("MIGRATION_BACKUP_PATH")),
		BackupSHA256:     strings.TrimSpace(os.Getenv("MIGRATION_BACKUP_SHA256")),
	}
	requireBackup, err := envBool("MIGRATION_REQUIRE_BACKUP", false)
	if err != nil {
		return Config{}, err
	}
	cfg.RequireBackup = requireBackup

	if raw := strings.TrimSpace(os.Getenv("MIGRATION_LOCK_TIMEOUT")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("migrate: MIGRATION_LOCK_TIMEOUT must be a positive duration, got %q", raw)
		}
		cfg.LockTimeout = d
	}
	if raw := strings.TrimSpace(os.Getenv("MIGRATION_STATEMENT_TIMEOUT")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("migrate: MIGRATION_STATEMENT_TIMEOUT must be a positive duration, got %q", raw)
		}
		cfg.StatementTimeout = d
	}
	return cfg, nil
}

// Run applies every unapplied migration in lexical order using the environment
// configuration.
func Run(ctx context.Context, pool *pgxpool.Pool) error {
	cfg, err := ConfigFromEnv()
	if err != nil {
		return err
	}
	return RunWithConfig(ctx, pool, cfg)
}

// RunWithConfig applies every unapplied migration in lexical order.
func RunWithConfig(ctx context.Context, pool *pgxpool.Pool, cfg Config) error {
	if pool == nil {
		return errors.New("migrate: nil pool")
	}
	if err := verifyBackup(cfg); err != nil {
		return err
	}
	if err := ensureSchemaMigrations(ctx, pool); err != nil {
		return err
	}
	if err := ensureBackfillTable(ctx, pool); err != nil {
		return err
	}

	names, err := embeddedMigrationNames()
	if err != nil {
		return err
	}
	for _, name := range names {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, name,
		).Scan(&exists); err != nil {
			return fmt.Errorf("migrate: check %s: %w", name, err)
		}
		if exists {
			continue
		}
		if err := applyMigration(ctx, pool, cfg, name); err != nil {
			return err
		}
	}
	return nil
}

func embeddedMigrationNames() ([]string, error) {
	entries, err := fs.ReadDir(migrationFS, "sql")
	if err != nil {
		return nil, fmt.Errorf("migrate: read embedded migrations: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		// 000 is a psql-only database bootstrap and is executed by initdb.
		if strings.HasPrefix(entry.Name(), "000_") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

func applyMigration(ctx context.Context, pool *pgxpool.Pool, cfg Config, name string) (err error) {
	body, err := migrationFS.ReadFile("sql/" + name)
	if err != nil {
		return fmt.Errorf("migrate: read %s: %w", name, err)
	}

	start := time.Now()
	defer func() {
		status := "success"
		if err != nil {
			status = "failure"
		}
		metrics.Default().ObserveMigrationDuration(name, status, time.Since(start).Seconds())
		if err == nil {
			metrics.Default().ObserveMigrationSuccess()
		}
	}()

	if hasNoTransactionDirective(string(body)) {
		err = applyWithoutTransaction(ctx, pool, cfg, name, string(body))
	} else {
		err = applyInTransaction(ctx, pool, cfg, name, string(body))
	}
	if err != nil {
		return err
	}
	log.Printf("migrate: applied %s", name)
	return nil
}

func applyInTransaction(ctx context.Context, pool *pgxpool.Pool, cfg Config, name, body string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate: begin %s: %w", name, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setLocalTimeouts(ctx, tx, cfg); err != nil {
		return fmt.Errorf("migrate: configure timeouts for %s: %w", name, err)
	}
	if _, err := tx.Exec(ctx, body, pgx.QueryExecModeSimpleProtocol); err != nil {
		return fmt.Errorf("migrate: apply %s: %w", name, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING`, name,
	); err != nil {
		return fmt.Errorf("migrate: record %s: %w", name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrate: commit %s: %w", name, err)
	}
	return nil
}

func applyWithoutTransaction(ctx context.Context, pool *pgxpool.Pool, cfg Config, name, body string) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrate: acquire connection for %s: %w", name, err)
	}
	defer conn.Release()

	if err := setSessionTimeouts(ctx, conn, cfg); err != nil {
		return fmt.Errorf("migrate: configure timeouts for %s: %w", name, err)
	}
	defer resetSessionTimeouts(conn)

	if _, err := conn.Exec(ctx, body, pgx.QueryExecModeSimpleProtocol); err != nil {
		return fmt.Errorf("migrate: apply %s: %w", name, err)
	}
	if _, err := conn.Exec(ctx,
		`INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING`, name,
	); err != nil {
		return fmt.Errorf("migrate: record %s: %w", name, err)
	}
	return nil
}

func setLocalTimeouts(ctx context.Context, tx pgx.Tx, cfg Config) error {
	if cfg.LockTimeout > 0 {
		if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '"+durationLiteral(cfg.LockTimeout)+"'"); err != nil {
			return err
		}
	}
	if cfg.StatementTimeout > 0 {
		if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = '"+durationLiteral(cfg.StatementTimeout)+"'"); err != nil {
			return err
		}
	}
	return nil
}

func setSessionTimeouts(ctx context.Context, conn *pgxpool.Conn, cfg Config) error {
	if cfg.LockTimeout > 0 {
		if _, err := conn.Exec(ctx, "SET lock_timeout = '"+durationLiteral(cfg.LockTimeout)+"'"); err != nil {
			return err
		}
	}
	if cfg.StatementTimeout > 0 {
		if _, err := conn.Exec(ctx, "SET statement_timeout = '"+durationLiteral(cfg.StatementTimeout)+"'"); err != nil {
			return err
		}
	}
	return nil
}

func resetSessionTimeouts(conn *pgxpool.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, statement := range []string{"RESET lock_timeout", "RESET statement_timeout"} {
		if _, err := conn.Exec(ctx, statement); err != nil {
			log.Printf("migrate: %s failed: %v", statement, err)
		}
	}
}

func durationLiteral(d time.Duration) string {
	return strconv.FormatInt(d.Milliseconds(), 10) + "ms"
}

func hasNoTransactionDirective(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == noTransactionDirective {
			return true
		}
	}
	return false
}

func ensureSchemaMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    VARCHAR(255) PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		return fmt.Errorf("migrate: create schema_migrations: %w", err)
	}
	return nil
}

func ensureBackfillTable(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS migration_backfills (
			name         TEXT PRIMARY KEY,
			cursor       TEXT NOT NULL DEFAULT '',
			completed_at TIMESTAMPTZ,
			updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		return fmt.Errorf("migrate: create migration_backfills: %w", err)
	}
	return nil
}

// verifyBackup requires a non-empty backup and, when a digest is available,
// verifies it before any migration runs. Production should set
// MIGRATION_REQUIRE_BACKUP=true; local development only logs a warning.
func verifyBackup(cfg Config) error {
	path := strings.TrimSpace(cfg.BackupPath)
	if path == "" {
		if cfg.RequireBackup {
			return errors.New("migrate: MIGRATION_BACKUP_PATH is required when MIGRATION_REQUIRE_BACKUP is true")
		}
		log.Printf("migrate: no pre-migration backup configured; set MIGRATION_BACKUP_PATH in production")
		return nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("migrate: inspect backup %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("migrate: backup %s is a directory", path)
	}
	if info.Size() == 0 {
		return fmt.Errorf("migrate: backup %s is empty", path)
	}

	expected := normalizeDigest(cfg.BackupSHA256)
	if strings.TrimSpace(cfg.BackupSHA256) != "" {
		if expected == "" {
			return errors.New("migrate: MIGRATION_BACKUP_SHA256 is not a valid SHA-256 digest")
		}
	} else {
		sidecar := path + ".sha256"
		data, readErr := os.ReadFile(sidecar)
		switch {
		case readErr == nil:
			fields := strings.Fields(string(data))
			if len(fields) == 0 {
				return fmt.Errorf("migrate: backup sidecar %s is empty", sidecar)
			}
			expected = normalizeDigest(fields[0])
			if expected == "" {
				return fmt.Errorf("migrate: backup sidecar %s does not start with a SHA-256 digest", sidecar)
			}
		case !errors.Is(readErr, os.ErrNotExist):
			return fmt.Errorf("migrate: read backup sidecar %s: %w", sidecar, readErr)
		}
	}
	if expected == "" {
		if cfg.RequireBackup {
			return fmt.Errorf("migrate: backup %s has no SHA-256; set MIGRATION_BACKUP_SHA256 or provide %s.sha256", path, path)
		}
		log.Printf("migrate: backup %s has no SHA-256; continuing because MIGRATION_REQUIRE_BACKUP is false", path)
		return nil
	}

	actual, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) != 1 {
		return fmt.Errorf("migrate: backup %s SHA-256 mismatch (expected %s, got %s)", path, expected, actual)
	}
	log.Printf("migrate: verified backup %s (sha256 %s)", path, actual)
	return nil
}

func normalizeDigest(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "sha256:")
	if len(value) != sha256.Size*2 {
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return value
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("migrate: open backup %s: %w", path, err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("migrate: hash backup %s: %w", path, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func envBool(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("migrate: %s must be a boolean, got %q", key, raw)
	}
	return value, nil
}
