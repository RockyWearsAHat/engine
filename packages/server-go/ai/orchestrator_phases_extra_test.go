package ai

import (
	"strings"
	"testing"
)

// setupPhasesDB creates a temporary project directory with config and initializes the DB for tests.
// Alias for setupPhasesDBProject; kept for compatibility within this file.
func setupPhasesDB(t *testing.T) string {
	return setupPhasesDBProject(t)
}

// noOpOrchestratorCallbacks returns a minimal OrchestratorConfig with no-op callbacks.
// Used to reduce repetition in tests that don't care about callback output.
func noOpOrchestratorCallbacks() (func(string, string), func(string), func(string)) {
	return func(string, string) {}, func(string) {}, func(string) {}
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

func TestOrchestratorPlanPhase_RepairsMissingAcceptance(t *testing.T) {
	dir := setupPhasesDB(t)
	callCount := 0
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn: func(c *ChatContext, _ string) {
			callCount++
			if callCount == 1 {
				c.OnChunk("1. Build thing\n   body without acceptance\n", false)
				return
			}
			c.OnChunk("1. Build thing\n   add implementation\n   Acceptance: `go test ./...` exits 0\n", false)
		},
	}
	state := &OrchestrationState{Brief: "brief", Owner: "o", Repo: "r"}
	cancel := make(chan struct{})
	if err := orchestratorPlanPhase(cfg, state, cancel); err != nil {
		t.Fatalf("orchestratorPlanPhase: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected planner + repair pass, got %d calls", callCount)
	}
	if len(state.Plan) != 1 {
		t.Fatalf("expected one repaired step, got %d", len(state.Plan))
	}
	if state.Plan[0].Acceptance == "" {
		t.Fatal("expected repaired acceptance command")
	}
}

func TestOrchestratorPlanPhase_RepairSynthesizesAcceptance(t *testing.T) {
	dir := setupPhasesDB(t)
	callCount := 0
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn: func(c *ChatContext, _ string) {
			callCount++
			if callCount == 1 {
				c.OnChunk("1. Build thing\n   body without acceptance\n", false)
				return
			}
			c.OnChunk("1. Build thing\n   body still without acceptance\n", false)
		},
	}
	state := &OrchestrationState{Brief: "brief", Owner: "o", Repo: "r"}
	cancel := make(chan struct{})
	if err := orchestratorPlanPhase(cfg, state, cancel); err != nil {
		t.Fatalf("orchestratorPlanPhase: %v", err)
	}
	if len(state.Plan) != 1 {
		t.Fatalf("expected one repaired step, got %d", len(state.Plan))
	}
	if !strings.Contains(state.Plan[0].Acceptance, "echo") {
		t.Fatalf("expected synthesized acceptance command, got %q", state.Plan[0].Acceptance)
	}
}

func TestOrchestratorPlanPhase_RepairFailureBubblesUp(t *testing.T) {
	dir := setupPhasesDB(t)
	callCount := 0
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn: func(c *ChatContext, _ string) {
			callCount++
			if callCount == 1 {
				c.OnChunk("1. Build thing\n   body without acceptance\n", false)
				return
			}
			// Empty repair output -> parse failure in repair pass.
		},
	}
	state := &OrchestrationState{Brief: "brief", Owner: "o", Repo: "r"}
	cancel := make(chan struct{})
	err := orchestratorPlanPhase(cfg, state, cancel)
	if err == nil || !strings.Contains(err.Error(), "repair pass failed") {
		t.Fatalf("expected repair failure, got %v", err)
	}
}

