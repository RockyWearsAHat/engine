package quota

import (
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func cheap() Config { return Config{Model: "haiku", Effort: "low", ContextBudget: 40_000} }
func mid() Config {
	return Config{Model: "sonnet", Effort: "medium", ContextBudget: 100_000, Subagents: 1}
}
func heavy() Config {
	return Config{Model: "opus", Effort: "high", ContextBudget: 200_000, Subagents: 4}
}

func record(l *Ledger, role string, c Config, n int, successes int, tokens int64) {
	for i := 0; i < n; i++ {
		l.Record(Outcome{Role: role, Config: c, Success: i < successes, Tokens: tokens, At: at("12:00")})
	}
}

// The core accounting decision: failed attempts are charged to the successes
// they made necessary. A cheap config that fails half the time really costs two
// attempts per result.
func TestTokensPerSuccessChargesFailures(t *testing.T) {
	l := NewLedger(LedgerOptions{})
	record(l, "review", cheap(), 10, 5, 10_000) // 100k tokens, 5 results
	s, ok := l.Stat("review", cheap())
	if !ok {
		t.Fatal("stat missing")
	}
	if got := s.TokensPerSuccess(); got != 20_000 {
		t.Errorf("tokensPerSuccess = %.0f, want 20000 (100k spent / 5 results)", got)
	}
	if got := s.Tokens / int64(s.Runs); got != 10_000 {
		t.Errorf("per-run cost = %d; the point is that per-run understates it 2x", got)
	}

	// A config that never succeeds is infinitely inefficient, not free.
	record(l, "review", mid(), 4, 0, 5_000)
	never, _ := l.Stat("review", mid())
	if !math.IsInf(never.TokensPerSuccess(), 1) {
		t.Errorf("a never-succeeding config scored %.0f, want +Inf", never.TokensPerSuccess())
	}
}

// SubagentsSpawned is the observable signal for whether a run actually
// delegated to subagents (dynamic team self-assembly) instead of one agent
// doing everything itself. It must survive Record into the aggregated Stat,
// or there is no way to see whether that delegation is happening at all.
func TestLedgerRecordsSubagentsSpawned(t *testing.T) {
	l := NewLedger(LedgerOptions{})
	l.Record(Outcome{Role: "implement", Config: mid(), Success: true, Tokens: 10_000, SubagentsSpawned: 3, At: at("12:00")})
	l.Record(Outcome{Role: "implement", Config: mid(), Success: true, Tokens: 10_000, SubagentsSpawned: 2, At: at("12:05")})
	l.Record(Outcome{Role: "implement", Config: mid(), Success: true, Tokens: 10_000, At: at("12:10")}) // no delegation this run

	s, ok := l.Stat("implement", mid())
	if !ok {
		t.Fatal("stat missing")
	}
	if s.SubagentsSpawned != 5 {
		t.Errorf("SubagentsSpawned = %d, want 5 (cumulative across runs)", s.SubagentsSpawned)
	}
	if s.Runs != 3 {
		t.Errorf("Runs = %d, want 3", s.Runs)
	}
}

func TestRecommendExploresCheapFirst(t *testing.T) {
	l := NewLedger(LedgerOptions{MinSamples: 3})
	cands := []Config{cheap(), mid(), heavy()}

	rec := l.Recommend("implement", cands)
	if !rec.Exploring {
		t.Error("with no history the ledger should be exploring")
	}
	if rec.Config.Model != "haiku" {
		t.Errorf("first exploration = %s, want the cheapest — learning a cheap config fails costs one cheap run", rec.Config)
	}

	// Once the cheap one is sampled, exploration moves up the list.
	record(l, "implement", cheap(), 3, 0, 5_000)
	rec = l.Recommend("implement", cands)
	if rec.Config.Model != "sonnet" {
		t.Errorf("second exploration = %s, want sonnet", rec.Config)
	}
}

// The whole objective in one test: once the evidence is in, the ledger picks the
// cheapest configuration that still works — not the best one available.
func TestRecommendPicksCheapestThatWorks(t *testing.T) {
	l := NewLedger(LedgerOptions{MinSamples: 3, SuccessBar: 0.8})
	cands := []Config{cheap(), mid(), heavy()}

	record(l, "review", cheap(), 10, 9, 8_000)   // 90% ok, very cheap
	record(l, "review", mid(), 10, 10, 40_000)   // perfect, 5x the cost
	record(l, "review", heavy(), 10, 10, 90_000) // perfect, 11x the cost

	rec := l.Recommend("review", cands)
	if rec.Config.Model != "haiku" {
		t.Fatalf("recommended %s for review; the cheapest clearing the bar was haiku", rec.Config)
	}
	if !rec.Confident {
		t.Error("with 10 samples each the recommendation should be confident")
	}
	if rec.Savings <= 0 {
		t.Error("expected a positive savings estimate versus the heaviest option")
	}

	// A role where the cheap option genuinely does not work must escalate.
	record(l, "implement", cheap(), 10, 2, 8_000)
	record(l, "implement", mid(), 10, 6, 40_000)
	record(l, "implement", heavy(), 10, 10, 90_000)
	rec = l.Recommend("implement", cands)
	if rec.Config.Model != "opus" {
		t.Errorf("recommended %s for implement; only opus clears the bar there", rec.Config)
	}
}

// Per-role learning is where the savings come from: the same ledger must reach
// opposite conclusions for different kinds of work.
func TestRecommendIsPerRole(t *testing.T) {
	l := NewLedger(LedgerOptions{MinSamples: 3, SuccessBar: 0.8})
	cands := []Config{cheap(), mid(), heavy()}
	record(l, "review", cheap(), 5, 5, 8_000)
	record(l, "review", mid(), 5, 5, 40_000)
	record(l, "review", heavy(), 5, 5, 90_000)
	record(l, "implement", cheap(), 5, 0, 8_000)
	record(l, "implement", mid(), 5, 1, 40_000)
	record(l, "implement", heavy(), 5, 5, 90_000)

	if got := l.Recommend("review", cands).Config.Model; got != "haiku" {
		t.Errorf("review -> %s, want haiku", got)
	}
	if got := l.Recommend("implement", cands).Config.Model; got != "opus" {
		t.Errorf("implement -> %s, want opus", got)
	}
}

func TestRecommendWhenNothingClearsTheBar(t *testing.T) {
	l := NewLedger(LedgerOptions{MinSamples: 2, SuccessBar: 0.95})
	cands := []Config{cheap(), heavy()}
	record(l, "flaky", cheap(), 10, 3, 8_000)
	record(l, "flaky", heavy(), 10, 7, 90_000)

	rec := l.Recommend("flaky", cands)
	if rec.Config.Model != "opus" {
		t.Errorf("got %s, want the best available when none clears the bar", rec.Config)
	}
	if !strings.Contains(rec.Reason, "no configuration clears") {
		t.Errorf("reason should say the bar was unmet, got %q", rec.Reason)
	}
}

// Pacing sets the ceiling; the ledger chooses beneath it. Neither may override
// the other.
func TestCandidatesRespectTierCeiling(t *testing.T) {
	p := DefaultPolicy()
	tests := []struct {
		tier      Tier
		maxModel  string
		wantEmpty bool
	}{
		{TierBlocked, "", true},
		{TierCritical, "haiku", false},
		{TierConserve, "sonnet", false},
		{TierSteady, "opus", false},
		{TierExpand, "opus", false},
	}
	for _, tt := range tests {
		t.Run(tt.tier.String(), func(t *testing.T) {
			cands := Candidates(planFor(tt.tier, p), p)
			if tt.wantEmpty {
				if len(cands) != 0 {
					t.Fatalf("blocked tier offered %d candidates", len(cands))
				}
				return
			}
			if len(cands) == 0 {
				t.Fatal("no candidates offered")
			}
			top := cands[len(cands)-1]
			if top.Model != tt.maxModel {
				t.Errorf("most expensive candidate = %s, want model %s", top, tt.maxModel)
			}
			// Cost order must be ascending, since Recommend relies on it.
			for i := 1; i < len(cands); i++ {
				if cands[i].ContextBudget < cands[i-1].ContextBudget {
					t.Errorf("candidate %d has a smaller context than %d — the list must ascend in cost", i, i-1)
				}
			}
			// And nothing may exceed the pacing ceiling.
			plan := planFor(tt.tier, p)
			for _, c := range cands {
				if c.ContextBudget > plan.MaxContextTokens {
					t.Errorf("%s exceeds the tier's context ceiling of %d", c, plan.MaxContextTokens)
				}
				if c.Subagents > plan.SubagentFanout {
					t.Errorf("%s exceeds the tier's fan-out ceiling of %d", c, plan.SubagentFanout)
				}
			}
		})
	}
}

func TestPlanApplyNeverExceedsCeiling(t *testing.T) {
	p := DefaultPolicy()
	plan := planFor(TierConserve, p)
	// A recommendation that asks for more than the tier allows must be clamped.
	greedy := Recommendation{Config: Config{Model: "opus", Effort: "high", ContextBudget: 500_000, Subagents: 99}}
	out := plan.Apply(greedy)
	if out.MaxContextTokens > plan.MaxContextTokens {
		t.Errorf("context %d exceeds the ceiling %d", out.MaxContextTokens, plan.MaxContextTokens)
	}
	if out.SubagentFanout > plan.SubagentFanout {
		t.Errorf("fan-out %d exceeds the ceiling %d", out.SubagentFanout, plan.SubagentFanout)
	}

	// A blocked plan cannot be talked into running.
	blocked := planFor(TierBlocked, p)
	if blocked.Apply(greedy).Allow {
		t.Error("Apply must not re-enable a blocked plan")
	}
}

func TestLedgerRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "efficiency.json")
	l := NewLedger(LedgerOptions{Path: path, MinSamples: 2})
	record(l, "review", cheap(), 6, 5, 8_000)
	l.Record(Outcome{Role: "review", Config: cheap(), Success: true, Tokens: 1_000_000, QuotaPct: 2, At: at("12:00")})
	if err := l.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	l2 := NewLedger(LedgerOptions{Path: path, MinSamples: 2})
	s, ok := l2.Stat("review", cheap())
	if !ok {
		t.Fatal("stat did not survive the round trip")
	}
	if s.Runs != 7 || s.Successes != 6 {
		t.Errorf("runs/successes = %d/%d, want 7/6", s.Runs, s.Successes)
	}
	// The tokens->quota calibration must survive too, or every restart loses the
	// only way to report cost in the unit that actually matters.
	if !strings.Contains(l2.Report(), "of a window") {
		t.Errorf("calibration lost across reload:\n%s", l2.Report())
	}
}

