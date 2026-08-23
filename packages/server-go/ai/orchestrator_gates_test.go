package ai

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// gitRepoWithDiff makes a real git repo with one uncommitted change, so
// gogit.GetDiff returns something for the critic to judge.
func gitRepoWithDiff(t *testing.T) string {
	t.Helper()
	dir := setupPhasesDB(t)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v unavailable: %v (%s)", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	run("add", ".")
	run("commit", "-m", "seed")
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app\n\nfunc Added() {}\n"), 0o644); err != nil {
		t.Fatalf("modify file: %v", err)
	}
	return dir
}

func gatesTestConfig(dir string) OrchestratorConfig {
	onPhase, onProgress, onError := noOpOrchestratorCallbacks()
	return OrchestratorConfig{
		ProjectPath:     dir,
		Owner:           "acme",
		Repo:            "demo",
		SessionIDPrefix: "gates-test",
		OnPhase:         onPhase,
		OnProgress:      onProgress,
		OnError:         onError,
	}
}

// ── critic gate ───────────────────────────────────────────────────────────────

// A gate that cannot run must not block the pipeline.
func TestOrchestratorCriticStep_CancelledApproves(t *testing.T) {
	cancel := make(chan struct{})
	close(cancel)
	verdict, findings := orchestratorCriticStep(gatesTestConfig(setupPhasesDB(t)), &PlanStep{Index: 1}, cancel)
	if verdict != CriticApprove || findings != "" {
		t.Fatalf("cancelled critic must approve, got %v %q", verdict, findings)
	}
}

func TestOrchestratorCriticStep_NoDiffApproves(t *testing.T) {
	// Not a git repo: GetDiff reports the "(no changes)" sentinel rather than
	// an error, and the critic must treat that as nothing to judge instead of
	// sending it to a model.
	orig := runCriticChatFn
	t.Cleanup(func() { runCriticChatFn = orig })
	called := false
	runCriticChatFn = func(ctx *ChatContext, input string) { called = true; ctx.OnChunk("APPROVE", true) }

	verdict, _ := orchestratorCriticStep(gatesTestConfig(setupPhasesDB(t)), &PlanStep{Index: 1}, make(chan struct{}))
	if called {
		t.Fatal("critic called a model with no diff to review")
	}
	if verdict != CriticApprove {
		t.Fatalf("no diff must approve, got %v", verdict)
	}
}

func TestOrchestratorCriticStep_ApprovesCleanDiff(t *testing.T) {
	dir := gitRepoWithDiff(t)
	orig := runCriticChatFn
	t.Cleanup(func() { runCriticChatFn = orig })
	saw := ""
	runCriticChatFn = func(ctx *ChatContext, input string) {
		saw = input
		ctx.OnChunk("APPROVE\nlooks fine", true)
	}

	verdict, findings := orchestratorCriticStep(gatesTestConfig(dir), &PlanStep{Index: 1}, make(chan struct{}))
	if verdict != CriticApprove {
		t.Fatalf("expected approve, got %v (%s)", verdict, findings)
	}
	if !strings.Contains(saw, "Added()") {
		t.Fatalf("critic did not receive the working-tree diff, got %q", saw)
	}
}

func TestOrchestratorCriticStep_RejectsAndReportsFindings(t *testing.T) {
	dir := gitRepoWithDiff(t)
	orig := runCriticChatFn
	t.Cleanup(func() { runCriticChatFn = orig })
	runCriticChatFn = func(ctx *ChatContext, input string) {
		ctx.OnChunk("- app.go:3 - no test accompanies this change\nREJECT: missing test", true)
	}

	verdict, findings := orchestratorCriticStep(gatesTestConfig(dir), &PlanStep{Index: 1}, make(chan struct{}))
	if verdict != CriticReject {
		t.Fatalf("expected reject, got %v", verdict)
	}
	if strings.TrimSpace(findings) == "" {
		t.Fatal("a rejection must carry findings back to the builder")
	}
}

// ── repair gate ───────────────────────────────────────────────────────────────

func TestOrchestratorRepairStep_NoErrorOrCancelledIsNoOp(t *testing.T) {
	cfg := gatesTestConfig(setupPhasesDB(t))
	if orchestratorRepairStep(cfg, &OrchestrationState{}, nil, make(chan struct{})) {
		t.Fatal("nil validate error must not trigger a repair")
	}
	cancel := make(chan struct{})
	close(cancel)
	if orchestratorRepairStep(cfg, &OrchestrationState{}, errors.New("boom"), cancel) {
		t.Fatal("cancelled repair must not run")
	}
}

func TestOrchestratorRepairStep_CancelDuringRunTest(t *testing.T) {
	dir := setupPhasesDB(t)
	origPlan := generateRepairPlanChatFn
	t.Cleanup(func() { generateRepairPlanChatFn = origPlan })

	cancel := make(chan struct{})
	var once sync.Once
	generateRepairPlanChatFn = func(ctx *ChatContext, input string) {
		// Cancel between planning and the re-validation, so runTest takes its
		// cancelled branch rather than booting the validator. The repair loop
		// re-plans on every attempt, so this must only close once.
		once.Do(func() { close(cancel) })
		ctx.OnChunk("Diagnosis: n/a", true)
	}

	cfg := gatesTestConfig(dir)
	if orchestratorRepairStep(cfg, &OrchestrationState{}, errors.New("validation failed"), cancel) {
		t.Fatal("a cancelled repair must not report resolved")
	}
}

// A gate that cannot even open a session must fall through rather than block
// the pipeline or claim a repair it never ran.
func TestOrchestratorGates_SessionFailureFallsThrough(t *testing.T) {
	orig := createSessionFn
	t.Cleanup(func() { createSessionFn = orig })
	createSessionFn = func(string, string, string) error { return errors.New("db down") }

	dir := gitRepoWithDiff(t)
	verdict, findings := orchestratorCriticStep(gatesTestConfig(dir), &PlanStep{Index: 1}, make(chan struct{}))
	if verdict != CriticApprove || findings != "" {
		t.Fatalf("critic must approve when it cannot run, got %v %q", verdict, findings)
	}

	if orchestratorRepairStep(gatesTestConfig(dir), &OrchestrationState{}, errors.New("boom"), make(chan struct{})) {
		t.Fatal("repair must not report resolved when it cannot run")
	}
}
