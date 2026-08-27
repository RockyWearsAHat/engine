package ai

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/engine/server/quota"
)

// This file is the engine's side of quota awareness: one gate that every
// claudecode dispatch passes through.
//
// It exists because the subscription's real constraint is invisible from inside
// the process. Engine's routing calls the Claude Max subscription "flat ~$0
// marginal cost", which is true in dollars and false in the currency that
// actually runs out — a rolling 5-hour window and a 7-day one, shared by every
// agent the orchestrator starts. Before this, the first time Engine learned it
// was out of quota was when a build died mid-step.
//
// The gate does three things per dispatch, in order:
//
//  1. asks the governor which account has room and how hard to push (quota.Plan)
//  2. asks the ledger for the CHEAPEST configuration that has historically
//     worked for this role, under that ceiling (quota.Recommendation)
//  3. after the run, records what it actually cost and whether it worked, so
//     step 2 gets smarter
//
// Off by default only in the sense that a machine with no accounts configured
// behaves exactly as before: one ambient login, no probing surprises, and an
// unreadable quota state maps to the same defaults Engine already used. Set
// ENGINE_QUOTA=0 to disable entirely.

// quotaGate holds the process-wide governor and efficiency ledger.
type quotaGate struct {
	governor *quota.Governor
	ledger   *quota.Ledger
	policy   quota.Policy
	enabled  bool

	// Bookkeeping for quota observations. obsActive maps a dispatch id to
	// whether another governed run overlapped it; an overlapped run can never
	// be measured, because the window moved for both of them.
	obsMu     sync.Mutex
	obsNextID uint64
	obsActive map[uint64]bool
}

var (
	gateOnce sync.Once
	gate     *quotaGate

	// The observer is built eagerly and separately from the gate. Recording a
	// rate_limit_event is a pure parse with no I/O, and it happens on a stream
	// the engine is already reading — so it must never be the thing that triggers
	// account resolution and shells out to `claude auth status` mid-stream.
	// Keeping it standalone also means limit events seen before the first
	// governed dispatch are not lost.
	observerOnce sync.Once
	observerInst *quota.Observer
)

func quotaObserver() *quota.Observer {
	observerOnce.Do(func() { observerInst = quota.NewObserver() })
	return observerInst
}

// resetQuotaGateForTest clears both singletons so a test can vary the
// environment. Test-only; the gate is deliberately process-wide in production
// because account resolution should happen once, not per dispatch.
func resetQuotaGateForTest() {
	gateOnce = sync.Once{}
	gate = nil
	observerOnce = sync.Once{}
	observerInst = nil
}

// gateBuiltForTest reports whether the (expensive) gate has been constructed.
func gateBuiltForTest() bool { return gate != nil }

// quotaEnabled reports whether quota governance is active. On unless explicitly
// disabled — an engine that cannot see its own fuel gauge is the problem this
// package exists to fix, so the awareness is the default and opting out is the
// deliberate act.
func quotaEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("ENGINE_QUOTA")))
	return v != "0" && v != "false" && v != "no" && v != "off"
}

// quotaLedgerPath is where the efficiency ledger persists.
//
// Deliberately MACHINE-GLOBAL rather than per-project: what the ledger learns is
// "a review of this size does not need Opus", which is a property of the role,
// not of the repository. Scoping it per project would make every new project
// re-learn the same lesson from scratch and pay for the lesson again.
func quotaLedgerPath() string {
	if v := strings.TrimSpace(os.Getenv("ENGINE_QUOTA_LEDGER")); v != "" {
		return v
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "Engine", "quota-efficiency.json")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".engine", "quota-efficiency.json")
	}
	return ""
}

