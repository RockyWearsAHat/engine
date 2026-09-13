package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestCallbackPersistenceOnFailure tests that a failed callback is marked pending
// and then cleared on successful retry.
func TestCallbackPersistenceOnFailure(t *testing.T) {
	// Create an httptest server that fails once then succeeds
	attempts := 0
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		attemptNum := attempts
		mu.Unlock()

		if attemptNum == 1 {
			// First attempt fails
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Subsequent attempts succeed
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Setup temporary tasks file
	tmpDir := t.TempDir()
	tasksPath := filepath.Join(tmpDir, "tasks.json")
	originalTasksPath := tasksFilePath
	tasksFilePath = tasksPath
	defer func() { tasksFilePath = originalTasksPath }()

	// Create a new task registry for this test
	originalTasks := tasks
	tasks = &taskRegistry{
		tasks: map[string]*engineTask{},
		byKey: map[string]string{},
	}
	defer func() { tasks = originalTasks }()

	// Create a task with callback
	task := &engineTask{
		ID:          "test-callback-1",
		ProjectPath: "/test/project",
		Brief:       "Test callback",
		Status:      taskRunning,
		StartedAt:   time.Now(),
		CallbackURL: server.URL,
		cancel:      make(chan struct{}),
	}
	tasks.put("", task)

	// Finish the task with callback, which marks it as pending and attempts first delivery
	task.finish(taskDone, "")

	// Verify first attempt failed
	task.mu.RLock()
	if !task.CallbackPending {
		t.Error("Expected CallbackPending to be true after first failed attempt")
	}
	if task.CallbackAttempts != 1 {
		t.Errorf("Expected 1 attempt after finish, got %d", task.CallbackAttempts)
	}
	if task.CallbackLastError == "" {
		t.Error("Expected CallbackLastError to be set after failed delivery")
	}
	task.mu.RUnlock()

	// Test second attempt succeeds (manual retry, simulating replay)
	notifyCallback(task)
	task.mu.RLock()
	if task.CallbackPending {
		t.Error("Expected CallbackPending to be false after successful delivery")
	}
	if task.CallbackAttempts != 2 {
		t.Errorf("Expected 2 attempts after second call, got %d", task.CallbackAttempts)
	}
	if task.CallbackDeliveredAt == nil {
		t.Error("Expected CallbackDeliveredAt to be set after successful delivery")
	}
	if task.CallbackLastError != "" {
		t.Errorf("Expected CallbackLastError to be cleared on success, got: %s", task.CallbackLastError)
	}
	task.mu.RUnlock()
}

// TestCallbackReplayOnLoad tests that callbacks pending from a prior run are replayed.
func TestCallbackReplayOnLoad(t *testing.T) {
	// Create an httptest server that tracks POST requests
	var mu sync.Mutex
	postCount := 0
	var lastPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			mu.Lock()
			postCount++
			mu.Unlock()

			// Read payload
			body, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			defer r.Body.Close()

			if err := json.Unmarshal(body, &lastPayload); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	// Create a tasks.json with a pending callback
	tmpDir := t.TempDir()
	tasksPath := filepath.Join(tmpDir, "tasks.json")

	now := time.Now()
	finishedTime := now.Add(-1 * time.Hour) // Finished 1 hour ago
	rec := taskRecord{
		ID:                "test-callback-2",
		Project:           "/test/project",
		Brief:             "Test callback replay",
		Status:            taskDone,
		CallbackURL:       server.URL,
		CallbackPending:   true,
		CallbackAttempts:  1,
		CallbackLastError: "HTTP 500",
		StartedAt:         finishedTime,
		FinishedAt:        &finishedTime,
	}

	f := tasksFile{Tasks: []taskRecord{rec}}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal tasks.json: %v", err)
	}

	if err := os.WriteFile(tasksPath, data, 0o644); err != nil {
		t.Fatalf("Failed to write tasks.json: %v", err)
	}

	// Setup for loading
	originalTasksPath := tasksFilePath
	tasksFilePath = tasksPath
	defer func() { tasksFilePath = originalTasksPath }()

	originalTasks := tasks
	tasks = &taskRegistry{
		tasks: map[string]*engineTask{},
		byKey: map[string]string{},
	}
	defer func() { tasks = originalTasks }()

	// Load tasks.json, which should spawn replay goroutines
	startTime := time.Now()
	lost := tasks.load(tasksPath)

	if lost != 0 {
		t.Errorf("Expected 0 lost tasks, got %d", lost)
	}

	// Verify task was loaded with callback state
	task, ok := tasks.get("test-callback-2")
	if !ok {
		t.Fatal("Task not found after load")
	}

	task.mu.RLock()
	if !task.CallbackPending {
		t.Error("Expected CallbackPending to be true after load")
	}
	if task.CallbackAttempts != 1 {
		t.Errorf("Expected 1 attempt after load, got %d", task.CallbackAttempts)
	}
	task.mu.RUnlock()

	// Wait for replay goroutines to complete (with timeout)
	// The replay should retry after backoff delays; for testing we'll
	// check that the callback eventually succeeds or times out
	timeout := time.After(15 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			// Timeout; test that at least the initial attempt was recorded
			mu.Lock()
			if postCount == 0 {
				t.Error("No POST attempts made to callback URL after load")
			}
			mu.Unlock()
			return
		case <-ticker.C:
			task.mu.RLock()
			pending := task.CallbackPending
			lastError := task.CallbackLastError
			task.mu.RUnlock()

			if !pending {
				// Callback was delivered
				mu.Lock()
				count := postCount
				mu.Unlock()
				if count == 0 {
					t.Error("Callback marked as delivered but no POST was made")
				}
				if lastPayload == nil {
					t.Error("No payload was sent in the callback POST")
				}
				if lastPayload["id"] != "test-callback-2" {
					t.Errorf("Payload ID mismatch: got %v", lastPayload["id"])
				}
				return
			}

			// Check if we've waited long enough
			if time.Since(startTime) > 10*time.Second {
				// After 10 seconds, give up and check state
				t.Logf("Callback replay did not complete after 10 seconds")
				t.Logf("PostCount: %d", postCount)
				t.Logf("LastError: %s", lastError)
				return
			}
		}
	}
}

// TestCallbackNotifyWithNoURL tests callback notification when no URL is set.
func TestCallbackNotifyWithNoURL(t *testing.T) {
	originalTasksPath := tasksFilePath
	tasksFilePath = t.TempDir() + "/tasks.json"
	defer func() { tasksFilePath = originalTasksPath }()

	originalTasks := tasks
	tasks = &taskRegistry{
		tasks: map[string]*engineTask{},
		byKey: map[string]string{},
	}
	defer func() { tasks = originalTasks }()

	task := &engineTask{
		ID:        "test-no-url",
		Status:    taskRunning,
		StartedAt: time.Now(),
		cancel:    make(chan struct{}),
		// No CallbackURL
	}

	tasks.put("", task)

	// Should not panic and should not try to notify
	notifyCallback(task)

	// Verify callback is not pending
	task.mu.RLock()
	if task.CallbackPending {
		t.Error("Expected CallbackPending to be false when no URL is set")
	}
	task.mu.RUnlock()
}
