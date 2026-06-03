package ai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEmit_NilFn verifies emit is safe with a nil callback.
func TestEmit_NilFn(t *testing.T) {
	emit(nil, "plan", "detail") // must not panic
}

// TestEmit_NonNilFn verifies emit calls the callback with the right args.
func TestEmit_NonNilFn(t *testing.T) {
	var gotPhase, gotDetail string
	emit(func(phase, detail string) {
		gotPhase = phase
		gotDetail = detail
	}, "execute", "building step 1")
	if gotPhase != "execute" || gotDetail != "building step 1" {
		t.Errorf("emit args: phase=%q detail=%q", gotPhase, gotDetail)
	}
}

// TestEmitErr_NilFn verifies emitErr is safe with a nil callback.
func TestEmitErr_NilFn(t *testing.T) {
	emitErr(nil, "something failed") // must not panic
}

// TestEmitErr_NonNilFn verifies emitErr calls the callback.
func TestEmitErr_NonNilFn(t *testing.T) {
	var got string
	emitErr(func(msg string) { got = msg }, "test error")
	if got != "test error" {
		t.Errorf("got %q", got)
	}
}

// TestCancelClosed_Nil verifies nil channel returns false.
func TestCancelClosed_Nil(t *testing.T) {
	if cancelClosed(nil) {
		t.Error("nil channel should return false")
	}
}

// TestCancelClosed_Open verifies open channel returns false.
func TestCancelClosed_Open(t *testing.T) {
	ch := make(chan struct{})
	if cancelClosed(ch) {
		t.Error("open channel should return false")
	}
}

// TestCancelClosed_Closed verifies closed channel returns true.
func TestCancelClosed_Closed(t *testing.T) {
	ch := make(chan struct{})
	close(ch)
	if !cancelClosed(ch) {
		t.Error("closed channel should return true")
	}
}

// TestOrchestratorMergedCancel_BothNil verifies nil+nil returns nil.
func TestOrchestratorMergedCancel_BothNil(t *testing.T) {
	if ch := orchestratorMergedCancel(nil, nil); ch != nil {
		t.Error("both nil should return nil")
	}
}

// TestOrchestratorMergedCancel_AOnly verifies only-a returns a.
func TestOrchestratorMergedCancel_AOnly(t *testing.T) {
	a := make(chan struct{})
	merged := orchestratorMergedCancel(a, nil)
	if merged != a {
		t.Error("only-a should return a directly")
	}
}

// TestOrchestratorMergedCancel_BOnly verifies only-b returns b.
func TestOrchestratorMergedCancel_BOnly(t *testing.T) {
	b := make(chan struct{})
	merged := orchestratorMergedCancel(nil, b)
	if merged != b {
		t.Error("only-b should return b directly")
	}
}

// TestOrchestratorMergedCancel_BothSet verifies merged closes when either input closes.
func TestOrchestratorMergedCancel_BothSet(t *testing.T) {
	a := make(chan struct{})
	b := make(chan struct{})
	merged := orchestratorMergedCancel(a, b)
	close(a)
	select {
	case <-merged:
		// expected
	case <-time.After(time.Second):
		t.Fatal("merged channel did not close when a closed")
	}
}