// quotaPolicyFromEnv reads the ceilings, falling back to the built-in defaults.
func quotaPolicyFromEnv() quota.Policy {
	p := quota.DefaultPolicy()
	if n := envInt("ENGINE_QUOTA_MAX_CONCURRENCY", 0); n > 0 {
		p.MaxConcurrency = n
	}
	if n := envInt("ENGINE_QUOTA_MAX_CONTEXT_TOKENS", 0); n > 0 {
		p.MaxContextTokens = n
	}
	if n := envInt("ENGINE_QUOTA_MAX_SUBAGENTS", 0); n > 0 {
		p.MaxSubagents = n
	}
	if v := strings.TrimSpace(os.Getenv("ENGINE_QUOTA_HEAVY_MODEL")); v != "" {
		p.HeavyModel = v
	}
	if v := strings.TrimSpace(os.Getenv("ENGINE_QUOTA_MID_MODEL")); v != "" {
		p.MidModel = v
	}
	if v := strings.TrimSpace(os.Getenv("ENGINE_QUOTA_CHEAP_MODEL")); v != "" {
		p.CheapModel = v
	}
	return p
}

func envInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// quotaGateInstance returns the process-wide gate, building it on first use.
//
// Account resolution runs `claude auth status` once per configured account,
// which is why it is deferred to first use rather than done at init: a machine
// that never touches the claudecode provider should never pay for it.
func quotaGateInstance() *quotaGate {
	gateOnce.Do(func() {
		policy := quotaPolicyFromEnv()
		g := &quotaGate{policy: policy, enabled: quotaEnabled()}
		if !g.enabled {
			gate = g
			return
		}
		runner := quota.DefaultRunner()
		reg := quota.NewRegistry(runner, quota.AccountsFromEnv(os.Getenv("ENGINE_CLAUDE_ACCOUNTS"))...)

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		reg.Resolve(ctx)
		cancel()

		prober := quota.NewProber(runner, 0)
		g.governor = quota.NewGovernor(reg, prober, policy)
		// Adopt the process-wide observer so events seen before this point still
		// count.
		g.governor.SetObserver(quotaObserver())
		// A status change on the wire drops the cached reading, so the next
		// decision re-reads instead of pacing against a number we know is stale.
		g.governor.Observer().OnChange(prober.Invalidate)
		g.ledger = quota.NewLedger(quota.LedgerOptions{
			Path:       quotaLedgerPath(),
			MinSamples: envInt("ENGINE_QUOTA_MIN_SAMPLES", 0),
			SuccessBar: envFloat("ENGINE_QUOTA_SUCCESS_BAR", 0),
		})
		gate = g
	})
	return gate
}

