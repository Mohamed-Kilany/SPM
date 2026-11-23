package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigFromEnvDefaults(t *testing.T) {
	for _, key := range []string{
		"MIGRATION_LOCK_TIMEOUT",
		"MIGRATION_STATEMENT_TIMEOUT",
		"MIGRATION_BACKUP_PATH",
		"MIGRATION_BACKUP_SHA256",
		"MIGRATION_REQUIRE_BACKUP",
	} {
		t.Setenv(key, "")
	}

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.LockTimeout != defaultLockTimeout {
		t.Errorf("LockTimeout = %s, want %s", cfg.LockTimeout, defaultLockTimeout)
	}
	if cfg.StatementTimeout != defaultStatementTimeout {
		t.Errorf("StatementTimeout = %s, want %s", cfg.StatementTimeout, defaultStatementTimeout)
	}
	if cfg.RequireBackup {
		t.Error("RequireBackup = true, want false by default")
	}
	if cfg.BackupPath != "" || cfg.BackupSHA256 != "" {
		t.Errorf("backup settings = %q/%q, want empty", cfg.BackupPath, cfg.BackupSHA256)
	}
}

func TestConfigFromEnvOverrides(t *testing.T) {
	t.Setenv("MIGRATION_LOCK_TIMEOUT", "750ms")
	t.Setenv("MIGRATION_STATEMENT_TIMEOUT", "45s")
	t.Setenv("MIGRATION_BACKUP_PATH", " C:/backups/spm.dump ")
	t.Setenv("MIGRATION_BACKUP_SHA256", " ABCDEF ")
	t.Setenv("MIGRATION_REQUIRE_BACKUP", "true")

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.LockTimeout != 750*time.Millisecond {
		t.Errorf("LockTimeout = %s, want 750ms", cfg.LockTimeout)
	}
	if cfg.StatementTimeout != 45*time.Second {
		t.Errorf("StatementTimeout = %s, want 45s", cfg.StatementTimeout)
	}
	if cfg.BackupPath != "C:/backups/spm.dump" {
		t.Errorf("BackupPath = %q, want trimmed path", cfg.BackupPath)
	}
	if cfg.BackupSHA256 != "ABCDEF" {
		t.Errorf("BackupSHA256 = %q, want trimmed digest", cfg.BackupSHA256)
	}
	if !cfg.RequireBackup {
		t.Error("RequireBackup = false, want true")
	}
}

func TestConfigFromEnvRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		key   string
		value string
	}{
		{key: "MIGRATION_LOCK_TIMEOUT", value: "soon"},
		{key: "MIGRATION_LOCK_TIMEOUT", value: "0s"},
		{key: "MIGRATION_LOCK_TIMEOUT", value: "-1s"},
		{key: "MIGRATION_STATEMENT_TIMEOUT", value: "2 minutes"},
		{key: "MIGRATION_STATEMENT_TIMEOUT", value: "-5s"},
		{key: "MIGRATION_REQUIRE_BACKUP", value: "yes-please"},
	}
	for _, test := range tests {
		t.Run(test.key+"="+test.value, func(t *testing.T) {
			t.Setenv(test.key, test.value)
			if _, err := ConfigFromEnv(); err == nil {
				t.Fatalf("ConfigFromEnv accepted %s=%q", test.key, test.value)
			}
		})
	}
}

func TestVerifyBackupAllowsUnconfiguredLocalRuns(t *testing.T) {
	if err := verifyBackup(Config{}); err != nil {
		t.Fatalf("verifyBackup without a backup: %v", err)
	}
}

func TestVerifyBackupRequiresPathInProduction(t *testing.T) {
	err := verifyBackup(Config{RequireBackup: true})
	if err == nil || !strings.Contains(err.Error(), "MIGRATION_BACKUP_PATH") {
		t.Fatalf("verifyBackup error = %v, want missing path error", err)
	}
}

