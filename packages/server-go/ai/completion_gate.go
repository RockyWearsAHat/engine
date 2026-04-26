package ai

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// BehavioralGateResult holds the outcome of a UI-level behavioral validation pass.
// It is written to the agent completion report so the static completion gate can
// verify that behavioral checks actually ran and passed.
type BehavioralGateResult struct {
	// Passed is true when the behavioral check ran and found no blocking issues.
	Passed bool `json:"passed"`
	// Skipped is true when the check could not run (e.g., Playwright not installed).
	// A skipped gate is not the same as a failed gate — it does not block completion,
	// but it is recorded so the next agent knows the check was not performed.
	Skipped bool `json:"skipped"`
	// SkipReason describes why the gate was skipped, if Skipped is true.
	SkipReason string `json:"skipReason,omitempty"`
	// ConsoleErrors is the list of browser-console errors captured during the run.
	ConsoleErrors []string `json:"consoleErrors,omitempty"`
	// ScreenshotPaths are paths to screenshots taken at key checkpoints.
	ScreenshotPaths []string `json:"screenshotPaths,omitempty"`
	// DurationMs is wall-clock duration of the behavioral run in milliseconds.
	DurationMs int64 `json:"durationMs"`
	// RanAt is the ISO-8601 timestamp when the gate ran.
	RanAt string `json:"ranAt"`
}

// nodeLookPath is injectable for tests to simulate node not being in PATH.
var nodeLookPath = exec.LookPath

// RunBehavioralGate runs the behavioral completion check script (Playwright) from
// the repository root. If the script is not present or Playwright is not installed,
// the gate is marked Skipped rather than Failed so it does not block a release.
//
// projectPath must be the absolute path to the repository root.
func RunBehavioralGate(projectPath string) BehavioralGateResult {
	start := time.Now()
	ranAt := start.UTC().Format(time.RFC3339)

	scriptPath := filepath.Join(projectPath, "scripts", "behavioral-completion-check.mjs")
	if _, err := os.Stat(scriptPath); err != nil {
		return BehavioralGateResult{
			Skipped:    true,
			SkipReason: "behavioral-completion-check.mjs not found",
			DurationMs: time.Since(start).Milliseconds(),
			RanAt:      ranAt,
		}
	}

	// Check that node is available.
	if _, err := nodeLookPath("node"); err != nil {
		return BehavioralGateResult{
			Skipped:    true,
			SkipReason: "node not found in PATH",
			DurationMs: time.Since(start).Milliseconds(),
			RanAt:      ranAt,
		}
	}

	cmd := exec.Command("node", scriptPath)
	cmd.Dir = projectPath
	out, err := cmd.CombinedOutput()
	durationMs := time.Since(start).Milliseconds()

	outputStr := strings.TrimSpace(string(out))

	if err != nil {
		// Script exited non-zero.  If output looks like "playwright not found" or
		// a module-not-found error, treat as Skipped so it doesn't hard-block.
		lower := strings.ToLower(outputStr)
		if strings.Contains(lower, "cannot find module") ||
			strings.Contains(lower, "playwright") && strings.Contains(lower, "not found") {
			return BehavioralGateResult{
				Skipped:    true,
				SkipReason: "Playwright not installed: " + firstLine(outputStr),
				DurationMs: durationMs,
				RanAt:      ranAt,
			}
		}
		// Real failure.
		return BehavioralGateResult{
			Passed:        false,
			ConsoleErrors: []string{outputStr},
			DurationMs:    durationMs,
			RanAt:         ranAt,
		}
	}

	// Try to parse structured JSON from the last line of output.
	lines := strings.Split(outputStr, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var parsed BehavioralGateResult
		if jsonErr := json.Unmarshal([]byte(line), &parsed); jsonErr == nil {
			parsed.DurationMs = durationMs
			parsed.RanAt = ranAt
			return parsed
		}
	}

	// Script exited 0 but produced no JSON — treat as passed.
	return BehavioralGateResult{
		Passed:     true,
		DurationMs: durationMs,
		RanAt:      ranAt,
	}
}

func firstLine(s string) string {
	if idx := strings.Index(s, "\n"); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return s
}
