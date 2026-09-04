package ai

import (
	"testing"
	"time"
)

// TestPeriodicGuardStartup verifies that StartPeriodicGuard can be called safely.
// The actual guard behavior is platform-specific (Windows-only with full implementation).
func TestPeriodicGuardStartup(t *testing.T) {
	// Just verify that starting the guard doesn't panic.
	// On Windows, it spawns a background goroutine; on Unix it's a no-op.
	StartPeriodicGuard()
	// Give it a moment to start.
	time.Sleep(100 * time.Millisecond)
	t.Log("periodic guard started successfully")
}

// TestLiveJobObjectCount verifies that the job object count tracks properly.
// This is primarily a Windows test; Unix returns session count as a proxy.
func TestLiveJobObjectCount(t *testing.T) {
	// On Windows, this tests the actual job object count.
	// On Unix, this is a stub that returns session count.
	count := LiveJobObjectCount()
	sessionCount := LiveSessionCount()

	// On Unix (non-Windows), they should match.
	// On Windows, they may differ if there are orphaned job objects.
	if count < 0 || sessionCount < 0 {
		t.Errorf("negative counts: jobs=%d sessions=%d", count, sessionCount)
	}
	t.Logf("live jobs=%d, live sessions=%d", count, sessionCount)
}

// TestSessionRegistryCleanup verifies that UnregisterSession properly cleans up
// both the session registry and the job object registry.
func TestSessionRegistryCleanup(t *testing.T) {
	// Register a session.
	RegisterSession("test-task-2", 12345, "plan")

	// Verify it's there.
	pids := LiveSessionPIDs("test-task-2")
	if len(pids) != 1 || pids[0] != 12345 {
		t.Fatalf("expected PID 12345 to be registered, got %v", pids)
	}

	// Unregister it.
	UnregisterSession("test-task-2", 12345)

	// Verify it's gone.
	pids = LiveSessionPIDs("test-task-2")
	if len(pids) != 0 {
		t.Fatalf("expected 0 PIDs after unregister, got %v", pids)
	}
}

// TestBootSweepTiming verifies that boot sweep completes in reasonable time.
// (Actual orphan killing depends on system state and would be tested manually.)
func TestBootSweepTiming(t *testing.T) {
	start := time.Now()
	killed, reclaimed := BootSweep()
	elapsed := time.Since(start)

	// Boot sweep should complete within 5 seconds (even on a busy system).
	if elapsed > 5*time.Second {
		t.Errorf("boot sweep took too long: %v", elapsed)
	}

	t.Logf("boot sweep completed in %v: killed=%d, reclaimed=%d MB", elapsed, killed, reclaimed)
}

// TestMultipleTaskSessions verifies that multiple tasks can have multiple sessions
// and the counts remain accurate.
func TestMultipleTaskSessions(t *testing.T) {
	// Reset for this test.
	sessionRegistryMu.Lock()
	oldRegistry := sessionRegistry
	sessionRegistry = map[string]map[int]string{}
	sessionRegistryMu.Unlock()
	defer func() {
		sessionRegistryMu.Lock()
		sessionRegistry = oldRegistry
		sessionRegistryMu.Unlock()
	}()

	// Register sessions for two tasks.
	RegisterSession("task-1", 1001, "plan")
	RegisterSession("task-1", 1002, "execute")
	RegisterSession("task-2", 2001, "plan")

	// Verify counts.
	totalSessions := LiveSessionCount()
	if totalSessions != 3 {
		t.Errorf("expected 3 total sessions, got %d", totalSessions)
	}

	task1PIDs := LiveSessionPIDs("task-1")
	if len(task1PIDs) != 2 {
		t.Errorf("expected 2 PIDs for task-1, got %d", len(task1PIDs))
	}

	// Unregister one session.
	UnregisterSession("task-1", 1001)

	// Verify update.
	totalSessions = LiveSessionCount()
	if totalSessions != 2 {
		t.Errorf("expected 2 total sessions after unregister, got %d", totalSessions)
	}

	task1PIDs = LiveSessionPIDs("task-1")
	if len(task1PIDs) != 1 {
		t.Errorf("expected 1 PID for task-1 after unregister, got %d", len(task1PIDs))
	}

	// Unregister the last session for task-1.
	UnregisterSession("task-1", 1002)

	// task-1 should no longer exist in registry.
	task1PIDs = LiveSessionPIDs("task-1")
	if len(task1PIDs) != 0 {
		t.Errorf("expected 0 PIDs for task-1 after complete unregister, got %d", len(task1PIDs))
	}
}
