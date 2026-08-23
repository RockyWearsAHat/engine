package quota

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestLedger builds a ledger with a known token→percent calibration so yield
// numbers in these tests are deterministic. 1 percentage point per million
// tokens keeps the arithmetic legible.
func newTestLedger(t *testing.T) *Ledger {
	t.Helper()
	l := NewLedger(LedgerOptions{MinSamples: 1})
	l.SetClock(func() time.Time { return at("12:00") })
	l.pctPerMTok = 1.0
	l.calibRuns = 1
	return l
}

func run(l *Ledger, project, role string, c Config, tokens int64) {
	l.Record(Outcome{Role: role, Config: c, Success: true, Tokens: tokens, ProjectID: project, At: at("12:00")})
}

// Aliases for the shared fixtures in efficiency_test.go, as values rather than
// constructors so the calls below read as configurations rather than calls.
var (
	thrifty = cheap()
	rich    = heavy()
)

// The headline claim, made concrete: a lot of good work done cheaply must score
// enormously better than a little good work done expensively. This is the number
// SARA is steering on, so if it does not separate these two cases by a wide
// margin, nothing else in the package matters.
func TestYieldRewardsManyCheapResultsOverFewExpensiveOnes(t *testing.T) {
	frugal := newTestLedger(t)
	// 100 praised projects, 30k tokens each — 3 percentage points in total.
	for i := 0; i < 100; i++ {
		id := "proj-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		run(frugal, id, "implement", thrifty, 30_000)
		frugal.RateProject(id, SatisfactionPraised, "")
	}

	lavish := newTestLedger(t)
	// 2 praised projects, 50M tokens each — 100 percentage points in total.
	for _, id := range []string{"big-1", "big-2"} {
		run(lavish, id, "implement", rich, 50_000_000)
		lavish.RateProject(id, SatisfactionPraised, "")
	}

	fs, ls := frugal.Scoreboard(), lavish.Scoreboard()
	if !fs.Known || !ls.Known {
		t.Fatal("both scoreboards should be known")
	}
	if fs.Yield <= ls.Yield {
		t.Fatalf("frugal yield %.2f must beat lavish yield %.2f", fs.Yield, ls.Yield)
	}
	// Both delivered praised work; the only difference is the quota spent. The
	// separation should be dramatic, not marginal, or the score will not actually
	// steer anything.
	if ratio := fs.Yield / ls.Yield; ratio < 100 {
		t.Errorf("frugal is only %.0fx better; expected a very large margin", ratio)
	}
	if fs.QuotaPerGood >= ls.QuotaPerGood {
		t.Errorf("quota per good result: frugal %.4f should be far below lavish %.4f", fs.QuotaPerGood, ls.QuotaPerGood)
	}
}

// The degenerate solution to any efficiency objective: deliver nothing, or
// deliver slop, and spend almost nothing doing it. If cheapness can buy its way
// past quality then the score optimises itself into garbage, so quality is a
// gate and this test is what holds the gate shut.
func TestCheapSlopNeverBeatsGoodWork(t *testing.T) {
	l := newTestLedger(t)

	// A thrifty configuration that always needs rework.
	for i := 0; i < 6; i++ {
		id := "slop-" + string(rune('a'+i))
		run(l, id, "implement", thrifty, 10_000)
		l.RateProject(id, SatisfactionRework, "")
	}
	// A richer one the user is consistently happy with, costing 20x more.
	for i := 0; i < 6; i++ {
		id := "good-" + string(rune('a'+i))
		run(l, id, "implement", rich, 200_000)
		l.RateProject(id, SatisfactionPraised, "")
	}

	// On raw yield alone the slop wins — that is precisely the trap.
	slopStat, _ := l.Stat("implement", thrifty)
	goodStat, _ := l.Stat("implement", rich)
	slopCost, _ := l.statQuotaCostLocked(slopStat)
	goodCost, _ := l.statQuotaCostLocked(goodStat)
	slopYield := slopStat.Yield(slopCost)
	goodYield := goodStat.Yield(goodCost)
	if slopYield <= goodYield {
		t.Fatalf("test is not exercising the trap: slop yield %.1f should out-rank good yield %.1f on the naive ratio",
			slopYield, goodYield)
	}

	// The gate must reject it anyway.
	rec := l.Recommend("implement", []Config{thrifty, rich})
	if rec.Config.Key() != rich.Key() {
		t.Errorf("recommended %s; the thrifty-but-reworked config must not win on price", rec.Config)
	}
}

