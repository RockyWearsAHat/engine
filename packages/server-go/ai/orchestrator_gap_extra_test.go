package ai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/engine/server/db"
)

func TestRunAutonomousProject_ProjectPathRequired(t *testing.T) {
	_, err := RunAutonomousProject(OrchestratorConfig{})
	if err == nil || !strings.Contains(err.Error(), "project path is required") {
		t.Fatalf("expected project path error, got %v", err)
	}
}

func TestRunAutonomousProject_LoadStateError(t *testing.T) {
	base := t.TempDir()
	projectPath := filepath.Join(base, "project-file")
	if err := os.WriteFile(projectPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write project path file: %v", err)
	}

	_, err := RunAutonomousProject(OrchestratorConfig{ProjectPath: projectPath})
	if err == nil || !strings.Contains(err.Error(), "load state") {
		t.Fatalf("expected load state error, got %v", err)
	}
}

func TestLoadOrCreateOrchestrationState_InvalidJSONFallsBackToFresh(t *testing.T) {
	dir := t.TempDir()
	engineDir := filepath.Join(dir, ".engine")
	if err := os.MkdirAll(engineDir, 0o755); err != nil {
		t.Fatalf("mkdir .engine: %v", err)
	}
	if err := os.WriteFile(filepath.Join(engineDir, orchestrationFile), []byte("{"), 0o644); err != nil {
		t.Fatalf("write invalid json: %v", err)
	}

	st, err := loadOrCreateOrchestrationState(dir, "owner", "repo", "brief")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.Owner != "owner" || st.Repo != "repo" {
		t.Fatalf("expected fresh state owner/repo, got %+v", st)
	}
}

