package main

import (
	"fmt"
	"testing"
	"time"
)

func TestEngineTaskSnapshot(t *testing.T) {
	task := &engineTask{
		ID:          "task-123",
		ProjectPath: "/path/to/project",
		Brief:       "Test task",
		Status:      taskRunning,
		Phase:       "testing",
		Detail:      "Running tests",
		StartedAt:   time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
		StepsDone:   5,
		StepsTotal:  10,
		Progress:    []string{"step 1", "step 2"},
	}

	snap := task.snapshot()

	if snap["id"] != "task-123" {
		t.Errorf("ID mismatch: got %v", snap["id"])
	}
	if snap["project"] != "/path/to/project" {
		t.Errorf("ProjectPath mismatch: got %v", snap["project"])
	}
	if snap["status"] != "running" {
		t.Errorf("Status mismatch: got %v", snap["status"])
	}
	if snap["stepsDone"] != 5 {
		t.Errorf("StepsDone mismatch: got %v", snap["stepsDone"])
	}
	if snap["stepsTotal"] != 10 {
		t.Errorf("StepsTotal mismatch: got %v", snap["stepsTotal"])
	}
	progress, ok := snap["progress"].([]string)
	if !ok || len(progress) != 2 {
		t.Errorf("Progress mismatch: got %v", snap["progress"])
	}
	if _, ok := snap["finishedAt"]; ok {
		t.Error("finishedAt should not be present for running task")
	}
	if _, ok := snap["error"]; ok {
		t.Error("error should not be present for running task")
	}
}

func TestEngineTaskSnapshotWithError(t *testing.T) {
	now := time.Now()
	task := &engineTask{
		ID:         "task-456",
		Status:     taskFailed,
		StartedAt:  now,
		FinishedAt: &now,
		Err:        "test error",
	}

	snap := task.snapshot()

	if snap["error"] != "test error" {
		t.Errorf("Error mismatch: got %v", snap["error"])
	}
	if _, ok := snap["finishedAt"]; !ok {
		t.Error("finishedAt should be present for finished task")
	}
}

func TestEngineTaskNote(t *testing.T) {
	task := &engineTask{
		ID:        "task-789",
		Status:    taskRunning,
		StartedAt: time.Now(),
		cancel:    make(chan struct{}),
	}

	task.note("phase1", "detail1")
	task.note("phase2", "detail2")

	if task.Phase != "phase2" {
		t.Errorf("Phase mismatch: got %v", task.Phase)
	}
	if task.Detail != "detail2" {
		t.Errorf("Detail mismatch: got %v", task.Detail)
	}
	if len(task.Progress) != 2 {
		t.Errorf("Progress length mismatch: got %d, want 2", len(task.Progress))
	}
}

func TestEngineTaskNoteBounded(t *testing.T) {
	task := &engineTask{
		ID:        "task-bounded",
		Status:    taskRunning,
		StartedAt: time.Now(),
		cancel:    make(chan struct{}),
	}

	for i := 0; i < taskProgressLines+10; i++ {
		task.note(fmt.Sprintf("phase%d", i), fmt.Sprintf("detail%d", i))
	}

	if len(task.Progress) > taskProgressLines {
		t.Errorf("Progress unbounded: got %d, max %d", len(task.Progress), taskProgressLines)
	}
	if len(task.Progress) != taskProgressLines {
		t.Errorf("Progress length: got %d, want %d", len(task.Progress), taskProgressLines)
	}
}

func TestEngineTaskSetPlan(t *testing.T) {
	task := &engineTask{
		ID:        "task-plan",
		Status:    taskRunning,
		StartedAt: time.Now(),
		cancel:    make(chan struct{}),
	}

	task.setPlan(3, 10)

	if task.StepsDone != 3 {
		t.Errorf("StepsDone mismatch: got %d", task.StepsDone)
	}
	if task.StepsTotal != 10 {
		t.Errorf("StepsTotal mismatch: got %d", task.StepsTotal)
	}
}

func TestEngineTaskFinish(t *testing.T) {
	task := &engineTask{
		ID:        "task-finish",
		Status:    taskRunning,
		StartedAt: time.Now(),
		cancel:    make(chan struct{}),
	}

	before := time.Now()
	task.finish(taskDone, "")
	after := time.Now()

	if task.Status != taskDone {
		t.Errorf("Status mismatch: got %v", task.Status)
	}
	if task.FinishedAt == nil {
		t.Error("FinishedAt should be set")
	} else if task.FinishedAt.Before(before) || task.FinishedAt.After(after.Add(time.Millisecond)) {
		t.Errorf("FinishedAt out of range: %v", task.FinishedAt)
	}
	if task.Err != "" {
		t.Errorf("Err should be empty, got %v", task.Err)
	}
}

func TestEngineTaskFinishWithError(t *testing.T) {
	task := &engineTask{
		ID:        "task-finish-err",
		Status:    taskRunning,
		StartedAt: time.Now(),
		cancel:    make(chan struct{}),
	}

	task.finish(taskFailed, "something went wrong")

	if task.Status != taskFailed {
		t.Errorf("Status mismatch: got %v", task.Status)
	}
	if task.Err != "something went wrong" {
		t.Errorf("Err mismatch: got %v", task.Err)
	}
}

func TestEngineTaskStop(t *testing.T) {
	task := &engineTask{
		ID:        "task-stop",
		Status:    taskRunning,
		StartedAt: time.Now(),
		cancel:    make(chan struct{}),
	}

	task.stop()

	select {
	case <-task.cancel:
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Error("cancel channel not closed")
	}
}

