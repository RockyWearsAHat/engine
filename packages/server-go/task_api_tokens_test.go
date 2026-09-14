package main

import (
	"testing"
	"time"
)

// TestSessionTokensAndTurnsCredit verifies that when a session exits, its token
// counts and turns are immediately credited to the running task record, so the
// engine can see activity before the task completes.
func TestSessionTokensAndTurnsCredit(t *testing.T) {
	task := &engineTask{
		ID:          "test-task-1",
		ProjectPath: "/tmp/test-project",
		Status:      taskRunning,
		StartedAt:   time.Now(),
		cancel:      make(chan struct{}),
	}

	// Simulate first session exit
	task.addSessionTokensAndTurns(100, 200, 5)

	// Verify first session's tokens are credited
	task.mu.RLock()
	if task.TokensIn != 100 {
		t.Errorf("after first session: TokensIn = %d, want 100", task.TokensIn)
	}
	if task.TokensOut != 200 {
		t.Errorf("after first session: TokensOut = %d, want 200", task.TokensOut)
	}
	if task.Turns != 5 {
		t.Errorf("after first session: Turns = %d, want 5", task.Turns)
	}
	firstLastTokenAt := task.LastTokenAt
	if firstLastTokenAt == nil {
		t.Error("after first session: LastTokenAt = nil, want non-nil")
	}
	task.mu.RUnlock()

	// Small delay to ensure LastTokenAt differs if timing is fast
	time.Sleep(10 * time.Millisecond)

	// Simulate second session exit
	task.addSessionTokensAndTurns(50, 150, 3)

	// Verify second session's tokens are added to the first
	task.mu.RLock()
	if task.TokensIn != 150 {
		t.Errorf("after second session: TokensIn = %d, want 150", task.TokensIn)
	}
	if task.TokensOut != 350 {
		t.Errorf("after second session: TokensOut = %d, want 350", task.TokensOut)
	}
	if task.Turns != 8 {
		t.Errorf("after second session: Turns = %d, want 8", task.Turns)
	}
	secondLastTokenAt := task.LastTokenAt
	if secondLastTokenAt == nil {
		t.Error("after second session: LastTokenAt = nil, want non-nil")
	}
	if secondLastTokenAt.Before(*firstLastTokenAt) {
		t.Error("after second session: LastTokenAt should be >= previous LastTokenAt")
	}
	task.mu.RUnlock()

	// Verify snapshot includes the new fields
	snapshot := task.snapshot()
	if tokensIn, ok := snapshot["tokensIn"].(int64); !ok || tokensIn != 150 {
		t.Errorf("snapshot tokensIn = %v, want 150", snapshot["tokensIn"])
	}
	if tokensOut, ok := snapshot["tokensOut"].(int64); !ok || tokensOut != 350 {
		t.Errorf("snapshot tokensOut = %v, want 350", snapshot["tokensOut"])
	}
	if turns, ok := snapshot["turns"].(int64); !ok || turns != 8 {
		t.Errorf("snapshot turns = %v, want 8", snapshot["turns"])
	}
	if snapshot["lastTokenAt"] == nil {
		t.Error("snapshot lastTokenAt = nil, want non-nil")
	}
}

// TestPublicAddSessionTokensAndTurnsToTask verifies the exported function that
// the task API wires up to the orchestrator config works correctly.
func TestPublicAddSessionTokensAndTurnsToTask(t *testing.T) {
	task := &engineTask{
		ID:          "test-task-2",
		ProjectPath: "/tmp/test-project",
		Status:      taskRunning,
		StartedAt:   time.Now(),
		cancel:      make(chan struct{}),
	}

	// Register task in the registry
	tasks.put("", task)

	// Call the public function
	AddSessionTokensAndTurnsToTask("test-task-2", 75, 225, 4)

	// Verify the tokens were credited
	task.mu.RLock()
	if task.TokensIn != 75 {
		t.Errorf("TokensIn = %d, want 75", task.TokensIn)
	}
	if task.TokensOut != 225 {
		t.Errorf("TokensOut = %d, want 225", task.TokensOut)
	}
	if task.Turns != 4 {
		t.Errorf("Turns = %d, want 4", task.Turns)
	}
	task.mu.RUnlock()
}

// TestAddSessionTokensAndTurnsToTaskWithNonExistentTask verifies the public
// function handles non-existent task IDs gracefully.
func TestAddSessionTokensAndTurnsToTaskWithNonExistentTask(t *testing.T) {
	// This should not panic
	AddSessionTokensAndTurnsToTask("nonexistent-task", 100, 200, 5)
}
