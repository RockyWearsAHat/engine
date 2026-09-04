package quota

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Governor: Snapshot in, "how hard to run" out.
//
// OBJECTIVE
//
// Target = 100% of the window used exactly at reset. Not the current burn
// line — the reset line. Windows do not roll over; quota unspent at reset is
// gone. So under target → raise parallelism and fan-out in proportion to
// how far under (paceBoost, capped at PaceBoostCap × policy). Over target →
// tiers cut, cheapest levers first (planFor).
//
// What the boost does NOT do: raise worker model tier. Model choice stays
// with the ledger (cheapest config that works, per role). Planner alone may
// step CheapModel → MidModel, only past PlannerSonnetAhead under target.
// Fable/opus stay rare; ≥90% haiku turns is a locked rule.
//
// Running out is catastrophic: every agent stops at once, window may not
// refill for days. So the governor still paces, and Blocked/Critical win
// over any boost.
//
// POOLED
//
// Quota is per Anthropic account, not per box. Mac and PC share the same
// window. See pooled.go: primary merges every box's /quota/snapshot, pushes
// it back via /quota/pooled, governor prefers it over local while fresh.
//
// PACE
//
//	pace = used_fraction / elapsed_fraction
//
// pace 1.0 = lands on 100% at reset (target). 2.0 = wall at half-time.
// 0.4 = wastes 60%. Ahead = 1 - pace/PaceTarget. Tiers are thresholds on
// pace; boost is proportional to Ahead.

// Tier is the coarse operating mode the governor selects.
type Tier int

const (
	// TierBlocked: a window is exhausted (or the CLI told us we were rejected).
	// Nothing may be dispatched until the reset.
	TierBlocked Tier = iota
	// TierCritical: nearly exhausted. Only work already in flight and the
	// cheapest possible configuration.
	TierCritical
	// TierConserve: on track to exhaust before the window resets. Cheapen every
	// lever that does not change whether the work succeeds.
	TierConserve
	// TierSteady: pace is roughly right. Default configuration. This is also the
	// tier chosen when limit state is UNKNOWN — an unmeasurable balance must
	// never read as an empty one.
	TierSteady
	// TierExpand: well under pace. Widest lever set; paceConcurrency then
	// boosts parallelism/fan-out further by Ahead. Worker model still comes
	// from the ledger, never from the tier.
	TierExpand
)

func (t Tier) String() string {
	switch t {
	case TierBlocked:
		return "blocked"
	case TierCritical:
		return "critical"
	case TierConserve:
		return "conserve"
	case TierSteady:
		return "steady"
	case TierExpand:
		return "expand"
	}
	return "unknown"
}

// Window durations. These are properties of the plan, not of our reading, so
// they are constants rather than inferred — inferring a window length from two
// observations of a percentage is exactly the kind of clever that breaks the
// first time someone starts a session mid-window.
const (
	SessionWindow = 5 * time.Hour
	WeekWindow    = 7 * 24 * time.Hour
)

// Pace returns the projected end-of-window utilization for w, given the window's
// nominal length, and whether it could be computed at all.
//
// Returns ok=false when there is no reset time to measure elapsed against; the
// caller must then fall back rather than treat 0 as "no usage".
func Pace(w Window, dur time.Duration, now time.Time) (float64, bool) {
	if !w.HasReset || w.ResetsAt == nil || dur <= 0 {
		return 0, false
	}
	remaining := w.ResetsAt.Sub(now)
	if remaining < 0 {
		remaining = 0
	}
	if remaining > dur {
		// A reset further out than the window is long. Either the clock is off or
		// the format changed; either way this is not a number to act on.
		return 0, false
	}
	elapsed := dur - remaining
	elapsedFrac := float64(elapsed) / float64(dur)
	// Guard the first moments of a window, where dividing by a near-zero elapsed
	// fraction turns any usage at all into an infinite pace.
	const minElapsed = 0.02
	if elapsedFrac < minElapsed {
		elapsedFrac = minElapsed
	}
	return w.Used() / elapsedFrac, true
}

