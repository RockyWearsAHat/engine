package ai

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

// captureLog routes the standard logger into a buffer for the test's life.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	return &buf
}

// shortSessionBudget shrinks the session ceiling and hard-stop grace to test
// scale and restores them afterwards.
func shortSessionBudget(t *testing.T, budget, grace time.Duration) {
	t.Helper()
	prevBudget, prevGrace := sessionBudget, planPhaseHardStopGrace
	sessionBudget, planPhaseHardStopGrace = budget, grace
	t.Cleanup(func() {
		sessionBudget, planPhaseHardStopGrace = prevBudget, prevGrace
	})
}

// recordKills replaces the tree-kill hook with one that records the PIDs it
// was asked to kill, and swaps the registry lookup for a fixed PID list so no
// real `claude` process is involved.
func recordKills(t *testing.T, registered []int) func() []int {
	t.Helper()
	var mu sync.Mutex
	var killed []int
	prevKill, prevLive := killSessionTreeFn, liveSessionPIDsFn
	killSessionTreeFn = func(pid int) error {
		mu.Lock()
		defer mu.Unlock()
		killed = append(killed, pid)
		return nil
	}
	liveSessionPIDsFn = func(taskID string) []int {
		if taskID == "" {
			return nil
		}
		return registered
	}
	t.Cleanup(func() {
		killSessionTreeFn, liveSessionPIDsFn = prevKill, prevLive
	})
	return func() []int {
		mu.Lock()
		defer mu.Unlock()
		return append([]int(nil), killed...)
	}
}

// stuckChatFn is a chat call that streams some output and then never returns
// on its own. honourCancel makes it return once its cancel channel closes,
// the way the real provider's cancel bridge does; otherwise it ignores cancel
// entirely, which is the stuck-session case the hard stop exists for. release
// unblocks every stuck call at test end so nothing leaks past the test.
func stuckChatFn(honourCancel bool, release <-chan struct{}, started chan<- time.Time) func(*ChatContext, string) {
	return func(ctx *ChatContext, _ string) {
		select {
		case started <- time.Now():
		default:
		}
		ctx.OnChunk("partial output before the ceiling", false)
		if honourCancel && ctx.Cancel != nil {
			select {
			case <-ctx.Cancel:
			case <-release:
			}
			return
		}
		<-release
	}
}

// sessionCeilingCases is shared by the execute and validate tests: the
// ceiling is one mechanism, and both phases must behave identically under it.
var sessionCeilingCases = []struct {
	name         string
	taskID       string // "" = not task mode
	honourCancel bool
	wantKill     bool
}{
	{name: "task mode, session ignores cancel: tree-killed at grace", taskID: "task-1", honourCancel: false, wantKill: true},
	{name: "task mode, session honours cancel: no kill needed", taskID: "task-1", honourCancel: true, wantKill: false},
	{name: "no task id, session ignores cancel: nothing registered to kill", taskID: "", honourCancel: false, wantKill: false},
	{name: "no task id, session honours cancel", taskID: "", honourCancel: true, wantKill: false},
}