// TestOrchestratorPlanPhase_RepairsChattyProse tests repair pass on chatty prose with no steps.
func TestOrchestratorPlanPhase_RepairsChattyProse(t *testing.T) {
	dir := setupPhasesDB(t)
	callCount := 0
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn: func(c *ChatContext, _ string) {
			callCount++
			if callCount == 1 {
				// First output: chatty prose with no numbered steps
				c.OnChunk("To build this feature, I would start by creating a project structure, then add tests, and implement the core logic. Let me break this down in detail...", false)
				return
			}
			if callCount == 2 {
				// Repair pass: return valid numbered plan
				c.OnChunk("1. Scaffold project\n   Create go.mod and initial structure.\n   Acceptance: `go test ./...` passes\n2. Add test\n   Write first failing test.\n   Acceptance: `go test ./...` passes\n", false)
				return
			}
			// Critique phase: approve the repaired plan
			c.OnChunk("COMPLETE", false)
		},
	}
	state := &OrchestrationState{Brief: "brief", Owner: "o", Repo: "r"}
	cancel := make(chan struct{})
	if err := orchestratorPlanPhase(cfg, state, cancel); err != nil {
		t.Fatalf("expected repair to succeed, got %v", err)
	}
	if len(state.Plan) == 0 {
		t.Error("expected plan steps after repair")
	}
	if callCount < 2 {
		t.Fatalf("expected at least initial pass + repair, got %d calls", callCount)
	}
}

// TestOrchestratorPlanPhase_EmptyOutputError tests error message for truly empty output.
func TestOrchestratorPlanPhase_EmptyOutputError(t *testing.T) {
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
	err := orchestratorPlanPhase(cfg, state, cancel)
	if err == nil {
		t.Error("expected error for empty output")
	}
	if !strings.Contains(err.Error(), "got 0 chars") {
		t.Fatalf("expected 'got 0 chars' in error, got %v", err)
	}
}

// TestOrchestratorPlanPhase_UnparsableWithExcerpt tests error message includes output excerpt.
func TestOrchestratorPlanPhase_UnparsableWithExcerpt(t *testing.T) {
	dir := setupPhasesDB(t)
	callCount := 0
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn: func(c *ChatContext, _ string) {
			callCount++
			if callCount == 1 {
				// Output is chatty prose, no numbered steps
				c.OnChunk("Here is my plan. I would create a structure with tests. This is a detailed explanation.", false)
				return
			}
			// Repair also produces unparsable output (no numbered steps)
			c.OnChunk("Still unable to format as numbered steps properly because the model is confused.", false)
		},
	}
	state := &OrchestrationState{Brief: "brief", Owner: "o", Repo: "r"}
	cancel := make(chan struct{})
	err := orchestratorPlanPhase(cfg, state, cancel)
	if err == nil {
		t.Error("expected error for unparsable output")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "got") || !strings.Contains(errMsg, "chars") {
		t.Fatalf("expected error format 'got N chars', got %v", err)
	}
	// Check that excerpt is in error message (should be from first output)
	if !strings.Contains(errMsg, "Here is my plan") {
		t.Fatalf("expected output excerpt in error message, got %v", err)
	}
}

func TestOrchestratorRepairPlanPhase_CreateSessionError(t *testing.T) {
	cfg := OrchestratorConfig{
		ProjectPath:     t.TempDir(),
		SessionIDPrefix: "t",
		ChatFn:          func(*ChatContext, string) {},
	}
	_, err := orchestratorRepairPlanPhase(cfg, &OrchestrationState{Brief: "brief"}, "bad", make(chan struct{}))
	if err == nil || !strings.Contains(err.Error(), "create plan repair session") {
		t.Fatalf("expected create plan repair session error, got %v", err)
	}
}

func TestOrchestratorRepairPlanPhase_EmptyOutput(t *testing.T) {
	dir := setupPhasesDB(t)
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "t",
		ChatFn:          func(*ChatContext, string) {},
	}
	_, err := orchestratorRepairPlanPhase(cfg, &OrchestrationState{Brief: "brief"}, "bad", make(chan struct{}))
	if err == nil || !strings.Contains(err.Error(), "repair output empty or unparsable") {
		t.Fatalf("expected empty repair output error, got %v", err)
	}
}