func TestLoadOrCreateOrchestrationState_MkdirError(t *testing.T) {
	base := t.TempDir()
	projectPath := filepath.Join(base, "as-file")
	if err := os.WriteFile(projectPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := loadOrCreateOrchestrationState(projectPath, "owner", "repo", "brief")
	if err == nil || !strings.Contains(err.Error(), "mkdir .engine") {
		t.Fatalf("expected mkdir error, got %v", err)
	}
}

func TestPersistOrchestration_WriteError(t *testing.T) {
	base := t.TempDir()
	projectPath := filepath.Join(base, "project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	// Block .engine directory creation by creating a file with same name.
	if err := os.WriteFile(filepath.Join(projectPath, ".engine"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}

	state := &OrchestrationState{Owner: "o", Repo: "r"}
	if err := persistOrchestration(projectPath, state); err == nil {
		t.Fatal("expected persist error when .engine is a file")
	}
}

func TestOrchestratorIntakePhase_NoDesignOutput(t *testing.T) {
	dir := setupIntakeDB(t)
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn:          func(_ *ChatContext, _ string) {},
	}
	state := &OrchestrationState{Brief: "brief"}
	cancel := make(chan struct{})
	_, err := orchestratorIntakePhase(cfg, state, cancel)
	if err == nil || !strings.Contains(err.Error(), "produced no design") {
		t.Fatalf("expected no design error, got %v", err)
	}
}

func TestOrchestratorIntakePhase_WriteDocError(t *testing.T) {
	base := t.TempDir()
	projectPath := filepath.Join(base, "project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, ".engine"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	// setupIntakeDB behavior for db session creation
	stateDir := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", stateDir)
	if err := db.Init(projectPath); err != nil {
		t.Fatalf("db init: %v", err)
	}

	cfg := OrchestratorConfig{
		ProjectPath:     projectPath,
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn:          func(c *ChatContext, _ string) { c.OnChunk("design", false) },
	}
	state := &OrchestrationState{Brief: "brief"}
	cancel := make(chan struct{})
	_, err := orchestratorIntakePhase(cfg, state, cancel)
	if err == nil || !strings.Contains(err.Error(), "persist design.md") {
		t.Fatalf("expected persist design error, got %v", err)
	}
}

func TestOrchestratorPRDPhase_BadSplitOutput(t *testing.T) {
	dir := setupIntakeDB(t)
	if err := WriteDoc(dir, DocDesign, "design"); err != nil {
		t.Fatalf("write design: %v", err)
	}
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn:          func(c *ChatContext, _ string) { c.OnChunk("missing separator", false) },
	}
	cancel := make(chan struct{})
	err := orchestratorPRDPhase(cfg, cancel)
	if err == nil || !strings.Contains(err.Error(), "---SPLIT---") {
		t.Fatalf("expected split error, got %v", err)
	}
}

func TestOrchestratorPRDPhase_PersistError(t *testing.T) {
	base := t.TempDir()
	projectPath := filepath.Join(base, "project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, ".engine"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	stateDir := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", stateDir)
	if err := db.Init(projectPath); err != nil {
		t.Fatalf("db init: %v", err)
	}
	// write design through legacy path to bypass WriteDoc blocker
	if err := os.WriteFile(filepath.Join(projectPath, ".engine"), []byte("x"), 0o644); err != nil {
		_ = err
	}

	cfg := OrchestratorConfig{
		ProjectPath:     projectPath,
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn:          func(c *ChatContext, _ string) { c.OnChunk("a---SPLIT---b", false) },
	}
	cancel := make(chan struct{})
	err := orchestratorPRDPhase(cfg, cancel)
	if err == nil {
		t.Fatal("expected persist error")
	}
}

func TestOrchestratorModuleIndexPhase_CreateSessionError(t *testing.T) {
	dir := t.TempDir()
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn:          func(c *ChatContext, _ string) { c.OnChunk("modules", false) },
	}
	cancel := make(chan struct{})
	err := orchestratorModuleIndexPhase(cfg, cancel)
	if err == nil || !strings.Contains(err.Error(), "create module-index session") {
		t.Fatalf("expected create session error, got %v", err)
	}
}

func TestSplitPRDOutput_EmptyHalves(t *testing.T) {
	if _, _, ok := splitPRDOutput("---SPLIT---prd"); ok {
		t.Fatal("expected false for empty vocab")
	}
	if _, _, ok := splitPRDOutput("vocab---SPLIT---"); ok {
		t.Fatal("expected false for empty prd")
	}
}

func TestParseReviewerVerdict_ApprovePrefixAndRejectNoReason(t *testing.T) {
	if verdict, _ := parseReviewerVerdict("done\nAPPROVE: looks good"); verdict != ReviewApprove {
		t.Fatalf("expected approve for prefix")
	}
	verdict, feedback := parseReviewerVerdict("- finding\nREJECT")
	if verdict != ReviewReject || !strings.Contains(feedback, "finding") {
		t.Fatalf("expected reject with body feedback, got verdict=%v feedback=%q", verdict, feedback)
	}
}

func TestParseReviewerVerdict_InconclusiveTerminalLine(t *testing.T) {
	verdict, feedback := parseReviewerVerdict("line1\nMAYBE")
	if verdict != ReviewInconclusive {
		t.Fatalf("expected inconclusive verdict, got %v", verdict)
	}
	if !strings.Contains(feedback, "MAYBE") {
		t.Fatalf("expected full body feedback, got %q", feedback)
	}
}

func TestOrchestratorPlanPhase_RejectsMissingAcceptance(t *testing.T) {
	dir := setupPhasesDB(t)
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn: func(c *ChatContext, _ string) {
			c.OnChunk("", false)
			c.OnToolCall("tool", map[string]string{"k": "v"})
			c.OnToolResult("tool", map[string]string{"k": "v"}, true)
			c.OnError("planner soft error")
			c.OnChunk("1. Build thing\n   body without acceptance\n", false)
		},
	}
	state := &OrchestrationState{Brief: "brief", Owner: "o", Repo: "r"}
	cancel := make(chan struct{})
	if err := orchestratorPlanPhase(cfg, state, cancel); err != nil {
		t.Fatalf("expected synthesized acceptance fallback, got %v", err)
	}
	if len(state.Plan) != 1 {
		t.Fatalf("expected one step, got %d", len(state.Plan))
	}
	if !strings.Contains(strings.ToLower(state.Plan[0].Acceptance), "echo") {
		t.Fatalf("expected synthesized runnable acceptance, got %q", state.Plan[0].Acceptance)
	}
}

