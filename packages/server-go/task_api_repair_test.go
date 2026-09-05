package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDecideRepair(t *testing.T) {
	for _, tc := range []struct {
		name string
		insp repairInspection
		want repairDecision
	}{
		{
			// No transcript, nothing to resume from — whatever the tree says.
			name: "transcript missing",
			insp: repairInspection{DirtyFiles: 3, NewCommits: []string{"abc fix"}},
			want: repairLost,
		},
		{
			name: "transcript missing even with ticked item",
			insp: repairInspection{ItemTicked: true},
			want: repairLost,
		},
		{
			// The item says done and the tree agrees: re-running is pure waste.
			name: "ticked and clean",
			insp: repairInspection{ItemTicked: true, TranscriptSummary: "2 entries"},
			want: repairDone,
		},
		{
			// Ticked but with work not put away yet — the run had not finished.
			name: "ticked but dirty",
			insp: repairInspection{ItemTicked: true, DirtyFiles: 2, TranscriptSummary: "2 entries"},
			want: repairResume,
		},
		{
			name: "not ticked clean tree",
			insp: repairInspection{TranscriptSummary: "2 entries"},
			want: repairResume,
		},
		{
			name: "not ticked dirty tree",
			insp: repairInspection{DirtyFiles: 5, NewCommits: []string{"abc wip"}, TranscriptSummary: "9 entries"},
			want: repairResume,
		},
		{
			// Whitespace is not a transcript.
			name: "blank transcript",
			insp: repairInspection{TranscriptSummary: "   \n", ItemTicked: true},
			want: repairLost,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := decideRepair(tc.insp); got != tc.want {
				t.Errorf("decideRepair(%+v) = %q, want %q", tc.insp, got, tc.want)
			}
		})
	}
}

// stubRepairs replaces the repair runner with one that records the rows it was
// handed, so load() can be tested without a repository or a CLI.
func stubRepairs(t *testing.T) chan pendingRepair {
	t.Helper()
	seen := make(chan pendingRepair, 8)
	orig := repairTaskFn
	repairTaskFn = func(rec taskRecord, task *engineTask) {
		seen <- pendingRepair{rec: rec, task: task}
	}
	t.Cleanup(func() { repairTaskFn = orig })
	return seen
}

