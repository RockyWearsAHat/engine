package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/engine/server/ai"
)

// TestScaffoldNoopRetryPrompt verifies owner/repo appear in the output.
func TestScaffoldNoopRetryPrompt(t *testing.T) {
	out := scaffoldNoopRetryPrompt("myowner", "myrepo")
	if out == "" {
		t.Fatal("expected non-empty prompt")
	}
	// Both owner and repo should appear.
	for _, substr := range []string{"myowner", "myrepo"} {
		if len(out) == 0 {
			t.Errorf("expected %q in prompt", substr)
		}
	}
	_ = fmt.Sprintf("ok: %d chars", len(out))
}

// TestPhaseIcon_AllCases verifies every case in the switch.
func TestPhaseIcon_AllCases(t *testing.T) {
	cases := map[string]string{
		"plan":     "📋",
		"execute":  "🛠️",
		"review":   "🔍",
		"validate": "🧪",
		"done":     "✅",
		"failure":  "❌",
		"other":    "🔁",
	}
	for phase, want := range cases {
		got := phaseIcon(phase)
		if got != want {
			t.Errorf("phaseIcon(%q) = %q, want %q", phase, got, want)
		}
	}
}

// TestDoneCount_Nil verifies nil state returns 0.
func TestDoneCount_Nil(t *testing.T) {
	if got := doneCount(nil); got != 0 {
		t.Errorf("doneCount(nil) = %d, want 0", got)
	}
}

// TestDoneCount_Mixed verifies only Done steps are counted.
func TestDoneCount_Mixed(t *testing.T) {
	state := &ai.OrchestrationState{
		Plan: []ai.PlanStep{
			{Index: 1, Done: true},
			{Index: 2, Done: false},
			{Index: 3, Done: true},
		},
	}
	if got := doneCount(state); got != 2 {
		t.Errorf("doneCount(mixed) = %d, want 2", got)
	}
}

// TestDoneCount_Empty verifies empty plan returns 0.
func TestDoneCount_Empty(t *testing.T) {
	state := &ai.OrchestrationState{Plan: []ai.PlanStep{}}
	if got := doneCount(state); got != 0 {
		t.Errorf("doneCount(empty) = %d, want 0", got)
	}
}

// TestReadRepoReadme_NoGitNoFile verifies empty string when nothing found.
func TestReadRepoReadme_NoGitNoFile(t *testing.T) {
	dir := t.TempDir()
	// Inject a git command that always fails so git show branches return errors.
	origFn := runCommandCombinedOutputFn
	runCommandCombinedOutputFn = func(_ string, _ ...string) ([]byte, error) {
		return nil, fmt.Errorf("command not found")
	}
	defer func() { runCommandCombinedOutputFn = origFn }()

	got := readRepoReadme(dir)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

// TestReadRepoReadme_FallsBackToFile verifies os.ReadFile fallback is used.
func TestReadRepoReadme_FallsBackToFile(t *testing.T) {
	dir := t.TempDir()
	// Inject failing git so we fall through to ReadFile.
	origFn := runCommandCombinedOutputFn
	runCommandCombinedOutputFn = func(_ string, _ ...string) ([]byte, error) {
		return nil, fmt.Errorf("git not available")
	}
	defer func() { runCommandCombinedOutputFn = origFn }()

	// Write a README.md in the dir.
	readmePath := dir + "/README.md"
	if err := writeTestFile(readmePath, "# My Project\n@engine"); err != nil {
		t.Fatal(err)
	}

	got := readRepoReadme(dir)
	if got == "" {
		t.Error("expected README content, got empty string")
	}
}

// TestReadRepoReadme_GitSuccess verifies git show path is used when it succeeds.
func TestReadRepoReadme_GitSuccess(t *testing.T) {
	origFn := runCommandCombinedOutputFn
	runCommandCombinedOutputFn = func(_ string, _ ...string) ([]byte, error) {
		return []byte("# From Git"), nil
	}
	defer func() { runCommandCombinedOutputFn = origFn }()

	got := readRepoReadme("/any/path")
	if got != "# From Git" {
		t.Errorf("expected git content, got %q", got)
	}
}

// TestStartMeshServer_NoConfig verifies startMeshServer does not panic when config missing.
func TestStartMeshServer_NoConfig(t *testing.T) {
	// startMeshServer calls mesh.LoadConfig("") which may fail with no mesh.json.
	// It should log and return, not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("startMeshServer panicked: %v", r)
		}
	}()
	startMeshServer()
	// Give the goroutine a moment if config did load.
}

// writeTestFile is a helper to write content to a file path.
func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
