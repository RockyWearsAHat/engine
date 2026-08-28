package quota

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// The efficiency ledger is the part that makes "use less over time" real.
//
// THE PROBLEM WITH THE OBVIOUS APPROACH
//
// The governor picks a configuration from a tier, and the tier comes from how
// much quota is left. That is pacing, and pacing alone will happily spend 3× the
// necessary quota on a task all week, because it has no idea what the task
// actually needed. Nothing in a percentage tells you that "review a diff" gets
// the same result from a 40k-context Sonnet run as from a 180k-context Opus run.
//
// So the ledger measures the one thing pacing cannot see: for each KIND of work,
// what did each configuration cost, and did it work? Then the cheapest
// configuration that still succeeds becomes the default for that kind of work,
// and the expensive one is only reached for when the cheap one has been failing.
// That is the whole of "intelligently scale over time" — it scales DOWN by
// default, and the evidence for scaling down is collected as a side effect of
// doing the work.
//
// WHAT COST IS MEASURED IN
//
// The currency we care about is window percentage points, but /usage reports
// whole percents, so a single run usually moves it by zero. Tokens are the
// fine-grained proxy: always available from the result event, strictly monotone
// in real quota spend. So the ledger accounts in tokens and keeps a calibration
// ratio (percent per million tokens) learned from the occasions when a quota
// delta IS observable. Reporting is in whichever unit the caller wants; the
// ranking is the same either way, because the map between them is monotone.

// Config is one dispatchable configuration for a unit of work.
type Config struct {
	// Model is the model tier, e.g. "opus", "sonnet", "haiku".
	Model string `json:"model"`
	// Effort is "low", "medium" or "high".
	Effort string `json:"effort"`
	// ContextBudget is the soft context ceiling in tokens.
	ContextBudget int `json:"contextBudget"`
	// Subagents is the fan-out width.
	Subagents int `json:"subagents"`
}

// Key is the ledger key for a config.
func (c Config) Key() string {
	return fmt.Sprintf("%s|%s|%d|%d", c.Model, c.Effort, c.ContextBudget, c.Subagents)
}

func (c Config) String() string {
	return fmt.Sprintf("%s/%s ctx=%dk fanout=%d", c.Model, c.Effort, c.ContextBudget/1000, c.Subagents)
}

// Outcome is what one run of a config on a kind of work actually cost and
// achieved.
type Outcome struct {
	// Role is the kind of work, e.g. "implement", "review", "plan". The ledger
	// learns per role because the right configuration differs sharply between
	// them — that is the entire source of the savings.
	Role string `json:"role"`
	// Config is what was dispatched.
	Config Config `json:"config"`
	// Success is whether the run completed mechanically — exited cleanly and
	// produced output. This is the ENGINE's judgement of itself and it is a weak
	// signal: it cannot tell work that landed from work that was quietly thrown
	// away. It is kept because it is always available and it correctly catches
	// hard failures, but it is not the objective. Satisfaction is, and that
	// arrives later via Ledger.RateProject.
	Success bool `json:"success"`
	// ProjectID names the unit of delivered work this run contributed to, so a
	// rating arriving later can be attributed back to the configuration that
	// earned it. Empty means the run is untracked for scoring purposes.
	ProjectID string `json:"projectId,omitempty"`
	// Tokens is total tokens billed for the run (input + output + cache).
	Tokens int64 `json:"tokens"`
	// USD is list-price cost of the run (CostUSD from usage counters). Not
	// what the subscription bills — what the same tokens would cost on API.
	USD float64 `json:"usd,omitempty"`
	// SubagentsSpawned is how many subagents the worker itself delegated to via
	// the Task tool during this run, when a fanout budget was advisory (not
	// enforced at 0). This is the observable signal for whether dynamic,
	// self-assembling team delegation actually happened on this task, as
	// opposed to a single agent doing everything itself.
	SubagentsSpawned int `json:"subagentsSpawned,omitempty"`
	// QuotaPct is the observed movement in the binding window, when one was
	// measurable. Zero means "not measured", not "free".
	QuotaPct float64 `json:"quotaPct,omitempty"`
	// Duration is wall time.
	Duration time.Duration `json:"-"`
	// At is when the run finished.
	At time.Time `json:"at"`
}

