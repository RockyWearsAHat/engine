package main

import (
	"bytes"
	"encoding/json"
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

// freshTaskAPI: empty registry, tasks.json in a temp state dir, routes on a
// private mux, wake POST captured. Restores globals on cleanup.
func freshTaskAPI(t *testing.T) (*http.ServeMux, string, *[]map[string]any) {
	t.Helper()
	stateDir := t.TempDir()
	t.Setenv("ENGINE_STATE_DIR", stateDir)
	origTasks, origPath, origHandle, origRun, origNotify := tasks, tasksFilePath, httpHandleFuncFn, runOrchestratorForTaskFn, notifyCallbackFn
	tasks = &taskRegistry{tasks: map[string]*engineTask{}, byKey: map[string]string{}}
	var mu sync.Mutex
	var wakes []map[string]any
	notifyCallbackFn = func(url string, payload map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		payload["_url"] = url
		wakes = append(wakes, payload)
	}
	mux := http.NewServeMux()
	httpHandleFuncFn = mux.HandleFunc
	t.Cleanup(func() {
		tasks, tasksFilePath, httpHandleFuncFn, runOrchestratorForTaskFn, notifyCallbackFn = origTasks, origPath, origHandle, origRun, origNotify
	})
	return mux, filepath.Join(stateDir, "tasks.json"), &wakes
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

// Restart: running rows come back as lost-on-restart, terminal, not alive.
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
	if code != http.StatusOK || got["status"] != string(taskLostOnRestart) || got["alive"] != false || got["finishedAt"] == nil {
		t.Fatalf("lost task wrong: %d %v", code, got)
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
	runOrchestratorForTaskFn = func(cfg ai.OrchestratorConfig) (*ai.OrchestrationState, error) {
		cfg.OnRunStats(ai.RunStats{Model: "haiku", InputTokens: 10, OutputTokens: 5, SubagentsSpawned: 1, Seen: true})
		cfg.OnRunStats(ai.RunStats{Model: "haiku", InputTokens: 10, OutputTokens: 5, Seen: true})
		cfg.OnCoach(1, false)
		<-cfg.Cancel
		return nil, nil
	}
	_, out := postTask(t, mux, map[string]any{"project": project, "brief": "cancel me"})
	id := out["id"].(string)
	time.Sleep(50 * time.Millisecond)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/task/cancel?id="+id, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel: %d", rec.Code)
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(*wakes) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("wake never fired on cancel")
		}
		time.Sleep(10 * time.Millisecond)
	}
	p := (*wakes)[0]
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
