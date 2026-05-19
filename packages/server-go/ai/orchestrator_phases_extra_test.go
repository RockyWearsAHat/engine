package ai

import (
	"strings"
	"testing"

	"github.com/engine/server/db"
)

// setupPhasesDB initialises the DB for phase tests.
func setupPhasesDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	stateDir := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", stateDir)
	if err := db.Init(dir); err != nil {
		t.Fatalf("db.Init: %v", err)
	}
	return dir
}

// TestChooseSessionPrefix_Custom verifies non-empty prefix is returned as-is.
func TestChooseSessionPrefix_Custom(t *testing.T) {
	cfg := OrchestratorConfig{SessionIDPrefix: "myprefix"}
	if got := chooseSessionPrefix(cfg); got != "myprefix" {
		t.Errorf("got %q, want %q", got, "myprefix")
	}
}

// TestChooseSessionPrefix_Repo verifies empty prefix + repo → "orch-<repo>".
func TestChooseSessionPrefix_Repo(t *testing.T) {
	cfg := OrchestratorConfig{Repo: "myrepo"}
	if got := chooseSessionPrefix(cfg); got != "orch-myrepo" {
		t.Errorf("got %q, want %q", got, "orch-myrepo")
	}
}

// TestChooseSessionPrefix_Default verifies empty prefix + empty repo → "orch".
func TestChooseSessionPrefix_Default(t *testing.T) {
	cfg := OrchestratorConfig{}
	if got := chooseSessionPrefix(cfg); got != "orch" {
		t.Errorf("got %q, want %q", got, "orch")
	}
}

// TestBuildPlannerPrompt verifies brief appears in output.
func TestBuildPlannerPrompt(t *testing.T) {
	out := buildPlannerPrompt("my project brief")
	if !strings.Contains(out, "my project brief") {
		t.Errorf("expected brief in prompt, got %q", out)
	}
}

// TestBuildStepPrompt verifies step title and state appear in output.
func TestBuildStepPrompt(t *testing.T) {
	state := &OrchestrationState{Owner: "o", Repo: "r", Plan: []PlanStep{{Index: 1, Title: "step title"}}}
	step := &PlanStep{Index: 1, Title: "step title", Acceptance: "`go test ./...`"}
	out := buildStepPrompt(state, step, "")
	if !strings.Contains(out, "step title") {
		t.Errorf("expected step title in prompt, got %q", out)
	}
}

// TestBuildStepPrompt_WithRedirect verifies redirect appears at top.
func TestBuildStepPrompt_WithRedirect(t *testing.T) {
	state := &OrchestrationState{Owner: "o", Repo: "r"}
	step := &PlanStep{Index: 1, Title: "t"}
	out := buildStepPrompt(state, step, "urgent: fix auth first")
	if !strings.Contains(out, "urgent: fix auth first") {
		t.Errorf("expected redirect in prompt, got %q", out)
	}
}

// TestBuildReviewerPrompt verifies step info appears in prompt.
func TestBuildReviewerPrompt(t *testing.T) {
	state := &OrchestrationState{Owner: "owner", Repo: "repo", Plan: []PlanStep{{Index: 1}}}
	step := &PlanStep{Index: 1, Title: "my feature", Acceptance: "`go test ./...`"}
	out := buildReviewerPrompt(state, step)
	if !strings.Contains(out, "my feature") {
		t.Errorf("expected step title in reviewer prompt, got %q", out)
	}
}

// TestBuildBehavioralValidatorPrompt_WithLiveURL verifies live URL appears in prompt.
func TestBuildBehavioralValidatorPrompt_WithLiveURL(t *testing.T) {
	state := &OrchestrationState{Owner: "o", Repo: "r", LiveURL: "https://example.com"}
	out := buildBehavioralValidatorPrompt(state)
	if !strings.Contains(out, "https://example.com") {
		t.Errorf("expected live URL in prompt, got %q", out)
	}
}

// TestBuildBehavioralValidatorPrompt_NoLiveURL verifies absence message when no URL.
func TestBuildBehavioralValidatorPrompt_NoLiveURL(t *testing.T) {
	state := &OrchestrationState{Owner: "o", Repo: "r"}
	out := buildBehavioralValidatorPrompt(state)
	if !strings.Contains(out, "live-url.txt") {
		t.Errorf("expected live-url reference in prompt, got %q", out)
	}
}

// TestOrchestratorPlanPhase_WithDB verifies plan is parsed and stored in state.
func TestOrchestratorPlanPhase_WithDB(t *testing.T) {
	dir := setupPhasesDB(t)
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn: func(c *ChatContext, _ string) {
			c.OnChunk("1. Scaffold project\n   Create go.mod and first test.\n   Acceptance: `go test ./...` passes\n", false)
		},
	}
	state := &OrchestrationState{Brief: "build a cli tool", Owner: "o", Repo: "r"}
	cancel := make(chan struct{})
	if err := orchestratorPlanPhase(cfg, state, cancel); err != nil {
		t.Fatalf("orchestratorPlanPhase: %v", err)
	}
	if len(state.Plan) == 0 {
		t.Error("expected at least one plan step")
	}
}

