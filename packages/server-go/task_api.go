package main

// The task API is how an external scheduler hands Engine one unit of work and
// finds out what became of it.
//
// It exists because SARA — the thing that actually runs unattended — had no way
// to reach this orchestrator at all. Its loop spawned `claude --bg` directly,
// so every plan/critic/review/repair/validate gate in ai/ was bypassed on every
// item it ever worked. This is the seam that connects them.
//
// WHY REST AND NOT /ws.
//
// The WebSocket hub is the richer surface and it is what the editor uses. It is
// the wrong shape for this caller. SARA's engine is a supervised process that
// gets restarted — by its own supervisor on crash, by ./start.sh --restart, by
// a config change — and after a restart it has to answer "is item 7 of this
// worklist still being worked?". Over a socket that means a session-resume
// protocol: reattach, replay what was missed, reconcile. None of that exists,
// and writing it would be the larger half of this job.
//
// Over REST the answer is a GET against an id the caller already wrote into its
// own state file. The engine can die mid-task, come back, and re-poll — no
// reconnection, no replay, no protocol. Task state lives here, in the process
// that owns the work, which is where it should live anyway. /quota and
// /quota/rate set exactly this precedent for exactly this caller.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/engine/server/ai"
	"github.com/engine/server/db"
	gogit "github.com/engine/server/git"
)

// taskStatus is the lifecycle of one dispatched unit of work.
type taskStatus string

const (
	taskRunning  taskStatus = "running"
	taskDone     taskStatus = "done"
	taskFailed   taskStatus = "failed"
	taskCanceled taskStatus = "canceled"
)

// taskProgressLines is how much of the phase narration is kept per task.
//
// Bounded because this is a long-lived in-memory record of a process that can
// run for an hour and emit a line per step attempt: unbounded, a busy day's
// tasks are a slow leak. The tail is the useful part — a caller asking what is
// happening wants the last thing, not the first.
const taskProgressLines = 40