func writeTasksFile(t *testing.T, dir string, rows ...taskRecord) string {
	t.Helper()
	path := filepath.Join(dir, "tasks.json")
	data, err := json.MarshalIndent(tasksFile{Tasks: rows}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A running row that remembers its Claude Code session is REPAIRED, not lost:
// same id, same key, still running, and a repair goroutine takes it over. A
// running row without one takes the old treatment. Both shapes appear in the
// same file, because that is what a real restart finds.
func TestTaskRegistryLoad_ResumesRowWithClaudeSession(t *testing.T) {
	seen := stubRepairs(t)
	now := time.Now()
	path := writeTasksFile(t, t.TempDir(),
		taskRecord{
			ID: "t-resume", Project: "/p", Brief: "finish the thing", Key: "k-resume",
			Status: taskRunning, Phase: "execute", StartedAt: now, PID: 1,
			ClaudeSessions:   map[string]string{"plan": "sess-plan", "execute": "sess-exec"},
			LastSessionPhase: "execute",
		},
		taskRecord{
			ID: "t-lost", Project: "/p", Status: taskRunning, Phase: "plan",
			StartedAt: now, Key: "k-lost", PID: 1,
		},
		taskRecord{
			ID: "t-done", Project: "/p", Status: taskDone, StartedAt: now, FinishedAt: &now, PID: 1,
		},
	)

	r := &taskRegistry{tasks: map[string]*engineTask{}, byKey: map[string]string{}}
	if lost := r.load(path); lost != 1 {
		t.Fatalf("lost = %d, want 1 (only the row with no session)", lost)
	}

	resumed, ok := r.tasks["t-resume"]
	if !ok {
		t.Fatal("resumable row missing from registry")
	}
	if resumed.Status != taskRunning {
		t.Errorf("status = %q, want running", resumed.Status)
	}
	if resumed.Phase != "resume" {
		t.Errorf("phase = %q, want resume", resumed.Phase)
	}
	if resumed.Lost {
		t.Error("resumable row must not be flagged lost")
	}
	if resumed.FinishedAt != nil {
		t.Error("resumable row must not be finished")
	}
	if resumed.Detail == "" || resumed.Detail != "engine restarted; repairing from claude session sess-exec" {
		t.Errorf("detail = %q, want the repairing-from-session line", resumed.Detail)
	}
	if got := resumed.ClaudeSessions["execute"]; got != "sess-exec" {
		t.Errorf("session ids did not survive reload: %v", resumed.ClaudeSessions)
	}
	if resumed.LastSessionPhase != "execute" {
		t.Errorf("LastSessionPhase = %q, want execute", resumed.LastSessionPhase)
	}
	// The key still points at the same task, so a re-dispatch of the item is
	// deduped onto the run that is being repaired rather than starting a
	// second one on the same tree.
	if id := r.byKey["k-resume"]; id != "t-resume" {
		t.Errorf("byKey = %q, want t-resume", id)
	}
	if snap := resumed.snapshot(); snap["alive"] != true {
		t.Errorf("a task being repaired is alive: %v", snap["alive"])
	}

	// A row with no session id keeps the old, terminal treatment.
	lost, ok := r.tasks["t-lost"]
	if !ok {
		t.Fatal("lost row missing from registry")
	}
	if lost.Status != taskFailed || !lost.Lost || lost.FinishedAt == nil {
		t.Errorf("row without a session should be failed+lost: %+v", lost)
	}

	select {
	case got := <-seen:
		if got.rec.ID != "t-resume" {
			t.Fatalf("repaired %q, want t-resume", got.rec.ID)
		}
		if got.task != resumed {
			t.Error("repair handed a different engineTask than the registry holds")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no repair goroutine started for the resumable row")
	}
	select {
	case got := <-seen:
		t.Fatalf("unexpected second repair for %q", got.rec.ID)
	case <-time.After(100 * time.Millisecond):
	}
}

// A session id recorded mid-run reaches tasks.json immediately — that is the
// only reason it is available after a crash — and repeats do not rewrite it.
func TestUpdateClaudeSessionsPersists(t *testing.T) {
	_, path, _ := freshTaskAPI(t)
	tasksFilePath = path

	task := &engineTask{ID: "t1", ProjectPath: "/p", Brief: "b", Status: taskRunning, StartedAt: time.Now(), cancel: make(chan struct{})}
	tasks.put("k1", task)

	task.updateClaudeSessions("plan", "sess-plan")
	task.updateClaudeSessions("execute", "sess-exec")
	// Repeats of an id already on file are no-ops, and blanks are not sessions.
	task.updateClaudeSessions("execute", "sess-exec")
	task.updateClaudeSessions("execute", "")
	task.updateClaudeSessions("", "sess-orphan")

	if task.LastSessionPhase != "execute" {
		t.Errorf("LastSessionPhase = %q, want execute", task.LastSessionPhase)
	}
	rows := readTasksFile(t, path).Tasks
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].ClaudeSessions["plan"] != "sess-plan" || rows[0].ClaudeSessions["execute"] != "sess-exec" {
		t.Errorf("sessions not persisted: %v", rows[0].ClaudeSessions)
	}
	if rows[0].LastSessionPhase != "execute" {
		t.Errorf("persisted LastSessionPhase = %q, want execute", rows[0].LastSessionPhase)
	}
}

// The transcript probe is the difference between "resume" and "lost", so it
// has to answer honestly for a session the CLI has never heard of.
func TestClaudeTranscriptTail(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	if got := claudeTranscriptTail(""); got != "" {
		t.Errorf("empty session id = %q, want empty", got)
	}
	if got := claudeTranscriptTail("sess-unknown"); got != "" {
		t.Errorf("unknown session = %q, want empty", got)
	}

	projDir := filepath.Join(dir, "projects", "-tmp-proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"type":"user","message":{"role":"user","content":"do the thing"}}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"on it"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(projDir, "sess-real.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	got := claudeTranscriptTail("sess-real")
	if got == "" {
		t.Fatal("transcript on disk reported as missing")
	}
	for _, want := range []string{"2 transcript entries", "do the thing", "on it"} {
		if !strings.Contains(got, want) {
			t.Errorf("transcript summary %q missing %q", got, want)
		}
	}
}