func TestSessionCeiling_ExecutePhase(t *testing.T) {
	for _, tc := range sessionCeilingCases {
		t.Run(tc.name, func(t *testing.T) {
			shortSessionBudget(t, 40*time.Millisecond, 40*time.Millisecond)
			killed := recordKills(t, []int{4242})
			logs := captureLog(t)
			release := make(chan struct{})
			defer close(release)
			started := make(chan time.Time, 1)

			projectDir := t.TempDir()
			brain, err := NewOrchestrationBrain(projectDir, "owner", "repo", "brief", "session")
			if err != nil {
				t.Fatalf("brain: %v", err)
			}
			if err := brain.UpdatePlan([]PlanStep{{Index: 1, Title: "Do the thing", Body: "do it"}}); err != nil {
				t.Fatalf("plan: %v", err)
			}
			if err := brain.AddTeam("team-general-0", "general", []int{0}, nil); err != nil {
				t.Fatalf("team: %v", err)
			}
			bus := NewEventBus()
			failed := bus.Subscribe(EventTeamFailed, 1)
			cfg := OrchestratorConfig{
				ProjectPath:     projectDir,
				SessionIDPrefix: "test",
				TaskID:          tc.taskID,
				TaskMode:        tc.taskID != "",
				ChatFn:          stuckChatFn(tc.honourCancel, release, started),
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w := &TeamWorker{
				teamID: "team-general-0", role: "general", steps: []int{0},
				brain: brain, bus: bus, cfg: cfg, ctx: ctx, cancel: cancel, maxTurns: 3,
			}

			w.run()
			// Measured from the first chat call, not from run(): the step's
			// prompt assembly has its own one-off warm-up cost, and the ceiling
			// bounds the session, not the setup.
			var elapsed time.Duration
			select {
			case s := <-started:
				elapsed = time.Since(s)
			default:
				t.Fatal("chat function was never called")
			}

			// The step failed on the budget, and that failed the team.
			select {
			case ev := <-failed:
				msg, _ := ev.Payload["error"].(string)
				if !strings.Contains(msg, "session budget hit") {
					t.Fatalf("team failure reason = %q, want it to name the session budget", msg)
				}
			case <-time.After(time.Second):
				t.Fatal("team never reported failure")
			}
			team, err := brain.GetTeam("team-general-0")
			if err != nil || team.Status != "failed" {
				t.Fatalf("team status = %q (err %v), want failed", team.Status, err)
			}
			// One turn's budget plus grace, not three turns' worth: the ceiling
			// ends the step, it does not just end the turn.
			if elapsed > 600*time.Millisecond {
				t.Fatalf("step took %s; the ceiling did not end it", elapsed)
			}

			wantLog := fmt.Sprintf("session budget hit task=%s phase=execute after", tc.taskID)
			if !strings.Contains(logs.String(), wantLog) {
				t.Fatalf("log missing %q:\n%s", wantLog, logs.String())
			}
			if got := killed(); tc.wantKill != (len(got) == 1 && got[0] == 4242) {
				t.Fatalf("killed PIDs = %v, wantKill=%v", got, tc.wantKill)
			}
		})
	}
}

func TestSessionCeiling_ValidatePhase(t *testing.T) {
	for _, tc := range sessionCeilingCases {
		t.Run(tc.name, func(t *testing.T) {
			shortSessionBudget(t, 40*time.Millisecond, 40*time.Millisecond)
			killed := recordKills(t, []int{4242})
			logs := captureLog(t)
			release := make(chan struct{})
			defer close(release)

			projectDir := t.TempDir()
			brain, err := NewOrchestrationBrain(projectDir, "owner", "repo", "brief", "session")
			if err != nil {
				t.Fatalf("brain: %v", err)
			}
			eo := &EventOrchestrator{
				cfg: OrchestratorConfig{
					ProjectPath:     projectDir,
					SessionIDPrefix: "test",
					TaskID:          tc.taskID,
					TaskMode:        tc.taskID != "",
					OnPhase:         func(string, string) {},
					OnProgress:      func(string) {},
					OnError:         func(string) {},
					ChatFn:          stuckChatFn(tc.honourCancel, release, make(chan time.Time, 1)),
				},
				brain: brain,
			}
			before := brain.OuterIterationCount()
			iteration := brain.NextOuterIteration()

			valid, feedback := eo.phaseValidate()

			if valid {
				t.Fatal("validation passed on a session that hit the budget")
			}
			if !strings.Contains(feedback, "session budget hit") {
				t.Fatalf("feedback = %q, want it to name the session budget", feedback)
			}
			if !strings.Contains(feedback, "partial output before the ceiling") {
				t.Fatalf("feedback dropped the output produced so far: %q", feedback)
			}
			// The budget hit is charged as the iteration it ran in, exactly as any
			// other failed validation: the counter advanced, and a re-plan follows.
			if iteration != before+1 || brain.OuterIterationCount() != before+1 {
				t.Fatalf("iteration counter: before=%d now=%d", before, brain.OuterIterationCount())
			}

			wantLog := fmt.Sprintf("session budget hit task=%s phase=validate after", tc.taskID)
			if !strings.Contains(logs.String(), wantLog) {
				t.Fatalf("log missing %q:\n%s", wantLog, logs.String())
			}
			if got := killed(); tc.wantKill != (len(got) == 1 && got[0] == 4242) {
				t.Fatalf("killed PIDs = %v, wantKill=%v", got, tc.wantKill)
			}
		})
	}
}

// A session that finishes inside the budget is left alone: no kill, no
// budget-hit line, and the step proceeds on its output.
func TestSessionCeiling_FastSessionUntouched(t *testing.T) {
	shortSessionBudget(t, 200*time.Millisecond, 40*time.Millisecond)
	killed := recordKills(t, []int{4242})
	logs := captureLog(t)

	cfg := OrchestratorConfig{
		ProjectPath: t.TempDir(), TaskID: "task-1", TaskMode: true,
		ChatFn: func(ctx *ChatContext, _ string) { ctx.OnChunk("signal_done", false) },
	}
	cc := newPhaseChat(cfg, "fast")
	if runBoundedSession(cfg, "execute", cc, "go") {
		t.Fatal("fast session reported as a budget hit")
	}
	if got := killed(); len(got) != 0 {
		t.Fatalf("fast session was killed: %v", got)
	}
	if strings.Contains(logs.String(), "session budget hit") {
		t.Fatalf("unexpected budget line:\n%s", logs.String())
	}
	if !strings.Contains(cc.GetOutput(), "signal_done") {
		t.Fatal("output was not captured")
	}
}

func TestSessionExitTelemetry(t *testing.T) {
	cases := []struct {
		name       string
		taskID     string // "" = not task mode
		stream     string // what `claude -p` printed; "" = died before any result event
		wall       time.Duration
		want       []string
		wantAbsent []string
	}{
		{
			name:   "task mode: turns, cli duration and usage",
			taskID: "task-1",
			stream: `{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}` + "\n" +
				`{"type":"result","subtype":"success","is_error":false,"result":"done","num_turns":5,"duration_ms":1234,` +
				`"usage":{"input_tokens":700,"output_tokens":42,"cache_read_input_tokens":10}}`,
			wall: 9 * time.Second,
			want: []string{"session exit task=task-1 phase=execute pid=777 turns=5 duration_ms=1234 tokens_in=700 tokens_out=42", " live="},
		},
		{
			name:   "not task mode: same telemetry, empty task",
			taskID: "",
			stream: `{"type":"result","subtype":"success","is_error":false,"result":"ok","num_turns":5,"duration_ms":1234,` +
				`"usage":{"input_tokens":700,"output_tokens":42}}`,
			wall: 9 * time.Second,
			want: []string{"session exit task= phase=plan pid=777 turns=5 duration_ms=1234 tokens_in=700 tokens_out=42"},
		},
		{
			name:       "result without usage: no token fields",
			taskID:     "task-1",
			stream:     `{"type":"result","subtype":"success","is_error":false,"result":"ok","num_turns":3,"duration_ms":500}`,
			wall:       9 * time.Second,
			want:       []string{"turns=3 duration_ms=500 live="},
			wantAbsent: []string{"tokens_in=", "tokens_out="},
		},
		{
			name:       "killed before a result event: zero turns, wall clock duration",
			taskID:     "task-1",
			stream:     `{"type":"assistant","message":{"content":[{"type":"text","text":"still going"}]}}`,
			wall:       9 * time.Second,
			want:       []string{"session exit task=task-1 phase=execute pid=777 turns=0 duration_ms=9000 live="},
			wantAbsent: []string{"tokens_in="},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &ChatContext{TaskID: tc.taskID, OnChunk: func(string, bool) {}, OnError: func(string) {}}
			var calls []ToolCall
			var text strings.Builder
			stats := parseClaudeStreamWithStats(ctx, strings.NewReader(tc.stream), &calls, &text, "")

			phase := "execute"
			if tc.taskID == "" {
				phase = "plan"
			}
			line := sessionExitLogLine(tc.taskID, phase, 777, stats, tc.wall)

			for _, w := range tc.want {
				if !strings.Contains(line, w) {
					t.Fatalf("line %q\nmissing %q", line, w)
				}
			}
			for _, w := range tc.wantAbsent {
				if strings.Contains(line, w) {
					t.Fatalf("line %q\nshould not contain %q", line, w)
				}
			}
		})
	}
}