// Stat is the accumulated record for one (role, config) pair.
type Stat struct {
	Role   string `json:"role"`
	Config Config `json:"config"`

	Runs      int   `json:"runs"`
	Successes int   `json:"successes"`
	Tokens    int64 `json:"tokens"`
	// USD is cumulative list-price cost across runs.
	USD float64 `json:"usd"`
	// SubagentsSpawned is the running total of Outcome.SubagentsSpawned across
	// all runs of this configuration — how much real dynamic team delegation
	// this configuration has actually produced, cumulative.
	SubagentsSpawned int `json:"subagentsSpawned,omitempty"`
	// QuotaPct and QuotaRuns track only the runs where quota movement was
	// actually observed, kept separate so an unmeasured run does not dilute the
	// average toward zero.
	QuotaPct  float64       `json:"quotaPct"`
	QuotaRuns int           `json:"quotaRuns"`
	Duration  time.Duration `json:"durationNs"`
	LastRun   time.Time     `json:"lastRun"`

	// Value is accumulated user-judged worth, from ratings attributed back to
	// this configuration by its share of each project's spend. It is the
	// numerator of yield and it can go negative — a configuration that reliably
	// produces rejected work is worse than one that produces nothing.
	Value float64 `json:"value"`
	// RatedShare is how much rated work this configuration is responsible for,
	// in project-shares rather than whole projects. Fractional because a project
	// is usually many runs across several configurations.
	RatedShare float64 `json:"ratedShare"`
	// Praised/Accepted/Rework/Rejected are the same shares split by rating.
	Praised  float64 `json:"praised"`
	Accepted float64 `json:"accepted"`
	Rework   float64 `json:"rework"`
	Rejected float64 `json:"rejected"`
}

// GoodRate is the share of this configuration's rated work that the user was
// happy with. This is the quality gate — see Ledger.Recommend.
//
// Returns 0 when nothing has been rated, which callers must read as "no
// evidence" rather than "bad". The distinction matters: treating unrated as bad
// would condemn every configuration the moment feedback got sparse.
func (s Stat) GoodRate() float64 {
	if s.RatedShare <= 0 {
		return 0
	}
	return (s.Praised + s.Accepted) / s.RatedShare
}

// Yield is value per quota percentage point for this configuration, given the
// ledger's estimate of what it cost.
func (s Stat) Yield(quotaCost float64) float64 {
	return yieldOf(s.Value, quotaCost)
}

// SuccessRate is successes/runs, 0 when unsampled.
func (s Stat) SuccessRate() float64 {
	if s.Runs == 0 {
		return 0
	}
	return float64(s.Successes) / float64(s.Runs)
}

// TokensPerSuccess is the real efficiency number: total spend divided by
// SUCCESSFUL runs, not by all runs.
//
// Dividing by all runs is the natural thing to write and it is wrong in a way
// that inverts the ranking. A config that is cheap per attempt but fails half
// the time costs two attempts per result — it is twice as expensive as its
// per-run number suggests, and the failures are what make it so. Charging the
// wasted attempts to the successes is what stops the ledger recommending a cheap
// configuration that never finishes anything.
func (s Stat) TokensPerSuccess() float64 {
	if s.Successes == 0 {
		if s.Runs == 0 {
			return math.Inf(1)
		}
		// Ran and never succeeded: infinitely inefficient, not free.
		return math.Inf(1)
	}
	return float64(s.Tokens) / float64(s.Successes)
}