func envFloat(key string, def float64) float64 {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// QuotaDispatch is the gate's verdict for one dispatch.
type QuotaDispatch struct {
	// Allow is false when there is no quota anywhere; the caller must not run.
	Allow bool
	// RetryAfter is how long to wait before trying again, when !Allow.
	RetryAfter time.Duration
	// Env is the environment the `claude` process must run with — this is what
	// selects the account. Nil means "inherit unchanged".
	Env []string
	// Model, when non-empty, is the model the gate wants used.
	Model string
	// MaxContextTokens / SubagentFanout / MaxConcurrency are the ceilings.
	MaxContextTokens int
	SubagentFanout   int
	MaxConcurrency   int
	// Account is the chosen account name, for outcome recording and logs.
	Account string
	// Role is the role this dispatch is for.
	Role AgentRole
	// Config is the configuration the ledger chose, needed to record the outcome
	// against the right key afterwards.
	Config quota.Config
	// ProjectID is the unit of delivered work this run counts toward, so that a
	// rating arriving later can be attributed back to this configuration.
	ProjectID string
	// Reason is the human explanation of the whole decision.
	Reason string
	// Governed is false when quota governance is off or unavailable, in which
	// case every field above except Allow is advisory only.
	Governed bool
	// startedAt is used to compute the run duration when recording.
	startedAt time.Time
	// obsID identifies this run in the gate's overlap bookkeeping; zero when the
	// run is not a measurement candidate.
	obsID uint64
	// pctBefore and fetchedBefore bracket the run for quota measurement: the
	// binding window's reading when it started, and the identity of the probe
	// that reading came from.
	pctBefore     float64
	fetchedBefore string
}

// beginQuotaObservation opens an observation window for a dispatch, and taints
// any observation already open.
//
// Two runs in flight at once cannot both be measured, and neither can either of
// them: the binding window moves for both, so the delta each one sees is the sum
// of the pair. The plan allows a maxConcurrency of 3, so this is the common case,
// not an edge case.
func (g *quotaGate) beginQuotaObservation() uint64 {
	g.obsMu.Lock()
	defer g.obsMu.Unlock()
	if g.obsActive == nil {
		g.obsActive = map[uint64]bool{}
	}
	overlapped := len(g.obsActive) > 0
	for id := range g.obsActive {
		g.obsActive[id] = true
	}
	g.obsNextID++
	g.obsActive[g.obsNextID] = overlapped
	return g.obsNextID
}

// releaseQuotaObservation frees a dispatch's observation if it is still open.
//
// quotaAfter normally closes it, but the dispatch path has error returns that
// never reach quotaAfter — a failed stdout pipe, a `claude` binary that will not
// start. An observation left open taints every later one for the life of the
// process, so calibration would stop for good after one bad spawn. Closing twice
// is a no-op, so the recording path keeps ownership of the reading.
func releaseQuotaObservation(d QuotaDispatch) {
	if d.obsID == 0 {
		return
	}
	quotaGateInstance().endQuotaObservation(d.obsID)
}

// endQuotaObservation closes an observation and reports whether the run had the
// binding window to itself for its whole duration.
func (g *quotaGate) endQuotaObservation(id uint64) bool {
	g.obsMu.Lock()
	defer g.obsMu.Unlock()
	overlapped, tracked := g.obsActive[id]
	delete(g.obsActive, id)
	return tracked && !overlapped
}

// bindingWindow reads the window the governor itself calls binding, rather than
// guessing which of session/week is tighter — Assessment.Binding already names
// it, and it can be a per-model sub-limit that neither of those two covers.
//
// The second return is the identity of the probe behind the reading. Status
// shares the prober's 90s cache, so two calls inside that window return the same
// snapshot; comparing FetchedAt is what tells a real second reading apart from
// the first one handed back twice.
func bindingWindow(st quota.Status, account string) (pct float64, fetchedAt string, known bool) {
	for _, a := range st.Accounts {
		if a.Name != account {
			continue
		}
		if !a.Ok {
			return 0, "", false
		}
		windows := append([]quota.WindowStatus{a.Session, a.Week}, a.PerModel...)
		for _, w := range windows {
			if w.Name == a.Assessment.Binding {
				return w.Percent, a.FetchedAt, w.Known
			}
		}
	}
	return 0, "", false
}

// quotaBefore consults the gate ahead of a claudecode dispatch.
//
// requestedModel is what routing already chose. The gate NEVER upgrades it —
// asking for Sonnet and getting Opus back would be a nasty surprise and the
// opposite of the objective — it only ever holds it or moves it cheaper.
func quotaBefore(ctx context.Context, role AgentRole, requestedModel, projectID string, parentEnv []string) QuotaDispatch {
	g := quotaGateInstance()
	// SubagentFanout starts at -1, meaning "no ceiling stated", NOT 0.
	//
	// This matters because 0 is a real instruction — spawn no subagents at all —
	// and the consumer (buildClaudeArgs) enforces it by removing the Task tool
	// from the session. Leaving the field at Go's zero value on the ungoverned
	// path would silently disable subagents for every run on every machine with
	// quota governance switched off, which is the opposite of what "ungoverned"
	// should mean.
	out := QuotaDispatch{Allow: true, Model: requestedModel, Role: role, ProjectID: projectID, SubagentFanout: -1, startedAt: time.Now()}
	if !g.enabled || g.governor == nil {
		out.Reason = "quota governance disabled"
		return out
	}

	plan := g.governor.Decide(ctx)
	out.Governed = true
	out.Account = plan.Account
	out.Reason = plan.Reason

	if !plan.Allow {
		out.Allow = false
		out.RetryAfter = plan.RetryAfter
		return out
	}

	// Open the measurement bracket. Reading Status here is free: Decide just
	// probed, so this hits the same cache entry.
	if pct, fetched, known := bindingWindow(g.governor.Status(ctx), plan.Account); known {
		out.pctBefore, out.fetchedBefore = pct, fetched
		out.obsID = g.beginQuotaObservation()
	}

	// The ledger picks the cheapest configuration that has worked for this role,
	// from within the ceiling the plan allows.
	cands := quota.Candidates(plan, g.policy)
	if len(cands) > 0 && g.ledger != nil {
		rec := g.ledger.Recommend(quotaRoleKey(role), cands)
		plan = plan.Apply(rec)
		out.Config = rec.Config
		out.Reason = plan.Reason
	}

	out.MaxContextTokens = plan.MaxContextTokens
	out.SubagentFanout = plan.SubagentFanout
	out.MaxConcurrency = plan.MaxConcurrency

	// Downgrade only. If routing asked for something cheaper than the gate would
	// have picked, routing wins — it knows something about this task that the
	// pacing tier does not.
	if plan.Model != "" && modelRank(plan.Model) < modelRank(requestedModel) {
		out.Model = plan.Model
	}

	// Select the account by env. This is the multi-account mechanism: the same
	// binary, a different CLAUDE_CONFIG_DIR, a different quota pool.
	if plan.ConfigDir != "" {
		out.Env = quota.Account{Name: plan.Account, ConfigDir: plan.ConfigDir}.Env(parentEnv)
	}
	return out
}

// quotaRoleKey is the ledger key for a role.
//
// It uses the role's LABEL, not its numeric value, because the ledger persists
// across restarts and upgrades: inserting a new AgentRole into the enum shifts
// every later constant, which would silently re-attribute months of "reviewer"
// history to whatever role inherited that integer. A label change orphans the
// old records instead, which is visible and harmless.
func quotaRoleKey(role AgentRole) string {
	return agentRoleLabel(role)
}

// modelRank orders model families by cost/capability so the gate can tell an
// upgrade from a downgrade. Unknown models rank highest, so an unrecognised
// name is never silently "downgraded" onto something weaker.
func modelRank(model string) int {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "haiku"):
		return 1
	case strings.Contains(m, "sonnet"):
		return 2
	case strings.Contains(m, "opus"):
		return 3
	default:
		return 4
	}
}

