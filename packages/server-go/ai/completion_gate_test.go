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

// ── WriteProjectProfileCache ──────────────────────────────────────────────────

func TestWriteProjectProfileCache_NilProfile_Noop(t *testing.T) {
	dir := t.TempDir()
	if err := WriteProjectProfileCache(dir, nil); err != nil {
		t.Fatalf("nil profile should be a noop, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".cache", "project-profile.json")); !os.IsNotExist(err) {
		t.Error("expected no file to be written for nil profile")
	}
}

func TestWriteProjectProfileCache_WritesJSON(t *testing.T) {
	dir := t.TempDir()
	profile := &ProjectProfile{
		ProjectPath:  dir,
		Type:         ProjectTypeWebApp,
		DeployTarget: "Vercel",
		DoneDefinition: []string{"checkout works"},
		Verification: VerificationStrategy{
			UsesPlaywright: true,
			Port:           3000,
			CheckURL:       "http://localhost:3000",
		},
	}

	if err := WriteProjectProfileCache(dir, profile); err != nil {
		t.Fatalf("WriteProjectProfileCache: %v", err)
	}

	dest := filepath.Join(dir, ".cache", "project-profile.json")
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("expected file at %s, got %v", dest, err)
	}

	var parsed ProjectProfile
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("written JSON is not valid: %v", err)
	}
	if parsed.Type != ProjectTypeWebApp {
		t.Errorf("type = %q, want web-app", parsed.Type)
	}
	if !parsed.Verification.UsesPlaywright {
		t.Error("usesPlaywright should be true")
	}
}

func TestWriteProjectProfileCache_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	profile := &ProjectProfile{Type: ProjectTypeCLI}

	if err := WriteProjectProfileCache(dir, profile); err != nil {
		t.Fatalf("WriteProjectProfileCache: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".cache")); err != nil {
		t.Errorf("expected .cache dir to be created: %v", err)
	}
}

func TestWriteProjectProfileCache_MkdirError(t *testing.T) {
	base := t.TempDir()
	projectFile := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(projectFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := WriteProjectProfileCache(projectFile, &ProjectProfile{Type: ProjectTypeCLI})
	if err == nil {
		t.Fatal("expected mkdir error, got nil")
	}
}

func TestWriteProjectProfileCache_WriteError(t *testing.T) {
	base := t.TempDir()
	projectDir := filepath.Join(base, "project")
	if err := os.MkdirAll(filepath.Join(projectDir, ".cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(projectDir, ".cache"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(projectDir, ".cache"), 0o755)
	})

	err := WriteProjectProfileCache(projectDir, &ProjectProfile{Type: ProjectTypeCLI})
	if err == nil {
		t.Fatal("expected write error with read-only cache dir, got nil")
	}
}
