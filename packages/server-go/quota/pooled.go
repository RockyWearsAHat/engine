package quota

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// Pooled snapshot. Two boxes (Mac, PC) share the same Anthropic accounts, so
// the same window. Each box probes its own ~/.claude and sees the same
// number, but each box governs alone. Fix: every box publishes
// GET /quota/snapshot; primary merges by account key; POST /quota/pooled
// pushes the merge back; governor prefers pooled over local while fresh.

// PooledFresh: pooled snapshot older than this → ignored, local probe wins.
const PooledFresh = 2 * time.Minute

// SnapshotAccount is one account as exported over the wire.
type SnapshotAccount struct {
	// Key is Identity.Key() — the pooling unit. Same key on two boxes = same
	// window. Falls back to "name:<name>" when identity unresolved.
	Key  string `json:"key"`
	Name string `json:"name"`
	// SessionPct / WeekPct: percent used, 0-100. -1 = unknown.
	SessionPct float64 `json:"sessionPct"`
	WeekPct    float64 `json:"weekPct"`
	// PacePct: projected end-of-window use of the binding window, 0-100+.
	// -1 = unknown. Target is 100 (PaceTarget).
	PacePct float64 `json:"pacePct"`
	// ResetAt is the binding window's reset, RFC3339. Empty = unknown.
	ResetAt string `json:"resetAt,omitempty"`
	// SessionResetAt / WeekResetAt keep both resets so a receiver can rebuild
	// pace per window, not just the binding one.
	SessionResetAt string `json:"sessionResetAt,omitempty"`
	WeekResetAt    string `json:"weekResetAt,omitempty"`
	// MaxConcurrency is what this box's governor would grant on this account.
	MaxConcurrency int `json:"maxConcurrency"`
}

// PooledSnapshot is the wire shape of GET /quota/snapshot and POST /quota/pooled.
type PooledSnapshot struct {
	Machine        string            `json:"machine"`
	Accounts       []SnapshotAccount `json:"accounts"`
	MaxConcurrency int               `json:"maxConcurrency"`
	GeneratedAt    time.Time         `json:"generatedAt"`
	// Stale is true when the snapshot is older than 2× the refresh interval
	// (or has never been refreshed). Callers should prefer fresh snapshots
	// but can use stale ones as fallback.
	Stale bool `json:"stale,omitempty"`
}

// Fresh reports whether the snapshot is young enough to govern on.
func (p PooledSnapshot) Fresh(now time.Time) bool {
	if p.GeneratedAt.IsZero() {
		return false
	}
	age := now.Sub(p.GeneratedAt)
	return age >= -PooledFresh && age < PooledFresh
}

// Find returns the account with key, if any.
func (p PooledSnapshot) Find(key string) (SnapshotAccount, bool) {
	for _, a := range p.Accounts {
		if a.Key == key {
			return a, true
		}
	}
	return SnapshotAccount{}, false
}

// accountKey: pooling key for a registry account.
func accountKey(a Account) string {
	if k := a.Identity.Key(); k != "unknown" {
		return k
	}
	return "name:" + a.Name
}

// Snapshot exports every usable account's current reading. Reads through the
// prober cache. Local numbers only — never re-exports a pooled snapshot, else
// primaries would echo each other forever.
func (g *Governor) Snapshot(ctx context.Context, machine string) PooledSnapshot {
	now := g.now()
	out := PooledSnapshot{Machine: machine, GeneratedAt: now}
	best := 0
	for _, a := range g.registry.Usable() {
		s := g.prober.Probe(ctx, a)
		s.Account = a.Name
		sa := snapshotAccount(a, s, g.policy, now)
		if sa.MaxConcurrency > best {
			best = sa.MaxConcurrency
		}
		out.Accounts = append(out.Accounts, sa)
	}
	out.MaxConcurrency = best
	return out
}

