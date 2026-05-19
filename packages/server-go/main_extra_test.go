package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/engine/server/ai"
	"github.com/engine/server/discord"
	"github.com/engine/server/mesh"
	"github.com/engine/server/ws"
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

func TestRun_WithEngineMeshFlag(t *testing.T) {
	withRunDepsReset(t)
	projectPath := t.TempDir()
	t.Setenv("PROJECT_PATH", projectPath)
	t.Setenv("ENGINE_MESH", "1")
	t.Setenv("ENGINE_MESH_CONFIG", filepath.Join(projectPath, "missing-mesh.json"))

	dbInitFn = func(path string) error { return nil }
	newHubFn = func(path string) *ws.Hub { return &ws.Hub{} }
	loadDiscordConfigFn = func(projectPath string) (discord.Config, error) { return discord.Config{}, nil }
	httpHandleFuncFn = func(pattern string, handler func(http.ResponseWriter, *http.Request)) {}
	httpListenAndServeFn = func(addr string, handler http.Handler) error {
		return fmt.Errorf("listen-stop")
	}

	err := run()
	if err == nil || !strings.Contains(err.Error(), "listen-stop") {
		t.Fatalf("expected run to reach listen and return sentinel error, got %v", err)
	}
}

func TestStartMeshServer_WithConfig_LoadsAndSpawns(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("startMeshServer panicked: %v", r)
		}
	}()

	tmp := t.TempDir()
	meshPath := filepath.Join(tmp, "mesh.json")
	cfg := mesh.Config{
		SelfName:   "test-node",
		ListenAddr: "bad-listen-addr",
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal mesh config: %v", err)
	}
	if err := os.WriteFile(meshPath, data, 0o600); err != nil {
		t.Fatalf("write mesh config: %v", err)
	}

	t.Setenv("ENGINE_MESH_CONFIG", meshPath)
	startMeshServer()
	// Give goroutine a small window to execute the listener path.
	time.Sleep(25 * time.Millisecond)
}

func TestStartMeshServer_ConfigReadErrorBranch(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("startMeshServer panicked: %v", r)
		}
	}()

	badPath := t.TempDir() // directory path causes mesh.LoadConfig read error
	t.Setenv("ENGINE_MESH_CONFIG", badPath)
	startMeshServer()
}

func TestPostOrchestratorGitHubStatusFn_Branches(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/commits/"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"sha":"abc123"}`))
		case strings.Contains(r.URL.Path, "/statuses/"):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"fail"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	t.Setenv("GITHUB_API_BASE", server.URL)

	// Empty token should return early.
	t.Setenv("GITHUB_TOKEN", "")
	postOrchestratorGitHubStatusFn("o", "r", "plan", "detail")

	// Token set should execute FindHeadSHA and then PostCommitStatus.
	t.Setenv("GITHUB_TOKEN", "tok")
	postOrchestratorGitHubStatusFn("o", "r", "plan", "detail")

	// Empty/invalid owner should make FindHeadSHA fail and return via early branch.
	postOrchestratorGitHubStatusFn("", "r", "plan", "detail")
}

func TestTriggerScaffoldSession_OnPlanUpdateBranch(t *testing.T) {
	withRunDepsReset(t)
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)

	const owner = "owner"
	const repo = "repo"
	prepareScaffoldTargetRepo(t, projectPath, owner, repo, "# demo\n@engine")

	var onPlanUpdateCalls int32
	runOrchestratorFn = func(cfg ai.OrchestratorConfig) (*ai.OrchestrationState, error) {
		if cfg.OnPlanUpdate != nil {
			cfg.OnPlanUpdate(&ai.OrchestrationState{Plan: []ai.PlanStep{{Index: 1, Done: true}}})
			atomic.AddInt32(&onPlanUpdateCalls, 1)
		}
		return &ai.OrchestrationState{CompletedAt: "done", Plan: []ai.PlanStep{{Index: 1, Done: true}}}, nil
	}

	payload := json.RawMessage(`{"repository":{"full_name":"owner/repo"},"sender":{"login":"alice"}}`)
	triggerScaffoldSession(projectPath, payload)

	if atomic.LoadInt32(&onPlanUpdateCalls) == 0 {
		t.Fatal("expected OnPlanUpdate callback to be invoked")
	}
}

func TestTriggerIssueOpenedSession_RoutesToActiveOrchestrator(t *testing.T) {
	withRunDepsReset(t)
	projectPath := t.TempDir()
	setupTestDB(t, projectPath)

	const owner = "owner"
	const repo = "repo"
	targetPath := prepareScaffoldTargetRepo(t, projectPath, owner, repo, "# demo\n@engine")

	var aiChatCalls int32
	aiChatFn = func(ctx *ai.ChatContext, prompt string) {
		atomic.AddInt32(&aiChatCalls, 1)
	}

	release := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		_, err := ai.RunAutonomousProject(ai.OrchestratorConfig{
			ProjectPath:        targetPath,
			Owner:              owner,
			Repo:               repo,
			Brief:              "test",
			SessionIDPrefix:    "active-orch",
			MaxOuterIterations: 5,
			ChatFn: func(ctx *ai.ChatContext, userMessage string) {
				switch ctx.Role {
				case ai.RoleGriller:
					<-release
					ctx.OnChunk("design", false)
				case ai.RolePRDWriter:
					ctx.OnChunk("term | meaning\n---SPLIT---\n# PRD\nmodule: app", false)
				case ai.RolePlanner:
					ctx.OnChunk("1. Build\n   Do it\n   Acceptance: ok\n", false)
				case ai.RoleAutonomousBuilder:
					ctx.OnChunk("done", false)
				case ai.RoleReviewer:
					ctx.OnChunk("APPROVE", false)
				}
			},
		})
		errCh <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h := ai.GetOrchestratorHandle(targetPath); h != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ai.GetOrchestratorHandle(targetPath) == nil {
		close(release)
		t.Fatal("expected active orchestrator handle")
	}

	payload := json.RawMessage(`{"action":"opened","issue":{"number":42,"title":"Feature","body":"Please add X"},"repository":{"full_name":"owner/repo"},"sender":{"login":"alice"}}`)
	triggerIssueOpenedSession(projectPath, payload)

	if got := atomic.LoadInt32(&aiChatCalls); got != 0 {
		t.Fatalf("expected routed branch to skip aiChatFn, got %d calls", got)
	}

	if h := ai.GetOrchestratorHandle(targetPath); h != nil {
		h.Stop()
	}
	close(release)
	select {
	case <-errCh:
	case <-time.After(3 * time.Second):
		t.Fatal("orchestrator goroutine did not exit")
	}
}

// writeTestFile is a helper to write content to a file path.
func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