// AvgQuotaPct is the mean observed window movement per run, over measured runs.
func (s Stat) AvgQuotaPct() float64 {
	if s.QuotaRuns == 0 {
		return 0
	}
	return s.QuotaPct / float64(s.QuotaRuns)
}

// Ledger records outcomes and recommends configurations.
type Ledger struct {
	mu    sync.RWMutex
	stats map[string]*Stat
	// pctPerMTok is the learned conversion from tokens to window percentage,
	// used only for reporting.
	pctPerMTok  float64
	calibRuns   int
	path        string
	minSamples  int
	successBar  float64
	dirty       bool
	lastSaved   time.Time
	saveEvery   time.Duration
	nowOverride func() time.Time

	// Scoring state. projects holds the not-yet-rated backlog so late feedback
	// can be attributed back; the rest is the running scoreboard.
	projects      map[string]*project
	projectOrder  []string
	totalValue    float64
	totalQuota    float64
	ratedProjects int
	praised       int
	accepted      int
	rework        int
	rejected      int
	history       []yieldPoint
	notes         []Rating

	// goodBar is the satisfaction gate: the share of rated work a configuration
	// must have landed well before its cheapness is allowed to count in its
	// favour at all.
	goodBar float64
}

// LedgerOptions tunes the learner.
type LedgerOptions struct {
	// Path is where the ledger persists. Empty = memory only.
	Path string
	// MinSamples is how many runs a config needs before its success rate is
	// trusted. Below this, the ledger explores rather than concludes.
	MinSamples int
	// SuccessBar is the mechanical completion rate a cheaper config must clear to
	// be preferred over a more expensive one. This is the floor, not the goal.
	SuccessBar float64
	// GoodBar is the share of RATED work a configuration must have landed well
	// before yield is allowed to select it. Quality is a gate rather than a term
	// in the ratio, because a cheap configuration that makes the user unhappy is
	// not a bargain at any price.
	GoodBar float64
}

// NewLedger builds a ledger, loading prior state from Path when present.
func NewLedger(opts LedgerOptions) *Ledger {
	if opts.MinSamples <= 0 {
		opts.MinSamples = 5
	}
	if opts.SuccessBar <= 0 {
		opts.SuccessBar = 0.8
	}
	if opts.GoodBar <= 0 {
		// Two thirds. Deliberately not higher: the bar has to be clearable by a
		// cheap configuration often enough that frugality can actually win, or
		// the gate silently becomes "always use the expensive one".
		opts.GoodBar = 0.67
	}
	l := &Ledger{
		stats:      map[string]*Stat{},
		projects:   map[string]*project{},
		path:       opts.Path,
		minSamples: opts.MinSamples,
		successBar: opts.SuccessBar,
		goodBar:    opts.GoodBar,
		saveEvery:  30 * time.Second,
		pctPerMTok: 0,
	}
	if l.path != "" {
		_ = l.Load()
	}
	return l
}

func (l *Ledger) now() time.Time {
	if l.nowOverride != nil {
		return l.nowOverride()
	}
	return time.Now()
}

// SetClock overrides the clock, for tests.
func (l *Ledger) SetClock(fn func() time.Time) { l.nowOverride = fn }

func statKey(role string, c Config) string {
	return strings.ToLower(strings.TrimSpace(role)) + "\x00" + c.Key()
}

