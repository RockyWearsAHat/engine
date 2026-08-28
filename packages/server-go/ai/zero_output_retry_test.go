package ai

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// stubBackoff swaps the real sleep for a recorder. Returns waits seen.
func stubBackoff(t *testing.T) *[]time.Duration {
	t.Helper()
	var waits []time.Duration
	orig := buildSleepFn
	buildSleepFn = func(d time.Duration, _ <-chan struct{}) bool {
		waits = append(waits, d)
		return true
	}
	t.Cleanup(func() { buildSleepFn = orig })
	return &waits
}

func zeroOutputState() (*OrchestrationState, *PlanStep) {
	st := &OrchestrationState{Owner: "acme", Repo: "demo", Plan: []PlanStep{{Index: 1, Title: "do thing", Attempts: 1}}}
	return st, &st.Plan[0]
}

// Provider reports usage, zero output tokens, instant return: backoff
// 1s/5s/15s then ErrProviderZeroOutput. Four runs total, all fresh sessions.
func TestBuildStep_ZeroTokensBacksOffThenProviderError(t *testing.T) {
	dir := setupPhasesDB(t)
	waits := stubBackoff(t)
	var sessions []string
	var phases []string
	cfg := OrchestratorConfig{
		ProjectPath: dir, SessionIDPrefix: "zt",
		OnPhase: func(p, d string) { phases = append(phases, p+": "+d) },
		ChatFn: func(ctx *ChatContext, _ string) {
			sessions = append(sessions, ctx.SessionID)
			ctx.OnRunStats(RunStats{Model: "haiku", Seen: true})
		},
	}
	st, step := zeroOutputState()
	err := orchestratorBuildStep(cfg, st, step, "", nil)
	if !errors.Is(err, ErrProviderZeroOutput) {
		t.Fatalf("want ErrProviderZeroOutput, got %v", err)
	}
	if len(*waits) != 3 || (*waits)[0] != time.Second || (*waits)[1] != 5*time.Second || (*waits)[2] != 15*time.Second {
		t.Fatalf("backoff schedule wrong: %v", *waits)
	}
	if len(sessions) != 4 {
		t.Fatalf("want 4 runs, got %d", len(sessions))
	}
	for i := 1; i < len(sessions); i++ {
		if sessions[i] == sessions[i-1] {
			t.Fatalf("retry reused session %s", sessions[i])
		}
	}
	tokenLines := 0
	for _, p := range phases {
		if strings.HasPrefix(p, "tokens: ") && strings.Contains(p, "out=0") {
			tokenLines++
		}
	}
	if tokenLines != 4 {
		t.Fatalf("want 4 token log lines, got %d: %v", tokenLines, phases)
	}
}

// Second run succeeds: one backoff, nil error.
func TestBuildStep_ZeroTokensRecoversOnRetry(t *testing.T) {
	dir := setupPhasesDB(t)
	waits := stubBackoff(t)
	calls := 0
	cfg := OrchestratorConfig{
		ProjectPath: dir, SessionIDPrefix: "zt",
		ChatFn: func(ctx *ChatContext, _ string) {
			calls++
			if calls == 1 {
				ctx.OnRunStats(RunStats{Seen: true})
				return
			}
			ctx.OnChunk("did the thing", false)
			ctx.OnRunStats(RunStats{Seen: true, OutputTokens: 42})
		},
	}
	st, step := zeroOutputState()
	if err := orchestratorBuildStep(cfg, st, step, "", nil); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if calls != 2 || len(*waits) != 1 {
		t.Fatalf("calls=%d waits=%v", calls, *waits)
	}
}

// No usage reported (stub/ollama) + no output = old "no output" error, no backoff.
func TestBuildStep_NoUsageNoOutputIsBuilderError(t *testing.T) {
	dir := setupPhasesDB(t)
	waits := stubBackoff(t)
	cfg := OrchestratorConfig{ProjectPath: dir, SessionIDPrefix: "zt", ChatFn: func(*ChatContext, string) {}}
	st, step := zeroOutputState()
	err := orchestratorBuildStep(cfg, st, step, "", nil)
	if err == nil || errors.Is(err, ErrProviderZeroOutput) || !strings.Contains(err.Error(), "no output") {
		t.Fatalf("want plain no-output error, got %v", err)
	}
	if len(*waits) != 0 {
		t.Fatalf("no backoff expected, got %v", *waits)
	}
}

// Provider fault refunds the attempt; other errors do not.
func TestRefundProviderAttempt(t *testing.T) {
	step := &PlanStep{Attempts: 2}
	if !refundProviderAttempt(step, errors.New("x: "+ErrProviderZeroOutput.Error())) && step.Attempts != 2 {
		t.Fatal("plain string error must not refund")
	}
	if refundProviderAttempt(step, errors.New("builder produced no output")) || step.Attempts != 2 {
		t.Fatalf("non-provider error refunded: attempts=%d", step.Attempts)
	}
	if !refundProviderAttempt(step, ErrProviderZeroOutput) || step.Attempts != 1 {
		t.Fatalf("provider error not refunded: attempts=%d", step.Attempts)
	}
	zero := &PlanStep{}
	if refundProviderAttempt(zero, ErrProviderZeroOutput) || zero.Attempts != 0 {
		t.Fatal("attempts must not go negative")
	}
}

// Reviewer notes reach the retry prompt.
func TestBuildStepPrompt_ReviewerNotesOnRetry(t *testing.T) {
	st, step := zeroOutputState()
	step.Attempts = 2
	step.LastFeedback = "REJECT: missing test"
	p := buildStepPrompt(st, step, "")
	if !strings.Contains(p, "REVIEWER NOTES") || !strings.Contains(p, "missing test") {
		t.Fatalf("prompt lacks reviewer notes:\n%s", p)
	}
}