func TestEngineTaskStopIdempotent(t *testing.T) {
	task := &engineTask{
		ID:        "task-stop-idem",
		Status:    taskRunning,
		StartedAt: time.Now(),
		cancel:    make(chan struct{}),
	}

	task.stop()
	task.stop() // Should not panic

	select {
	case <-task.cancel:
		// Expected
	default:
		t.Error("cancel channel not closed")
	}
}

func TestTaskRegistryGetPut(t *testing.T) {
	reg := &taskRegistry{
		tasks: map[string]*engineTask{},
		byKey: map[string]string{},
	}

	task := &engineTask{
		ID:        "test-id",
		Status:    taskRunning,
		StartedAt: time.Now(),
		cancel:    make(chan struct{}),
	}

	reg.put("", task)

	retrieved, ok := reg.get("test-id")
	if !ok {
		t.Error("task not found after put")
	}
	if retrieved.ID != "test-id" {
		t.Errorf("ID mismatch: got %v", retrieved.ID)
	}
}

func TestTaskRegistryGetNotFound(t *testing.T) {
	reg := &taskRegistry{
		tasks: map[string]*engineTask{},
		byKey: map[string]string{},
	}

	_, ok := reg.get("nonexistent")
	if ok {
		t.Error("should not find nonexistent task")
	}
}

func TestTaskRegistryLiveByKey(t *testing.T) {
	reg := &taskRegistry{
		tasks: map[string]*engineTask{},
		byKey: map[string]string{},
	}

	task := &engineTask{
		ID:        "live-task",
		Status:    taskRunning,
		StartedAt: time.Now(),
		cancel:    make(chan struct{}),
	}

	reg.put("mykey", task)

	retrieved, running := reg.liveByKey("mykey")
	if !running {
		t.Error("should find live task")
	}
	if retrieved.ID != "live-task" {
		t.Errorf("ID mismatch: got %v", retrieved.ID)
	}
}

func TestTaskRegistryLiveByKeyEmptyKey(t *testing.T) {
	reg := &taskRegistry{
		tasks: map[string]*engineTask{},
		byKey: map[string]string{},
	}

	_, running := reg.liveByKey("")
	if running {
		t.Error("empty key should not find task")
	}
}

func TestTaskRegistryLiveByKeyFinished(t *testing.T) {
	reg := &taskRegistry{
		tasks: map[string]*engineTask{},
		byKey: map[string]string{},
	}

	now := time.Now()
	task := &engineTask{
		ID:         "finished-task",
		Status:     taskDone,
		StartedAt:  time.Now(),
		FinishedAt: &now,
		cancel:     make(chan struct{}),
	}

	reg.put("finkey", task)

	_, running := reg.liveByKey("finkey")
	if running {
		t.Error("finished task should not be live")
	}
}

func TestTaskRegistryList(t *testing.T) {
	reg := &taskRegistry{
		tasks: map[string]*engineTask{},
		byKey: map[string]string{},
	}

	task1 := &engineTask{
		ID:        "task1",
		Status:    taskRunning,
		StartedAt: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
		cancel:    make(chan struct{}),
	}
	task2 := &engineTask{
		ID:        "task2",
		Status:    taskRunning,
		StartedAt: time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC),
		cancel:    make(chan struct{}),
	}

	reg.put("", task1)
	reg.put("", task2)

	list := reg.list(0)
	if len(list) != 2 {
		t.Errorf("list length: got %d, want 2", len(list))
	}
	// Newest first
	if list[0]["id"] != "task2" {
		t.Errorf("first task should be task2 (newest), got %v", list[0]["id"])
	}
	if list[1]["id"] != "task1" {
		t.Errorf("second task should be task1 (oldest), got %v", list[1]["id"])
	}
}

func TestTaskRegistryListLimit(t *testing.T) {
	reg := &taskRegistry{
		tasks: map[string]*engineTask{},
		byKey: map[string]string{},
	}

	for i := 0; i < 5; i++ {
		task := &engineTask{
			ID:        fmt.Sprintf("task%d", i),
			Status:    taskRunning,
			StartedAt: time.Now().Add(time.Duration(i) * time.Second),
			cancel:    make(chan struct{}),
		}
		reg.put("", task)
	}

	list := reg.list(3)
	if len(list) != 3 {
		t.Errorf("list length: got %d, want 3", len(list))
	}
}

func TestCancelClosed(t *testing.T) {
	ch := make(chan struct{})
	if cancelClosed(ch) {
		t.Error("new channel should not be closed")
	}

	close(ch)
	if !cancelClosed(ch) {
		t.Error("closed channel should be detected")
	}
}

func TestShortToken(t *testing.T) {
	tok := shortToken()
	if len(tok) != 4 {
		t.Errorf("token length: got %d, want 4", len(tok))
	}
	for _, c := range tok {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Errorf("token contains non-hex character: %c", c)
		}
	}
}

func TestEngineTaskSnapshotThreadSafety(t *testing.T) {
	task := &engineTask{
		ID:        "task-concurrent",
		Status:    taskRunning,
		StartedAt: time.Now(),
		cancel:    make(chan struct{}),
	}

	done := make(chan struct{})

	go func() {
		for i := 0; i < 100; i++ {
			task.note(fmt.Sprintf("phase%d", i), fmt.Sprintf("detail%d", i))
		}
		done <- struct{}{}
	}()

	go func() {
		for i := 0; i < 100; i++ {
			_ = task.snapshot()
		}
		done <- struct{}{}
	}()

	<-done
	<-done
}