// Assessment is the governor's read of one account.
type Assessment struct {
	Account string `json:"account"`
	Tier    Tier   `json:"-"`
	// TierName is the string form, for JSON consumers.
	TierName string `json:"tier"`
	// Known is false when the snapshot could not be read; Tier is then Steady.
	Known bool `json:"known"`

	// SessionPace / WeekPace are projected end-of-window utilizations (1.0 = will
	// land exactly on the limit as it resets). Valid only when the matching
	// *PaceKnown is true.
	SessionPace      float64 `json:"sessionPace"`
	SessionPaceKnown bool    `json:"sessionPaceKnown"`
	WeekPace         float64 `json:"weekPace"`
	WeekPaceKnown    bool    `json:"weekPaceKnown"`

	// Pace is the worse of the two paces — the forecast we act on. PaceTarget
	// is the line it is steered to: 1.0 = land on 100% exactly at reset.
	// Ahead = 1 - Pace/PaceTarget: +0.3 means "30% under the target line",
	// negative means overspending. Valid only when PaceKnown.
	Pace       float64 `json:"pace"`
	PaceKnown  bool    `json:"paceKnown"`
	PaceTarget float64 `json:"paceTarget"`
	Ahead      float64 `json:"ahead"`

	// Headroom is the unspent share of the tightest window, 0-1.
	Headroom float64 `json:"headroom"`
	// Binding names the window that constrains us.
	Binding string `json:"binding"`
	// ResetsIn is how long until the binding window refills (0 if unknown).
	ResetsIn time.Duration `json:"-"`

	Reason string `json:"reason"`
}

// PaceTarget: governor steers to 100% used exactly at window reset. Not the
// current burn line — the reset line. Under it → open up. Over it → cut.
const PaceTarget = 1.0

// PlannerSonnetAhead: planner may run MidModel only when this far under
// target. Below it planner stays on CheapModel.
const PlannerSonnetAhead = 0.30

// Assess reduces a snapshot to a tier.
func Assess(s Snapshot, now time.Time) Assessment {
	a := Assessment{Account: s.Account, Tier: TierSteady, Known: s.Ok, Headroom: 1, PaceTarget: PaceTarget}
	if !s.Ok {
		a.Reason = "limit state unknown (" + s.Err + "); holding the conservative default"
		a.TierName = a.Tier.String()
		return a
	}

	tight := s.Tightest()
	a.Headroom = tight.Remaining()
	a.Binding = tight.Name
	if tight.HasReset && tight.ResetsAt != nil {
		if d := tight.ResetsAt.Sub(now); d > 0 {
			a.ResetsIn = d
		}
	}

	a.SessionPace, a.SessionPaceKnown = Pace(s.Session, SessionWindow, now)
	a.WeekPace, a.WeekPaceKnown = Pace(s.Week, WeekWindow, now)

	// The forecast we act on is the worse of the two windows we can compute.
	// Either one stopping us stops everything, so the binding forecast is the max.
	worstPace, paceKnown := 0.0, false
	if a.SessionPaceKnown {
		worstPace, paceKnown = a.SessionPace, true
	}
	if a.WeekPaceKnown && (!paceKnown || a.WeekPace > worstPace) {
		worstPace, paceKnown = a.WeekPace, true
	}
	a.Pace, a.PaceKnown = worstPace, paceKnown
	if paceKnown {
		a.Ahead = 1 - worstPace/PaceTarget
	}

	switch {
	case tight.Percent >= 100:
		a.Tier = TierBlocked
		a.Reason = fmt.Sprintf("%s window exhausted (%.0f%%)", tight.Name, tight.Percent)
	case tight.Percent >= 95:
		a.Tier = TierCritical
		a.Reason = fmt.Sprintf("%s window at %.0f%% — cheapest configuration only", tight.Name, tight.Percent)
	case tight.Percent >= 80 || (paceKnown && worstPace > 1.15):
		a.Tier = TierConserve
		if paceKnown && worstPace > 1.15 {
			a.Reason = fmt.Sprintf("projected to reach %.0f%% of the %s window before it resets", worstPace*100, a.Binding)
		} else {
			a.Reason = fmt.Sprintf("%s window at %.0f%%", tight.Name, tight.Percent)
		}
	case paceKnown && worstPace < 0.85 && tight.Percent < 60:
		a.Tier = TierExpand
		a.Reason = fmt.Sprintf("projected to use only %.0f%% of the %s window — room for a richer configuration if the work needs one", worstPace*100, a.Binding)
	default:
		a.Tier = TierSteady
		if paceKnown {
			a.Reason = fmt.Sprintf("on pace for %.0f%% of the %s window", worstPace*100, a.Binding)
		} else {
			a.Reason = fmt.Sprintf("%s window at %.0f%%; no reset time to pace against", tight.Name, tight.Percent)
		}
	}
	a.TierName = a.Tier.String()
	return a
}