// Record folds one outcome into the ledger.
func (l *Ledger) Record(o Outcome) {
	if o.At.IsZero() {
		o.At = l.now()
	}
	k := statKey(o.Role, o.Config)

	l.mu.Lock()
	s, ok := l.stats[k]
	if !ok {
		s = &Stat{Role: strings.ToLower(strings.TrimSpace(o.Role)), Config: o.Config}
		l.stats[k] = s
	}
	s.Runs++
	if o.Success {
		s.Successes++
	}
	s.Tokens += o.Tokens
	s.USD += o.USD
	s.SubagentsSpawned += o.SubagentsSpawned
	s.Duration += o.Duration
	s.LastRun = o.At
	if o.QuotaPct > 0 {
		s.QuotaPct += o.QuotaPct
		s.QuotaRuns++
		if o.Tokens > 0 {
			// Running mean of percent-per-million-tokens.
			ratio := o.QuotaPct / (float64(o.Tokens) / 1e6)
			l.calibRuns++
			l.pctPerMTok += (ratio - l.pctPerMTok) / float64(l.calibRuns)
		}
	}
	// Track the run against its project so a rating arriving later can find the
	// configuration that earned it.
	l.recordProjectPart(o, o.QuotaPct)

	l.dirty = true
	shouldSave := l.path != "" && l.now().Sub(l.lastSaved) > l.saveEvery
	l.mu.Unlock()

	if shouldSave {
		_ = l.Save()
	}
}

// Stat returns the record for a (role, config), if any.
func (l *Ledger) Stat(role string, c Config) (Stat, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	s, ok := l.stats[statKey(role, c)]
	if !ok {
		return Stat{}, false
	}
	return *s, true
}

// Recommendation is the ledger's choice plus why.
type Recommendation struct {
	Config Config `json:"config"`
	// Exploring is true when the choice was made to gather evidence rather than
	// to exploit what is already known.
	Exploring bool `json:"exploring"`
	// Confident is true when the chosen config has cleared MinSamples.
	Confident bool   `json:"confident"`
	Reason    string `json:"reason"`
	// Savings estimates the tokens saved per success versus the most expensive
	// candidate, when both are sampled.
	Savings float64 `json:"savings,omitempty"`
	// Yield is the chosen configuration's results-per-percent, when it was
	// selected on yield rather than on the mechanical fallback.
	Yield float64 `json:"yield,omitempty"`
	// GoodRate is the share of its rated work the user was happy with.
	GoodRate float64 `json:"goodRate,omitempty"`
}

