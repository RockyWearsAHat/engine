package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/engine/server/ai"
)

// wakeLog: mutex-guarded wake capture. Read via snapshot() — the hook fires
// from task goroutines.
type wakeLog struct {
	mu    sync.Mutex
	wakes []map[string]any
}

func (l *wakeLog) snapshot() []map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]map[string]any(nil), l.wakes...)
}

// freshTaskAPI: empty registry, tasks.json in a temp state dir, routes on a
// private mux, wake POST captured. Restores globals on cleanup.
func freshTaskAPI(t *testing.T) (*http.ServeMux, string, *wakeLog) {
	t.Helper()
	stateDir := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", stateDir)
	origTasks, origPath, origHandle, origRun, origNotify, origCheckpoint := tasks, tasksFilePath, httpHandleFuncFn, runOrchestratorForTaskFn, notifyCallbackFn, checkpointFn
	tasks = &taskRegistry{tasks: map[string]*engineTask{}, byKey: map[string]string{}}
	wakes := &wakeLog{}
	notifyCallbackFn = func(url string, payload map[string]any) {
		wakes.mu.Lock()
		defer wakes.mu.Unlock()
		payload["_url"] = url
		wakes.wakes = append(wakes.wakes, payload)
	}
	mux := http.NewServeMux()
	httpHandleFuncFn = mux.HandleFunc
	// Default to a no-op: most tests don't care about checkpointing and
	// must never shell out to the real git-checkpoint CLI or touch a
	// remote. Tests that DO care override this themselves.
	checkpointFn = func(repoPath, message string, push bool) error { return nil }
	t.Cleanup(func() {
		tasks, tasksFilePath, httpHandleFuncFn, runOrchestratorForTaskFn, notifyCallbackFn, checkpointFn = origTasks, origPath, origHandle, origRun, origNotify, origCheckpoint
	})
	return mux, filepath.Join(stateDir, "tasks.json"), wakes
}

func postTask(t *testing.T, mux *http.ServeMux, body map[string]any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/task", bytes.NewReader(b)))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func getTask(t *testing.T, mux *http.ServeMux, path string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func readTasksFile(t *testing.T, path string) tasksFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var f tasksFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return f
}

// POST /task answers in <200ms while the planner sleeps 5s; GET never waits.
func TestTaskAPI_PostAndGetDoNotBlockOnPlanner(t *testing.T) {
	mux, _, _ := freshTaskAPI(t)
	project := t.TempDir()
	registerTaskRoutes(project)
	planning := make(chan struct{})
	runOrchestratorForTaskFn = func(cfg ai.OrchestratorConfig) (*ai.OrchestrationState, error) {
		close(planning)
		select {
		case <-time.After(5 * time.Second):
		case <-cfg.Cancel:
		}
		return &ai.OrchestrationState{}, nil
	}

	start := time.Now()
	code, out := postTask(t, mux, map[string]any{"project": project, "brief": "slow plan"})
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("POST /task took %s", d)
	}
	if code != http.StatusAccepted || out["id"] == nil {
		t.Fatalf("want 202 with id, got %d %v", code, out)
	}
	id := out["id"].(string)
	select {
	case <-planning:
	case <-time.After(2 * time.Second):
		t.Fatal("planner never started")
	}

	for _, path := range []string{"/task?id=" + id, "/task/" + id} {
		start = time.Now()
		gcode, got := getTask(t, mux, path)
		if d := time.Since(start); d > 200*time.Millisecond {
			t.Fatalf("GET %s took %s", path, d)
		}
		if gcode != http.StatusOK || got["status"] != "running" || got["alive"] != true {
			t.Fatalf("GET %s: %d %v", path, gcode, got)
		}
	}
	task, _ := tasks.get(id)
	task.stop()
}