// TestOrchestratorPlanPhase_EmptyOutput verifies error when ChatFn emits nothing.
func TestOrchestratorPlanPhase_EmptyOutput(t *testing.T) {
	dir := setupPhasesDB(t)
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn:          func(_ *ChatContext, _ string) {}, // emits nothing
	}
	state := &OrchestrationState{Brief: "brief", Owner: "o", Repo: "r"}
	cancel := make(chan struct{})
	if err := orchestratorPlanPhase(cfg, state, cancel); err == nil {
		t.Error("expected error for empty plan output")
	}
}

// TestOrchestratorBuildStep_WithDB verifies builder step runs without error.
func TestOrchestratorBuildStep_WithDB(t *testing.T) {
	dir := setupPhasesDB(t)
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		Owner:           "o",
		Repo:            "r",
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn:          func(c *ChatContext, _ string) { c.OnChunk("step output", false) },
	}
	state := &OrchestrationState{Owner: "o", Repo: "r", Plan: []PlanStep{{Index: 1}}}
	step := &PlanStep{Index: 1, Title: "scaffold project", Acceptance: "`go test ./...`"}
	cancel := make(chan struct{})
	if err := orchestratorBuildStep(cfg, state, step, "", cancel); err != nil {
		t.Fatalf("orchestratorBuildStep: %v", err)
	}
}

// TestOrchestratorBuildStep_EmptyOutput verifies error when builder emits nothing.
func TestOrchestratorBuildStep_EmptyOutput(t *testing.T) {
	dir := setupPhasesDB(t)
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		Owner:           "o",
		Repo:            "r",
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn:          func(_ *ChatContext, _ string) {}, // emits nothing
	}
	state := &OrchestrationState{Owner: "o", Repo: "r"}
	step := &PlanStep{Index: 1, Title: "t"}
	cancel := make(chan struct{})
	if err := orchestratorBuildStep(cfg, state, step, "", cancel); err == nil {
		t.Error("expected error for empty builder output")
	}
}

// TestOrchestratorReviewStep_Approve verifies APPROVE verdict is returned.
func TestOrchestratorReviewStep_Approve(t *testing.T) {
	dir := setupPhasesDB(t)
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		Owner:           "o",
		Repo:            "r",
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn:          func(c *ChatContext, _ string) { c.OnChunk("Looks good.\nAPPROVE", false) },
	}
	state := &OrchestrationState{Owner: "o", Repo: "r", Plan: []PlanStep{{Index: 1}}}
	step := &PlanStep{Index: 1, Title: "t"}
	cancel := make(chan struct{})
	verdict, _ := orchestratorReviewStep(cfg, state, step, cancel)
	if verdict != ReviewApprove {
		t.Errorf("expected ReviewApprove, got %v", verdict)
	}
}

// TestOrchestratorReviewStep_Reject verifies REJECT verdict is returned.
func TestOrchestratorReviewStep_Reject(t *testing.T) {
	dir := setupPhasesDB(t)
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		Owner:           "o",
		Repo:            "r",
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn:          func(c *ChatContext, _ string) { c.OnChunk("Tests fail.\nREJECT: tests do not pass", false) },
	}
	state := &OrchestrationState{Owner: "o", Repo: "r", Plan: []PlanStep{{Index: 1}}}
	step := &PlanStep{Index: 1, Title: "t"}
	cancel := make(chan struct{})
	verdict, _ := orchestratorReviewStep(cfg, state, step, cancel)
	if verdict != ReviewReject {
		t.Errorf("expected ReviewReject, got %v", verdict)
	}
}

// TestOrchestratorValidatePhase_Approve verifies success path.
func TestOrchestratorValidatePhase_Approve(t *testing.T) {
	dir := setupPhasesDB(t)
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		Owner:           "o",
		Repo:            "r",
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn:          func(c *ChatContext, _ string) { c.OnChunk("All good.\nAPPROVE", false) },
	}
	state := &OrchestrationState{Owner: "o", Repo: "r"}
	cancel := make(chan struct{})
	summary, err := orchestratorValidatePhase(cfg, state, cancel)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !strings.Contains(summary, "APPROVE") {
		t.Errorf("expected APPROVE in summary, got %q", summary)
	}
}

// TestOrchestratorValidatePhase_Reject verifies failure path.
func TestOrchestratorValidatePhase_Reject(t *testing.T) {
	dir := setupPhasesDB(t)
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		Owner:           "o",
		Repo:            "r",
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn:          func(c *ChatContext, _ string) { c.OnChunk("Build broken.\nREJECT: build fails", false) },
	}
	state := &OrchestrationState{Owner: "o", Repo: "r"}
	cancel := make(chan struct{})
	_, err := orchestratorValidatePhase(cfg, state, cancel)
	if err == nil {
		t.Error("expected error for rejected validation")
	}
}
