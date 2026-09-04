package ai

import (
	"testing"
)

func TestLiveTaskSessionCount_Empty(t *testing.T) {
	// Clear the registry for this test
	sessionRegistryMu.Lock()
	oldRegistry := sessionRegistry
	sessionRegistry = make(map[string]map[int]string)
	sessionRegistryMu.Unlock()
	defer func() {
		sessionRegistryMu.Lock()
		sessionRegistry = oldRegistry
		sessionRegistryMu.Unlock()
	}()

	count := LiveTaskSessionCount("unknown-task")
	if count != 0 {
		t.Errorf("expected 0 sessions for unknown task, got %d", count)
	}
}

func TestLiveTaskSessionCount_SingleSession(t *testing.T) {
	// Clear the registry for this test
	sessionRegistryMu.Lock()
	oldRegistry := sessionRegistry
	sessionRegistry = make(map[string]map[int]string)
	sessionRegistryMu.Unlock()
	defer func() {
		sessionRegistryMu.Lock()
		sessionRegistry = oldRegistry
		sessionRegistryMu.Unlock()
	}()

	taskID := "test-item-1"
	RegisterSession(taskID, 1234, "execute")
	count := LiveTaskSessionCount(taskID)
	if count != 1 {
		t.Errorf("expected 1 session for task %s, got %d", taskID, count)
	}
}

func TestLiveTaskSessionCount_MultipleSessions(t *testing.T) {
	// Clear the registry for this test
	sessionRegistryMu.Lock()
	oldRegistry := sessionRegistry
	sessionRegistry = make(map[string]map[int]string)
	sessionRegistryMu.Unlock()
	defer func() {
		sessionRegistryMu.Lock()
		sessionRegistry = oldRegistry
		sessionRegistryMu.Unlock()
	}()

	taskID := "test-item-2"
	RegisterSession(taskID, 1001, "plan")
	RegisterSession(taskID, 1002, "execute")
	RegisterSession(taskID, 1003, "execute")
	count := LiveTaskSessionCount(taskID)
	if count != 3 {
		t.Errorf("expected 3 sessions for task %s, got %d", taskID, count)
	}

	// Unregister one and verify count drops
	UnregisterSession(taskID, 1001)
	count = LiveTaskSessionCount(taskID)
	if count != 2 {
		t.Errorf("expected 2 sessions after unregister, got %d", count)
	}
}

func TestLiveTaskSessionCount_TasksIsolated(t *testing.T) {
	// Clear the registry for this test
	sessionRegistryMu.Lock()
	oldRegistry := sessionRegistry
	sessionRegistry = make(map[string]map[int]string)
	sessionRegistryMu.Unlock()
	defer func() {
		sessionRegistryMu.Lock()
		sessionRegistry = oldRegistry
		sessionRegistryMu.Unlock()
	}()

	task1 := "item-1"
	task2 := "item-2"
	RegisterSession(task1, 1001, "execute")
	RegisterSession(task1, 1002, "execute")
	RegisterSession(task2, 2001, "execute")

	count1 := LiveTaskSessionCount(task1)
	count2 := LiveTaskSessionCount(task2)
	if count1 != 2 {
		t.Errorf("expected 2 sessions for task %s, got %d", task1, count1)
	}
	if count2 != 1 {
		t.Errorf("expected 1 session for task %s, got %d", task2, count2)
	}
}
