// Package quota gives Engine awareness of the Claude subscription limits it is
// actually spending, and a governor that decides how hard to push against them.
//
// # WHY THIS EXISTS
//
// Engine's role routing calls the Claude Max subscription "flat ~$0 marginal
// cost". That is true in dollars and false in every way that matters: the
// subscription is rationed by a rolling 5-hour session window and a 7-day week
// window, and when either is exhausted every agent stops at once. So the real
// currency Engine spends is not dollars, it is *quota*, and until now nothing in
// the process could see the balance. An autonomous builder that cannot see its
// own fuel gauge cannot pace itself: it either idles far below what it is
// allowed to use, or it burns a week's allowance overnight and goes dark.
//
// # WHERE THE NUMBERS COME FROM
//
// Claude Code exposes limit state in exactly three places, and only one of them
// is both precise and free:
//
//   - `claude -p "/usage"` — a LOCAL slash command. It resolves without an
//     inference turn (num_turns 0, total_cost_usd 0), works headlessly, needs no
//     credential handling by us, and returns exact percentages, reset times, AND
//     an attribution breakdown of what is driving the usage. This is the source
//     this package reads. See Probe.
//   - the `rate_limit_event` stream event, emitted by every `claude -p` run the
//     engine already makes. It carries status/window/reset but the SDK
//     deliberately strips utilization, so it is a free freshness signal between
//     probes and an instant "we just got rejected" alarm — not a gauge. See
//     Observe.
//   - the `anthropic-ratelimit-unified-*` response headers on a 1-token
//     "quota check" API call. Precise, but costs a request and requires reading
//     the OAuth token out of the Keychain. Deliberately NOT used: /usage gives
//     the same numbers for free.
//
// Everything here degrades rather than fails. A snapshot that could not be taken
// is an Unknown snapshot, and the governor treats Unknown as "proceed at the
// conservative default" — never as "0% used, go wild".
package quota

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Window is one rationing window: how much of it is spent and when it refills.
//
// Percent is 0-100 as reported. ResetsAt is zero when the source did not print a
// reset (per-model week lines often omit it); callers must check HasReset before
// reasoning about time, because a zero time silently reads as "reset in 1970",
// which would make every pace calculation conclude the window is infinitely
// overdue.
type Window struct {
	// Name is the canonical window key: "session" (5h), "week" (7d, all models),
	// or "week:<model>" for a per-model weekly sub-limit.
	Name string `json:"name"`
	// Label is the human title as printed, e.g. "Current week (all models)".
	Label string `json:"label"`
	// Percent used, 0-100.
	Percent float64 `json:"percent"`
	// ResetsAt is when this window refills. Only meaningful if HasReset.
	// Omitted from JSON when nil (reset time unknown).
	ResetsAt *time.Time `json:"resetsAt,omitempty"`
	HasReset bool       `json:"hasReset"`
}

// Remaining is the unspent share of the window, 0-1.
func (w Window) Remaining() float64 {
	r := 1 - w.Percent/100
	return clamp01(r)
}

// Used is the spent share of the window, 0-1.
func (w Window) Used() float64 { return clamp01(w.Percent / 100) }

// Share is one attributed contributor: a name and the percentage of usage it
// accounted for.
type Share struct {
	Name    string  `json:"name"`
	Percent float64 `json:"percent"`
}

// Behaviour keys. These are the levers the governor can actually pull, which is
// why they are canonicalised rather than left as prose: each one names a
// decision Engine makes on every run.
const (
	// BehaviourHighContext: share of usage spent in sessions over ~150k context.
	// Lever: context trimming / earlier compaction.
	BehaviourHighContext = "high_context"
	// BehaviourSubagentHeavy: share from subagent-heavy sessions.
	// Lever: subagent fan-out width.
	BehaviourSubagentHeavy = "subagent_heavy"
	// BehaviourParallelSessions: share spent while many sessions ran at once.
	// Lever: orchestrator concurrency.
	BehaviourParallelSessions = "parallel_sessions"
	// BehaviourLongSessions: share from very long-lived sessions.
	// Lever: session recycling.
	BehaviourLongSessions = "long_sessions"
)

// Period is the attribution block for one lookback window ("Last 24h" /
// "Last 7d"). It answers *what* the quota went to, which is what makes
// "use less" actionable rather than aspirational.
type Period struct {
	// Span is "24h" or "7d" as printed.
	Span     string `json:"span"`
	Requests int    `json:"requests"`
	Sessions int    `json:"sessions"`
	// Behaviours maps a Behaviour* key to the percentage of usage it explained.
	// These are independent characteristics, NOT a partition — they overlap and
	// do not sum to 100. Treating them as a breakdown is the obvious misreading,
	// so never normalise them.
	Behaviours map[string]float64 `json:"behaviours,omitempty"`
	// Raw behaviour lines, kept verbatim so a phrasing change upstream degrades
	// to "unparsed but visible" instead of "silently dropped".
	RawBehaviours []string `json:"rawBehaviours,omitempty"`

	TopSkills     []Share `json:"topSkills,omitempty"`
	TopSubagents  []Share `json:"topSubagents,omitempty"`
	TopPlugins    []Share `json:"topPlugins,omitempty"`
	TopMCPServers []Share `json:"topMcpServers,omitempty"`
}