func TestVerifyBackupChecksFileAndDigest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spm.dump")
	if err := os.WriteFile(path, []byte("pre-migration backup"), 0o600); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	digest := sha256.Sum256([]byte("pre-migration backup"))
	good := hex.EncodeToString(digest[:])

	t.Run("matching digest", func(t *testing.T) {
		if err := verifyBackup(Config{BackupPath: path, BackupSHA256: "sha256:" + strings.ToUpper(good)}); err != nil {
			t.Fatalf("verifyBackup: %v", err)
		}
	})

	t.Run("matching sidecar", func(t *testing.T) {
		sidecar := path + ".sha256"
		if err := os.WriteFile(sidecar, []byte(good+"  spm.dump\n"), 0o600); err != nil {
			t.Fatalf("write sidecar: %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(sidecar) })
		if err := verifyBackup(Config{BackupPath: path}); err != nil {
			t.Fatalf("verifyBackup: %v", err)
		}
	})

	t.Run("mismatched digest", func(t *testing.T) {
		wrong := strings.Repeat("0", 64)
		err := verifyBackup(Config{BackupPath: path, BackupSHA256: wrong})
		if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
			t.Fatalf("verifyBackup error = %v, want mismatch", err)
		}
	})

	t.Run("malformed inline digest", func(t *testing.T) {
		err := verifyBackup(Config{BackupPath: path, BackupSHA256: "not-a-digest"})
		if err == nil || !strings.Contains(err.Error(), "not a valid SHA-256") {
			t.Fatalf("verifyBackup error = %v, want invalid digest", err)
		}
	})

	t.Run("malformed sidecar", func(t *testing.T) {
		sidecar := path + ".sha256"
		if err := os.WriteFile(sidecar, []byte("garbage\n"), 0o600); err != nil {
			t.Fatalf("write sidecar: %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(sidecar) })
		err := verifyBackup(Config{BackupPath: path})
		if err == nil || !strings.Contains(err.Error(), "does not start with a SHA-256") {
			t.Fatalf("verifyBackup error = %v, want invalid sidecar", err)
		}
	})

	t.Run("empty sidecar", func(t *testing.T) {
		sidecar := path + ".sha256"
		if err := os.WriteFile(sidecar, nil, 0o600); err != nil {
			t.Fatalf("write sidecar: %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(sidecar) })
		err := verifyBackup(Config{BackupPath: path})
		if err == nil || !strings.Contains(err.Error(), "sidecar") || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("verifyBackup error = %v, want empty sidecar", err)
		}
	})

	t.Run("required without digest", func(t *testing.T) {
		err := verifyBackup(Config{BackupPath: path, RequireBackup: true})
		if err == nil || !strings.Contains(err.Error(), "has no SHA-256") {
			t.Fatalf("verifyBackup error = %v, want missing digest", err)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		err := verifyBackup(Config{BackupPath: filepath.Join(dir, "missing.dump")})
		if err == nil || !strings.Contains(err.Error(), "inspect backup") {
			t.Fatalf("verifyBackup error = %v, want inspect error", err)
		}
	})

	t.Run("directory", func(t *testing.T) {
		err := verifyBackup(Config{BackupPath: dir})
		if err == nil || !strings.Contains(err.Error(), "is a directory") {
			t.Fatalf("verifyBackup error = %v, want directory error", err)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		empty := filepath.Join(dir, "empty.dump")
		if err := os.WriteFile(empty, nil, 0o600); err != nil {
			t.Fatalf("write empty backup: %v", err)
		}
		err := verifyBackup(Config{BackupPath: empty})
		if err == nil || !strings.Contains(err.Error(), "is empty") {
			t.Fatalf("verifyBackup error = %v, want empty error", err)
		}
	})
}

func TestNormalizeDigest(t *testing.T) {
	digest := strings.Repeat("a1", 32)
	if got := normalizeDigest("sha256:" + strings.ToUpper(digest)); got != digest {
		t.Errorf("normalizeDigest = %q, want %q", got, digest)
	}
	for _, invalid := range []string{"", "sha256:", digest[:10], strings.Repeat("z", 64)} {
		if got := normalizeDigest(invalid); got != "" {
			t.Errorf("normalizeDigest(%q) = %q, want empty", invalid, got)
		}
	}
}

func TestHasNoTransactionDirective(t *testing.T) {
	if hasNoTransactionDirective("CREATE TABLE t (id int);") {
		t.Error("plain migration reported as non-transactional")
	}
	if !hasNoTransactionDirective("-- migrate:no-transaction\nCREATE INDEX CONCURRENTLY i ON t (id);") {
		t.Error("directive was not detected")
	}
	if hasNoTransactionDirective("-- migrate:no-transaction-please") {
		t.Error("a longer comment must not match the directive")
	}
	if !hasNoTransactionDirective("  -- migrate:no-transaction  \r\nSELECT 1;") {
		t.Error("directive with surrounding whitespace was not detected")
	}
}

func TestDurationLiteral(t *testing.T) {
	if got := durationLiteral(1500 * time.Millisecond); got != "1500ms" {
		t.Errorf("durationLiteral = %q, want 1500ms", got)
	}
}

func TestRunRejectsInvalidEnvironmentBeforeConnecting(t *testing.T) {
	t.Setenv("MIGRATION_LOCK_TIMEOUT", "not-a-duration")
	err := Run(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "MIGRATION_LOCK_TIMEOUT") {
		t.Fatalf("Run error = %v, want configuration error", err)
	}
}

func TestRunWithConfigRejectsNilPool(t *testing.T) {
	if err := RunWithConfig(context.Background(), nil, Config{}); err == nil {
		t.Fatal("RunWithConfig accepted a nil pool")
	}
}