// engineTask is one dispatched unit of work and everything a poller can learn
// about it.
type engineTask struct {
	mu sync.RWMutex

	ID          string     `json:"id"`
	ProjectPath string     `json:"project"`
	Brief       string     `json:"brief"`
	Status      taskStatus `json:"status"`
	Phase       string     `json:"phase"`
	Detail      string     `json:"detail"`
	StartedAt   time.Time  `json:"startedAt"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
	Err         string     `json:"error,omitempty"`
	StepsDone   int        `json:"stepsDone"`
	StepsTotal  int        `json:"stepsTotal"`
	Progress    []string   `json:"progress"`

	// Liveness by evidence. LastTokenAt/LastToolAt move on streamed tokens
	// and tool calls; FirstProgressAt is the first of either. Pollers judge
	// "stuck" from these, not from a clock.
	FirstProgressAt *time.Time `json:"firstProgressAt,omitempty"`
	LastTokenAt     *time.Time `json:"lastTokenAt,omitempty"`
	LastToolAt      *time.Time `json:"lastToolAt,omitempty"`

	// Run tallies. Model = last model a provider run reported.
	Model            string `json:"model,omitempty"`
	TokensIn         int64  `json:"tokensIn"`
	TokensOut        int64  `json:"tokensOut"`
	SubagentsSpawned int    `json:"subagentsSpawned"`
	Coached          int    `json:"coached"`
	Escalated        bool   `json:"escalated"`

	// Lost: was running when engine restarted. Status is failed (SARA's
	// gateway knows that word); Lost tells it apart from a real failure.
	Lost bool `json:"lost"`

	// restored: loaded from tasks.json, no goroutine owns it. Never alive.
	restored bool

	// CallbackURL, if the caller supplied one, is POSTed to (empty body) when
	// this task reaches a terminal state. It is a wake signal only, not a
	// delivery guarantee: fire-and-forget, short timeout, errors ignored. The
	// caller's GET-by-id (above) stays the mandatory source of truth -- this
	// just lets it stop waiting on BUSY_POLL_MS and ask sooner.
	CallbackURL string `json:"-"`

	cancel chan struct{}
	once   sync.Once
}

// snapshot renders the task under the read lock. Every field is written by the
// orchestrator's callbacks on another goroutine while an HTTP handler reads
// them, so the copy has to be taken here rather than by marshalling the live
// struct.
func (t *engineTask) snapshot() map[string]any {
	t.mu.RLock()
	defer t.mu.RUnlock()

	progress := make([]string, len(t.Progress))
	copy(progress, t.Progress)

	out := map[string]any{
		"id":         t.ID,
		"project":    t.ProjectPath,
		"status":     string(t.Status),
		"phase":      t.Phase,
		"detail":     t.Detail,
		"startedAt":  t.StartedAt.UTC().Format(time.RFC3339),
		"stepsDone":  t.StepsDone,
		"stepsTotal": t.StepsTotal,
		"progress":   progress,
		// alive: this process owns a goroutine for it right now.
		"alive":            t.Status == taskRunning && !t.restored,
		"model":            t.Model,
		"tokensIn":         t.TokensIn,
		"tokensOut":        t.TokensOut,
		"subagentsSpawned": t.SubagentsSpawned,
		"coached":          t.Coached,
		"escalated":        t.Escalated,
		"lost":             t.Lost,
	}
	if t.FirstProgressAt != nil {
		out["firstProgressAt"] = t.FirstProgressAt.UTC().Format(time.RFC3339)
	}
	if t.LastTokenAt != nil {
		out["lastTokenAt"] = t.LastTokenAt.UTC().Format(time.RFC3339)
	}
	if t.LastToolAt != nil {
		out["lastToolAt"] = t.LastToolAt.UTC().Format(time.RFC3339)
	}
	if t.FinishedAt != nil {
		out["finishedAt"] = t.FinishedAt.UTC().Format(time.RFC3339)
	}
	if t.Err != "" {
		out["error"] = t.Err
	}
	return out
}

func (t *engineTask) note(phase, detail string) {
	t.mu.Lock()
	if phase != "" {
		t.Phase = phase
	}
	t.Detail = detail
	line := time.Now().UTC().Format("15:04:05") + " " + phase
	if detail != "" {
		line += ": " + detail
	}
	t.Progress = append(t.Progress, line)
	if len(t.Progress) > taskProgressLines {
		t.Progress = t.Progress[len(t.Progress)-taskProgressLines:]
	}
	t.mu.Unlock()
	tasks.persist()
}

func (t *engineTask) setPlan(done, total int) {
	t.mu.Lock()
	changed := t.StepsDone != done || t.StepsTotal != total
	t.StepsDone, t.StepsTotal = done, total
	t.mu.Unlock()
	if changed {
		tasks.persist()
	}
}

// activity stamps liveness. Not persisted per token — too hot; persisted on
// the next note/plan/finish, and the first one (firstProgressAt) right away.
func (t *engineTask) activity(kind string) {
	now := time.Now()
	t.mu.Lock()
	first := t.FirstProgressAt == nil
	if first {
		t.FirstProgressAt = &now
	}
	if kind == "tool" {
		t.LastToolAt = &now
	} else {
		t.LastTokenAt = &now
	}
	t.mu.Unlock()
	if first {
		tasks.persist()
	}
}

func (t *engineTask) addRun(s ai.RunStats) {
	t.mu.Lock()
	if s.Model != "" {
		t.Model = s.Model
	}
	t.TokensIn += s.InputTokens
	t.TokensOut += s.OutputTokens
	t.SubagentsSpawned += s.SubagentsSpawned
	t.mu.Unlock()
	tasks.persist()
}

func (t *engineTask) setCoach(coached int, escalated bool) {
	t.mu.Lock()
	t.Coached = coached
	t.Escalated = t.Escalated || escalated
	t.mu.Unlock()
	tasks.persist()
}

func (t *engineTask) finish(status taskStatus, errMsg string) {
	t.mu.Lock()
	now := time.Now()
	t.Status = status
	t.FinishedAt = &now
	t.Err = errMsg
	t.mu.Unlock()
	tasks.persist()
	notifyCallback(t.callbackTarget(), t.completionPayload())
}

// callbackTarget: caller's URL, else SARA's wake port (SARA_ENGINE_WAKE_PORT,
// default 24777). Fires on EVERY terminal state — done, failed, canceled.
func (t *engineTask) callbackTarget() string {
	t.mu.RLock()
	url := t.CallbackURL
	t.mu.RUnlock()
	if url != "" {
		return url
	}
	return defaultWakeURL()
}

// wakePortEnv / wakePortDefault: SARA's completion listener.
const (
	wakePortEnv     = "SARA_ENGINE_WAKE_PORT"
	wakePortDefault = "24777"
)

func defaultWakeURL() string {
	port := strings.TrimSpace(os.Getenv(wakePortEnv))
	if port == "" {
		port = wakePortDefault
	}
	return "http://127.0.0.1:" + port + "/task-complete"
}

// completionPayload is what the wake POST carries. Outcome = status string.
func (t *engineTask) completionPayload() map[string]any {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return map[string]any{
		"id":               t.ID,
		"project":          t.ProjectPath,
		"outcome":          string(t.Status),
		"model":            t.Model,
		"tokensIn":         t.TokensIn,
		"tokensOut":        t.TokensOut,
		"subagentsSpawned": t.SubagentsSpawned,
		"coached":          t.Coached,
		"escalated":        t.Escalated,
		"error":            t.Err,
	}
}

// notifyCallbackFn is the wake POST. Var so tests capture it.
var notifyCallbackFn = func(url string, payload map[string]any) {
	body, _ := json.Marshal(payload)
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return
	}
	resp.Body.Close()
}

// notifyCallback: fire-and-forget wake POST with the outcome payload. Not a
// delivery guarantee — GET-by-id stays the truth. Runs synchronously (short
// timeout); finish() already sits on the task's own goroutine.
func notifyCallback(url string, payload map[string]any) {
	if url == "" {
		return
	}
	notifyCallbackFn(url, payload)
}

func (t *engineTask) stop() {
	t.once.Do(func() { close(t.cancel) })
}

// taskRegistry holds every task this process has run since it started.
type taskRegistry struct {
	mu    sync.RWMutex
	tasks map[string]*engineTask
	// byKey deduplicates: the same worklist item dispatched twice must not
	// become two orchestrators racing on one repository. SARA's engine already
	// tracks what it dispatched, but it can restart between dispatching and
	// recording, and "the supervisor came back and re-sent everything" is the
	// exact case that produces two agents editing the same files.
	byKey map[string]string
}

var tasks = &taskRegistry{
	tasks: map[string]*engineTask{},
	byKey: map[string]string{},
}

// tasksFilePath is where the registry lives on disk. Empty = not persisted
// (unit tests). Set by registerTaskRoutes.
var tasksFilePath string

// persistMu serializes tasks.json writes. Registry lock is NOT held while
// writing — a slow disk must never stall GET /task.
var persistMu sync.Mutex

// taskRecord is the on-disk row. Same shape as snapshot(); no mutex.
type taskRecord struct {
	ID               string     `json:"id"`
	Project          string     `json:"project"`
	Brief            string     `json:"brief"`
	Key              string     `json:"key,omitempty"`
	Status           taskStatus `json:"status"`
	Phase            string     `json:"phase"`
	Detail           string     `json:"detail"`
	StartedAt        time.Time  `json:"startedAt"`
	FinishedAt       *time.Time `json:"finishedAt,omitempty"`
	Err              string     `json:"error,omitempty"`
	StepsDone        int        `json:"stepsDone"`
	StepsTotal       int        `json:"stepsTotal"`
	Progress         []string   `json:"progress"`
	FirstProgressAt  *time.Time `json:"firstProgressAt,omitempty"`
	LastTokenAt      *time.Time `json:"lastTokenAt,omitempty"`
	LastToolAt       *time.Time `json:"lastToolAt,omitempty"`
	Model            string     `json:"model,omitempty"`
	TokensIn         int64      `json:"tokensIn"`
	TokensOut        int64      `json:"tokensOut"`
	SubagentsSpawned int        `json:"subagentsSpawned"`
	Coached          int        `json:"coached"`
	Escalated        bool       `json:"escalated"`
	CallbackURL      string     `json:"callbackUrl,omitempty"`
	Lost             bool       `json:"lost,omitempty"`
	// PID of the process that ran it. Not compared on reload — every
	// running row found on reload is marked failed/lost regardless of PID.
	PID int `json:"pid"`
}

type tasksFile struct {
	Tasks []taskRecord `json:"tasks"`
}

func (t *engineTask) record(key string) taskRecord {
	t.mu.RLock()
	defer t.mu.RUnlock()
	progress := make([]string, len(t.Progress))
	copy(progress, t.Progress)
	return taskRecord{
		ID: t.ID, Project: t.ProjectPath, Brief: t.Brief, Key: key,
		Status: t.Status, Phase: t.Phase, Detail: t.Detail,
		StartedAt: t.StartedAt, FinishedAt: t.FinishedAt, Err: t.Err,
		StepsDone: t.StepsDone, StepsTotal: t.StepsTotal, Progress: progress,
		FirstProgressAt: t.FirstProgressAt, LastTokenAt: t.LastTokenAt, LastToolAt: t.LastToolAt,
		Model: t.Model, TokensIn: t.TokensIn, TokensOut: t.TokensOut,
		SubagentsSpawned: t.SubagentsSpawned, Coached: t.Coached, Escalated: t.Escalated,
		CallbackURL: t.CallbackURL, Lost: t.Lost, PID: os.Getpid(),
	}
}

// persist writes every task to tasksFilePath (tmp + rename). Called on accept
// (before plan) and on every state change. No-op when path unset.
func (r *taskRegistry) persist() {
	path := tasksFilePath
	if path == "" {
		return
	}
	r.mu.RLock()
	keyOf := map[string]string{}
	for k, id := range r.byKey {
		keyOf[id] = k
	}
	all := make([]*engineTask, 0, len(r.tasks))
	for _, t := range r.tasks {
		all = append(all, t)
	}
	r.mu.RUnlock()

	f := tasksFile{Tasks: make([]taskRecord, 0, len(all))}
	for _, t := range all {
		f.Tasks = append(f.Tasks, t.record(keyOf[t.ID]))
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		log.Printf("task api: marshal tasks.json: %v", err)
		return
	}
	persistMu.Lock()
	defer persistMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("task api: mkdir for tasks.json: %v", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		log.Printf("task api: write tasks.json: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("task api: rename tasks.json: %v", err)
	}
}

// load reads tasks.json. Every row still running becomes failed + lost:true
// (terminal, alive=false). Status stays "failed" — a word SARA's gateway
// knows; an unknown status reads as running forever. Returns count lost.
func (r *taskRegistry) load(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var f tasksFile
	if err := json.Unmarshal(data, &f); err != nil {
		log.Printf("task api: tasks.json unreadable, ignoring: %v", err)
		return 0
	}
	lost := 0
	r.mu.Lock()
	for _, rec := range f.Tasks {
		if _, exists := r.tasks[rec.ID]; exists {
			continue
		}
		t := &engineTask{
			ID: rec.ID, ProjectPath: rec.Project, Brief: rec.Brief,
			Status: rec.Status, Phase: rec.Phase, Detail: rec.Detail,
			StartedAt: rec.StartedAt, FinishedAt: rec.FinishedAt, Err: rec.Err,
			StepsDone: rec.StepsDone, StepsTotal: rec.StepsTotal, Progress: rec.Progress,
			FirstProgressAt: rec.FirstProgressAt, LastTokenAt: rec.LastTokenAt, LastToolAt: rec.LastToolAt,
			Model: rec.Model, TokensIn: rec.TokensIn, TokensOut: rec.TokensOut,
			SubagentsSpawned: rec.SubagentsSpawned, Coached: rec.Coached, Escalated: rec.Escalated,
			CallbackURL: rec.CallbackURL, Lost: rec.Lost, restored: true,
			cancel: make(chan struct{}),
		}
		if t.Status == taskRunning {
			now := time.Now()
			t.Status = taskFailed
			t.Lost = true
			t.FinishedAt = &now
			t.Err = "engine restarted while task was running"
			t.Progress = append(t.Progress, now.UTC().Format("15:04:05")+" failed (lost): engine restarted while running")
			lost++
		}
		r.tasks[t.ID] = t
		if rec.Key != "" {
			r.byKey[rec.Key] = t.ID
		}
	}
	r.mu.Unlock()
	return lost
}

// taskRegistryPath: <ENGINE_STATE_DIR>/tasks.json, else <project>/.engine/tasks.json.
func taskRegistryPath(defaultProjectPath string) string {
	if dir := strings.TrimSpace(os.Getenv("ENGINE_STATE_DIR")); dir != "" {
		return filepath.Join(dir, "tasks.json")
	}
	return filepath.Join(strings.TrimSpace(defaultProjectPath), ".engine", "tasks.json")
}

func (r *taskRegistry) get(id string) (*engineTask, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tasks[id]
	return t, ok
}

// liveByKey returns the task already running for this dedupe key, if any.
func (r *taskRegistry) liveByKey(key string) (*engineTask, bool) {
	if key == "" {
		return nil, false
	}
	r.mu.RLock()
	id, ok := r.byKey[key]
	r.mu.RUnlock()
	if !ok {
		return nil, false
	}
	t, ok := r.get(id)
	if !ok {
		return nil, false
	}
	t.mu.RLock()
	running := t.Status == taskRunning
	t.mu.RUnlock()
	return t, running
}

func (r *taskRegistry) put(key string, t *engineTask) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks[t.ID] = t
	if key != "" {
		r.byKey[key] = t.ID
	}
}

// list returns every task, newest first, capped.
func (r *taskRegistry) list(limit int) []map[string]any {
	r.mu.RLock()
	all := make([]*engineTask, 0, len(r.tasks))
	for _, t := range r.tasks {
		all = append(all, t)
	}
	r.mu.RUnlock()

	// Newest first without sorting the whole set: callers want the recent tail.
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		if all[i].StartedAt.Before(all[j].StartedAt) {
			all[i], all[j] = all[j], all[i]
		}
	}
	out := make([]map[string]any, 0, len(all))
	for _, t := range all {
		out = append(out, t.snapshot())
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// runOrchestratorForTaskFn is the injection seam for tests.
var runOrchestratorForTaskFn = func(cfg ai.OrchestratorConfig) (*ai.OrchestrationState, error) {
	return runOrchestratorFn(cfg)
}

// checkpointFn is the injection seam for tests. Bound to git.Checkpoint in
// production: commit+push via the `git checkpoint` CLI, not a hand-rolled
// push. Only called on a worklist item's success — see the switch below.
var checkpointFn = gogit.Checkpoint

// checkpointMessage builds the commit message for a successful worklist item.
// Falls back to the task id when the brief is empty so the CLI never gets a
// blank -m.
func checkpointMessage(taskID, brief string) string {
	brief = strings.TrimSpace(brief)
	if brief == "" {
		return fmt.Sprintf("task %s: completed", taskID)
	}
	return fmt.Sprintf("task %s: %s", taskID, brief)
}

// startTask dispatches one unit of work and returns immediately.
func startTask(projectPath, brief, owner, repo, dedupeKey, requestedModel, role, callbackURL string, teamSize int) *engineTask {
	id := fmt.Sprintf("task-%d-%s", time.Now().UnixNano()/1e6, shortToken())
	t := &engineTask{
		ID:          id,
		ProjectPath: projectPath,
		Brief:       brief,
		Status:      taskRunning,
		Phase:       "queued",
		StartedAt:   time.Now(),
		CallbackURL: callbackURL,
		cancel:      make(chan struct{}),
	}
	tasks.put(dedupeKey, t)
	// On disk BEFORE the plan runs. Restart between here and the first phase
	// still leaves a row for SARA to find.
	tasks.persist()

	go func() {
		defer func() {
			// An orchestrator panic must not take the server with it. The whole
			// value of this endpoint to SARA is that it always answers; a
			// process that dies on one bad task strands every other one.
			if rec := recover(); rec != nil {
				t.note("error", fmt.Sprintf("orchestrator panicked: %v", rec))
				t.finish(taskFailed, fmt.Sprintf("orchestrator panicked: %v", rec))
				log.Printf("task %s: panic: %v", id, rec)
			}
		}()

		cfg := ai.OrchestratorConfig{
			ProjectPath:     projectPath,
			Owner:           owner,
			Repo:            repo,
			Brief:           brief,
			SessionIDPrefix: id,
			TaskMode:        true,
			TaskID:          id,
			PlanSteps:       0, // unknown at dispatch time — see ShouldRunEventOrchestrator comment
			RequestedModel:  requestedModel,
			RequestedRole:   role,
			TeamSize:        teamSize,
			Cancel:          t.cancel,
			ChatFn:          aiChatFn,
			OnPhase: func(phase, detail string) {
				t.note(phase, detail)
				log.Printf("[task %s] %s %s", id, phase, detail)
			},
			OnProgress: func(msg string) { t.note("progress", msg) },
			OnPlanUpdate: func(st *ai.OrchestrationState) {
				t.setPlan(doneCount(st), len(st.Plan))
			},
			OnError:    func(msg string) { t.note("error", msg) },
			OnActivity: t.activity,
			OnRunStats: t.addRun,
			OnCoach:    t.setCoach,
		}

		// One orchestrator per working tree at a time. Task-mode runs edit
		// the project's checkout directly (no worktree), so two of them on
		// the same repo would review each other's half-written diffs. The
		// task stays in phase "queued" while it waits — its poller reads that
		// as "not started", not "stalled" — and different projects do not
		// wait on each other.
		release := projectGate.acquire(projectPath, t.cancel)
		if release == nil {
			t.finish(taskCanceled, "canceled while queued")
			return
		}
		defer release()
		if cancelClosed(t.cancel) {
			t.finish(taskCanceled, "canceled while queued")
			return
		}
		t.note("orchestrator", "started — working tree free")

		var state *ai.OrchestrationState
		var runErr error
		// db.WithProject scopes the per-project database for the run, the same
		// way the scaffold path does.
		if dbErr := db.WithProject(projectPath, func() error {
			state, runErr = runOrchestratorForTaskFn(cfg)
			return nil
		}); dbErr != nil {
			runErr = fmt.Errorf("db.WithProject: %w", dbErr)
		}

		if state != nil {
			t.setPlan(doneCount(state), len(state.Plan))
		}
		switch {
		case cancelClosed(t.cancel):
			t.finish(taskCanceled, "canceled")
		case runErr != nil:
			t.finish(taskFailed, runErr.Error())
		default:
			// Success only. Push targets whatever branch is currently checked
			// out in projectPath (normally main) — task-mode items have no
			// branch-override field on OrchestratorConfig/engineTask today,
			// so there is nothing else to respect here. A failed/canceled
			// run must never reach this branch, so nothing gets committed or
			// pushed for it.
			if ckErr := checkpointFn(projectPath, checkpointMessage(id, t.Brief), true); ckErr != nil {
				log.Printf("task %s: checkpoint failed: %v", id, ckErr)
				t.note("checkpoint", fmt.Sprintf("checkpoint/push failed: %v", ckErr))
			} else {
				t.note("checkpoint", "checkpointed and pushed")
			}
			t.finish(taskDone, "")
		}
	}()

	return t
}

// projectGates serializes task-mode orchestrators per project path.
type projectGates struct {
	mu    sync.Mutex
	slots map[string]chan struct{}
}

var projectGate = &projectGates{slots: map[string]chan struct{}{}}

// acquire blocks until the project's slot is free or cancel fires. It
// returns the release func, or nil if cancelled while waiting.
func (g *projectGates) acquire(projectPath string, cancel <-chan struct{}) func() {
	key := strings.TrimSpace(projectPath)
	g.mu.Lock()
	slot, ok := g.slots[key]
	if !ok {
		slot = make(chan struct{}, 1)
		g.slots[key] = slot
	}
	g.mu.Unlock()
	select {
	case slot <- struct{}{}:
		return func() { <-slot }
	case <-cancel:
		return nil
	}
}

// cancelClosed reports whether a cancel channel has fired.
func cancelClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func shortToken() string {
	return fmt.Sprintf("%04x", time.Now().UnixNano()&0xffff)
}

// registerTaskRoutes wires the task surface onto the default mux.
func registerTaskRoutes(defaultProjectPath string) {
	tasksFilePath = taskRegistryPath(defaultProjectPath)
	if lost := tasks.load(tasksFilePath); lost > 0 {
		log.Printf("task api: reloaded %s — %d task(s) marked failed+lost (was running when engine restarted)", tasksFilePath, lost)
	}
	tasks.persist()

	// GET /task/<id>: path form of GET /task?id=. Never touches the planner —
	// registry read under RLock, nothing else.
	httpHandleFuncFn("/task/", func(w http.ResponseWriter, r *http.Request) {
		cors(w, "GET, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "GET required", http.StatusMethodNotAllowed)
			return
		}
		id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/task/"), "/")
		t, ok := tasks.get(id)
		if !ok {
			http.Error(w, "no such task", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, t.snapshot())
	})

	// Comms bridge callback. claude -p workers hit this via the stdio MCP
	// bridge (ai/mcp_bridge.go) for agent_* and friends.
	httpHandleFuncFn(ai.MCPBridgeToolPath, ai.MCPBridgeHTTPHandler)

	httpHandleFuncFn("/task", func(w http.ResponseWriter, r *http.Request) {
		cors(w, "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		switch r.Method {
		case http.MethodGet:
			id := strings.TrimSpace(r.URL.Query().Get("id"))
			if id == "" {
				writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks.list(50)})
				return
			}
			t, ok := tasks.get(id)
			if !ok {
				http.Error(w, "no such task", http.StatusNotFound)
				return
			}
			writeJSON(w, http.StatusOK, t.snapshot())

		case http.MethodPost:
			var body struct {
				Project string `json:"project"`
				Brief   string `json:"brief"`
				Owner   string `json:"owner"`
				Repo    string `json:"repo"`
				// Key deduplicates a re-sent dispatch. SARA passes the worklist
				// item's identity here.
				Key string `json:"key"`
				// Model is the caller's chosen model tier for this task (SARA's
				// chooseModel). Optional — an empty value leaves model
				// resolution exactly as it was before this field existed.
				Model string `json:"model"`
				// Role is the specialized agent role for this task (e.g., "design-reviewer").
				// Optional — empty defaults to orchestrator behavior.
				Role string `json:"role"`
				// TeamSize is an explicit request for more than one concurrent
				// worker on this item. Optional — 0/absent means the default,
				// one item = one worker (ai.effectiveTeamSize: cfg.TeamSize,
				// else MYEDITOR_TEAM_SIZE, else 1). Only set this when the
				// dispatch genuinely wants a team.
				TeamSize int `json:"teamSize"`
				// CallbackURL, if set, is POSTed to (fire-and-forget, empty
				// body) when this task reaches a terminal state. Optional --
				// an empty value leaves the caller polling GET-by-id exactly
				// as it always has.
				CallbackURL string `json:"callbackUrl"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
				http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
				return
			}
			if strings.TrimSpace(body.Project) == "" {
				body.Project = defaultProjectPath
			}
			if strings.TrimSpace(body.Brief) == "" {
				http.Error(w, "brief is required", http.StatusBadRequest)
				return
			}
			if existing, running := tasks.liveByKey(body.Key); running {
				// Not an error: the caller asked for work that is already being
				// done, and the honest answer is the id of the task doing it.
				snap := existing.snapshot()
				snap["deduped"] = true
				writeJSON(w, http.StatusOK, snap)
				return
			}
			t := startTask(body.Project, body.Brief, body.Owner, body.Repo, body.Key, body.Model, body.Role, body.CallbackURL, body.TeamSize)
			writeJSON(w, http.StatusAccepted, t.snapshot())

		default:
			http.Error(w, "GET or POST required", http.StatusMethodNotAllowed)
		}
	})

	httpHandleFuncFn("/task/cancel", func(w http.ResponseWriter, r *http.Request) {
		cors(w, "POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		t, ok := tasks.get(id)
		if !ok {
			http.Error(w, "no such task", http.StatusNotFound)
			return
		}
		t.stop()
		// Also stop the orchestrator handle: the cancel channel is checked at
		// step boundaries, and a run inside a long builder turn needs the
		// handle's own stop to reach the process.
		if h := ai.GetTaskHandle(t.ProjectPath, t.ID); h != nil {
			h.Stop()
		}
		writeJSON(w, http.StatusOK, t.snapshot())
	})

	// The levers currently in force. SARA reads this to size its own
	// concurrency, which is how one budget decision reaches both processes.
	httpHandleFuncFn("/levers", func(w http.ResponseWriter, r *http.Request) {
		cors(w, "GET, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		lv := ai.CurrentQuotaLevers(ctx)
		writeJSON(w, http.StatusOK, map[string]any{
			"maxConcurrency":   lv.MaxConcurrency,
			"subagentFanout":   lv.SubagentFanout,
			"maxContextTokens": lv.MaxContextTokens,
			"tier":             lv.TierName,
			"governed":         lv.Governed,
			"parallelDefault":  ai.EventOrchestratorEnabled(),
		})
	})
}

func cors(w http.ResponseWriter, methods string) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", methods)
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("task api: encode: %v", err)
	}
}