// quotaAfter records what a dispatch actually cost and whether it worked.
//
// The success bar is the caller's: here, "the run finished without an error
// event and produced output". That is coarse, but it only has to be CONSISTENT —
// the ledger compares configurations against each other, not against truth.
func quotaAfter(d QuotaDispatch, stats claudeRunStats, success bool) {
	g := quotaGateInstance()
	// Close the measurement bracket before deciding whether to record. An
	// observation left open taints every later one for the life of the process,
	// so a run that is not worth recording must still release it — and a run
	// with no ledger key still overlapped whatever ran beside it.
	recording := g.enabled && g.ledger != nil && d.Governed && d.Config.Model != ""
	quotaPct := g.closeQuota(d, recording)
	if !recording {
		return
	}
	g.ledger.Record(quota.Outcome{
		Role:             quotaRoleKey(d.Role),
		Config:           d.Config,
		Success:          success,
		Tokens:           stats.TotalTokens(),
		SubagentsSpawned: stats.SubagentsSpawned,
		ProjectID:        d.ProjectID,
		Duration:         time.Since(d.startedAt),
		At:               time.Now(),
		QuotaPct:         quotaPct,
	})
}

// measureQuota returns how far the binding window moved over this run, but only
// when that movement is honestly attributable to it — otherwise zero, meaning
// "not measured".
//
// Zero is a deliberate answer here, not a failure. The ledger uses these
// readings to learn pctPerMTok, a running mean with no outlier rejection, and
// once that is non-zero it becomes the denominator for EVERY configuration's
// yield — including ones that never ran concurrently. A wrong reading therefore
// does not stay local to the run that produced it, so a rare clean measurement
// is worth far more than a frequent approximate one. Both guards below reject
// far more runs than they accept, and that is the intended ratio.
func (g *quotaGate) closeQuota(d QuotaDispatch, recording bool) float64 {
	if d.obsID == 0 {
		return 0
	}
	// Always close the bracket, whatever we decide about the reading.
	solo := g.endQuotaObservation(d.obsID)
	if !solo || !recording || g.governor == nil {
		return 0
	}
	pct, fetched, known := bindingWindow(g.governor.Status(context.Background()), d.Account)
	// A reading from the same probe as the "before" one is that probe handed
	// back twice, not a measurement: its delta is always exactly 0. Runs shorter
	// than the prober's TTL are simply not measurable, and silently recording
	// their 0 would look like a run that cost nothing.
	if !known || fetched == "" || fetched == d.fetchedBefore {
		return 0
	}
	// A window that reset mid-run reads lower than it started. There is no delta
	// to recover from that, only a negative number to discard.
	if pct <= d.pctBefore {
		return 0
	}
	return pct - d.pctBefore
}

