package quota

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// The governor turns a Snapshot into a decision about how hard to run.
//
// THE OBJECTIVE, STATED PRECISELY
//
// It is tempting to read "use the whole limit" as the goal and write a scheduler
// that pushes utilization to 100%. That is the wrong objective and it produces a
// worse engine. The goal is to MINIMISE QUOTA SPENT PER UNIT OF FINISHED WORK,
// subject to never being stopped by a wall we could have seen coming. Quota
// saved is not quota wasted — it is capacity available for the next thing, and
// a cheaper configuration that still ships the feature is strictly better than
// an expensive one that also ships it.
//
// The limit still matters, for two reasons that pull in opposite directions and
// have to be balanced rather than picked between:
//
//   - Running out is catastrophic: every agent stops at once, mid-task, and the
//     window may not refill for days. So the governor paces.
//   - Unspent quota expires. The 5-hour and 7-day windows do not roll over, so
//     capacity left idle while real work was queued is capacity destroyed. So
//     the governor does not hoard either.
//
// PACE IS THE WHOLE TRICK
//
// Utilization alone cannot tell you whether you are in trouble: 50% of the week
// is fine on day six and alarming on day one. What matters is utilization
// measured against how much of the window has ELAPSED. Divide one by the other
// and you get a number that is both a pace and a forecast:
//
//	pace = used_fraction / elapsed_fraction
//
// At a steady burn, pace is exactly the utilization the window will reach by the
// time it resets. pace = 1.0 means "finishes the window exactly as it refills" —
// perfect use. pace = 2.0 means "will hit the wall halfway through". pace = 0.4
// means "will waste 60% of this window". So one number answers both questions
// the engine actually has, and the tiers below are just thresholds on it.

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
	// TierExpand: well under pace with capacity that will expire unused. Permit
	// the wider, more thorough configuration.
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
	if !w.HasReset || dur <= 0 {
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

	// Headroom is the unspent share of the tightest window, 0-1.
	Headroom float64 `json:"headroom"`
	// Binding names the window that constrains us.
	Binding string `json:"binding"`
	// ResetsIn is how long until the binding window refills (0 if unknown).
	ResetsIn time.Duration `json:"-"`

	Reason string `json:"reason"`
}

// Assess reduces a snapshot to a tier.
func Assess(s Snapshot, now time.Time) Assessment {
	a := Assessment{Account: s.Account, Tier: TierSteady, Known: s.Ok, Headroom: 1}
	if !s.Ok {
		a.Reason = "limit state unknown (" + s.Err + "); holding the conservative default"
		a.TierName = a.Tier.String()
		return a
	}

	tight := s.Tightest()
	a.Headroom = tight.Remaining()
	a.Binding = tight.Name
	if tight.HasReset {
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
		a.Reason = fmt.Sprintf("projected to use only %.0f%% of the %s window — capacity would expire unused", worstPace*100, a.Binding)
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
	pl := Plan{Allow: true, Tier: t, TierName: t.String()}
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

	snaps := g.prober.ProbeAll(ctx, accounts)

	type cand struct {
		acc  Account
		as   Assessment
		snap Snapshot
	}
	var cands []cand
	for _, a := range accounts {
		s := snaps[a.Name]
		s.Account = a.Name
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
	pl.Reason = fmt.Sprintf("%s on %q: %s", best.as.Tier, best.acc.Name, best.as.Reason)
	return pl
}

// Score ranks an account for selection. Higher is better.
//
// The ranking encodes the use-it-or-lose-it asymmetry. Between two accounts with
// equal headroom, the better one to spend is the one whose window resets SOONER,
// because its unspent capacity is the capacity about to expire. Hoarding the
// soon-to-reset account and draining the one with days left is the intuitive
// move and it is backwards.
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
		s := g.prober.Probe(ctx, a)
		as := Assess(s, now)
		fmt.Fprintf(&b, "%-12s %-9s %s\n", a.Name, as.Tier, s.Detail())
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
	if ok && w.HasReset {
		ws.ResetsAt = w.ResetsAt.Format(time.RFC3339)
		if d := w.ResetsAt.Sub(now); d > 0 {
			ws.ResetsIn = d.Round(time.Minute).String()
		}
	}
	return ws
}

// AccountStatus is the full readable state of one account.
type AccountStatus struct {
	Name           string         `json:"name"`
	Disabled       bool           `json:"disabled,omitempty"`
	DisabledReason string         `json:"disabledReason,omitempty"`
	Ok             bool           `json:"ok"`
	Error          string         `json:"error,omitempty"`
	FetchedAt      string         `json:"fetchedAt,omitempty"`
	PlanNote       string         `json:"planNote,omitempty"`
	Session        WindowStatus   `json:"session"`
	Week           WindowStatus   `json:"week"`
	PerModel       []WindowStatus `json:"perModel,omitempty"`
	Assessment     Assessment     `json:"assessment"`
	ResetsIn       string         `json:"resetsIn,omitempty"`
	UsingOverage   bool           `json:"usingOverage,omitempty"`
	LimitStatus    string         `json:"limitStatus,omitempty"`
	Score          float64        `json:"score"`
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
		s := g.prober.Probe(ctx, a)
		s.Account = a.Name
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
