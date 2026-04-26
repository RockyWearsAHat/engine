package ai

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// ── WorkingState.RecordAttempt ────────────────────────────────────────────────

func TestWorkingState_RecordAttempt_IncrementsCount(t *testing.T) {
	ws := &WorkingState{}
	ws.RecordAttempt("")
	if ws.AttemptCount != 1 {
		t.Errorf("expected 1 attempt, got %d", ws.AttemptCount)
	}
}

func TestWorkingState_RecordAttempt_AppendsApproach(t *testing.T) {
	ws := &WorkingState{}
	ws.RecordAttempt("direct edit")
	if len(ws.FailedApproaches) != 1 || ws.FailedApproaches[0] != "direct edit" {
		t.Errorf("expected failed approach stored, got %v", ws.FailedApproaches)
	}
}

func TestWorkingState_RecordAttempt_EmptyApproachNotAppended(t *testing.T) {
	ws := &WorkingState{}
	ws.RecordAttempt("")
	if len(ws.FailedApproaches) != 0 {
		t.Errorf("expected no failed approaches for empty string, got %v", ws.FailedApproaches)
	}
}

func TestWorkingState_RecordAttempt_SetsUpdatedAt(t *testing.T) {
	before := time.Now().Add(-time.Millisecond)
	ws := &WorkingState{}
	ws.RecordAttempt("")
	if ws.UpdatedAt.Before(before) {
		t.Error("expected UpdatedAt to be set to now")
	}
}

// ── WorkingState.AddObservation ───────────────────────────────────────────────

func TestWorkingState_AddObservation_AppendsNonEmpty(t *testing.T) {
	ws := &WorkingState{}
	ws.AddObservation("tests passed")
	if len(ws.Observations) != 1 || ws.Observations[0] != "tests passed" {
		t.Errorf("expected observation stored, got %v", ws.Observations)
	}
}

func TestWorkingState_AddObservation_IgnoresEmpty(t *testing.T) {
	ws := &WorkingState{}
	ws.AddObservation("")
	if len(ws.Observations) != 0 {
		t.Errorf("expected no observation for empty string, got %v", ws.Observations)
	}
}

// ── WorkingState.SetSuccess ───────────────────────────────────────────────────

func TestWorkingState_SetSuccess_StoresDesc(t *testing.T) {
	ws := &WorkingState{}
	ws.SetSuccess("extracted helper func")
	if ws.LastSuccess != "extracted helper func" {
		t.Errorf("expected LastSuccess set, got %q", ws.LastSuccess)
	}
}

// ── WorkingState.ContextBlock ─────────────────────────────────────────────────

func TestWorkingState_ContextBlock_EmptyTaskReturnsEmpty(t *testing.T) {
	ws := &WorkingState{}
	if ws.ContextBlock() != "" {
		t.Error("expected empty string for zero-value WorkingState")
	}
}

func TestWorkingState_ContextBlock_NilReturnsEmpty(t *testing.T) {
	var ws *WorkingState
	if ws.ContextBlock() != "" {
		t.Error("expected empty string for nil WorkingState")
	}
}

func TestWorkingState_ContextBlock_ContainsTask(t *testing.T) {
	ws := &WorkingState{CurrentTask: "refactor auth module"}
	block := ws.ContextBlock()
	if !strings.Contains(block, "refactor auth module") {
		t.Errorf("expected task in block, got %q", block)
	}
}

func TestWorkingState_ContextBlock_ContainsFailedApproaches(t *testing.T) {
	ws := &WorkingState{
		CurrentTask:      "fix login",
		FailedApproaches: []string{"patch A", "patch B"},
	}
	block := ws.ContextBlock()
	if !strings.Contains(block, "patch A") {
		t.Errorf("expected failed approaches in block, got %q", block)
	}
}

func TestWorkingState_ContextBlock_CapsFiveObservations(t *testing.T) {
	ws := &WorkingState{CurrentTask: "t", Observations: []string{"1", "2", "3", "4", "5", "6", "7"}}
	block := ws.ContextBlock()
	// Only last 5 should appear; "1" and "2" should be trimmed
	if strings.Contains(block, "recent_observations: 1") {
		t.Error("expected early observations trimmed from block")
	}
	if !strings.Contains(block, "7") {
		t.Error("expected most-recent observation in block")
	}
}