// QuotaReport renders current limit state across accounts plus what the ledger
// has learned. Used by the status surface so the operator can see the fuel gauge
// and the savings without reading logs.
func QuotaReport(ctx context.Context) string {
	g := quotaGateInstance()
	if !g.enabled {
		return "quota governance is disabled (ENGINE_QUOTA=0)"
	}
	if g.governor == nil {
		return "quota governance unavailable"
	}
	var b strings.Builder
	b.WriteString(g.governor.Report(ctx))
	if g.ledger != nil {
		// Score first: it is the objective, and the per-config detail below only
		// matters as an explanation of it.
		b.WriteString("\n\n")
		b.WriteString(g.ledger.ScoreReport())
		b.WriteString("\n\n")
		b.WriteString(g.ledger.Report())
	}
	return b.String()
}

// QuotaStatus returns the machine-readable fuel gauge for an external
// supervisor (SARA) to poll.
//
// The whole point of measuring quota is that the thing DECIDING what to build
// can see it. The gate handles the reflex — pace down, pick the roomier account,
// stop before the wall — but only the supervisor can make the strategic call to
// defer a project until the window resets, and it cannot make that call from
// inside this process. So the gauge has to leave the process.
//
// Reads through the prober's cache, so polling this does not itself cost quota.
func QuotaStatus(ctx context.Context) (quota.Status, bool) {
	g := quotaGateInstance()
	if !g.enabled || g.governor == nil {
		return quota.Status{}, false
	}
	return g.governor.Status(ctx), true
}

// RateProject records how delivered work was received, which is the signal the
// whole efficiency score is built on.
//
// This has to come from outside the engine. The engine can see that a run exited
// cleanly and produced text; it cannot see that the result was thrown away, or
// that the user was delighted. Only the supervisor speaking for the user knows
// that, so it tells us, and the rating is attributed back across the runs that
// produced the project in proportion to what each one spent.
//
// projectID is the project path the work was done under. Returns false when
// nothing is on file for it — feedback that arrived after the backlog rolled
// over, or for work this engine did not do.
func RateProject(projectID, satisfaction, note string) (bool, error) {
	s, ok := quota.ParseSatisfaction(satisfaction)
	if !ok {
		return false, fmt.Errorf("unknown satisfaction %q: want praised, accepted, rework or rejected", satisfaction)
	}
	if s == quota.SatisfactionUnknown {
		return false, fmt.Errorf("satisfaction is required")
	}
	g := quotaGateInstance()
	if !g.enabled || g.ledger == nil {
		return false, fmt.Errorf("quota governance is disabled (ENGINE_QUOTA=0); ratings are not being collected")
	}
	return g.ledger.RateProject(projectID, s, note), nil
}

