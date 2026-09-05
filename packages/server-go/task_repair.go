package main

// Repair is what happens to a running task when the engine restarts under it.
//
// Before this existed the answer was uniform: every running row became
// failed + lost, and SARA re-dispatched the item from nothing. That threw away
// work that was already on disk and paid for the same item twice — and the
// restart is not rare, it is how this engine is deployed (supervisor restart,
// ./start.sh --restart, a config change, a crash).
//
// What makes recovery possible is that the process doing the work is not this
// one. `claude -p` keeps its own conversation, addressed by a session id, and
// will continue it on request (`claude --resume <id>`). The task API persists
// that id per phase as soon as the CLI announces it (engineTask.
// updateClaudeSessions), so a restart has a handle on the actual work rather
// than only on the fact that work was happening.
//
// The repair itself is three questions asked of the world, not of memory:
// what does the working tree look like, what landed already, and does the CLI
// still have the transcript. decideRepair (task_api.go) turns those answers
// into finish / resume / give up.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/engine/server/ai"
	"github.com/engine/server/db"
	gogit "github.com/engine/server/git"
)

// repairTranscriptTailLines is how much of a session transcript is read back
// as evidence. The tail only has to answer "does this session still exist and
// what was it last doing" — enough to brief the resumed run, not a replay.
const repairTranscriptTailLines = 20

// repairTask decides what became of one interrupted task and acts on it.
//
// rec is the row as it was written before the restart (session ids, start
// time); t is the live task restored from it, already registered and still
// reported as running.
func repairTask(rec taskRecord, t *engineTask) {
	defer func() {
		// A panic here must not take the server down, and must not leave the
		// task reported as running forever either.
		if p := recover(); p != nil {
			log.Printf("task api: repair of %s panicked: %v", rec.ID, p)
			t.note("resume", fmt.Sprintf("repair panicked: %v", p))
			t.markLost("repair panicked while resuming")
		}
	}()

	sid := strings.TrimSpace(rec.ClaudeSessions[rec.LastSessionPhase])
	insp := inspectForRepair(rec)

	switch decideRepair(insp) {
	case repairLost:
		log.Printf("task api: %s no transcript for session %s — marked failed+lost", rec.ID, sid)
		t.note("resume", "no claude transcript for session "+sid+" — cannot resume")
		t.markLost("engine restarted while task was running")

	case repairDone:
		log.Printf("task api: %s already complete on disk — finishing", rec.ID)
		t.note("resume", fmt.Sprintf("work already complete on disk (%d new commit(s), clean tree) — finishing",
			len(insp.NewCommits)))
		completeTaskSuccess(t)

	case repairResume:
		log.Printf("task api: resuming %s from claude session %s (%d uncommitted, %d new commits)",
			rec.ID, sid, insp.DirtyFiles, len(insp.NewCommits))
		t.note("resume", fmt.Sprintf("resuming claude session %s (%d uncommitted, %d new commits)",
			sid, insp.DirtyFiles, len(insp.NewCommits)))
		resumeTaskRun(rec, t, sid, insp)
	}
}

// markLost puts a task into the terminal state a restart used to give every
// running row: failed, with lost set so the gateway can tell it from a task
// that actually failed at its work.
func (t *engineTask) markLost(reason string) {
	t.mu.Lock()
	t.Lost = true
	t.mu.Unlock()
	t.finish(taskFailed, reason)
}

// inspectForRepair gathers the evidence decideRepair judges. Every probe is
// read-only and every failure is an absence, not an error: a repo that cannot
// be read reports no commits and no dirty files, which decides "resume" — the
// safe answer, because resuming re-does at worst what was already done, while
// a wrong "done" drops the work silently.
func inspectForRepair(rec taskRecord) repairInspection {
	return repairInspection{
		DirtyFiles:        countDirtyFiles(rec.Project),
		NewCommits:        commitsSince(rec.Project, rec.StartedAt),
		ItemTicked:        dxItemTicked(rec.Project, rec.Brief),
		TranscriptSummary: claudeTranscriptTail(rec.ClaudeSessions[rec.LastSessionPhase]),
	}
}