// tasks.json holds the task before the planner runs.
func TestTaskAPI_PersistsBeforePlan(t *testing.T) {
	mux, path, _ := freshTaskAPI(t)
	project := t.TempDir()
	registerTaskRoutes(project)
	seen := make(chan bool, 1)
	runOrchestratorForTaskFn = func(cfg ai.OrchestratorConfig) (*ai.OrchestrationState, error) {
		f := readTasksFile(t, path)
		found := false
		for _, r := range f.Tasks {
			if r.ID == cfg.TaskID && r.Status == taskRunning && r.PID == os.Getpid() {
				found = true
			}
		}
		seen <- found
		return &ai.OrchestrationState{}, nil
	}
	code, _ := postTask(t, mux, map[string]any{"project": project, "brief": "persist me", "key": "item-1"})
	if code != http.StatusAccepted {
		t.Fatalf("code %d", code)
	}
	select {
	case ok := <-seen:
		if !ok {
			t.Fatal("tasks.json did not hold the running task when the planner started")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("planner never ran")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		f := readTasksFile(t, path)
		if len(f.Tasks) == 1 && f.Tasks[0].Status == taskDone && f.Tasks[0].Key == "item-1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal state never persisted: %+v", f)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Restart: running rows come back as failed + lost:true, terminal, not alive.
func TestTaskAPI_ReloadMarksRunningLost(t *testing.T) {
	mux, path, _ := freshTaskAPI(t)
	now := time.Now()
	f := tasksFile{Tasks: []taskRecord{
		{ID: "t-run", Project: "/p", Status: taskRunning, Phase: "plan", StartedAt: now, Key: "k1", PID: 1},
		{ID: "t-done", Project: "/p", Status: taskDone, StartedAt: now, FinishedAt: &now, PID: 1},
	}}
	data, _ := json.MarshalIndent(f, "", "  ")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	registerTaskRoutes(t.TempDir())

	code, got := getTask(t, mux, "/task/t-run")
	if code != http.StatusOK || got["status"] != string(taskFailed) || got["alive"] != false || got["finishedAt"] == nil {
		t.Fatalf("lost task wrong: %d %v", code, got)
	}
	// Status must be a word the gateway knows (done/failed/canceled); lost
	// flag tells it apart from a real failure. Survives a second reload too.
	if got["lost"] != true {
		t.Fatalf("lost flag missing: %v", got)
	}
	switch got["status"] {
	case "done", "failed", "canceled":
	default:
		t.Fatalf("unknown status for gateway: %v", got["status"])
	}
	tasks.persist()
	r2 := &taskRegistry{tasks: map[string]*engineTask{}, byKey: map[string]string{}}
	if n := r2.load(path); n != 0 {
		t.Fatalf("second reload re-lost %d", n)
	}
	if t2, ok := r2.tasks["t-run"]; !ok || t2.Status != taskFailed || !t2.Lost {
		t.Fatalf("lost flag did not survive reload: %+v", t2)
	}
	if _, running := tasks.liveByKey("k1"); running {
		t.Fatal("lost task must not dedupe a re-dispatch")
	}
	code, got = getTask(t, mux, "/task/t-done")
	if code != http.StatusOK || got["status"] != "done" {
		t.Fatalf("done task wrong: %d %v", code, got)
	}
	// Re-dispatch of the same key is accepted, not deduped.
	code, out := postTask(t, mux, map[string]any{"project": "/p", "brief": "again", "key": "k1"})
	if code != http.StatusAccepted || out["deduped"] == true {
		t.Fatalf("re-dispatch after loss: %d %v", code, out)
	}
	if task, ok := tasks.get(out["id"].(string)); ok {
		task.stop()
	}
}

// Every terminal state fires the wake POST with the outcome payload; the
// default target is SARA's wake port.
func TestTaskAPI_WakeFiresOnCancelWithPayload(t *testing.T) {
	mux, _, wakes := freshTaskAPI(t)
	t.Setenv(wakePortEnv, "25555")
	project := t.TempDir()
	registerTaskRoutes(project)
	// ready carries the TaskID once stats + coach recorded. Cancel only
	// after OUR id arrives — a goroutine leaked from an earlier test can
	// call this fn too (global hook) and would false-signal; then cancel
	// lands while still queued (payload model "", tokens 0).
	ready := make(chan string, 8)
	runOrchestratorForTaskFn = func(cfg ai.OrchestratorConfig) (*ai.OrchestrationState, error) {
		cfg.OnRunStats(ai.RunStats{Model: "haiku", InputTokens: 10, OutputTokens: 5, SubagentsSpawned: 1, Seen: true})
		cfg.OnRunStats(ai.RunStats{Model: "haiku", InputTokens: 10, OutputTokens: 5, Seen: true})
		cfg.OnCoach(1, false)
		ready <- cfg.TaskID
		<-cfg.Cancel
		return nil, nil
	}
	_, out := postTask(t, mux, map[string]any{"project": project, "brief": "cancel me"})
	id := out["id"].(string)
	for {
		select {
		case got := <-ready:
			if got == id {
				goto started
			}
		case <-time.After(3 * time.Second):
			t.Fatal("orchestrator never started for our task")
		}
	}
started:
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/task/cancel?id="+id, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel: %d", rec.Code)
	}
	// Match wake by id — a task leaked from an earlier test can fire its
	// wake into this capture too (global hook).
	deadline := time.Now().Add(3 * time.Second)
	var p map[string]any
	for p == nil {
		if time.Now().After(deadline) {
			t.Fatal("wake never fired on cancel")
		}
		for _, w := range wakes.snapshot() {
			if w["id"] == id {
				p = w
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if p["_url"] != "http://127.0.0.1:25555/task-complete" {
		t.Fatalf("wake url: %v", p["_url"])
	}
	if p["id"] != id || p["outcome"] != "canceled" || p["model"] != "haiku" ||
		p["tokensIn"].(int64) != 20 || p["tokensOut"].(int64) != 10 || p["subagentsSpawned"].(int) != 1 ||
		p["coached"].(int) != 1 || p["escalated"] != false || p["project"] != project {
		t.Fatalf("payload: %v", p)
	}
	_, got := getTask(t, mux, "/task/"+id)
	if got["tokensIn"].(float64) != 20 || got["model"] != "haiku" || got["alive"] != false {
		t.Fatalf("status tallies: %v", got)
	}
}

// A successful task-mode run checkpoints and pushes exactly once, against
// the project path, with a message that carries the task id and brief.
func TestTaskAPI_ChecksPointsAndPushesOnSuccess(t *testing.T) {
	mux, _, _ := freshTaskAPI(t)
	project := t.TempDir()
	registerTaskRoutes(project)
	runOrchestratorForTaskFn = func(cfg ai.OrchestratorConfig) (*ai.OrchestrationState, error) {
		return &ai.OrchestrationState{}, nil
	}
	type call struct {
		repoPath, message string
		push              bool
	}
	calls := make(chan call, 8)
	checkpointFn = func(repoPath, message string, push bool) error {
		calls <- call{repoPath, message, push}
		return nil
	}

	code, out := postTask(t, mux, map[string]any{"project": project, "brief": "ship the widget"})
	if code != http.StatusAccepted {
		t.Fatalf("code %d", code)
	}
	id := out["id"].(string)

	select {
	case c := <-calls:
		if c.repoPath != project {
			t.Fatalf("checkpoint repoPath = %q, want %q", c.repoPath, project)
		}
		if !c.push {
			t.Fatalf("checkpoint push = false, want true")
		}
		if !strings.Contains(c.message, id) || !strings.Contains(c.message, "ship the widget") {
			t.Fatalf("checkpoint message = %q, want it to mention %q and the brief", c.message, id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("checkpointFn was never called on success")
	}
	select {
	case c := <-calls:
		t.Fatalf("checkpointFn called a second time: %+v", c)
	case <-time.After(100 * time.Millisecond):
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		_, got := getTask(t, mux, "/task/"+id)
		if got["status"] == "done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task never finished: %v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A failed run must never be checkpointed or pushed.
func TestTaskAPI_NoCheckpointOnFailure(t *testing.T) {
	mux, _, _ := freshTaskAPI(t)
	project := t.TempDir()
	registerTaskRoutes(project)
	runOrchestratorForTaskFn = func(cfg ai.OrchestratorConfig) (*ai.OrchestrationState, error) {
		return nil, fmt.Errorf("boom")
	}
	calls := make(chan struct{}, 8)
	checkpointFn = func(repoPath, message string, push bool) error {
		calls <- struct{}{}
		return nil
	}

	_, out := postTask(t, mux, map[string]any{"project": project, "brief": "will fail"})
	id := out["id"].(string)

	deadline := time.Now().Add(3 * time.Second)
	for {
		_, got := getTask(t, mux, "/task/"+id)
		if got["status"] == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task never failed: %v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case <-calls:
		t.Fatal("checkpointFn was called on a failed run")
	case <-time.After(100 * time.Millisecond):
	}
}

// A canceled run must never be checkpointed or pushed either.
func TestTaskAPI_NoCheckpointOnCancel(t *testing.T) {
	mux, _, _ := freshTaskAPI(t)
	project := t.TempDir()
	registerTaskRoutes(project)
	started := make(chan struct{})
	runOrchestratorForTaskFn = func(cfg ai.OrchestratorConfig) (*ai.OrchestrationState, error) {
		close(started)
		<-cfg.Cancel
		return nil, nil
	}
	calls := make(chan struct{}, 8)
	checkpointFn = func(repoPath, message string, push bool) error {
		calls <- struct{}{}
		return nil
	}

	_, out := postTask(t, mux, map[string]any{"project": project, "brief": "will cancel"})
	id := out["id"].(string)
	<-started
	task, _ := tasks.get(id)
	task.stop()

	deadline := time.Now().Add(3 * time.Second)
	for {
		_, got := getTask(t, mux, "/task/"+id)
		if got["status"] == "canceled" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task never canceled: %v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case <-calls:
		t.Fatal("checkpointFn was called on a canceled run")
	case <-time.After(100 * time.Millisecond):
	}
}

// Liveness stamps show up in status.
func TestTaskAPI_ActivityStamps(t *testing.T) {
	mux, _, _ := freshTaskAPI(t)
	project := t.TempDir()
	registerTaskRoutes(project)
	runOrchestratorForTaskFn = func(cfg ai.OrchestratorConfig) (*ai.OrchestrationState, error) {
		cfg.OnActivity("token")
		cfg.OnActivity("tool")
		<-cfg.Cancel
		return nil, nil
	}
	_, out := postTask(t, mux, map[string]any{"project": project, "brief": "stamp"})
	id := out["id"].(string)
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, got := getTask(t, mux, "/task/"+id)
		if got["firstProgressAt"] != nil && got["lastTokenAt"] != nil && got["lastToolAt"] != nil {
			if s, _ := got["lastToolAt"].(string); !strings.HasSuffix(s, "Z") {
				t.Fatalf("lastToolAt not RFC3339 UTC: %v", s)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stamps missing: %v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, _ := tasks.get(id)
	task.stop()
}