// A configuration that produces rejected work must rank BELOW one that has done
// nothing, not merely equal to it. Zero-valued rejection would make burning
// quota for nothing a neutral act.
func TestRejectedWorkScoresNegative(t *testing.T) {
	if SatisfactionRejected.Value() >= 0 {
		t.Fatalf("rejected value = %v, must be negative", SatisfactionRejected.Value())
	}
	l := newTestLedger(t)
	for i := 0; i < 4; i++ {
		id := "bad-" + string(rune('a'+i))
		run(l, id, "review", thrifty, 50_000)
		l.RateProject(id, SatisfactionRejected, "")
	}
	s, ok := l.Stat("review", thrifty)
	if !ok {
		t.Fatal("no stat recorded")
	}
	if s.Value >= 0 {
		t.Errorf("accumulated value = %v, want negative", s.Value)
	}
	if sb := l.Scoreboard(); sb.Yield >= 0 {
		t.Errorf("scoreboard yield = %v, want negative for consistently rejected work", sb.Yield)
	}
}

// Praise has to be worth measurably more than mere acceptance, or the engine is
// indifferent between delighting the user and just satisfying them — and it will
// settle on whichever is cheaper, which is always the latter.
func TestPraiseOutranksAcceptance(t *testing.T) {
	if SatisfactionPraised.Value() <= SatisfactionAccepted.Value() {
		t.Fatal("praised must be worth more than accepted")
	}
	praised, accepted := newTestLedger(t), newTestLedger(t)
	for i := 0; i < 5; i++ {
		id := "p-" + string(rune('a'+i))
		run(praised, id, "implement", thrifty, 100_000)
		praised.RateProject(id, SatisfactionPraised, "")

		id = "a-" + string(rune('a'+i))
		run(accepted, id, "implement", thrifty, 100_000)
		accepted.RateProject(id, SatisfactionAccepted, "")
	}
	if praised.Scoreboard().Yield <= accepted.Scoreboard().Yield {
		t.Error("identical spend, better reception — praise must score higher")
	}
}

// Ratings arrive after the work, and a project is usually several runs across
// several configurations. Credit has to land on the configuration that actually
// did the spending, or an expensive run hides behind thrifty ones.
func TestRatingIsAttributedByShareOfSpend(t *testing.T) {
	l := newTestLedger(t)
	run(l, "proj", "implement", thrifty, 100_000) // 10% of the spend
	run(l, "proj", "implement", rich, 900_000)    // 90% of the spend
	if !l.RateProject("proj", SatisfactionPraised, "nice") {
		t.Fatal("rating did not match the project")
	}

	cs, _ := l.Stat("implement", thrifty)
	rs, _ := l.Stat("implement", rich)
	total := cs.Value + rs.Value
	if total <= 0 {
		t.Fatalf("no value attributed: %v", total)
	}
	if got := rs.Value / total; got < 0.85 || got > 0.95 {
		t.Errorf("expensive run got %.0f%% of the credit, want ~90%%", got*100)
	}
	if got := cs.RatedShare; got < 0.05 || got > 0.15 {
		t.Errorf("thrifty run rated share = %.2f, want ~0.10", got)
	}
}

// Feedback is sparse and may never arrive at all. An engine that stalls or
// degrades without it would be worse than the one that came before, so the
// unrated path must keep working on mechanical evidence alone.
func TestUnratedWorkFallsBackToCompletion(t *testing.T) {
	l := NewLedger(LedgerOptions{MinSamples: 2})
	l.SetClock(func() time.Time { return at("12:00") })
	for i := 0; i < 3; i++ {
		l.Record(Outcome{Role: "implement", Config: thrifty, Success: true, Tokens: 10_000, At: at("12:00")})
		l.Record(Outcome{Role: "implement", Config: rich, Success: true, Tokens: 500_000, At: at("12:00")})
	}
	rec := l.Recommend("implement", []Config{thrifty, rich})
	if rec.Config.Key() != thrifty.Key() {
		t.Errorf("recommended %s, want the thrifty config", rec.Config)
	}
	if sb := l.Scoreboard(); sb.Known {
		t.Error("with no ratings the score must be UNKNOWN, not a number")
	}
}

// Unrated work must not drag a configuration down. If it did, every config would
// decay toward zero simply because feedback is sparse, and the ledger would end
// up recommending whichever config happened to be rated most recently.
func TestUnratedRunsDoNotCountAgainstAConfig(t *testing.T) {
	l := newTestLedger(t)
	for i := 0; i < 3; i++ {
		id := "rated-" + string(rune('a'+i))
		run(l, id, "implement", thrifty, 50_000)
		l.RateProject(id, SatisfactionPraised, "")
	}
	before, _ := l.Stat("implement", thrifty)
	goodBefore := before.GoodRate()

	// A pile of unrated runs on the same config.
	for i := 0; i < 20; i++ {
		run(l, "unrated", "implement", thrifty, 50_000)
	}
	after, _ := l.Stat("implement", thrifty)
	if after.GoodRate() != goodBefore {
		t.Errorf("good rate moved from %.2f to %.2f on unrated runs alone", goodBefore, after.GoodRate())
	}
}

