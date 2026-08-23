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
}

// quotaBefore consults the gate ahead of a claudecode dispatch.
//
// requestedModel is what routing already chose. The gate NEVER upgrades it —
// asking for Sonnet and getting Opus back would be a nasty surprise and the
// opposite of the objective — it only ever holds it or moves it cheaper.
func quotaBefore(ctx context.Context, role AgentRole, requestedModel, projectID string, parentEnv []string) QuotaDispatch {
	g := quotaGateInstance()
	out := QuotaDispatch{Allow: true, Model: requestedModel, Role: role, ProjectID: projectID, startedAt: time.Now()}
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
	if !g.enabled || g.ledger == nil || !d.Governed || d.Config.Model == "" {
		return
	}
	g.ledger.Record(quota.Outcome{
		Role:      quotaRoleKey(d.Role),
		Config:    d.Config,
		Success:   success,
		Tokens:    stats.TotalTokens(),
		ProjectID: d.ProjectID,
		Duration:  time.Since(d.startedAt),
		At:        time.Now(),
	})
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
