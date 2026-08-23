package ai

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/engine/server/db"
)

// TestOrchestratorBuildStep_ClaudeCode_Live is the full-stack integration proof:
// it drives the REAL orchestrator build primitive (orchestratorBuildStep) →
// real Chat pipeline → provider resolution → claudecodeProvider → `claude -p`,
// and verifies that an actual, tested Go module lands on disk. This is the
// "does Engine actually build software through the Claude Code backend" test.
//
// Gated behind ENGINE_LIVE_CLAUDECODE=1 (uses the local `claude` subscription).
//
//	ENGINE_LIVE_CLAUDECODE=1 GOWORK=off go test ./ai/ -run TestOrchestratorBuildStep_ClaudeCode_Live -v -timeout 600s
func TestOrchestratorBuildStep_ClaudeCode_Live(t *testing.T) {
	if os.Getenv("ENGINE_LIVE_CLAUDECODE") != "1" {
		t.Skip("set ENGINE_LIVE_CLAUDECODE=1 to run the live orchestrator+ClaudeCode test")
	}
	if _, err := exec.LookPath(claudeBinary); err != nil {
		t.Skipf("claude CLI not on PATH: %v", err)
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not on PATH: %v", err)
	}

	dir := t.TempDir()
	if err := db.Init(dir); err != nil {
		t.Fatalf("db.Init: %v", err)
	}

	// Route the WHOLE orchestrator → Chat path through the Claude Code backend.
	// No ANTHROPIC_API_KEY is set; this proves the subscription-backed path.
	t.Setenv("ENGINE_MODEL_PROVIDER", "claudecode")
	t.Setenv("ENGINE_MODEL", "claude-opus-4-8")
	t.Setenv("ANTHROPIC_API_KEY", "")

	cfg := OrchestratorConfig{
		ProjectPath: dir,
		Owner:       "engine-test",
		Repo:        "calc",
	}
	state := &OrchestrationState{
		Owner: "engine-test",
		Repo:  "calc",
		Brief: "A tiny Go calculator library.",
		Plan: []PlanStep{{
			Index:      1,
			Title:      "Add function with a passing test",
			Body:       "Initialize a Go module named `calc` with `go mod init calc`. Create add.go with `package calc` and an exported `func Add(a, b int) int` returning a+b. Create add_test.go with a Go test that asserts Add(2,3)==5. Run `GOWORK=off go test ./...` and confirm it passes.",
			Acceptance: "`GOWORK=off go test ./...` exits 0 with the Add test passing",
		}},
	}

	done := make(chan struct{})
	var buildErr error
	start := time.Now()
	go func() {
		defer close(done)
		buildErr = orchestratorBuildStep(cfg, state, &state.Plan[0], "", nil)
	}()
	select {
	case <-done:
	case <-time.After(570 * time.Second):
		t.Fatal("orchestratorBuildStep did not finish within timeout")
	}
	t.Logf("build step took %s", time.Since(start))
	if buildErr != nil {
		t.Fatalf("orchestratorBuildStep returned error: %v", buildErr)
	}

	// Verify real source files landed on disk.
	addGo := filepath.Join(dir, "add.go")
	if _, err := os.Stat(addGo); err != nil {
		entries, _ := os.ReadDir(dir)
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("add.go not created (dir contains: %v)", names)
	}
	src, _ := os.ReadFile(addGo)
	if !strings.Contains(string(src), "func Add") {
		t.Fatalf("add.go missing Add function:\n%s", string(src))
	}

	// Verify the module actually compiles AND the generated test passes —
	// i.e. Engine produced working, verified software through this backend.
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated module failed `go test ./...`: %v\n%s", err, string(out))
	}
	t.Logf("go test output:\n%s", strings.TrimSpace(string(out)))
}