// countDirtyFiles is how many paths the project has uncommitted (modified,
// staged, or untracked). Non-zero means the interrupted run had work it had
// not put away yet.
func countDirtyFiles(projectPath string) int {
	if strings.TrimSpace(projectPath) == "" {
		return 0
	}
	out, err := gogit.RunGit(projectPath, "status", "--porcelain")
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// commitsSince lists what landed in the project after the task started —
// the work the interrupted run already finished and committed.
func commitsSince(projectPath string, since time.Time) []string {
	if strings.TrimSpace(projectPath) == "" || since.IsZero() {
		return nil
	}
	out, err := gogit.RunGit(projectPath, "log",
		"--since="+since.Format(time.RFC3339), "--pretty=%h %s", "-n", "50")
	if err != nil {
		return nil
	}
	var commits []string
	for _, line := range strings.Split(out, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			commits = append(commits, s)
		}
	}
	return commits
}

// dxItemTicked reports whether the worklist item this task was working is
// already checked off in the project's dx documents.
//
// It is a corroborating signal, never a lone one: a ticked item only ends the
// task when the working tree is also clean (decideRepair). Projects without dx
// documents simply answer false, which resumes — the conservative direction.
func dxItemTicked(projectPath, brief string) bool {
	needle := repairNeedle(brief)
	if needle == "" || strings.TrimSpace(projectPath) == "" {
		return false
	}
	var docs []string
	for _, pattern := range []string{"*.dx", ".dx/*.dx", ".dx/.doc/*", ".doc/*"} {
		matches, err := filepath.Glob(filepath.Join(projectPath, pattern))
		if err == nil {
			docs = append(docs, matches...)
		}
	}
	for _, doc := range docs {
		info, err := os.Stat(doc)
		if err != nil || info.IsDir() || info.Size() > 4<<20 {
			continue
		}
		data, err := os.ReadFile(doc)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			low := strings.ToLower(line)
			if strings.Contains(low, "[x]") && strings.Contains(normalizeSpace(low), needle) {
				return true
			}
		}
	}
	return false
}

// repairNeedle is the distinctive opening of a brief, normalised, used to find
// the brief's own line in a document. Short briefs are matched whole; a long
// one is cut to its first clause so wrapped or lightly edited item text still
// matches. Too short to be distinctive means no match at all.
func repairNeedle(brief string) string {
	n := normalizeSpace(strings.ToLower(brief))
	if len(n) < 12 {
		return ""
	}
	if len(n) > 60 {
		n = n[:60]
	}
	return n
}

func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// claudeTranscriptTail returns a short summary of the CLI's own transcript for
// a session, and "" when the CLI has no record of it.
//
// Empty is the load-bearing answer: --resume against a session the CLI cannot
// find fails, so a task whose transcript is gone is genuinely unrecoverable
// and must take the lost path rather than burn a dispatch discovering that.
//
// Transcripts live under <CLAUDE_CONFIG_DIR|~/.claude>/projects/<encoded
// project path>/<session id>.jsonl. The encoding of the directory name is the
// CLI's business and has changed before, so the session id — which is unique —
// is globbed for across project directories rather than re-derived.
func claudeTranscriptTail(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".claude")
	}
	matches, err := filepath.Glob(filepath.Join(dir, "projects", "*", sessionID+".jsonl"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	f, err := os.Open(matches[0])
	if err != nil {
		return ""
	}
	defer f.Close()

	tail := make([]string, 0, repairTranscriptTailLines)
	total := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		total++
		if len(tail) == repairTranscriptTailLines {
			tail = tail[1:]
		}
		tail = append(tail, line)
	}
	if total == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d transcript entries; tail:\n", total)
	for _, line := range tail {
		b.WriteString(transcriptLineText(line))
		b.WriteString("\n")
	}
	return b.String()
}

// transcriptLineText renders one transcript JSONL entry as a short line. The
// transcript schema is the CLI's own and not ours to depend on, so anything
// unrecognised falls back to a truncated copy of the raw line — the point is
// evidence of what the session was doing, not a faithful decode.
func transcriptLineText(line string) string {
	var entry struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &entry); err != nil || entry.Type == "" {
		return truncateForBrief(line, 200)
	}
	role := entry.Message.Role
	if role == "" {
		role = entry.Type
	}
	text := ""
	switch c := entry.Message.Content.(type) {
	case string:
		text = c
	case []any:
		var parts []string
		for _, raw := range c {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if s, _ := m["text"].(string); strings.TrimSpace(s) != "" {
				parts = append(parts, s)
				continue
			}
			if name, _ := m["name"].(string); name != "" {
				parts = append(parts, "["+name+"]")
			}
		}
		text = strings.Join(parts, " ")
	}
	return role + ": " + truncateForBrief(normalizeSpace(text), 200)
}