// Behaviour returns the percentage recorded for key, and whether it was present.
func (p Period) Behaviour(key string) (float64, bool) {
	if p.Behaviours == nil {
		return 0, false
	}
	v, ok := p.Behaviours[key]
	return v, ok
}

// Snapshot is one reading of one account's limit state.
//
// Ok distinguishes "we measured this" from "we could not measure and these are
// zero values". Every consumer must branch on it; the whole point of this
// package is that an unknown balance is not the same as an empty one.
type Snapshot struct {
	// Account is the registry name of the account this reading belongs to.
	Account string `json:"account"`
	// Ok is false when the probe failed or the output could not be parsed.
	Ok bool `json:"ok"`
	// Err is the failure reason when !Ok.
	Err string `json:"err,omitempty"`
	// FetchedAt is when this reading was taken.
	FetchedAt time.Time `json:"fetchedAt"`

	// PlanNote is the leading line ("You are currently using your subscription
	// to power your Claude Code usage"), kept for display and for spotting the
	// day the account silently falls back to API credits.
	PlanNote string `json:"planNote,omitempty"`

	// Session is the rolling 5-hour window.
	Session Window `json:"session"`
	// Week is the 7-day all-models window.
	Week Window `json:"week"`
	// PerModel holds weekly sub-limits ("Current week (Fable)") keyed in order
	// of appearance. A model with its own exhausted sub-limit blocks that model
	// while the account overall is still fine — which is why these are kept
	// rather than folded into Week.
	PerModel []Window `json:"perModel,omitempty"`

	Last24h Period `json:"last24h"`
	Last7d  Period `json:"last7d"`

	// Raw is the exact text parsed, so a format drift is diagnosable from a
	// stored snapshot instead of only reproducible live.
	Raw string `json:"raw,omitempty"`
}

// Windows returns session, week, and every per-model window in one slice, which
// is what a "can I run at all" check wants: the binding constraint is whichever
// window is fullest, regardless of which one it is.
func (s Snapshot) Windows() []Window {
	out := make([]Window, 0, 2+len(s.PerModel))
	out = append(out, s.Session, s.Week)
	out = append(out, s.PerModel...)
	return out
}

// ModelWindow returns the weekly sub-limit for a model by display name
// (case-insensitive, matched on substring so "opus" finds "Opus 4.8").
func (s Snapshot) ModelWindow(model string) (Window, bool) {
	needle := strings.ToLower(strings.TrimSpace(model))
	if needle == "" {
		return Window{}, false
	}
	for _, w := range s.PerModel {
		name := strings.ToLower(modelOf(w.Name))
		// Match either direction: the caller may ask for "opus" against a window
		// named "opus 4.8", or for the full model id against a short window name.
		if name != "" && (strings.Contains(name, needle) || strings.Contains(needle, name)) {
			return w, true
		}
		if strings.Contains(strings.ToLower(w.Label), needle) {
			return w, true
		}
	}
	return Window{}, false
}

// Tightest returns the window closest to exhaustion. This is the one that will
// stop the engine, so it is the one worth pacing against.
func (s Snapshot) Tightest() Window {
	ws := s.Windows()
	best := ws[0]
	for _, w := range ws[1:] {
		if w.Percent > best.Percent {
			best = w
		}
	}
	return best
}

// Summary renders a one-line human/log form.
func (s Snapshot) Summary() string {
	return fmt.Sprintf("quota[%s]: %s", s.Account, s.Detail())
}

// Detail is Summary without the account prefix, for renderings that already have
// the account in a column of their own.
func (s Snapshot) Detail() string {
	if !s.Ok {
		return fmt.Sprintf("unknown (%s)", s.Err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "session %.0f%%", s.Session.Percent)
	if s.Session.HasReset && s.Session.ResetsAt != nil {
		fmt.Fprintf(&b, " (resets %s)", s.Session.ResetsAt.Format(time.Kitchen))
	}
	fmt.Fprintf(&b, ", week %.0f%%", s.Week.Percent)
	if s.Week.HasReset && s.Week.ResetsAt != nil {
		fmt.Fprintf(&b, " (resets %s)", s.Week.ResetsAt.Format("Jan 2"))
	}
	for _, w := range s.PerModel {
		fmt.Fprintf(&b, ", %s %.0f%%", modelOf(w.Name), w.Percent)
	}
	return b.String()
}

// modelOf extracts the model part of a "week:<model>" window name.
func modelOf(name string) string {
	if strings.HasPrefix(name, "week:") {
		return strings.TrimPrefix(name, "week:")
	}
	return ""
}

// topShares sorts a share list descending and trims to n.
func topShares(in []Share, n int) []Share {
	out := append([]Share(nil), in...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Percent > out[j].Percent })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Max(0, math.Min(1, v))
}
