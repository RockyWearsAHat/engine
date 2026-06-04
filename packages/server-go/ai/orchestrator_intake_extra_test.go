package ai

import (
	"strings"
	"testing"

	"github.com/engine/server/db"
)

// setupIntakeDB initialises the DB so db.CreateSession succeeds.
func setupIntakeDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	stateDir := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", stateDir)
	if err := db.Init(dir); err != nil {
		t.Fatalf("db.Init: %v", err)
	}
	return dir
}

// TestBuildGrillerPrompt verifies the brief appears in the output.
func TestBuildGrillerPrompt(t *testing.T) {
	out := buildGrillerPrompt("my test brief")
	if !strings.Contains(out, "my test brief") {
		t.Errorf("expected brief in prompt, got %q", out)
	}
}

// TestOrchestratorIntakePhase_ShortCircuit verifies pre-written DocDesign skips DB.
func TestOrchestratorIntakePhase_ShortCircuit(t *testing.T) {
	dir := t.TempDir()
	if err := WriteDoc(dir, DocDesign, "existing design content"); err != nil {
		t.Fatalf("write existing design doc: %v", err)
	}
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		SessionIDPrefix: "t",
	}
	state := &OrchestrationState{Brief: "brief"}
	cancel := make(chan struct{})
	got, err := orchestratorIntakePhase(cfg, state, cancel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "existing design content") {
		t.Errorf("expected existing content, got %q", got)
	}
}

// TestOrchestratorIntakePhase_LegacyContext verifies legacy DocContext is used when DocDesign absent.
func TestOrchestratorIntakePhase_LegacyContext(t *testing.T) {
	dir := t.TempDir()
	if err := WriteDoc(dir, DocContext, "legacy design content"); err != nil {
		t.Fatalf("write legacy context doc: %v", err)
	}
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		SessionIDPrefix: "t",
	}
	state := &OrchestrationState{Brief: "brief"}
	cancel := make(chan struct{})
	got, err := orchestratorIntakePhase(cfg, state, cancel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "legacy design content") {
		t.Errorf("expected legacy content, got %q", got)
	}
}

// TestOrchestratorIntakePhase_WithDB verifies the full path via DB + ChatFn.
func TestOrchestratorIntakePhase_WithDB(t *testing.T) {
	dir := setupIntakeDB(t)
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		SessionIDPrefix: "t",
		ChatFn:          func(c *ChatContext, _ string) { c.OnChunk("generated design", false) },
	}
	state := &OrchestrationState{Brief: "build something"}
	cancel := make(chan struct{})
	got, err := orchestratorIntakePhase(cfg, state, cancel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "generated design") {
		t.Errorf("expected generated design, got %q", got)
	}
}

// TestOrchestratorPRDPhase_ShortCircuit verifies both docs existing skips full phase.
func TestOrchestratorPRDPhase_ShortCircuit(t *testing.T) {
	dir := t.TempDir()
	if err := WriteDoc(dir, DocVocabulary, "vocab content"); err != nil {
		t.Fatalf("write vocab doc: %v", err)
	}
	if err := WriteDoc(dir, DocPRD, "prd content"); err != nil {
		t.Fatalf("write prd doc: %v", err)
	}
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		SessionIDPrefix: "t",
	}
	cancel := make(chan struct{})
	if err := orchestratorPRDPhase(cfg, cancel); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestOrchestratorPRDPhase_MissingDesign verifies error when design.md absent.
func TestOrchestratorPRDPhase_MissingDesign(t *testing.T) {
	dir := t.TempDir()
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		SessionIDPrefix: "t",
	}
	cancel := make(chan struct{})
	err := orchestratorPRDPhase(cfg, cancel)
	if err == nil {
		t.Error("expected error when design.md missing")
	}
}

// TestOrchestratorPRDPhase_WithDB verifies the full path with DB + ChatFn + split output.
func TestOrchestratorPRDPhase_WithDB(t *testing.T) {
	dir := setupIntakeDB(t)
	if err := WriteDoc(dir, DocDesign, "my design concept"); err != nil {
		t.Fatalf("write design doc: %v", err)
	}
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		SessionIDPrefix: "t",
		ChatFn: func(c *ChatContext, _ string) {
			c.OnChunk("vocab content---SPLIT---prd content", false)
		},
	}
	cancel := make(chan struct{})
	if err := orchestratorPRDPhase(cfg, cancel); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prd := ReadDoc(dir, DocPRD)
	if !strings.Contains(prd, "prd content") {
		t.Errorf("expected prd content written, got %q", prd)
	}
}

// TestOrchestratorModuleIndexPhase_WithDB verifies the phase with DB + ChatFn.
func TestOrchestratorModuleIndexPhase_WithDB(t *testing.T) {
	dir := setupIntakeDB(t)
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		SessionIDPrefix: "t",
		ChatFn:          func(c *ChatContext, _ string) { c.OnChunk("module index output", false) },
	}
	cancel := make(chan struct{})
	if err := orchestratorModuleIndexPhase(cfg, cancel); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	modules := ReadDoc(dir, DocModules)
	if !strings.Contains(modules, "module index output") {
		t.Errorf("expected module index written, got %q", modules)
	}
}

// TestOrchestratorModuleIndexPhase_EmptyOutput verifies error when ChatFn emits nothing.
func TestOrchestratorModuleIndexPhase_EmptyOutput(t *testing.T) {
	dir := setupIntakeDB(t)
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		SessionIDPrefix: "t",
		ChatFn:          func(_ *ChatContext, _ string) {}, // emits nothing
	}
	cancel := make(chan struct{})
	if err := orchestratorModuleIndexPhase(cfg, cancel); err == nil {
		t.Error("expected error when module indexer produced no output")
	}
}

// TestOrchestratorArchitectReview_WithDB verifies the architect review path.
func TestOrchestratorArchitectReview_WithDB(t *testing.T) {
	dir := setupIntakeDB(t)
	cfg := OrchestratorConfig{
		ProjectPath:     dir,
		Owner:           "o",
		Repo:            "r",
		OnPhase:         func(string, string) {},
		OnProgress:      func(string) {},
		OnError:         func(string) {},
		SessionIDPrefix: "t",
		ChatFn:          func(c *ChatContext, _ string) { c.OnChunk("LGTM, no issues found.", false) },
	}
	state := &OrchestrationState{Owner: "o", Repo: "r"}
	step := &PlanStep{Index: 1, Title: "implement feature"}
	cancel := make(chan struct{})
	verdict, _ := orchestratorArchitectReview(cfg, state, step, cancel)
	// Verdict is ReviewInconclusive or some parsed value; just confirm no panic.
	_ = verdict
}