// A single lucky thrifty run must not win on yield. Yield is a ratio, so one
// praised project costing almost nothing posts a spectacular number from no real
// evidence.
func TestOneFlukeDoesNotWinOnYield(t *testing.T) {
	l := newTestLedger(t)
	// Rich config: solidly evidenced and well liked.
	for i := 0; i < 6; i++ {
		id := "solid-" + string(rune('a'+i))
		run(l, id, "implement", rich, 300_000)
		l.RateProject(id, SatisfactionPraised, "")
	}
	// Cheap config: one tiny praised run.
	run(l, "fluke", "implement", thrifty, 1_000)
	l.RateProject("fluke", SatisfactionPraised, "")

	rec := l.Recommend("implement", []Config{thrifty, rich})
	if rec.Config.Key() != rich.Key() {
		t.Errorf("recommended %s on the strength of a single run; want the evidenced config", rec.Config)
	}
}

// A project is rated once per delivery, not once ever — SARA ships from the same
// path repeatedly.
func TestProjectCanBeRatedAgainAfterMoreWork(t *testing.T) {
	l := newTestLedger(t)
	run(l, "/repo", "implement", thrifty, 50_000)
	if !l.RateProject("/repo", SatisfactionAccepted, "") {
		t.Fatal("first rating did not match")
	}
	if l.RateProject("/repo", SatisfactionPraised, "") {
		t.Error("a second rating with no new work in between should not match")
	}
	run(l, "/repo", "implement", thrifty, 50_000)
	if !l.RateProject("/repo", SatisfactionPraised, "") {
		t.Error("after new work, the project must be rateable again")
	}
	if sb := l.Scoreboard(); sb.Projects != 2 {
		t.Errorf("rated projects = %d, want 2", sb.Projects)
	}
}

// The trend is what makes "continuously improving" checkable rather than a
// claim. Same quality, progressively less quota, must read as improvement.
func TestTrendRisesWhenTheSameWorkGetsCheaper(t *testing.T) {
	l := newTestLedger(t)
	// Early: expensive praised work.
	for i := 0; i < 5; i++ {
		id := "early-" + string(rune('a'+i))
		run(l, id, "implement", rich, 1_000_000)
		l.RateProject(id, SatisfactionPraised, "")
	}
	// Later: same praise, a tenth of the spend.
	for i := 0; i < 5; i++ {
		id := "late-" + string(rune('a'+i))
		run(l, id, "implement", thrifty, 100_000)
		l.RateProject(id, SatisfactionPraised, "")
	}
	sb := l.Scoreboard()
	if sb.Trend <= 1 {
		t.Errorf("trend = %.2f, want > 1 for the same results at a tenth of the cost", sb.Trend)
	}
	if !sb.Improving() {
		t.Error("Improving() should be true: yield up, quality held")
	}
}

// Getting cheaper by getting worse is the one pattern that must never read as
// improvement, because it is the failure this whole design is guarding against.
func TestGettingCheaperByGettingWorseIsNotImprovement(t *testing.T) {
	l := newTestLedger(t)
	for i := 0; i < 5; i++ {
		id := "early-" + string(rune('a'+i))
		run(l, id, "implement", rich, 500_000)
		l.RateProject(id, SatisfactionPraised, "")
	}
	for i := 0; i < 8; i++ {
		id := "late-" + string(rune('a'+i))
		run(l, id, "implement", thrifty, 5_000)
		l.RateProject(id, SatisfactionRejected, "")
	}
	sb := l.Scoreboard()
	if sb.Improving() {
		t.Errorf("Improving() must be false when quality collapsed (goodRate %.2f)", sb.GoodRate)
	}
}

// A run too small to move any measurable quota must not report an infinite
// yield, or "do many trivial things" becomes the winning strategy.
func TestTinyRunsDoNotProduceInfiniteYield(t *testing.T) {
	l := newTestLedger(t)
	run(l, "tiny", "implement", thrifty, 1)
	l.RateProject("tiny", SatisfactionPraised, "")
	sb := l.Scoreboard()
	if !isFinitePositive(sb.Yield) {
		t.Fatalf("yield = %v, want a finite positive number", sb.Yield)
	}
	if sb.Yield > SatisfactionPraised.Value()/minQuotaCost {
		t.Errorf("yield %v exceeds the floor-implied maximum", sb.Yield)
	}
}