// QuotaScore returns the headline efficiency score — results the user was happy
// with, per percentage point of window — and whether it is known at all.
func QuotaScore() (quota.Scoreboard, bool) {
	g := quotaGateInstance()
	if !g.enabled || g.ledger == nil {
		return quota.Scoreboard{}, false
	}
	return g.ledger.Scoreboard(), true
}

// QuotaLevers are the execution ceilings the governor's current plan implies.
//
// quotaBefore already computes these per dispatch, but a dispatch is the wrong
// granularity for two of them: how many teams may run at once, and how much
// context a prompt may carry, are decided BEFORE the process that would be
// gated is started. So this is the same plan read at the point the decision is
// actually made.
type QuotaLevers struct {
	// MaxConcurrency caps simultaneous agent sessions — used as the parallel
	// team cap.
	MaxConcurrency int
	// SubagentFanout caps how many subagents one session may spawn.
	SubagentFanout int
	// MaxContextTokens is the soft ceiling on assembled prompt context.
	MaxContextTokens int
	// TierName is the governor's current operating mode, for logs.
	TierName string
	// Governed is false when quota governance is off or unreadable; the
	// ceilings above are then the built-in policy defaults, not a live read.
	Governed bool
}

// CurrentQuotaLevers reports the ceilings in force right now.
//
// Deliberately re-read on every call rather than captured once at start-up: the
// whole point of a governor is that a run which begins with room and later hits
// the wall narrows itself instead of carrying its opening allowance to the end.
// Reads through the prober's cache, so calling it in a loop is cheap.
func CurrentQuotaLevers(ctx context.Context) QuotaLevers {
	g := quotaGateInstance()
	policy := g.policy
	out := QuotaLevers{
		MaxConcurrency:   policy.MaxConcurrency,
		SubagentFanout:   policy.MaxSubagents,
		MaxContextTokens: policy.MaxContextTokens,
		TierName:         "ungoverned",
	}
	if !g.enabled || g.governor == nil {
		return out
	}
	plan := g.governor.Decide(ctx)
	out.Governed = true
	out.TierName = plan.TierName
	// A blocked plan means "run nothing", but the ceilings still have to be
	// usable numbers — a caller that asks for the cap while blocked should get
	// 0 concurrency and be told so, not divide by a zero context budget.
	out.MaxConcurrency = plan.MaxConcurrency
	out.SubagentFanout = plan.SubagentFanout
	if plan.MaxContextTokens > 0 {
		out.MaxContextTokens = plan.MaxContextTokens
	}
	return out
}

// QuotaSnapshotLine is a one-line summary for progress messages.
func QuotaSnapshotLine(ctx context.Context) string {
	g := quotaGateInstance()
	if !g.enabled || g.governor == nil {
		return ""
	}
	plan := g.governor.Decide(ctx)
	if plan.Account == "" {
		return fmt.Sprintf("quota: %s", plan.Reason)
	}
	return fmt.Sprintf("quota[%s]: %s", plan.Account, plan.Reason)
}

// observeLimitEvent parses a raw stream line and, when governance is enabled,
// records it against the account's quota pool. Safe to call with any line;
// non-events are ignored.
//
// Parsing is UNCONDITIONAL and recording is what ENGINE_QUOTA gates. Turning off
// automatic pacing is a statement about who decides how hard to run, not a
// request to stop being told that a limit was hit — that message is useful to
// anyone, and suppressing it would make the disabled mode strictly worse than
// the behaviour it replaced.
//
// Deliberately does NOT touch the gate: this runs inline on a live stream, and
// building the gate resolves accounts by shelling out. Nothing here does I/O.
func observeLimitEvent(account, line string) (quota.LimitEvent, bool) {
	ev, ok := quota.ParseLimitEvent(line, time.Now())
	if !ok {
		return quota.LimitEvent{}, false
	}
	if quotaEnabled() {
		if account == "" {
			account = quota.DefaultAccountName
		}
		quotaObserver().Record(account, ev)
	}
	return ev, true
}