func TestWorkingState_ContextBlock_WrappedInXMLTags(t *testing.T) {
	ws := &WorkingState{CurrentTask: "test task"}
	block := ws.ContextBlock()
	if !strings.HasPrefix(block, "<working_state>") {
		t.Errorf("expected <working_state> tag prefix, got %q", block)
	}
	if !strings.HasSuffix(block, "</working_state>") {
		t.Errorf("expected </working_state> suffix, got %q", block)
	}
}

// ── PersistWorkingState / LoadWorkingStateForSession ─────────────────────────

func TestPersistAndLoad_RoundTrip(t *testing.T) {
	var saved map[string]string
	orig := saveWorkingStateFn
	origLoad := loadWorkingStateFn
	t.Cleanup(func() {
		saveWorkingStateFn = orig
		loadWorkingStateFn = origLoad
	})

	saveWorkingStateFn = func(sessionID, stateJSON string) error {
		saved = map[string]string{sessionID: stateJSON}
		return nil
	}
	loadWorkingStateFn = func(sessionID string) (string, error) {
		return saved[sessionID], nil
	}

	ws := WorkingState{
		CurrentTask:  "implement classifier",
		AttemptCount: 2,
	}
	PersistWorkingState("sess-1", &ws)

	loaded := LoadWorkingStateForSession("sess-1")
	if loaded.CurrentTask != "implement classifier" {
		t.Errorf("expected task preserved through round-trip, got %q", loaded.CurrentTask)
	}
	if loaded.AttemptCount != 2 {
		t.Errorf("expected attempt count preserved, got %d", loaded.AttemptCount)
	}
}

func TestPersistWorkingState_NilIgnored(t *testing.T) {
	called := false
	orig := saveWorkingStateFn
	t.Cleanup(func() { saveWorkingStateFn = orig })
	saveWorkingStateFn = func(string, string) error { called = true; return nil }

	PersistWorkingState("sess-1", nil)
	if called {
		t.Error("expected no DB call for nil WorkingState")
	}
}

func TestLoadWorkingState_EmptySessionReturnsZero(t *testing.T) {
	ws := LoadWorkingStateForSession("")
	if ws.CurrentTask != "" || ws.AttemptCount != 0 {
		t.Error("expected zero-value WorkingState for empty session ID")
	}
}

// ── ContextBlock: optional field branches ─────────────────────────────────────

func TestWorkingState_ContextBlock_WithHypothesis(t *testing.T) {
	ws := &WorkingState{CurrentTask: "fix bug", CurrentHypothesis: "nil deref in handler"}
	block := ws.ContextBlock()
	if !strings.Contains(block, "hypothesis: nil deref in handler") {
		t.Errorf("expected hypothesis in block, got %q", block)
	}
}

func TestWorkingState_ContextBlock_WithLastSuccess(t *testing.T) {
	ws := &WorkingState{CurrentTask: "fix bug", LastSuccess: "tests passed after removing nil"}
	block := ws.ContextBlock()
	if !strings.Contains(block, "last_success: tests passed after removing nil") {
		t.Errorf("expected last_success in block, got %q", block)
	}
}

// ── LoadWorkingStateForSession: invalid JSON branch ───────────────────────────

func TestLoadWorkingStateForSession_InvalidJSON_ReturnsZero(t *testing.T) {
	orig := loadWorkingStateFn
	t.Cleanup(func() { loadWorkingStateFn = orig })
	loadWorkingStateFn = func(string) (string, error) {
		return "{not valid json", nil
	}

	ws := LoadWorkingStateForSession("sess-bad")
	if ws.CurrentTask != "" {
		t.Errorf("expected zero-value on invalid JSON, got task %q", ws.CurrentTask)
	}
}

func TestLoadWorkingStateForSession_LoadError_ReturnsZero(t *testing.T) {
	orig := loadWorkingStateFn
	t.Cleanup(func() { loadWorkingStateFn = orig })
	loadWorkingStateFn = func(string) (string, error) {
		return "", errors.New("db unavailable")
	}

	ws := LoadWorkingStateForSession("sess-err")
	if ws.CurrentTask != "" || ws.AttemptCount != 0 {
		t.Error("expected zero-value WorkingState on load error")
	}
}