// TestCountUnchecked covers countUnchecked with various states.
func TestCountUnchecked(t *testing.T) {
	state := &OrchestrationState{
		Plan: []PlanStep{
			{Done: true},
			{Done: false},
			{Done: false},
		},
	}
	if got := countUnchecked(state); got != 2 {
		t.Errorf("got %d, want 2", got)
	}
	state2 := &OrchestrationState{Plan: []PlanStep{{Done: true}}}
	if got := countUnchecked(state2); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

// TestPickNextStep_AllDone verifies -1 returned when all steps done.
func TestPickNextStep_AllDone(t *testing.T) {
	state := &OrchestrationState{Plan: []PlanStep{{Done: true}, {Done: true}}}
	if got := pickNextStep(state); got != -1 {
		t.Errorf("got %d, want -1", got)
	}
}

// TestPickNextStep_Empty verifies -1 returned for empty plan.
func TestPickNextStep_Empty(t *testing.T) {
	state := &OrchestrationState{Plan: nil}
	if got := pickNextStep(state); got != -1 {
		t.Errorf("got %d, want -1", got)
	}
}

// TestPickNextStep_FindsFirst verifies first undone index returned.
func TestPickNextStep_FindsFirst(t *testing.T) {
	state := &OrchestrationState{Plan: []PlanStep{{Done: true}, {Done: false}, {Done: false}}}
	if got := pickNextStep(state); got != 1 {
		t.Errorf("got %d, want 1", got)
	}
}

// TestUnmarkLastDone_Empty verifies nil returned for empty plan.
func TestUnmarkLastDone_Empty(t *testing.T) {
	state := &OrchestrationState{Plan: nil}
	if got := unmarkLastDone(state); got != nil {
		t.Error("expected nil for empty plan")
	}
}

// TestUnmarkLastDone_NoDone verifies nil returned when no done steps.
func TestUnmarkLastDone_NoDone(t *testing.T) {
	state := &OrchestrationState{Plan: []PlanStep{{Done: false}, {Done: false}}}
	if got := unmarkLastDone(state); got != nil {
		t.Error("expected nil when no done steps")
	}
}

// TestUnmarkLastDone_HasDone verifies last done step is unmarked.
func TestUnmarkLastDone_HasDone(t *testing.T) {
	state := &OrchestrationState{Plan: []PlanStep{{Done: true, Index: 1}, {Done: true, Index: 2}}}
	got := unmarkLastDone(state)
	if got == nil {
		t.Fatal("expected non-nil step")
	}
	if got.Index != 2 {
		t.Errorf("expected last done (index 2), got %d", got.Index)
	}
	if state.Plan[1].Done {
		t.Error("step should be unmarked after call")
	}
	if !state.Plan[0].Done {
		t.Error("earlier step should remain done")
	}
}

// TestReadLiveURL_Missing verifies empty string when file absent.
func TestReadLiveURL_Missing(t *testing.T) {
	dir := t.TempDir()
	if got := readLiveURL(dir); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// TestReadLiveURL_Present verifies trimmed content returned when file exists.
func TestReadLiveURL_Present(t *testing.T) {
	dir := t.TempDir()
	engineDir := filepath.Join(dir, ".engine")
	if err := os.MkdirAll(engineDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(engineDir, "live-url.txt"), []byte("  https://myapp.example.com  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readLiveURL(dir); got != "https://myapp.example.com" {
		t.Errorf("got %q, want %q", got, "https://myapp.example.com")
	}
}

// TestChatFnFor_Nil verifies nil ChatFn falls back to runChatFn.
func TestChatFnFor_Nil(t *testing.T) {
	cfg := OrchestratorConfig{}
	fn := cfg.chatFnFor()
	if fn == nil {
		t.Error("expected non-nil fallback function")
	}
}

// TestChatFnFor_NonNil verifies set ChatFn is returned.
func TestChatFnFor_NonNil(t *testing.T) {
	called := false
	customFn := func(ctx *ChatContext, msg string) { called = true }
	cfg := OrchestratorConfig{ChatFn: customFn}
	fn := cfg.chatFnFor()
	fn(nil, "")
	if !called {
		t.Error("expected custom ChatFn to be called")
	}
}

// TestOrchestratorHandle_NilSafe verifies all handle methods handle nil receiver.
func TestOrchestratorHandle_NilSafe(t *testing.T) {
	var h *OrchestratorHandle
	h.Stop()
	h.Pause()
	h.Resume()
	h.Redirect("msg")
	if got := h.takeRedirect(); got != "" {
		t.Errorf("nil takeRedirect expected empty, got %q", got)
	}
	if h.isPaused() {
		t.Error("nil isPaused should return false")
	}
}

// TestRunAutonomousProject_EmptyPath verifies an empty project path returns error.
func TestRunAutonomousProject_EmptyPath(t *testing.T) {
	cfg := OrchestratorConfig{ProjectPath: ""}
	_, err := RunAutonomousProject(cfg)
	if err == nil {
		t.Error("expected error for empty project path")
	}
}

func TestRunAutonomousProject_RepoRunWithoutStartupTeamStillErrorsAsBefore(t *testing.T) {
	dir := t.TempDir()
	cfg := OrchestratorConfig{ProjectPath: dir, Owner: "o", Repo: "r", MaxOuterIterations: 1}
	_, err := RunAutonomousProject(cfg)
	if err == nil {
		t.Fatal("expected orchestrator error")
	}
	// No startup team should not hard-fail at bootstrap; error source should be
	// the regular execution flow (for example planner/provider setup).
	if strings.Contains(err.Error(), "startup team required") {
		t.Fatalf("unexpected startup-team hard failure: %v", err)
	}
}

func TestRunAutonomousProject_CancelWhilePaused(t *testing.T) {
	h := &OrchestratorHandle{cancel: make(chan struct{})}
	h.paused = true

	paused, err := orchestratorPausedStep(h.cancel, h)
	if err != nil {
		t.Fatalf("unexpected error before cancellation: %v", err)
	}
	if !paused {
		t.Fatal("expected paused state before cancellation")
	}

	close(h.cancel)
	paused, err = orchestratorPausedStep(h.cancel, h)
	if err == nil || !strings.Contains(err.Error(), "cancelled while paused") {
		t.Fatalf("expected paused cancellation error, got %v", err)
	}
	if paused {
		t.Fatal("expected paused result to be false after cancellation")
	}
}

// TestRunAutonomousProject_EventOrchestratorRoute verifies USE_EVENT_ORCHESTRATOR=1
// routes to the event orchestrator path.
func TestRunAutonomousProject_EventOrchestratorRoute(t *testing.T) {
	t.Setenv("USE_EVENT_ORCHESTRATOR", "1")
	dir := t.TempDir()
	cfg := OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "",
		Repo:               "",
		Brief:              "test brief",
		MaxOuterIterations: 1,
		ChatFn: func(ctx *ChatContext, msg string) {
			ctx.OnChunk("design output", false)
		},
		OnProgress: func(string) {},
		OnPhase:    func(string, string) {},
		OnError:    func(string) {},
	}
	// This calls RunEventOrchestratorAsState which returns quickly with
	// an EventOrchestrator running in a goroutine.
	state, err := RunAutonomousProject(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state == nil {
		t.Error("expected non-nil state from event orchestrator path")
	}
	// Give goroutine a moment to start before test exits.
	time.Sleep(20 * time.Millisecond)
}