// Recommend picks the configuration for a role with the best YIELD — the most
// user-approved result per percentage point of window — among those that clear
// the quality gate.
//
// Candidates must be supplied in ascending cost order — cheapest first. The
// ledger does not know the price list; the caller does.
//
// The rule, in order:
//
//  1. Any candidate with too few samples is tried, cheapest-first. Exploration
//     is biased to the CHEAP end deliberately: the cost of learning that a cheap
//     config fails is one cheap run, while learning that an expensive config
//     succeeds teaches nothing we wanted to know.
//  2. Among candidates whose RATED work clears the quality gate, take the
//     highest yield. This is the step that implements the objective: between two
//     configurations the user is equally happy with, the one that got there on a
//     tenth of the quota is ten times better, and it wins by exactly that much.
//  3. If ratings exist but nothing clears the gate, take the highest quality
//     regardless of cost. When the user is not happy, cheapness is not the
//     problem to solve.
//  4. If no candidate has been rated at all, fall back to mechanical success:
//     the cheapest configuration that completes. Feedback may be sparse or may
//     never arrive, and an engine that stalls without it would be worse than the
//     one that came before.
func (l *Ledger) Recommend(role string, candidates []Config) Recommendation {
	if len(candidates) == 0 {
		return Recommendation{Reason: "no candidate configurations supplied"}
	}
	l.mu.RLock()
	defer l.mu.RUnlock()

	get := func(c Config) Stat {
		if s, ok := l.stats[statKey(role, c)]; ok {
			return *s
		}
		return Stat{Role: role, Config: c}
	}

	// 1. Explore the cheapest under-sampled candidate.
	for _, c := range candidates {
		s := get(c)
		if s.Runs < l.minSamples {
			return Recommendation{
				Config:    c,
				Exploring: true,
				Reason: fmt.Sprintf("%s has only %d/%d samples for %q — trying it to find out whether the cheaper option suffices",
					c, s.Runs, l.minSamples, role),
			}
		}
	}

	// 2. Best yield among candidates the user is actually happy with.
	//
	// minRatedShare guards against deciding on a single fluke: one praised
	// project is not evidence that a configuration is good, and yield is a ratio,
	// so a lucky cheap run can post a spectacular number from nothing.
	const minRatedShare = 2.0
	var bestC Config
	var bestStat Stat
	bestYield := math.Inf(-1)
	anyRated := false
	for _, c := range candidates {
		s := get(c)
		if s.RatedShare <= 0 {
			continue
		}
		anyRated = true
		if s.RatedShare < minRatedShare || s.GoodRate() < l.goodBar {
			continue
		}
		cost, costKnown := l.statQuotaCostLocked(s)
		if !costKnown {
			// No measured spend means no denominator. Ranking it anyway would
			// float it to the top on the floor value alone.
			continue
		}
		y := s.Yield(cost)
		if y > bestYield {
			bestC, bestStat, bestYield = c, s, y
		}
	}
	if isFinitePositive(bestYield) {
		rec := Recommendation{
			Config:    bestC,
			Confident: true,
			Yield:     bestYield,
			GoodRate:  bestStat.GoodRate(),
			Reason: fmt.Sprintf("%s yields %.1f results per %% of window on %q at %.0f%% good — best return on quota among configurations the user is happy with",
				bestC, bestYield, role, bestStat.GoodRate()*100),
		}
		// Say what choosing it saves versus the most expensive candidate, since
		// that comparison is the whole argument for the frugal choice.
		if worst := get(candidates[len(candidates)-1]); worst.RatedShare > 0 && worst.Config.Key() != bestC.Key() {
			if wc, ok := l.statQuotaCostLocked(worst); ok {
				if wy := worst.Yield(wc); isFinitePositive(wy) && bestYield > wy {
					rec.Reason += fmt.Sprintf(" (%.1fx the yield of %s)", bestYield/wy, worst.Config)
				}
			}
		}
		return rec
	}

	// 3. Rated, but nothing the user liked enough. Quality first.
	if anyRated {
		best := candidates[0]
		bs := get(best)
		for _, c := range candidates[1:] {
			s := get(c)
			if s.RatedShare > 0 && s.GoodRate() > bs.GoodRate() {
				best, bs = c, s
			}
		}
		return Recommendation{
			Config:    best,
			Confident: true,
			GoodRate:  bs.GoodRate(),
			Reason: fmt.Sprintf("no configuration clears the %.0f%% satisfaction gate on %q; %s is the best at %.0f%% — spending more is the right move when the results are not landing",
				l.goodBar*100, role, best, bs.GoodRate()*100),
		}
	}

	// 4. No ratings anywhere: fall back to mechanical completion, cheapest first.
	worst := get(candidates[len(candidates)-1])
	for _, c := range candidates {
		s := get(c)
		if s.SuccessRate() >= l.successBar {
			rec := Recommendation{
				Config:    c,
				Confident: true,
				Reason: fmt.Sprintf("%s completes %.0f%% of the time on %q at %s/success — cheapest configuration clearing the %.0f%% bar (no user ratings yet)",
					c, s.SuccessRate()*100, role, humanTokens(s.TokensPerSuccess()), l.successBar*100),
			}
			if worst.Successes > 0 && s.Successes > 0 {
				if d := worst.TokensPerSuccess() - s.TokensPerSuccess(); d > 0 {
					rec.Savings = d
					rec.Reason += fmt.Sprintf(" (saves %s/success versus %s)", humanTokens(d), worst.Config)
				}
			}
			return rec
		}
	}

	best := candidates[0]
	bs := get(best)
	for _, c := range candidates[1:] {
		s := get(c)
		if s.SuccessRate() > bs.SuccessRate() {
			best, bs = c, s
		}
	}
	return Recommendation{
		Config:    best,
		Confident: true,
		Reason: fmt.Sprintf("no configuration clears the %.0f%% completion bar on %q; %s is the best available at %.0f%%",
			l.successBar*100, role, best, bs.SuccessRate()*100),
	}
}