// Plan is what the governor tells the engine to do for one dispatch.
type Plan struct {
	// Allow is false when nothing should be dispatched right now.
	Allow bool `json:"allow"`
	// Account is the account name to run under (empty = ambient default).
	Account string `json:"account,omitempty"`
	// ConfigDir is that account's CLAUDE_CONFIG_DIR (empty = inherit).
	ConfigDir string `json:"configDir,omitempty"`

	Tier     Tier   `json:"-"`
	TierName string `json:"tier"`

	// MaxConcurrency caps simultaneous agent sessions. Parallelism is a real
	// quota multiplier — the usage attribution shows a third of spend happening
	// while 4+ sessions ran at once — so it is the first lever to move.
	MaxConcurrency int `json:"maxConcurrency"`
	// Model is the preferred tier: "opus", "sonnet" or "haiku". Empty means
	// "leave the caller's choice alone".
	Model string `json:"model,omitempty"`
	// Effort is the reasoning effort: "low", "medium", "high". Empty = untouched.
	Effort string `json:"effort,omitempty"`
	// MaxContextTokens is a soft ceiling on assembled context. The single
	// biggest attributed driver is usage at >150k context, so trimming context
	// is the highest-leverage saving available that does not change what gets
	// built.
	MaxContextTokens int `json:"maxContextTokens"`
	// SubagentFanout caps how many subagents a role may spawn.
	SubagentFanout int `json:"subagentFanout"`
	// PlannerModel is the ceiling for the planner role: CheapModel unless
	// more than PlannerSonnetAhead under target, then MidModel. Cap only —
	// never raises a caller's cheaper choice.
	PlannerModel string `json:"plannerModel,omitempty"`

	// RetryAfter is how long to wait before asking again, when !Allow.
	RetryAfter time.Duration `json:"-"`
	// Reason explains the decision, for logs and for the Discord status the user
	// reads instead of watching.
	Reason string `json:"reason"`

	// Assessment is the underlying read, kept so callers can render detail.
	Assessment Assessment `json:"assessment"`
}

// Policy holds the tunable ceilings. Defaults are deliberately conservative at
// the top end: TierExpand is permission to be thorough, not permission to fan
// out without bound.
type Policy struct {
	MaxConcurrency   int
	MaxContextTokens int
	MaxSubagents     int

	// HeavyModel is used when quota is plentiful, CheapModel when it is not.
	HeavyModel string
	MidModel   string
	CheapModel string
}

// DefaultPolicy returns the built-in ceilings.
func DefaultPolicy() Policy {
	return Policy{
		MaxConcurrency:   4,
		MaxContextTokens: 150_000,
		MaxSubagents:     4,
		HeavyModel:       "opus",
		MidModel:         "sonnet",
		CheapModel:       "haiku",
	}
}

// PaceBoostCap: under-target boost never exceeds this multiple of policy.
const PaceBoostCap = 2.0

// paceBoost is the multiplier for a lever when under target. ahead 0 → 1x,
// ahead 0.5 → 1.5x, ahead ≥1 → 2x. Never below 1x: over target is the
// tier's job (Conserve/Critical already cut), not this function's.
func paceBoost(as Assessment) float64 {
	if !as.PaceKnown || as.Ahead <= 0 || as.Tier < TierSteady {
		return 1
	}
	b := 1 + as.Ahead
	if b > PaceBoostCap {
		b = PaceBoostCap
	}
	return b
}

