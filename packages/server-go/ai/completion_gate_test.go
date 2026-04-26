package ai

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// ── RunBehavioralGate ─────────────────────────────────────────────────────────

func TestRunBehavioralGate_MissingScript_Skipped(t *testing.T) {
	dir := t.TempDir()
	result := RunBehavioralGate(dir)

	if !result.Skipped {
		t.Errorf("expected Skipped=true when script missing, got Passed=%v Skipped=%v SkipReason=%q",
			result.Passed, result.Skipped, result.SkipReason)
	}
	if result.SkipReason == "" {
		t.Error("expected non-empty SkipReason")
	}
	if result.RanAt == "" {
		t.Error("expected non-empty RanAt")
	}
}

func TestRunBehavioralGate_ScriptExits0_NoJSON_Passed(t *testing.T) {
	dir := t.TempDir()
	scriptDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Script that exits 0 but emits no JSON.
	scriptPath := filepath.Join(scriptDir, "behavioral-completion-check.mjs")
	if err := os.WriteFile(scriptPath, []byte("process.exit(0);\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	result := RunBehavioralGate(dir)

	if !result.Passed {
		t.Errorf("expected Passed=true for zero-exit script, got Passed=%v Skipped=%v", result.Passed, result.Skipped)
	}
	if result.DurationMs < 0 {
		t.Errorf("expected non-negative DurationMs, got %d", result.DurationMs)
	}
}

func TestRunBehavioralGate_ScriptExits0_WithJSON_Parsed(t *testing.T) {
	dir := t.TempDir()
	scriptDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(BehavioralGateResult{
		Passed:          true,
		ConsoleErrors:   nil,
		ScreenshotPaths: []string{"/tmp/screen.png"},
	})
	script := "process.stdout.write(" + string(rune(96)) + string(payload) + "\\n" + string(rune(96)) + "); process.exit(0);\n"
	// Use a simpler approach — embed the JSON as a console.log call.
	script = "console.log('" + string(payload) + "'); process.exit(0);\n"
	scriptPath := filepath.Join(scriptDir, "behavioral-completion-check.mjs")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	result := RunBehavioralGate(dir)

	if !result.Passed {
		t.Errorf("expected Passed=true from parsed JSON, got %+v", result)
	}
	if len(result.ScreenshotPaths) != 1 {
		t.Errorf("expected 1 screenshot path parsed, got %d", len(result.ScreenshotPaths))
	}
}

func TestRunBehavioralGate_ScriptExits1_Failure(t *testing.T) {
	dir := t.TempDir()
	scriptDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "console.error('nav failed'); process.exit(1);\n"
	scriptPath := filepath.Join(scriptDir, "behavioral-completion-check.mjs")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	result := RunBehavioralGate(dir)

	if result.Passed {
		t.Error("expected Passed=false for exit-1 script")
	}
	if result.Skipped {
		t.Error("expected Skipped=false for exit-1 failure")
	}
}

func TestRunBehavioralGate_ScriptExits1_PlaywrightNotFound_Skipped(t *testing.T) {
	dir := t.TempDir()
	scriptDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "console.error('playwright not found in PATH'); process.exit(1);\n"
	scriptPath := filepath.Join(scriptDir, "behavioral-completion-check.mjs")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	result := RunBehavioralGate(dir)

	if !result.Skipped {
		t.Errorf("expected Skipped=true for missing Playwright, got Passed=%v Skipped=%v ConsoleErrors=%v",
			result.Passed, result.Skipped, result.ConsoleErrors)
	}
}

// ── firstLine ─────────────────────────────────────────────────────────────────

func TestFirstLine_SingleLine(t *testing.T) {
	if got := firstLine("hello"); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}

func TestFirstLine_MultiLine(t *testing.T) {
	if got := firstLine("first\nsecond\nthird"); got != "first" {
		t.Errorf("expected 'first', got %q", got)
	}
}

func TestFirstLine_Empty(t *testing.T) {
	if got := firstLine(""); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// TestRunBehavioralGate_NodeNotFound covers the `if _, err := nodeLookPath("node"); err != nil`
// return block by injecting a mock that returns an error.
func TestRunBehavioralGate_NodeNotFound_Skipped(t *testing.T) {
	origLook := nodeLookPath
	t.Cleanup(func() { nodeLookPath = origLook })
	nodeLookPath = func(string) (string, error) {
		return "", errors.New("node not found")
	}

	dir := t.TempDir()
	scriptDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Script must exist so the stat check passes.
	scriptPath := filepath.Join(scriptDir, "behavioral-completion-check.mjs")
	if err := os.WriteFile(scriptPath, []byte("process.exit(0);\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	result := RunBehavioralGate(dir)

	if !result.Skipped {
		t.Errorf("expected Skipped=true when node not in PATH, got Passed=%v Skipped=%v", result.Passed, result.Skipped)
	}
	if result.SkipReason == "" {
		t.Error("expected non-empty SkipReason")
	}
}

// TestRunBehavioralGate_InvalidJSON_FallsThrough covers the branch where a line
// starts with '{' but JSON unmarshal fails — the loop continues and the function
// falls through to the default Passed:true return.
func TestRunBehavioralGate_InvalidJSON_FallsThrough(t *testing.T) {
	dir := t.TempDir()
	scriptDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Output a line starting with '{' but not valid JSON, then exit 0.
	script := "console.log('{not valid json at all}'); process.exit(0);\n"
	scriptPath := filepath.Join(scriptDir, "behavioral-completion-check.mjs")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	result := RunBehavioralGate(dir)

	// Invalid JSON is skipped; no valid JSON found → default Passed:true.
	if !result.Passed {
		t.Errorf("expected Passed=true when JSON is malformed, got Passed=%v Skipped=%v", result.Passed, result.Skipped)
	}
}