func TestOrchestratorBuildStep_CreateSessionError(t *testing.T) {
	dir := t.TempDir()
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn:          func(*ChatContext, string) {},
	}
	state := &OrchestrationState{Owner: "o", Repo: "r", Plan: []PlanStep{{Index: 1, Title: "x"}}}
	step := &PlanStep{Index: 1, Title: "x", Acceptance: "`echo ok`"}
	cancel := make(chan struct{})
	err := orchestratorBuildStep(cfg, state, step, "", cancel)
	if err == nil || !strings.Contains(err.Error(), "create step session") {
		t.Fatalf("expected create session error, got %v", err)
	}
}

func TestOrchestratorBuildStep_OnErrorAppendsOutput(t *testing.T) {
	dir := setupPhasesDB(t)
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn: func(c *ChatContext, _ string) {
			c.OnChunk("", false)
			c.OnError("boom")
			c.OnChunk("builder output", false)
		},
	}
	state := &OrchestrationState{Owner: "o", Repo: "r", Plan: []PlanStep{{Index: 1, Title: "x"}}}
	step := &PlanStep{Index: 1, Title: "x", Acceptance: "`echo ok`"}
	cancel := make(chan struct{})
	if err := orchestratorBuildStep(cfg, state, step, "", cancel); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOrchestratorReviewStep_CreateSessionError(t *testing.T) {
	dir := t.TempDir()
	cfg := OrchestratorConfig{ProjectPath: dir, SessionIDPrefix: "t"}
	state := &OrchestrationState{Owner: "o", Repo: "r", Plan: []PlanStep{{Index: 1, Title: "x"}}}
	step := &PlanStep{Index: 1, Title: "x"}
	cancel := make(chan struct{})
	verdict, msg := orchestratorReviewStep(cfg, state, step, cancel)
	if verdict != ReviewInconclusive || !strings.Contains(msg, "create review session") {
		t.Fatalf("expected review session error, got verdict=%v msg=%q", verdict, msg)
	}
}

func TestOrchestratorValidatePhase_CreateSessionError(t *testing.T) {
	dir := t.TempDir()
	cfg := OrchestratorConfig{ProjectPath: dir, SessionIDPrefix: "t"}
	state := &OrchestrationState{Owner: "o", Repo: "r", Brief: "brief"}
	cancel := make(chan struct{})
	_, err := orchestratorValidatePhase(cfg, state, cancel)
	if err == nil || !strings.Contains(err.Error(), "create validate session") {
		t.Fatalf("expected validate session error, got %v", err)
	}
}

func TestOrchestratorPRDPhase_CallsAllCallbacksAndRejectsEmptyHalves(t *testing.T) {
	dir := setupIntakeDB(t)
	if err := WriteDoc(dir, DocDesign, "design"); err != nil {
		t.Fatalf("write design: %v", err)
	}
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn: func(c *ChatContext, _ string) {
			c.OnChunk("", false)
			c.OnToolCall("tool", map[string]string{"k": "v"})
			c.OnToolResult("tool", map[string]string{"k": "v"}, true)
			c.OnError("soft")
			c.OnChunk("---SPLIT---only-prd", false)
		},
	}
	cancel := make(chan struct{})
	err := orchestratorPRDPhase(cfg, cancel)
	if err == nil || !strings.Contains(err.Error(), "---SPLIT---") {
		t.Fatalf("expected split validation error, got %v", err)
	}
}

func TestOrchestratorModuleIndexPhase_RejectsNoOutputWithCallbackCalls(t *testing.T) {
	dir := setupIntakeDB(t)
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		SessionIDPrefix: "t",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		ChatFn: func(c *ChatContext, _ string) {
			c.OnChunk("", false)
			c.OnToolCall("tool", nil)
			c.OnToolResult("tool", nil, true)
			c.OnError("soft")
		},
	}
	cancel := make(chan struct{})
	err := orchestratorModuleIndexPhase(cfg, cancel)
	if err == nil || !strings.Contains(err.Error(), "produced no output") {
		t.Fatalf("expected no output error, got %v", err)
	}
}

func TestReadContextDoc_LegacyPathFallback(t *testing.T) {
	dir := t.TempDir()
	engineDir := filepath.Join(dir, ".engine")
	if err := os.MkdirAll(engineDir, 0o755); err != nil {
		t.Fatalf("mkdir .engine: %v", err)
	}
	legacy := "legacy context"
	if err := os.WriteFile(filepath.Join(engineDir, contextFile), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy context file: %v", err)
	}
	if got := readContextDoc(dir); got != legacy {
		t.Fatalf("expected legacy context fallback, got %q", got)
	}
}