// Found live: a configuration with no measured spend divided by the yield floor
// and posted a huge number. Because attributed value grows with share of spend,
// that ranked the MOST expensive configuration first — a clean inversion of the
// objective. Unmeasured cost must read as unknown, never as cheap.
func TestUnmeasuredSpendIsNotTreatedAsCheap(t *testing.T) {
	l := newTestLedger(t)
	// Ratings arrive for configs that have no run history in this ledger, so the
	// stats exist with value but no tokens.
	l.RateProject("nothing", SatisfactionPraised, "") // no parts: no match
	l.mu.Lock()
	l.stats[statKey("implement", rich)] = &Stat{
		Role: "implement", Config: rich, Value: 9.0, RatedShare: 5, Praised: 5,
	}
	l.stats[statKey("implement", thrifty)] = &Stat{
		Role: "implement", Config: thrifty, Value: 1.0, RatedShare: 5, Praised: 5,
	}
	l.mu.Unlock()

	for _, c := range []Config{rich, thrifty} {
		s, _ := l.Stat("implement", c)
		if _, known := l.statQuotaCostLocked(s); known {
			t.Fatalf("%s reports a measured cost it does not have", c)
		}
	}
	// Neither is rankable, so yield must not pick the expensive one.
	rec := l.Recommend("implement", []Config{thrifty, rich})
	if rec.Config.Key() == rich.Key() && strings.Contains(rec.Reason, "yield") {
		t.Errorf("won on yield with no measured spend: %s", rec.Reason)
	}
	if got := l.ScoreReport(); !strings.Contains(got, "yield      ?") {
		t.Errorf("report should show unknown yield, got:\n%s", got)
	}
}

// Praise arrives from a person long after the work, and the engine restarts in
// between as a matter of course. If delivered-but-unrated work did not survive
// that, every restart would silently discard the scarcest input the score has.
func TestPendingWorkStaysRateableAcrossARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")

	l := NewLedger(LedgerOptions{Path: path, MinSamples: 1})
	l.SetClock(func() time.Time { return at("12:00") })
	l.pctPerMTok, l.calibRuns = 1.0, 1
	run(l, "/repo", "implement", thrifty, 100_000)
	run(l, "/repo", "implement", rich, 900_000)
	if err := l.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// The engine bounces; a new ledger reads the same file.
	l2 := NewLedger(LedgerOptions{Path: path, MinSamples: 1})
	l2.SetClock(func() time.Time { return at("13:00") })
	if !l2.RateProject("/repo", SatisfactionPraised, "shipped yesterday") {
		t.Fatal("the work was unrateable after a restart — the feedback would have been lost")
	}

	// And it must still attribute by share of spend, not evenly, or the restart
	// would have quietly degraded the accounting instead of dropping it.
	cs, _ := l2.Stat("implement", thrifty)
	rs, _ := l2.Stat("implement", rich)
	if got := rs.Value / (cs.Value + rs.Value); got < 0.85 || got > 0.95 {
		t.Errorf("expensive run got %.0f%% of the credit after reload, want ~90%%", got*100)
	}
	if sb := l2.Scoreboard(); !sb.Known || sb.Praised != 1 {
		t.Errorf("scoreboard = %+v, want one praised project", sb)
	}
}

// Once rated, a bucket is closed and must not linger on disk — otherwise a
// reload would resurrect it and the same delivery could be rated twice.
func TestRatedWorkIsNotRestoredAsPending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	l := NewLedger(LedgerOptions{Path: path, MinSamples: 1})
	l.SetClock(func() time.Time { return at("12:00") })
	l.pctPerMTok, l.calibRuns = 1.0, 1
	run(l, "/repo", "implement", thrifty, 100_000)
	if !l.RateProject("/repo", SatisfactionPraised, "") {
		t.Fatal("rating did not match")
	}

	l2 := NewLedger(LedgerOptions{Path: path, MinSamples: 1})
	if l2.RateProject("/repo", SatisfactionPraised, "") {
		t.Error("already-rated work came back after a reload; it could be rated twice")
	}
	if sb := l2.Scoreboard(); sb.Praised != 1 {
		t.Errorf("praised = %d, want 1", sb.Praised)
	}
}

// The score has to survive a restart, or "improving over time" resets every time
// the engine is bounced.
func TestScoreboardPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	l := NewLedger(LedgerOptions{Path: path, MinSamples: 1})
	l.SetClock(func() time.Time { return at("12:00") })
	l.pctPerMTok, l.calibRuns = 1.0, 1
	for i := 0; i < 3; i++ {
		id := "p-" + string(rune('a'+i))
		run(l, id, "implement", thrifty, 100_000)
		l.RateProject(id, SatisfactionPraised, "great work")
	}
	if err := l.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	before := l.Scoreboard()

	l2 := NewLedger(LedgerOptions{Path: path, MinSamples: 1})
	after := l2.Scoreboard()
	if !after.Known {
		t.Fatal("reloaded ledger reports no score")
	}
	if after.Projects != before.Projects || after.Praised != before.Praised {
		t.Errorf("counts did not survive: before %+v after %+v", before, after)
	}
	if after.Yield != before.Yield {
		t.Errorf("yield %v != %v after reload", after.Yield, before.Yield)
	}
}