// Candidates builds the cost-ordered candidate list a tier permits.
//
// This is where the ledger and the governor meet: the governor's tier sets the
// CEILING (what we are allowed to spend), and the ledger picks from underneath
// it (what we actually need to spend). A tight tier simply truncates the list,
// so pacing and learning compose instead of fighting — the ledger can never
// recommend past the pacing limit, and pacing never forces spend the ledger
// knows is unnecessary.
func Candidates(p Plan, policy Policy) []Config {
	if !p.Allow {
		return nil
	}
	ctx := p.MaxContextTokens
	if ctx <= 0 {
		ctx = policy.MaxContextTokens
	}
	fan := p.SubagentFanout

	// Cheapest first.
	all := []Config{
		{Model: policy.CheapModel, Effort: "low", ContextBudget: minInt(ctx, 40_000), Subagents: 0},
		{Model: policy.MidModel, Effort: "low", ContextBudget: minInt(ctx, 60_000), Subagents: 0},
		{Model: policy.MidModel, Effort: "medium", ContextBudget: minInt(ctx, 100_000), Subagents: minInt(fan, 1)},
		{Model: policy.HeavyModel, Effort: "medium", ContextBudget: minInt(ctx, 120_000), Subagents: minInt(fan, 2)},
		{Model: policy.HeavyModel, Effort: "high", ContextBudget: ctx, Subagents: fan},
	}

	// Truncate at the tier's ceiling.
	ceiling := len(all)
	switch p.Tier {
	case TierCritical:
		ceiling = 1
	case TierConserve:
		ceiling = 3
	case TierSteady:
		ceiling = 4
	}
	if ceiling > len(all) {
		ceiling = len(all)
	}
	out := all[:ceiling]

	// Drop duplicates produced by clamping (a 40k ceiling collapses several rows
	// onto each other), keeping the cheapest instance of each.
	seen := map[string]bool{}
	var deduped []Config
	for _, c := range out {
		if seen[c.Key()] {
			continue
		}
		seen[c.Key()] = true
		deduped = append(deduped, c)
	}
	return deduped
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Apply folds a recommendation into a plan, producing the final dispatch
// configuration.
func (p Plan) Apply(r Recommendation) Plan {
	if !p.Allow || r.Config.Model == "" {
		return p
	}
	out := p
	out.Model = r.Config.Model
	out.Effort = r.Config.Effort
	if r.Config.ContextBudget > 0 && r.Config.ContextBudget < out.MaxContextTokens {
		out.MaxContextTokens = r.Config.ContextBudget
	}
	if r.Config.Subagents < out.SubagentFanout {
		out.SubagentFanout = r.Config.Subagents
	}
	out.Reason = p.Reason + "; " + r.Reason
	return out
}

// Report renders what has been learned, ordered by role then cost efficiency.
func (l *Ledger) Report() string {
	l.mu.RLock()
	stats := make([]Stat, 0, len(l.stats))
	for _, s := range l.stats {
		stats = append(stats, *s)
	}
	pct := l.pctPerMTok
	l.mu.RUnlock()

	if len(stats) == 0 {
		return "efficiency: nothing recorded yet"
	}
	sort.SliceStable(stats, func(i, j int) bool {
		if stats[i].Role != stats[j].Role {
			return stats[i].Role < stats[j].Role
		}
		return stats[i].TokensPerSuccess() < stats[j].TokensPerSuccess()
	})
	var b strings.Builder
	role := ""
	for _, s := range stats {
		if s.Role != role {
			role = s.Role
			fmt.Fprintf(&b, "%s:\n", role)
		}
		cost := humanTokens(s.TokensPerSuccess())
		line := fmt.Sprintf("  %-34s %3d runs  %3.0f%% ok  %s/success", s.Config.String(), s.Runs, s.SuccessRate()*100, cost)
		if pct > 0 && s.Successes > 0 {
			line += fmt.Sprintf("  (~%.2f%% of a window)", s.TokensPerSuccess()/1e6*pct)
		}
		if s.SubagentsSpawned > 0 {
			line += fmt.Sprintf("  %d subagents delegated", s.SubagentsSpawned)
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func humanTokens(v float64) string {
	if math.IsInf(v, 1) {
		return "n/a"
	}
	switch {
	case v >= 1e6:
		return fmt.Sprintf("%.1fM tok", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("%.0fk tok", v/1e3)
	default:
		return fmt.Sprintf("%.0f tok", v)
	}
}

// persisted is the on-disk shape.
//
// The scoreboard totals are stored alongside the per-config stats rather than
// recomputed from them, because they are not derivable: a project's cost is
// shared across several configurations, so summing per-config value and cost
// would double-count the denominators. The history is what makes the trend
// survive a restart, and the trend is how anyone can tell the score is actually
// improving rather than just being high.
type persisted struct {
	Version    int     `json:"version"`
	PctPerMTok float64 `json:"pctPerMTok"`
	CalibRuns  int     `json:"calibRuns"`
	Stats      []Stat  `json:"stats"`

	TotalValue    float64      `json:"totalValue,omitempty"`
	TotalQuota    float64      `json:"totalQuota,omitempty"`
	RatedProjects int          `json:"ratedProjects,omitempty"`
	Praised       int          `json:"praised,omitempty"`
	Accepted      int          `json:"accepted,omitempty"`
	Rework        int          `json:"rework,omitempty"`
	Rejected      int          `json:"rejected,omitempty"`
	History       []yieldPoint `json:"history,omitempty"`
	Notes         []Rating     `json:"notes,omitempty"`

	// Pending carries the work that has been delivered but not yet rated, so a
	// restart between shipping and hearing back does not throw the feedback away.
	Pending []pendingProject `json:"pending,omitempty"`
}

// Save writes the ledger to disk atomically.
func (l *Ledger) Save() error {
	l.mu.Lock()
	if l.path == "" {
		l.mu.Unlock()
		return nil
	}
	p := persisted{
		Version: 1, PctPerMTok: l.pctPerMTok, CalibRuns: l.calibRuns,
		TotalValue: l.totalValue, TotalQuota: l.totalQuota, RatedProjects: l.ratedProjects,
		Praised: l.praised, Accepted: l.accepted, Rework: l.rework, Rejected: l.rejected,
		History: append([]yieldPoint(nil), l.history...),
		Notes:   append([]Rating(nil), l.notes...),
		Pending: l.pendingLocked(),
	}
	for _, s := range l.stats {
		p.Stats = append(p.Stats, *s)
	}
	path := l.path
	l.dirty = false
	l.lastSaved = l.now()
	l.mu.Unlock()

	sort.SliceStable(p.Stats, func(i, j int) bool {
		if p.Stats[i].Role != p.Stats[j].Role {
			return p.Stats[i].Role < p.Stats[j].Role
		}
		return p.Stats[i].Config.Key() < p.Stats[j].Config.Key()
	})
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load reads the ledger from disk. A missing file is not an error — an engine
// with no history simply explores from scratch.
func (l *Ledger) Load() error {
	data, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var p persisted
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("reading efficiency ledger %s: %w", l.path, err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pctPerMTok, l.calibRuns = p.PctPerMTok, p.CalibRuns
	l.totalValue, l.totalQuota, l.ratedProjects = p.TotalValue, p.TotalQuota, p.RatedProjects
	l.praised, l.accepted, l.rework, l.rejected = p.Praised, p.Accepted, p.Rework, p.Rejected
	l.history = p.History
	l.notes = p.Notes
	for i := range p.Stats {
		s := p.Stats[i]
		l.stats[statKey(s.Role, s.Config)] = &s
	}
	l.restorePending(p.Pending)
	return nil
}