func TestParsePlanFromText_AcceptanceStopsAtRefactor(t *testing.T) {
	input := strings.Join([]string{
		"1. Step",
		"   Body line",
		"   Acceptance:",
		"   ```bash",
		"   go test ./...",
		"   ```",
		"   Refactor: keep module boundaries tight",
	}, "\n")
	steps := parsePlanFromText(input)
	if len(steps) != 1 {
		t.Fatalf("expected one step, got %d", len(steps))
	}
	if !strings.Contains(steps[0].Acceptance, "go test ./...") {
		t.Fatalf("expected command in acceptance, got %q", steps[0].Acceptance)
	}
	if strings.Contains(strings.ToLower(steps[0].Acceptance), "refactor") {
		t.Fatalf("refactor text should not remain in acceptance, got %q", steps[0].Acceptance)
	}
}

func TestParsePlanFromText_AcceptanceJoinsMultipleLines(t *testing.T) {
	input := strings.Join([]string{
		"1. Step",
		"   Body line",
		"   Acceptance:",
		"   ```bash",
		"",
		"   go test ./...",
		"   go test ./... -run Smoke",
		"   ```",
	}, "\n")
	steps := parsePlanFromText(input)
	if len(steps) != 1 {
		t.Fatalf("expected one step, got %d", len(steps))
	}
	want := "go test ./... go test ./... -run Smoke"
	if steps[0].Acceptance != want {
		t.Fatalf("expected joined acceptance %q, got %q", want, steps[0].Acceptance)
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

// ── Plan decomposition gate ───────────────────────────────────────────────────

// TestValidatePlanDecomposition_CleanPlan verifies a well-decomposed plan passes.
func TestValidatePlanDecomposition_CleanPlan(t *testing.T) {
	steps := []PlanStep{
		{Index: 1, Title: "Scaffold project", Body: "Create go.mod and initial structure."},
		{Index: 2, Title: "Add test", Body: "Write first failing test."},
		{Index: 3, Title: "Implement feature", Body: "Minimal code to pass test."},
	}
	if err := validatePlanDecomposition(steps); err != nil {
		t.Fatalf("expected clean plan to pass, got %v", err)
	}
}

// TestValidatePlanDecomposition_RejectsAndThen verifies "and then" is rejected.
func TestValidatePlanDecomposition_RejectsAndThen(t *testing.T) {
	steps := []PlanStep{
		{Index: 1, Title: "Add migration and then wire UI", Body: "Create migration, then wire UI."},
	}
	err := validatePlanDecomposition(steps)
	if err == nil {
		t.Fatal("expected error for 'and then'")
	}
	if !strings.Contains(err.Error(), "and then") {
		t.Fatalf("expected 'and then' in error, got %v", err)
	}
}

// TestValidatePlanDecomposition_RejectsAlso verifies "also" is rejected.
func TestValidatePlanDecomposition_RejectsAlso(t *testing.T) {
	steps := []PlanStep{
		{Index: 1, Title: "Add feature", Body: "Implement feature and also write docs."},
	}
	err := validatePlanDecomposition(steps)
	if err == nil {
		t.Fatal("expected error for 'also'")
	}
	if !strings.Contains(err.Error(), "also") {
		t.Fatalf("expected 'also' in error, got %v", err)
	}
}

// TestValidatePlanDecomposition_RejectsTooManyVerbs verifies >3 verbs are rejected.
func TestValidatePlanDecomposition_RejectsTooManyVerbs(t *testing.T) {
	steps := []PlanStep{
		{Index: 1, Title: "Add migration and write UI and create tests and deploy", Body: "Multiple actions."},
	}
	err := validatePlanDecomposition(steps)
	if err == nil {
		t.Fatal("expected error for too many verbs")
	}
	if !strings.Contains(err.Error(), "verbs") {
		t.Fatalf("expected 'verbs' in error, got %v", err)
	}
}

// TestValidatePlanDecomposition_SplitConcerns rejects compound steps.
// The prompt says this plan should be split into 3 steps.
func TestValidatePlanDecomposition_SplitConcerns(t *testing.T) {
	steps := []PlanStep{
		{Index: 1, Title: "Add migration and then wire UI and write docs", Body: "Combine three tasks."},
	}
	err := validatePlanDecomposition(steps)
	if err == nil {
		t.Fatal("expected error for compound step")
	}
	if !strings.Contains(err.Error(), "and then") {
		t.Fatalf("expected decomposition error, got %v", err)
	}
}

// TestCountActionVerbs verifies verb counting heuristic.
func TestCountActionVerbs(t *testing.T) {
	tests := []struct {
		text string
		want int
	}{
		{"add migration", 1},
		{"add migration and wire UI", 2},
		{"add migration and wire UI and write docs", 3},
		{"add test write implementation refactor", 4}, // add + test + write + refactor
		{"add test write impl refactor update", 5},   // add + test + write + refactor + update
	}
	for _, tt := range tests {
		got := countActionVerbs(tt.text)
		if got != tt.want {
			t.Errorf("countActionVerbs(%q) = %d, want %d", tt.text, got, tt.want)
		}
	}
}

// TestOrchestratorPlanPhase_SplitsOversizedPlan verifies gate splits multi-concern steps.
func TestOrchestratorPlanPhase_SplitsOversizedPlan(t *testing.T) {
	dir := setupPhasesDB(t)
	callCount := 0
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "t",
		OnPhase: func(kind, msg string) {
			if kind == "plan" && strings.Contains(msg, "split") {
				// Capture split logging.
			}
		},
		OnProgress: func(string) {},
		OnError:    func(string) {},
		ChatFn: func(c *ChatContext, input string) {
			callCount++
			if callCount == 1 {
				// First plan: oversized step with "and then".
				c.OnChunk("1. Add migration and then wire UI and write docs\n   Body\n   Acceptance: `go test ./...` passes\n", false)
				return
			}
			if callCount == 2 {
				// Split pass: 3 clean steps.
				c.OnChunk("1. Add migration\n   Create migration.\n   Acceptance: `go test ./...` passes\n2. Wire UI\n   Connect frontend.\n   Acceptance: `go test ./...` passes\n3. Write docs\n   Document.\n   Acceptance: `go test ./...` passes\n", false)
				return
			}
			// Critique phase: approve the split plan.
			c.OnChunk("COMPLETE", false)
		},
	}
	state := &OrchestrationState{Brief: "build a feature", Owner: "o", Repo: "r"}
	cancel := make(chan struct{})
	if err := orchestratorPlanPhase(cfg, state, cancel); err != nil {
		t.Fatalf("orchestratorPlanPhase: %v", err)
	}
	if callCount < 2 {
		t.Fatalf("expected at least planner + split pass, got %d calls", callCount)
	}
	if len(state.Plan) != 3 {
		t.Fatalf("expected 3 split steps, got %d", len(state.Plan))
	}
	// Verify each step has acceptance.
	for i, step := range state.Plan {
		if step.Acceptance == "" {
			t.Fatalf("step %d missing acceptance", i+1)
		}
	}
}

// TestOrchestratorPlanPhase_RejectsAfterTwoSplitAttempts verifies max 2 passes.
func TestOrchestratorPlanPhase_RejectsAfterTwoSplitAttempts(t *testing.T) {
	dir := setupPhasesDB(t)
	callCount := 0
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn: func(c *ChatContext, _ string) {
			callCount++
			if callCount == 1 {
				// First plan: oversized.
				c.OnChunk("1. Add migration and then wire UI\n   Body\n   Acceptance: `go test ./...` passes\n", false)
				return
			}
			// Split pass: still oversized (still has "and then").
			c.OnChunk("1. Add migration and then wire UI\n   Still combined.\n   Acceptance: `go test ./...` passes\n", false)
		},
	}
	state := &OrchestrationState{Brief: "brief", Owner: "o", Repo: "r"}
	cancel := make(chan struct{})
	err := orchestratorPlanPhase(cfg, state, cancel)
	if err == nil {
		t.Fatal("expected error after second split rejection")
	}
	if !strings.Contains(err.Error(), "plan gate rejected") {
		t.Fatalf("expected 'plan gate rejected' in error, got %v", err)
	}
}
