//go:build integration

package integration

import (
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Memory limit constants mirroring docker-compose.yaml (Requirement 10)
// ---------------------------------------------------------------------------

// memoryLimits maps container name patterns to their configured memory limit
// in megabytes as defined in docker-compose.yaml.
var memoryLimits = map[string]int64{
	"api_backend": 256,
	"postgres":    256,
	"nats":        128,
	"gitea":       512,
	"argocd":      256,
	"act_runner":  64,
	"ui_envoy":    32,
	"ui_static":   32,
}

// totalBudgetMB is the maximum total idle memory across all services (Req 10.1).
const totalBudgetMB int64 = 1536

// ---------------------------------------------------------------------------
// dockerStats holds a single container's memory usage from `docker stats`.
// ---------------------------------------------------------------------------

type dockerStats struct {
	Name     string `json:"Name"`
	MemUsage string `json:"MemUsage"` // e.g. "45.6MiB / 256MiB"
}

// parseMemoryMiB parses a memory string like "45.6MiB" into MiB as float64.
func parseMemoryMiB(s string) float64 {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)

	multiplier := 1.0
	switch {
	case strings.HasSuffix(s, "gib"):
		multiplier = 1024
		s = strings.TrimSuffix(s, "gib")
	case strings.HasSuffix(s, "mib"):
		s = strings.TrimSuffix(s, "mib")
	case strings.HasSuffix(s, "kib"):
		multiplier = 1.0 / 1024
		s = strings.TrimSuffix(s, "kib")
	case strings.HasSuffix(s, "gb"):
		multiplier = 1024
		s = strings.TrimSuffix(s, "gb")
	case strings.HasSuffix(s, "mb"):
		s = strings.TrimSuffix(s, "mb")
	case strings.HasSuffix(s, "kb"):
		multiplier = 1.0 / 1024
		s = strings.TrimSuffix(s, "kb")
	case strings.HasSuffix(s, "b"):
		multiplier = 1.0 / (1024 * 1024)
		s = strings.TrimSuffix(s, "b")
	}

	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v * multiplier
}

// ---------------------------------------------------------------------------
// TestResourceLimits_IdleMemoryWithinBudget
//
// After 60 seconds of idle (no active requests), queries `docker stats` for
// each container and asserts:
//  1. Each container's memory usage ≤ its configured limit.
//  2. The sum of all configured limits ≤ totalBudgetMB (1,536 MB).
//
// Requirement 10.1, 10.7, 10.8
// ---------------------------------------------------------------------------

func TestResourceLimits_IdleMemoryWithinBudget(t *testing.T) {
	skipIfNoStack(t)

	// docker must be available for this test.
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker binary not found in PATH — skipping resource limit test")
	}

	// Wait 60 seconds with no activity to reach idle state (Req 10.1 definition).
	t.Log("Waiting 60s for stack to reach idle state...")
	time.Sleep(60 * time.Second)

	// Query docker stats for all running containers (one-shot, no stream).
	out, err := exec.Command(
		"docker", "stats", "--no-stream", "--format", "{{json .}}",
	).Output()
	require.NoError(t, err, "docker stats should succeed")

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")

	type containerMem struct {
		name  string
		usage float64 // MiB
		limit int64   // MiB from memoryLimits map
	}

	var containers []containerMem

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var stat dockerStats
		if err := json.Unmarshal([]byte(line), &stat); err != nil {
			continue
		}

		// Match container name against our known services.
		for svc, limit := range memoryLimits {
			if strings.Contains(strings.ToLower(stat.Name), svc) {
				// MemUsage format: "45.6MiB / 256MiB" — we want the left side.
				parts := strings.SplitN(stat.MemUsage, "/", 2)
				usedMiB := parseMemoryMiB(parts[0])
				containers = append(containers, containerMem{
					name:  stat.Name,
					usage: usedMiB,
					limit: limit,
				})
				break
			}
		}
	}

	// Assert each container is within its individual limit.
	var totalUsed float64
	for _, c := range containers {
		t.Logf("Container %s: %.1f MiB used / %d MiB limit", c.name, c.usage, c.limit)
		assert.LessOrEqualf(t, c.usage, float64(c.limit),
			"container %s uses %.1f MiB which exceeds its %d MiB limit", c.name, c.usage, c.limit)
		totalUsed += c.usage
	}

	// Assert total configured memory limits do not exceed budget.
	var totalConfiguredLimits int64
	for _, limit := range memoryLimits {
		totalConfiguredLimits += limit
	}
	t.Logf("Total configured limits: %d MB (budget: %d MB)", totalConfiguredLimits, totalBudgetMB)
	assert.LessOrEqualf(t, totalConfiguredLimits, totalBudgetMB,
		"sum of all memory limits (%d MB) exceeds budget (%d MB)", totalConfiguredLimits, totalBudgetMB)

	t.Logf("Total actual memory used at idle: %.1f MiB across %d containers",
		totalUsed, len(containers))
}

// ---------------------------------------------------------------------------
// TestResourceLimits_ConfiguredLimitsSum — unit-style check (no Docker needed)
//
// Verifies that the hardcoded limits in this file sum to ≤ 1,536 MB.
// This test runs even without the full stack.
// ---------------------------------------------------------------------------

func TestResourceLimits_ConfiguredLimitsSum(t *testing.T) {
	var total int64
	for svc, limit := range memoryLimits {
		t.Logf("  %-15s %d MB", svc, limit)
		total += limit
	}
	t.Logf("Total: %d MB / budget %d MB", total, totalBudgetMB)
	assert.LessOrEqual(t, total, totalBudgetMB,
		"configured memory limits (%d MB) must not exceed %d MB budget", total, totalBudgetMB)
}