func TestRunEventOrchestrator_DefaultCallbacksAndMaxIterations(t *testing.T) {
	dir, err := os.MkdirTemp("", "engine-event-default-callbacks-")
	if err != nil {
		t.Fatalf("mkdir temp dir: %v", err)
	}
	stateDir, err := os.MkdirTemp("", "engine-event-state-")
	if err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	t.Setenv("ENGINE_STATE_DIR", stateDir)
	if err := db.Init(dir); err != nil {
		t.Fatalf("db init: %v", err)
	}

	cfg := OrchestratorConfig{
		ProjectPath: dir,
		Owner:       "o",
		Repo:        "r",
		Brief:       "brief",
		// Intentionally nil callbacks and nil ChatFn to cover defaulting logic.
		MaxOuterIterations: 0,
		Cancel:            func() <-chan struct{} { ch := make(chan struct{}); close(ch); return ch }(),
	}

	brain, err := RunEventOrchestrator(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if brain == nil {
		t.Fatal("expected non-nil brain")
	}
}

func TestNewChatContextForPhase_CallbackFieldsCallable(t *testing.T) {
	cc := newChatContextForPhase("/tmp/proj", "sess")
	cc.Ctx.OnError("x")
	cc.Ctx.OnToolCall("tool", map[string]string{"a": "b"})
	cc.Ctx.OnToolResult("tool", map[string]string{"a": "b"}, true)
	cc.Ctx.OnChunk("", false)
	cc.Ctx.OnChunk("ok", false)
	if got := cc.GetOutput(); got != "ok" {
		t.Fatalf("expected captured output, got %q", got)
	}
}

func TestRunAutonomousProject_PlanPhaseFailure(t *testing.T) {
	dir := setupPhasesDB(t)
	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "o",
		Repo:               "r",
		Brief:              "brief",
		SessionIDPrefix:    "t",
		MaxOuterIterations: 2,
		OnPhase:            func(string, string) {},
		OnProgress:         func(string) {},
		OnError:            func(string) {},
		ChatFn: func(c *ChatContext, _ string) {
			switch c.Role {
			case RoleGriller:
				c.OnChunk("design", false)
			case RolePRDWriter:
				c.OnChunk("vocab---SPLIT---prd", false)
			case RolePlanner:
				// No planner output => plan phase failure.
			}
		},
	}
	_, err := RunAutonomousProject(cfg)
	if err == nil || !strings.Contains(err.Error(), "plan output empty") {
		t.Fatalf("expected plan phase failure, got %v", err)
	}
}