func truncateForBrief(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// resumeTaskRun continues an interrupted task: the same orchestrator path a
// fresh dispatch takes, with the Claude Code session id set so the execution
// session picks its own conversation back up, and a brief that says what
// happened rather than restating the item as if nothing had.
//
// It takes the same admission gates as startTask. A repair is a running task
// like any other — skipping the gates would let a restart with several
// interrupted rows put all of them on the box at once, which is the failure
// the gates exist to prevent.
func resumeTaskRun(rec taskRecord, t *engineTask, sessionID string, insp repairInspection) {
	cfg := ai.OrchestratorConfig{
		ProjectPath:     rec.Project,
		Brief:           repairBrief(rec, insp),
		SessionIDPrefix: rec.ID,
		TaskMode:        true,
		TaskID:          rec.ID,
		// The tier the run was already using. Persisted per task as the last
		// model a provider run reported, which is the closest thing on disk to
		// the dispatch's original choice.
		RequestedModel:  rec.Model,
		ResumeSessionID: sessionID,
		Cancel:          t.cancel,
		ChatFn:          aiChatFn,
		OnPhase: func(phase, detail string) {
			t.note(phase, detail)
			log.Printf("[task %s] %s %s", rec.ID, phase, detail)
		},
		OnProgress: func(msg string) { t.note("progress", msg) },
		OnPlanUpdate: func(st *ai.OrchestrationState) {
			t.setPlan(doneCount(st), len(st.Plan))
		},
		OnError:         func(msg string) { t.note("error", msg) },
		OnActivity:      t.activity,
		OnRunStats:      t.addRun,
		OnCoach:         t.setCoach,
		OnClaudeSession: t.updateClaudeSessions,
	}

	releaseGlobal := taskGate.acquire(t.cancel)
	if releaseGlobal == nil {
		t.finish(taskCanceled, "canceled while queued for repair")
		return
	}
	defer releaseGlobal()

	release := projectGate.acquire(rec.Project, t.cancel)
	if release == nil {
		t.finish(taskCanceled, "canceled while queued for repair")
		return
	}
	defer release()
	if cancelClosed(t.cancel) {
		t.finish(taskCanceled, "canceled while queued for repair")
		return
	}
	t.note("resume", "working tree free — continuing session "+sessionID)

	var state *ai.OrchestrationState
	var runErr error
	if dbErr := db.WithProject(rec.Project, func() error {
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
		completeTaskSuccess(t)
	}
}

// repairBrief tells the resumed session what it is walking back into.
//
// The original item stays at the top — it is still the objective — followed by
// what the tree says already happened. Without this the resumed conversation
// has its own memory of the work but no idea it was interrupted, and the most
// likely thing it does is redo the last thing it remembers starting.
func repairBrief(rec taskRecord, insp repairInspection) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(rec.Brief))
	b.WriteString("\n\n--- RESUMING AFTER AN ENGINE RESTART ---\n")
	b.WriteString("You were already working this item and your session was interrupted. ")
	b.WriteString("Continue from where you left off. Do NOT start over, and do not redo work that is already committed.\n\n")
	fmt.Fprintf(&b, "Uncommitted files in the working tree: %d\n", insp.DirtyFiles)
	if len(insp.NewCommits) > 0 {
		b.WriteString("Commits made since this task started:\n")
		for _, c := range insp.NewCommits {
			b.WriteString("  " + c + "\n")
		}
	} else {
		b.WriteString("No commits have landed since this task started.\n")
	}
	if s := strings.TrimSpace(insp.TranscriptSummary); s != "" {
		b.WriteString("\nWhat your session was last doing:\n")
		b.WriteString(truncateForBrief(s, 2000))
		b.WriteString("\n")
	}
	b.WriteString("\nFirst, verify the current state of the work yourself, then finish it.\n")
	return b.String()
}