func snapshotAccount(a Account, s Snapshot, policy Policy, now time.Time) SnapshotAccount {
	sa := SnapshotAccount{Key: accountKey(a), Name: a.Name, SessionPct: -1, WeekPct: -1, PacePct: -1}
	as := Assess(s, now)
	pl := planFor(as.Tier, policy)
	sa.MaxConcurrency = pl.MaxConcurrency
	if !s.Ok {
		return sa
	}
	sa.SessionPct, sa.WeekPct = s.Session.Percent, s.Week.Percent
	if as.PaceKnown {
		sa.PacePct = as.Pace * 100
	}
	if s.Session.HasReset && s.Session.ResetsAt != nil {
		sa.SessionResetAt = s.Session.ResetsAt.Format(time.RFC3339)
	}
	if s.Week.HasReset && s.Week.ResetsAt != nil {
		sa.WeekResetAt = s.Week.ResetsAt.Format(time.RFC3339)
	}
	tight := s.Tightest()
	if tight.HasReset && tight.ResetsAt != nil {
		sa.ResetAt = tight.ResetsAt.Format(time.RFC3339)
	}
	sa.MaxConcurrency = paceConcurrency(pl.MaxConcurrency, policy.MaxConcurrency, as)
	return sa
}

// MergeSnapshots folds many box snapshots into one. Same key on two boxes =
// one account: keep the higher percents (the wall is the wall), newest
// reset, lowest concurrency grant. Unknown (-1) never beats a known number.
func MergeSnapshots(machine string, snaps ...PooledSnapshot) PooledSnapshot {
	out := PooledSnapshot{Machine: machine}
	byKey := map[string]SnapshotAccount{}
	var order []string
	for _, s := range snaps {
		if s.GeneratedAt.After(out.GeneratedAt) {
			out.GeneratedAt = s.GeneratedAt
		}
		for _, a := range s.Accounts {
			if strings.TrimSpace(a.Key) == "" {
				a.Key = "name:" + a.Name
			}
			cur, seen := byKey[a.Key]
			if !seen {
				byKey[a.Key] = a
				order = append(order, a.Key)
				continue
			}
			byKey[a.Key] = mergeAccount(cur, a)
		}
	}
	sort.Strings(order)
	best := 0
	for _, k := range order {
		a := byKey[k]
		out.Accounts = append(out.Accounts, a)
		if a.MaxConcurrency > best {
			best = a.MaxConcurrency
		}
	}
	out.MaxConcurrency = best
	return out
}

func mergeAccount(a, b SnapshotAccount) SnapshotAccount {
	out := a
	if a.Name == "" {
		out.Name = b.Name
	}
	out.SessionPct = maxKnown(a.SessionPct, b.SessionPct)
	out.WeekPct = maxKnown(a.WeekPct, b.WeekPct)
	out.PacePct = maxKnown(a.PacePct, b.PacePct)
	out.ResetAt = laterReset(a.ResetAt, b.ResetAt)
	out.SessionResetAt = laterReset(a.SessionResetAt, b.SessionResetAt)
	out.WeekResetAt = laterReset(a.WeekResetAt, b.WeekResetAt)
	if b.MaxConcurrency > 0 && (a.MaxConcurrency == 0 || b.MaxConcurrency < a.MaxConcurrency) {
		out.MaxConcurrency = b.MaxConcurrency
	}
	return out
}

func maxKnown(a, b float64) float64 {
	if a < 0 {
		return b
	}
	if b < 0 {
		return a
	}
	if b > a {
		return b
	}
	return a
}

func laterReset(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	ta, ea := time.Parse(time.RFC3339, a)
	tb, eb := time.Parse(time.RFC3339, b)
	if ea != nil {
		return b
	}
	if eb != nil {
		return a
	}
	if tb.After(ta) {
		return b
	}
	return a
}