func TestRunAutonomousProject_RegeneratesInvalidLoadedPlan(t *testing.T) {
	dir := setupPhasesDB(t)
	_ = WriteDoc(dir, DocDesign, "design")
	_ = WriteDoc(dir, DocVocabulary, "vocab")
	_ = WriteDoc(dir, DocPRD, "prd")

	loaded := &OrchestrationState{
		Owner: "o",
		Repo:  "r",
		Brief: "brief",
		Plan: []PlanStep{{
			Index: 1, Title: "bad step", Body: "missing acceptance", Done: false,
		}},
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := persistOrchestration(dir, loaded); err != nil {
		t.Fatalf("persist loaded state: %v", err)
	}

	updates := 0
	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "o",
		Repo:               "r",
		Brief:              "brief",
		SessionIDPrefix:    "t",
		MaxOuterIterations: 3,
		OnPhase:            func(string, string) {},
		OnProgress:         func(string) {},
		OnError:            func(string) {},
		OnPlanUpdate:       func(*OrchestrationState) { updates++ },
		ChatFn: func(c *ChatContext, msg string) {
			switch c.Role {
			case RolePlanner:
				c.OnChunk("1. Build\n   do thing\n   Acceptance: `echo ok`\n", false)
			case RoleAutonomousBuilder:
				c.OnChunk("builder ok", false)
			case RoleReviewer:
				if strings.Contains(msg, "Final behavioral validation") {
					c.OnChunk("validated\nAPPROVE", false)
					return
				}
				c.OnChunk("review\nAPPROVE", false)
			case RoleModuleIndexer:
				c.OnChunk("modules", false)
			}
		},
	}

	st, err := RunAutonomousProject(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st == nil || len(st.Plan) == 0 || !st.Plan[0].Done {
		t.Fatalf("expected completed regenerated plan, got %+v", st)
	}
	if updates == 0 {
		t.Fatal("expected OnPlanUpdate to be called")
	}
}

func TestRunAutonomousProject_SkipsExhaustedAndEmitsSkip(t *testing.T) {
	dir := setupPhasesDB(t)
	_ = WriteDoc(dir, DocDesign, "design")
	_ = WriteDoc(dir, DocVocabulary, "vocab")
	_ = WriteDoc(dir, DocPRD, "prd")
	state := &OrchestrationState{
		Owner: "o", Repo: "r", Brief: "brief",
		Plan: []PlanStep{{Index: 1, Title: "stuck", Attempts: OrchestratorMaxStepAttempts, Done: false, Acceptance: "`echo ok`"}},
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := persistOrchestration(dir, state); err != nil {
		t.Fatalf("persist state: %v", err)
	}

	var phases []string
	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "o",
		Repo:               "r",
		Brief:              "brief",
		SessionIDPrefix:    "t",
		MaxOuterIterations: 2,
		OnPhase: func(phase, detail string) {
			phases = append(phases, phase+":"+detail)
		},
		OnProgress: func(string) {},
		OnError:    func(string) {},
		ChatFn: func(c *ChatContext, msg string) {
			if c.Role == RoleReviewer && strings.Contains(msg, "Final behavioral validation") {
				c.OnChunk("ok\nAPPROVE", false)
			}
		},
	}

	st, err := RunAutonomousProject(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st == nil || len(st.Plan) == 0 || !st.Plan[0].Done {
		t.Fatalf("expected skipped step done, got %+v", st)
	}
	foundSkip := false
	for _, p := range phases {
		if strings.Contains(p, "skip:") {
			foundSkip = true
			break
		}
	}
	if !foundSkip {
		t.Fatalf("expected skip phase emission, got %v", phases)
	}
}

func TestRunAutonomousProject_UsesLiveURLWhenPresent(t *testing.T) {
	dir := setupPhasesDB(t)
	_ = WriteDoc(dir, DocDesign, "design")
	_ = WriteDoc(dir, DocVocabulary, "vocab")
	_ = WriteDoc(dir, DocPRD, "prd")
	if err := os.WriteFile(filepath.Join(dir, ".engine", "live-url.txt"), []byte("https://example.com"), 0o644); err != nil {
		t.Fatalf("write live-url: %v", err)
	}

	state := &OrchestrationState{
		Owner: "o", Repo: "r", Brief: "brief",
		Plan:      []PlanStep{{Index: 1, Title: "already done", Done: true, Acceptance: "`echo ok`"}},
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := persistOrchestration(dir, state); err != nil {
		t.Fatalf("persist state: %v", err)
	}

	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "o",
		Repo:               "r",
		Brief:              "brief",
		SessionIDPrefix:    "t",
		MaxOuterIterations: 2,
		OnPhase:            func(string, string) {},
		OnProgress:         func(string) {},
		OnError:            func(string) {},
		ChatFn: func(c *ChatContext, msg string) {
			if c.Role == RoleReviewer && strings.Contains(msg, "Final behavioral validation") {
				c.OnChunk("ok\nAPPROVE", false)
			}
		},
	}

	st, err := RunAutonomousProject(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.LiveURL != "https://example.com" {
		t.Fatalf("expected live URL to be loaded, got %q", st.LiveURL)
	}
}

func TestRunAutonomousProject_BuilderErrorBranch(t *testing.T) {
	dir := setupPhasesDB(t)
	_ = WriteDoc(dir, DocDesign, "design")
	_ = WriteDoc(dir, DocVocabulary, "vocab")
	_ = WriteDoc(dir, DocPRD, "prd")
	state := &OrchestrationState{
		Owner: "o", Repo: "r", Brief: "brief",
		Plan:      []PlanStep{{Index: 1, Title: "build", Done: false, Acceptance: "`echo ok`"}},
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := persistOrchestration(dir, state); err != nil {
		t.Fatalf("persist state: %v", err)
	}

	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "o",
		Repo:               "r",
		Brief:              "brief",
		SessionIDPrefix:    "t",
		MaxOuterIterations: 1,
		OnPhase:            func(string, string) {},
		OnProgress:         func(string) {},
		OnError:            func(string) {},
		ChatFn:             func(*ChatContext, string) {}, // builder emits no output
	}

	st, err := RunAutonomousProject(cfg)
	if err == nil || !strings.Contains(err.Error(), "safety cap") {
		t.Fatalf("expected eventual safety cap, got state=%+v err=%v", st, err)
	}
}

func TestRunAutonomousProject_ReviewRejectBranch(t *testing.T) {
	dir := setupPhasesDB(t)
	_ = WriteDoc(dir, DocDesign, "design")
	_ = WriteDoc(dir, DocVocabulary, "vocab")
	_ = WriteDoc(dir, DocPRD, "prd")
	state := &OrchestrationState{
		Owner: "o", Repo: "r", Brief: "brief",
		Plan:      []PlanStep{{Index: 1, Title: "build", Done: false, Acceptance: "`echo ok`"}},
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := persistOrchestration(dir, state); err != nil {
		t.Fatalf("persist state: %v", err)
	}

	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "o",
		Repo:               "r",
		Brief:              "brief",
		SessionIDPrefix:    "t",
		MaxOuterIterations: 1,
		OnPhase:            func(string, string) {},
		OnProgress:         func(string) {},
		OnError:            func(string) {},
		ChatFn: func(c *ChatContext, msg string) {
			switch c.Role {
			case RoleAutonomousBuilder:
				c.OnChunk("built", false)
			case RoleReviewer:
				if strings.Contains(msg, "Final behavioral validation") {
					c.OnChunk("ok\nAPPROVE", false)
					return
				}
				c.OnChunk("reject\nREJECT: fix it", false)
			}
		},
	}

	st, err := RunAutonomousProject(cfg)
	if err == nil || !strings.Contains(err.Error(), "safety cap") {
		t.Fatalf("expected eventual safety cap, got state=%+v err=%v", st, err)
	}
}

func TestOrchestratorMergedCancel_SecondChannelTriggers(t *testing.T) {
	a := make(chan struct{})
	b := make(chan struct{})
	merged := orchestratorMergedCancel(a, b)
	close(b)
	select {
	case <-merged:
	case <-time.After(time.Second):
		t.Fatal("expected merged cancel to close when second channel closes")
	}
}

func TestPersistOrchestration_WriteFileErrorOnDirectoryPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".engine", orchestrationFile), 0o755); err != nil {
		t.Fatalf("mkdir orchestration path as dir: %v", err)
	}
	state := &OrchestrationState{Owner: "o", Repo: "r"}
	if err := persistOrchestration(dir, state); err == nil {
		t.Fatal("expected write error when orchestration path is a directory")
	}
}