func TestLedgerMissingFileIsNotAnError(t *testing.T) {
	l := NewLedger(LedgerOptions{Path: filepath.Join(t.TempDir(), "absent.json")})
	if err := l.Load(); err != nil {
		t.Errorf("loading an absent ledger should be fine, got %v", err)
	}
	if !strings.Contains(l.Report(), "nothing recorded") {
		t.Errorf("report = %q", l.Report())
	}
}

func TestUnmeasuredQuotaDoesNotDiluteTheAverage(t *testing.T) {
	l := NewLedger(LedgerOptions{})
	l.Record(Outcome{Role: "r", Config: cheap(), Success: true, Tokens: 1000, QuotaPct: 4, At: at("12:00")})
	l.Record(Outcome{Role: "r", Config: cheap(), Success: true, Tokens: 1000, At: at("12:00")}) // not measured
	s, _ := l.Stat("r", cheap())
	if got := s.AvgQuotaPct(); got != 4 {
		t.Errorf("avg quota = %.2f, want 4 — an unmeasured run is not a zero-cost run", got)
	}
}

func TestLedgerIsConcurrencySafe(t *testing.T) {
	l := NewLedger(LedgerOptions{})
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			for j := 0; j < 200; j++ {
				l.Record(Outcome{Role: "r", Config: cheap(), Success: true, Tokens: 10, At: at("12:00")})
				l.Recommend("r", []Config{cheap(), heavy()})
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("timed out")
		}
	}
	s, _ := l.Stat("r", cheap())
	if s.Runs != 1600 {
		t.Errorf("runs = %d, want 1600", s.Runs)
	}
}