// toSnapshot rebuilds a governor-readable Snapshot from a wire account.
// Unknown (-1) values stay as-is; the window is considered known only when
// both session and week percentages are >= 0.
//
// The week and session reset times may come from multiple field names in the
// pooled record (weekResetAt vs week.resetAt, etc.), so this function tries
// each known variant.
func (sa SnapshotAccount) toSnapshot(name string, fetched time.Time) Snapshot {
	sessionKnown := sa.SessionPct >= 0
	weekKnown := sa.WeekPct >= 0
	s := Snapshot{Account: name, Ok: sessionKnown && weekKnown, FetchedAt: fetched, PlanNote: "pooled"}
	if !s.Ok {
		s.Err = "pooled snapshot carried incomplete reading"
		return s
	}
	s.Session = Window{Name: "session", Label: "Current session", Percent: sa.SessionPct}
	s.Week = Window{Name: "week", Label: "Current week (all models)", Percent: sa.WeekPct}

	// Parse session reset from SessionResetAt or fall back to ResetAt (binding window).
	if sessionReset := parseResetRFC3339(sa.SessionResetAt); sessionReset != nil {
		s.Session.ResetsAt = sessionReset
		s.Session.HasReset = true
	} else if sessionReset := parseResetRFC3339(sa.ResetAt); sessionReset != nil {
		// Fall back to binding window's reset if session-specific one is missing.
		s.Session.ResetsAt = sessionReset
		s.Session.HasReset = true
	}

	// Parse week reset from WeekResetAt or fall back to ResetAt (binding window).
	if weekReset := parseResetRFC3339(sa.WeekResetAt); weekReset != nil {
		s.Week.ResetsAt = weekReset
		s.Week.HasReset = true
	} else if weekReset := parseResetRFC3339(sa.ResetAt); weekReset != nil {
		// Fall back to binding window's reset if week-specific one is missing.
		s.Week.ResetsAt = weekReset
		s.Week.HasReset = true
	}
	return s
}

// parseResetRFC3339 parses an RFC3339 time string and returns a pointer to the parsed time,
// or nil if the string is empty or invalid. This is used for parsing pooled account reset
// times, which come in RFC3339 format from the wire.
func parseResetRFC3339(s string) *time.Time {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t
	}
	return nil
}

func max0(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}

// pooledStore holds the last merged snapshot pushed to this box.
type pooledStore struct {
	mu   sync.RWMutex
	snap PooledSnapshot
	set  bool
}

func (p *pooledStore) put(s PooledSnapshot) {
	p.mu.Lock()
	p.snap, p.set = s, true
	p.mu.Unlock()
}

func (p *pooledStore) get() (PooledSnapshot, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.snap, p.set
}

// SetPooled stores a merged snapshot from the primary.
func (g *Governor) SetPooled(s PooledSnapshot) {
	if s.GeneratedAt.IsZero() {
		s.GeneratedAt = g.now()
	}
	g.pooled.put(s)
}

// Pooled returns the stored snapshot and whether it is still fresh.
func (g *Governor) Pooled() (PooledSnapshot, bool) {
	s, ok := g.pooled.get()
	if !ok {
		return PooledSnapshot{}, false
	}
	return s, s.Fresh(g.now())
}

// readAccount: pooled reading if fresh, keyed, and fully known; else local probe.
// Merge rule: known beats unknown. Known local snapshot always wins over pooled
// with -1 values. Returns source "pooled" or "local".
func (g *Governor) readAccount(ctx context.Context, a Account, local map[string]Snapshot) (Snapshot, string) {
	localSnap := Snapshot{}
	localOk := false
	if local != nil {
		localSnap = local[a.Name]
		localOk = localSnap.Ok
	}

	if p, fresh := g.Pooled(); fresh {
		if sa, ok := p.Find(accountKey(a)); ok {
			// Pooled row exists. Use it only if it has a complete reading AND
			// either local is unknown OR pooled has higher percentages (worst wins).
			if sa.SessionPct >= 0 && sa.WeekPct >= 0 {
				s := sa.toSnapshot(a.Name, p.GeneratedAt)
				if s.Ok && (!localOk || (localSnap.Session.Percent <= sa.SessionPct && localSnap.Week.Percent <= sa.WeekPct)) {
					// Defensively use local snapshot's resets if the pooled snapshot
					// doesn't have them. The pooled merge may drop field names in folding
					// the data back into the window view.
					if localOk {
						if !s.Session.HasReset && localSnap.Session.HasReset && localSnap.Session.ResetsAt != nil {
							s.Session.ResetsAt = localSnap.Session.ResetsAt
							s.Session.HasReset = true
						}
						if !s.Week.HasReset && localSnap.Week.HasReset && localSnap.Week.ResetsAt != nil {
							s.Week.ResetsAt = localSnap.Week.ResetsAt
							s.Week.HasReset = true
						}
					}
					return s, "pooled"
				}
			}
		}
	}

	// Fall back to local probe.
	if local == nil {
		localSnap = g.prober.Probe(ctx, a)
	}
	localSnap.Account = a.Name
	return localSnap, "local"
}