func TestRunEventOrchestrator_ErrorPaths(t *testing.T) {
	cfg := OrchestratorConfig{ProjectPath: ""}
	if _, err := RunEventOrchestrator(cfg); err == nil || !strings.Contains(err.Error(), "project path") {
		t.Fatalf("expected RunEventOrchestrator project path error, got %v", err)
	}
	if _, err := RunEventOrchestratorAsState(cfg); err == nil || !strings.Contains(err.Error(), "project path") {
		t.Fatalf("expected RunEventOrchestratorAsState project path error, got %v", err)
	}
}

func TestReadyTeams_BlockedMissingDependency(t *testing.T) {
	dir := t.TempDir()
	brain, _ := NewOrchestrationBrain(dir, "owner", "repo", "brief", "t")
	if err := brain.AddTeam("team-1", "api", []int{0}, []string{"missing"}); err != nil {
		t.Fatalf("add team: %v", err)
	}
	ready := brain.ReadyTeams()
	if len(ready) != 0 {
		t.Fatalf("expected blocked team to be excluded, got %d", len(ready))
	}
}

func TestBrainPersist_WriteError(t *testing.T) {
	base := t.TempDir()
	projectPath := filepath.Join(base, "project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, ".engine"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}

	brain, _ := NewOrchestrationBrain(projectPath, "owner", "repo", "brief", "t")
	if err := brain.persist(); err == nil {
		t.Fatal("expected persist error")
	}
}