// paceConcurrency: base lever from the tier, raised by paceBoost, capped at
// PaceBoostCap × policy ceiling. Blocked (0) stays 0.
func paceConcurrency(base, policyMax int, as Assessment) int {
	if base < 1 || policyMax < 1 {
		return base
	}
	boosted := int(float64(base)*paceBoost(as) + 0.5)
	lim := int(float64(policyMax)*PaceBoostCap + 0.5)
	if boosted > lim {
		boosted = lim
	}
	if boosted < base {
		boosted = base
	}
	return boosted
}

// plannerModelFor: MidModel only when far enough under target.
func plannerModelFor(as Assessment, p Policy) string {
	if as.PaceKnown && as.Ahead > PlannerSonnetAhead && as.Tier >= TierSteady {
		return p.MidModel
	}
	return p.CheapModel
}

func max_(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// planFor maps a tier onto concrete levers.
//
// The ordering of what gets cut is the interesting part, and it follows from the
// objective: cut the things that cost quota WITHOUT changing whether the work
// succeeds, before cutting the things that do.
//
//	1st  context size      — 150k-context sessions are the top attributed driver
//	                         and most of that context is usually not load-bearing
//	2nd  parallelism       — running four sessions at once does not make any one
//	                         of them smarter, it just spends four times as fast
//	3rd  subagent fan-out  — same argument, one level down
//	4th  reasoning effort  — starts to trade quality, so it moves later
//	5th  model tier        — the biggest quality lever, so it moves last
//
// That order is why TierConserve still runs the heavy model: an Opus run on a
// trimmed context is usually both cheaper AND better than a Sonnet run on a
// bloated one, and swapping the model first is the intuitive move that makes
// results worse while saving less.
func planFor(t Tier, p Policy) Plan {
	pl := Plan{Allow: true, Tier: t, TierName: t.String(), PlannerModel: p.CheapModel}
	switch t {
	case TierBlocked:
		pl.Allow = false
		pl.MaxConcurrency = 0
		pl.SubagentFanout = 0
	case TierCritical:
		pl.MaxConcurrency = 1
		pl.MaxContextTokens = min(p.MaxContextTokens, 60_000)
		pl.SubagentFanout = 0
		pl.Effort = "low"
		pl.Model = p.CheapModel
	case TierConserve:
		pl.MaxConcurrency = max(1, p.MaxConcurrency/2)
		pl.MaxContextTokens = min(p.MaxContextTokens, 100_000)
		pl.SubagentFanout = max(1, p.MaxSubagents/2)
		pl.Effort = "medium"
		pl.Model = p.HeavyModel
	case TierSteady:
		pl.MaxConcurrency = max(1, p.MaxConcurrency*3/4)
		pl.MaxContextTokens = p.MaxContextTokens
		pl.SubagentFanout = max(1, p.MaxSubagents/2)
		pl.Effort = "medium"
		pl.Model = p.HeavyModel
	case TierExpand:
		pl.MaxConcurrency = p.MaxConcurrency
		pl.MaxContextTokens = p.MaxContextTokens
		pl.SubagentFanout = p.MaxSubagents
		pl.Effort = "high"
		pl.Model = p.HeavyModel
	}
	return pl
}

// Governor decides, per dispatch, whether to run and how.
type Governor struct {
	registry *Registry
	prober   *Prober
	policy   Policy
	now      func() time.Time

	// observer carries the free rate_limit_event signal; see observer.go.
	observer *Observer
	// pooled is the last merged fleet snapshot; see pooled.go.
	pooled pooledStore
}

// NewGovernor wires a governor over a registry and prober.
func NewGovernor(reg *Registry, prober *Prober, policy Policy) *Governor {
	if policy.MaxConcurrency == 0 {
		policy = DefaultPolicy()
	}
	return &Governor{
		registry: reg,
		prober:   prober,
		policy:   policy,
		now:      time.Now,
		observer: NewObserver(),
	}
}

// SetClock overrides the clock, for tests.
func (g *Governor) SetClock(now func() time.Time) { g.now = now }

// Observer exposes the passive event sink so the provider can feed it.
func (g *Governor) Observer() *Observer { return g.observer }

// SetObserver adopts an existing event sink.
//
// Observing is free and must never block, so callers typically create the
// Observer eagerly and feed it from the moment the process starts, then hand it
// to the Governor once one is built. Without this the two would disagree: events
// recorded before the (comparatively expensive) governor exists would be lost,
// and a rejection seen during startup would be forgotten by the time it mattered.
func (g *Governor) SetObserver(o *Observer) {
	if o != nil {
		g.observer = o
	}
}

// Decide picks an account and a configuration for the next dispatch.
//
// It probes every usable account (cached, single-flighted, free) and chooses by
// Score below. When every account is blocked it returns Allow=false with the
// shortest wait, so the caller can sleep exactly long enough instead of polling.
func (g *Governor) Decide(ctx context.Context) Plan {
	now := g.now()
	accounts := g.registry.Usable()
	if len(accounts) == 0 {
		pl := planFor(TierSteady, g.policy)
		pl.Reason = "no usable Claude accounts configured; running with default limits"
		return pl
	}

	// Probe locally only for accounts the pooled snapshot cannot answer.
	var toProbe []Account
	if p, fresh := g.Pooled(); fresh {
		for _, a := range accounts {
			// Row missing OR row with no reading (primary's probe failed) → probe here.
			sa, ok := p.Find(accountKey(a))
			if !ok || (sa.SessionPct < 0 && sa.WeekPct < 0) {
				toProbe = append(toProbe, a)
			}
		}
	} else {
		toProbe = accounts
	}
	snaps := g.prober.ProbeAll(ctx, toProbe)

	type cand struct {
		acc  Account
		as   Assessment
		snap Snapshot
	}
	var cands []cand
	for _, a := range accounts {
		s, _ := g.readAccount(ctx, a, snaps)
		as := Assess(s, now)
		// A hard "rejected" seen on the wire outranks any cached percentage: the
		// server just told us no, and the /usage number may be a minute stale.
		if g.observer.Rejected(a.Name, now) {
			as.Tier = TierBlocked
			as.TierName = as.Tier.String()
			as.Reason = "the API rejected a request on this account for rate limits"
			if r, ok := g.observer.ResetsAt(a.Name); ok && r.After(now) {
				as.ResetsIn = r.Sub(now)
			}
		} else if g.observer.UsingOverage(a.Name) {
			// Spilling into paid overage is the one state that looks fine from the
			// percentages and is not. The subscription has stopped being flat-cost
			// and every further token is billed, so the objective — spend as little
			// as possible for the result — says pull back hard and say so, NOT that
			// there is now more room to use. A safety net is a reason to stay off
			// it, not a licence to lean on it.
			if as.Tier > TierConserve {
				as.Tier = TierConserve
				as.TierName = as.Tier.String()
			}
			as.Reason = "account is spending PAID OVERAGE — the subscription is no longer flat-cost; " + as.Reason
		}
		cands = append(cands, cand{acc: a, as: as, snap: s})
	}

	// Best first. Ties break on name so the choice is stable and logs are
	// diffable rather than flapping between equivalent accounts.
	sort.SliceStable(cands, func(i, j int) bool {
		si, sj := Score(cands[i].as), Score(cands[j].as)
		if si != sj {
			return si > sj
		}
		return cands[i].acc.Name < cands[j].acc.Name
	})

	best := cands[0]
	if best.as.Tier == TierBlocked {
		wait := time.Duration(0)
		for _, c := range cands {
			if c.as.ResetsIn > 0 && (wait == 0 || c.as.ResetsIn < wait) {
				wait = c.as.ResetsIn
			}
		}
		if wait == 0 {
			wait = 10 * time.Minute // no reset known; check back rather than spin
		}
		pl := planFor(TierBlocked, g.policy)
		pl.Assessment = best.as
		pl.Account, pl.ConfigDir = best.acc.Name, best.acc.ConfigDir
		pl.RetryAfter = wait
		pl.Reason = fmt.Sprintf("every account is out of quota (%s); next reset in %s", best.as.Reason, wait.Round(time.Minute))
		return pl
	}

	pl := planFor(best.as.Tier, g.policy)
	pl.Account, pl.ConfigDir = best.acc.Name, best.acc.ConfigDir
	pl.Assessment = best.as
	// Target is 100% at reset. Under it → raise parallelism and fan-out in
	// proportion to how far under. Model tier is NOT raised here: planner
	// gets MidModel only past PlannerSonnetAhead, workers stay on the ledger.
	pl.MaxConcurrency = paceConcurrency(pl.MaxConcurrency, g.policy.MaxConcurrency, best.as)
	pl.SubagentFanout = paceConcurrency(pl.SubagentFanout, g.policy.MaxSubagents, best.as)
	pl.PlannerModel = plannerModelFor(best.as, g.policy)
	pl.Reason = fmt.Sprintf("%s on %q: %s", best.as.Tier, best.acc.Name, best.as.Reason)
	if best.as.PaceKnown && best.as.Ahead > 0 {
		pl.Reason += fmt.Sprintf("; %.0f%% under target, boost x%.2f", best.as.Ahead*100, paceBoost(best.as))
	}
	return pl
}

// Score ranks an account for selection. Higher is better.
//
// This chooses WHERE to spend, never WHETHER or HOW MUCH — those are the tier's
// and the ledger's jobs. The distinction is what keeps the use-it-or-lose-it
// reasoning below legitimate: shifting a run from one pool to another does not
// spend an extra token, so it cannot lower yield.
//
// Given that, between two accounts with equal headroom the better one to draw
// from is the one whose window resets SOONER, because its unspent capacity is
// the capacity about to expire. Hoarding the soon-to-reset account and draining
// the one with days left is the intuitive move and it is backwards.
func Score(a Assessment) float64 {
	if a.Tier == TierBlocked {
		return -1
	}
	// Headroom dominates: an account with room is always preferable to a tight
	// one, whatever the timing.
	score := a.Headroom * 100
	// Then, among comparable accounts, prefer the one whose capacity expires
	// soonest. Normalised against the week so the bonus stays small relative to
	// headroom and can only break near-ties.
	if a.ResetsIn > 0 {
		urgency := 1 - float64(a.ResetsIn)/float64(WeekWindow)
		if urgency < 0 {
			urgency = 0
		}
		score += urgency * 5
	}
	// An unknown account is usable but not preferred over a measured one with
	// real headroom.
	if !a.Known {
		score -= 10
	}
	return score
}

// Report renders the current state of every account, for logs and for the status
// the user reads instead of watching the build.
func (g *Governor) Report(ctx context.Context) string {
	now := g.now()
	accounts := g.registry.All()
	if len(accounts) == 0 {
		return "quota: no accounts configured"
	}
	var b strings.Builder
	for _, a := range accounts {
		if a.Disabled {
			fmt.Fprintf(&b, "%-12s disabled — %s\n", a.Name, a.DisabledReason)
			continue
		}
		s, src := g.readAccount(ctx, a, nil)
		as := Assess(s, now)
		fmt.Fprintf(&b, "%-12s %-9s %s [%s]\n", a.Name, as.Tier, s.Detail(), src)
		if as.SessionPaceKnown || as.WeekPaceKnown {
			fmt.Fprintf(&b, "%-12s %-9s pace: session %s, week %s\n", "", "", pct(as.SessionPace, as.SessionPaceKnown), pct(as.WeekPace, as.WeekPaceKnown))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func pct(v float64, known bool) string {
	if !known {
		return "n/a"
	}
	return fmt.Sprintf("%.0f%%", v*100)
}

// WindowStatus is one rate-limit window, flattened for JSON consumers.
//
// Percent is only meaningful when Known is true. A window that could not be read
// reports Known=false and Percent=0, and the two must never be conflated: "we do
// not know" and "nothing used" lead to opposite decisions.
type WindowStatus struct {
	Name     string  `json:"name"`
	Label    string  `json:"label,omitempty"`
	Known    bool    `json:"known"`
	Percent  float64 `json:"percent"`
	ResetsAt string  `json:"resetsAt,omitempty"`
	ResetsIn string  `json:"resetsIn,omitempty"`
}

func windowStatus(w Window, ok bool, now time.Time) WindowStatus {
	ws := WindowStatus{Name: w.Name, Label: w.Label, Known: ok, Percent: w.Percent}
	if ok && w.HasReset && w.ResetsAt != nil {
		ws.ResetsAt = w.ResetsAt.Format(time.RFC3339)
		if d := w.ResetsAt.Sub(now); d > 0 {
			ws.ResetsIn = d.Round(time.Minute).String()
		}
	}
	return ws
}

// AccountStatus is the full readable state of one account.
type AccountStatus struct {
	Name           string `json:"name"`
	Disabled       bool   `json:"disabled,omitempty"`
	DisabledReason string `json:"disabledReason,omitempty"`
	Ok             bool   `json:"ok"`
	Error          string `json:"error,omitempty"`
	FetchedAt      string `json:"fetchedAt,omitempty"`
	PlanNote       string `json:"planNote,omitempty"`
	// Source is "local" (own probe) or "pooled" (fleet snapshot from primary).
	Source       string         `json:"source,omitempty"`
	Session      WindowStatus   `json:"session"`
	Week         WindowStatus   `json:"week"`
	PerModel     []WindowStatus `json:"perModel,omitempty"`
	Assessment   Assessment     `json:"assessment"`
	ResetsIn     string         `json:"resetsIn,omitempty"`
	UsingOverage bool           `json:"usingOverage,omitempty"`
	LimitStatus  string         `json:"limitStatus,omitempty"`
	Score        float64        `json:"score"`
}

// Status is the whole fuel gauge: every account, plus the plan currently in
// force. This is what an external supervisor reads to answer "how much room is
// left and what is the engine doing about it" without parsing a log line.
type Status struct {
	At       string          `json:"at"`
	Accounts []AccountStatus `json:"accounts"`
	Plan     Plan            `json:"plan"`
	// RetryAfterSeconds mirrors Plan.RetryAfter, which is a time.Duration and so
	// would otherwise reach a JSON consumer as a nanosecond integer. Only set when
	// the plan is a hold.
	RetryAfterSeconds float64 `json:"retryAfterSeconds,omitempty"`
}

// Status assembles the gauge. It shares the prober's cache with Decide, so
// polling it is cheap and does not itself burn quota.
func (g *Governor) Status(ctx context.Context) Status {
	now := g.now()
	st := Status{At: now.Format(time.RFC3339), Plan: g.Decide(ctx)}
	if st.Plan.RetryAfter > 0 {
		st.RetryAfterSeconds = st.Plan.RetryAfter.Seconds()
	}
	for _, a := range g.registry.All() {
		as := AccountStatus{Name: a.Name, Disabled: a.Disabled, DisabledReason: a.DisabledReason}
		if a.Disabled {
			st.Accounts = append(st.Accounts, as)
			continue
		}
		s, src := g.readAccount(ctx, a, nil)
		as.Source = src
		as.Ok, as.PlanNote = s.Ok, s.PlanNote
		as.Error = s.Err
		if !s.FetchedAt.IsZero() {
			as.FetchedAt = s.FetchedAt.Format(time.RFC3339)
		}
		as.Session = windowStatus(s.Session, s.Ok, now)
		as.Week = windowStatus(s.Week, s.Ok, now)
		for _, w := range s.PerModel {
			as.PerModel = append(as.PerModel, windowStatus(w, s.Ok, now))
		}
		as.Assessment = Assess(s, now)
		if as.Assessment.ResetsIn > 0 {
			as.ResetsIn = as.Assessment.ResetsIn.Round(time.Minute).String()
		}
		as.UsingOverage = g.observer.UsingOverage(a.Name)
		if ev, ok := g.observer.Last(a.Name); ok {
			as.LimitStatus = ev.Status
		}
		as.Score = Score(as.Assessment)
		st.Accounts = append(st.Accounts, as)
	}
	return st
}
