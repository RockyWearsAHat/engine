package quota

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Same account key on two boxes = one account. Merge keeps worst percent,
// lowest concurrency grant, and one row.
func TestMergeSnapshotsDedupesSameKey(t *testing.T) {
	t0 := at("12:00")
	mac := PooledSnapshot{Machine: "mac", GeneratedAt: t0, Accounts: []SnapshotAccount{
		{Key: "org:1", Name: "work", SessionPct: 40, WeekPct: 20, PacePct: 80, MaxConcurrency: 4},
		{Key: "org:2", Name: "side", SessionPct: 5, WeekPct: 5, PacePct: 10, MaxConcurrency: 8},
	}}
	pc := PooledSnapshot{Machine: "pc", GeneratedAt: t0.Add(time.Minute), Accounts: []SnapshotAccount{
		{Key: "org:1", Name: "work-pc", SessionPct: 55, WeekPct: 18, PacePct: 110, MaxConcurrency: 3},
	}}
	m := MergeSnapshots("mac", mac, pc)
	if len(m.Accounts) != 2 {
		t.Fatalf("accounts = %d, want 2 (org:1 deduped): %+v", len(m.Accounts), m.Accounts)
	}
	w, ok := m.Find("org:1")
	if !ok {
		t.Fatal("org:1 missing")
	}
	if w.SessionPct != 55 || w.WeekPct != 20 || w.PacePct != 110 {
		t.Errorf("merged pct = s%.0f w%.0f p%.0f, want worst of each (55/20/110)", w.SessionPct, w.WeekPct, w.PacePct)
	}
	if w.MaxConcurrency != 3 {
		t.Errorf("maxConcurrency = %d, want 3 (lowest grant)", w.MaxConcurrency)
	}
	if w.Name != "work" {
		t.Errorf("name = %q, want first seen", w.Name)
	}
	if !m.GeneratedAt.Equal(t0.Add(time.Minute)) {
		t.Errorf("generatedAt = %v, want newest", m.GeneratedAt)
	}
	if m.MaxConcurrency != 8 {
		t.Errorf("fleet maxConcurrency = %d, want 8", m.MaxConcurrency)
	}
}

func TestPooledFreshness(t *testing.T) {
	now := at("12:00")
	if !(PooledSnapshot{GeneratedAt: now.Add(-time.Minute)}).Fresh(now) {
		t.Error("1 min old should be fresh")
	}
	if (PooledSnapshot{GeneratedAt: now.Add(-3 * time.Minute)}).Fresh(now) {
		t.Error("3 min old should be stale")
	}
	if (PooledSnapshot{}).Fresh(now) {
		t.Error("zero time never fresh")
	}
}

// Fresh pooled reading beats the local probe; stale one falls back to it.
func TestGovernorPrefersFreshPooled(t *testing.T) {
	now := at("12:00")
	r := &fakeRunner{
		auth:  map[string]string{"work": authJSON("a@example.com", "org-1")},
		usage: map[string]string{"work": usageJSON(10, 10, "2:30pm", "Aug 26 at 12:00pm")},
	}
	reg := NewRegistry(r, Account{Name: "work", ConfigDir: "/w"})
	reg.Resolve(context.Background())
	prober := NewProber(r, time.Minute)
	prober.SetClock(func() time.Time { return now })
	g := NewGovernor(reg, prober, DefaultPolicy())
	g.SetClock(func() time.Time { return now })

	g.SetPooled(PooledSnapshot{Machine: "primary", GeneratedAt: now.Add(-30 * time.Second), Accounts: []SnapshotAccount{
		{Key: "org:org-1", Name: "work", SessionPct: 97, WeekPct: 50, PacePct: 190,
			SessionResetAt: now.Add(150 * time.Minute).Format(time.RFC3339)},
	}})
	st := g.Status(context.Background())
	if st.Accounts[0].Source != "pooled" {
		t.Fatalf("source = %q, want pooled", st.Accounts[0].Source)
	}
	if st.Plan.Tier != TierCritical {
		t.Errorf("tier = %s, want critical from pooled 97%%", st.Plan.Tier)
	}
	if r.calls["work:usage"] != 0 {
		t.Errorf("local probe ran %d times while pooled was fresh", r.calls["work:usage"])
	}

	g.SetPooled(PooledSnapshot{Machine: "primary", GeneratedAt: now.Add(-10 * time.Minute), Accounts: []SnapshotAccount{
		{Key: "org:org-1", Name: "work", SessionPct: 97, WeekPct: 50},
	}})
	st = g.Status(context.Background())
	if st.Accounts[0].Source != "local" {
		t.Fatalf("source = %q, want local after pooled went stale", st.Accounts[0].Source)
	}
	if st.Plan.Tier == TierCritical {
		t.Errorf("stale pooled still governing: %s", st.Plan.Reason)
	}
}

// Fresh pooled row with no reading (-1/-1: primary's own probe failed) must
// not block local probe. Governor probes locally, source = local.
func TestGovernorProbesLocallyWhenPooledRowHasNoReading(t *testing.T) {
	now := at("12:00")
	r := &fakeRunner{
		auth:  map[string]string{"work": authJSON("a@example.com", "org-1")},
		usage: map[string]string{"work": usageJSON(10, 10, "2:30pm", "Aug 26 at 12:00pm")},
	}
	reg := NewRegistry(r, Account{Name: "work", ConfigDir: "/w"})
	reg.Resolve(context.Background())
	prober := NewProber(r, time.Minute)
	prober.SetClock(func() time.Time { return now })
	g := NewGovernor(reg, prober, DefaultPolicy())
	g.SetClock(func() time.Time { return now })

	g.SetPooled(PooledSnapshot{Machine: "primary", GeneratedAt: now.Add(-30 * time.Second), Accounts: []SnapshotAccount{
		{Key: "org:org-1", Name: "work", SessionPct: -1, WeekPct: -1, PacePct: -1},
	}})
	pl := g.Decide(context.Background())
	if r.calls["work:usage"] != 1 {
		t.Fatalf("local probe ran %d times, want 1 (pooled row had no reading)", r.calls["work:usage"])
	}
	st := g.Status(context.Background())
	if st.Accounts[0].Source != "local" {
		t.Fatalf("source = %q, want local when pooled row has no reading", st.Accounts[0].Source)
	}
	if !st.Accounts[0].Ok {
		t.Fatalf("account not ok after local probe: %s", st.Accounts[0].Error)
	}
	if pl.Tier == TierBlocked {
		t.Errorf("blocked despite healthy local probe: %s", pl.Reason)
	}
}

func TestSnapshotExportsKeyAndPace(t *testing.T) {
	now := at("12:00")
	r := &fakeRunner{
		auth:  map[string]string{"work": authJSON("a@example.com", "org-1")},
		usage: map[string]string{"work": usageJSON(10, 10, "2:30pm", "Aug 26 at 12:00pm")},
	}
	reg := NewRegistry(r, Account{Name: "work", ConfigDir: "/w"})
	reg.Resolve(context.Background())
	prober := NewProber(r, time.Minute)
	prober.SetClock(func() time.Time { return now })
	g := NewGovernor(reg, prober, DefaultPolicy())
	g.SetClock(func() time.Time { return now })
	s := g.Snapshot(context.Background(), "mac")
	if s.Machine != "mac" || len(s.Accounts) != 1 {
		t.Fatalf("snapshot = %+v", s)
	}
	a := s.Accounts[0]
	if a.Key != "org:org-1" || a.SessionPct != 10 || a.WeekPct != 10 || a.PacePct < 0 {
		t.Errorf("account = %+v", a)
	}
	if a.MaxConcurrency < 1 || s.MaxConcurrency != a.MaxConcurrency {
		t.Errorf("concurrency = %d / %d", a.MaxConcurrency, s.MaxConcurrency)
	}
}

func TestCostUSDPositiveForUsage(t *testing.T) {
	usd := CostUSD("haiku", 10_000, 2_000, 1_000, 50_000)
	// 10k*1 + 1k*1.25 + 50k*0.1 + 2k*5 = 0.01+0.00125+0.005+0.01 = 0.02625
	if usd < 0.026 || usd > 0.027 {
		t.Fatalf("usd = %v, want ~0.02625", usd)
	}
	if CostUSD("claude-opus-5", 1000, 0, 0, 0) != 0.005 {
		t.Errorf("opus id substring should price")
	}
	if CostUSD("llama3", 1000, 1000, 0, 0) != 0 {
		t.Errorf("unknown model must price 0")
	}
	l := NewLedger(LedgerOptions{})
	l.Record(Outcome{Role: "implement", Config: Config{Model: "haiku", Effort: "low"}, Success: true, Tokens: 63_000, USD: usd, At: at("12:00")})
	st, ok := l.Stat("implement", Config{Model: "haiku", Effort: "low"})
	if !ok || st.USD <= 0 {
		t.Fatalf("stat usd = %v ok=%v, want > 0", st.USD, ok)
	}
}

func TestAccountsFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	if _, err := AccountsFromFile(path); err != nil {
		t.Fatalf("missing file must be nil, got %v", err)
	}
	os.WriteFile(path, []byte(`[{"name":"work","configDir":"/w"},{"name":"work","configDir":"/w2"},{"name":"dup","configDir":"/w"},{"configDir":"/side"}]`), 0o600)
	accs, err := AccountsFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(accs) != 2 || accs[0].Name != "work" || accs[1].Name != "side" || accs[1].ConfigDir != "/side" {
		t.Fatalf("accounts = %+v", accs)
	}
	t.Setenv(AccountsFileEnv, path)
	got, note := LoadAccounts("env=/e")
	if len(got) != 2 || note == "" {
		t.Fatalf("LoadAccounts = %+v (%s)", got, note)
	}
	t.Setenv(AccountsFileEnv, filepath.Join(dir, "nope.json"))
	got, _ = LoadAccounts("env=/e")
	if len(got) != 1 || got[0].Name != "env" {
		t.Fatalf("env fallback = %+v", got)
	}
}

// Snapshot after probe returns real 64% week -> weekPct is 64, not -1.
func TestSnapshotAfterProbeReturns64Week(t *testing.T) {
	now := at("12:00")
	r := &fakeRunner{
		auth:  map[string]string{"work": authJSON("a@example.com", "org-1")},
		usage: map[string]string{"work": usageJSON(10, 64, "2:30pm", "Aug 26 at 12:00pm")},
	}
	reg := NewRegistry(r, Account{Name: "work", ConfigDir: "/w"})
	reg.Resolve(context.Background())
	prober := NewProber(r, time.Minute)
	prober.SetClock(func() time.Time { return now })
	g := NewGovernor(reg, prober, DefaultPolicy())
	g.SetClock(func() time.Time { return now })

	s := g.Snapshot(context.Background(), "mac")
	if len(s.Accounts) != 1 {
		t.Fatalf("accounts = %d, want 1", len(s.Accounts))
	}
	a := s.Accounts[0]
	if a.WeekPct != 64 {
		t.Errorf("weekPct = %.0f, want 64", a.WeekPct)
	}
	if a.SessionPct != 10 {
		t.Errorf("sessionPct = %.0f, want 10", a.SessionPct)
	}
}

// SetPooled with -1 row over a known local 64 -> source stays local, not pooled.
func TestPooledWithUnknownDoesNotOverrideKnownLocal(t *testing.T) {
	now := at("12:00")
	r := &fakeRunner{
		auth:  map[string]string{"work": authJSON("a@example.com", "org-1")},
		usage: map[string]string{"work": usageJSON(10, 64, "2:30pm", "Aug 26 at 12:00pm")},
	}
	reg := NewRegistry(r, Account{Name: "work", ConfigDir: "/w"})
	reg.Resolve(context.Background())
	prober := NewProber(r, time.Minute)
	prober.SetClock(func() time.Time { return now })
	g := NewGovernor(reg, prober, DefaultPolicy())
	g.SetClock(func() time.Time { return now })

	// Set pooled with unknown (-1) values
	g.SetPooled(PooledSnapshot{Machine: "primary", GeneratedAt: now.Add(-30 * time.Second), Accounts: []SnapshotAccount{
		{Key: "org:org-1", Name: "work", SessionPct: -1, WeekPct: -1, PacePct: -1},
	}})

	st := g.Status(context.Background())
	if st.Accounts[0].Source != "local" {
		t.Fatalf("source = %q, want local (pooled row has -1 values)", st.Accounts[0].Source)
	}
	if st.Accounts[0].Week.Percent != 64 {
		t.Errorf("week%% = %.0f, want 64 from local", st.Accounts[0].Week.Percent)
	}
	if st.Accounts[0].Week.Known != true {
		t.Errorf("week known = %v, want true", st.Accounts[0].Week.Known)
	}
}

// Never-probed account -> snapshot ok:false, pct -1.
func TestNeverProbedAccountSnapshotUnknown(t *testing.T) {
	now := at("12:00")
	r := &fakeRunner{
		auth:  map[string]string{"work": authJSON("a@example.com", "org-1")},
		usage: map[string]string{}, // No usage data for "work"
	}
	reg := NewRegistry(r, Account{Name: "work", ConfigDir: "/w"})
	reg.Resolve(context.Background())
	prober := NewProber(r, time.Minute)
	prober.SetClock(func() time.Time { return now })
	g := NewGovernor(reg, prober, DefaultPolicy())
	g.SetClock(func() time.Time { return now })

	s := g.Snapshot(context.Background(), "mac")
	if len(s.Accounts) != 1 {
		t.Fatalf("accounts = %d, want 1", len(s.Accounts))
	}
	a := s.Accounts[0]
	if a.SessionPct != -1 {
		t.Errorf("sessionPct = %.0f, want -1 (unknown)", a.SessionPct)
	}
	if a.WeekPct != -1 {
		t.Errorf("weekPct = %.0f, want -1 (unknown)", a.WeekPct)
	}
}

// Pooled record with week reset in weekResetAt field (the gateway merge format)
// should carry the reset through to the status and compute weekPaceKnown correctly.
func TestPooledRecordWithWeekResetKeepsItInStatus(t *testing.T) {
	now := at("12:00")
	r := &fakeRunner{
		auth:  map[string]string{"default": authJSON("user@example.com", "org-default")},
		usage: map[string]string{"default": usageJSON(8, 16, "11:59pm", "Sep 5 at 3:00pm")},
	}
	reg := NewRegistry(r, Account{Name: "default", ConfigDir: "/config"})
	reg.Resolve(context.Background())
	prober := NewProber(r, time.Minute)
	prober.SetClock(func() time.Time { return now })
	g := NewGovernor(reg, prober, DefaultPolicy())
	g.SetClock(func() time.Time { return now })

	// Simulate the gateway's merge output: SnapshotAccount with flat weekResetAt
	// and sessionResetAt fields (not nested). The week reset is at Sep 5 @ 3pm (15:00).
	weekResetTime := now.Add(27 * time.Hour) // Will reset tomorrow at 3pm
	// Session reset is within the 5-hour window (in about 4 hours)
	sessionResetTime := now.Add(4 * time.Hour)
	g.SetPooled(PooledSnapshot{
		Machine:     "primary",
		GeneratedAt: now,
		Accounts: []SnapshotAccount{
			{
				Key:            "org:org-default",
				Name:           "default",
				SessionPct:     8,
				WeekPct:        16,
				PacePct:        50,
				SessionResetAt: sessionResetTime.Format(time.RFC3339),
				WeekResetAt:    weekResetTime.Format(time.RFC3339),
				MaxConcurrency: 2,
			},
		},
		MaxConcurrency: 2,
	})

	// Get status and verify week reset is present and weekPaceKnown is computed
	st := g.Status(context.Background())
	if len(st.Accounts) != 1 {
		t.Fatalf("accounts = %d, want 1", len(st.Accounts))
	}
	acc := st.Accounts[0]

	if acc.Source != "pooled" {
		t.Errorf("source = %q, want pooled", acc.Source)
	}

	// Week window should have the reset time
	if acc.Week.ResetsAt == "" {
		t.Error("week.resetsAt is empty, should have the reset time")
	}
	if acc.Week.ResetsIn == "" {
		t.Error("week.resetsIn is empty, should be computed")
	}
	if !acc.Week.Known {
		t.Error("week.known should be true")
	}

	// Assessment should have weekPaceKnown=true (not false as in the bug)
	if !acc.Assessment.WeekPaceKnown {
		t.Errorf("weekPaceKnown = false, want true (week reset is now present)")
	}
	if acc.Assessment.WeekPace == 0 {
		t.Errorf("weekPace = 0, want non-zero (should be computed from 16%% usage and ~27h until reset)")
	}

	// Session should also have the reset
	if acc.Session.ResetsAt == "" {
		t.Error("session.resetsAt is empty, should have the reset time")
	}
	if !acc.Assessment.SessionPaceKnown {
		t.Errorf("sessionPaceKnown = false, want true (session reset is present)")
	}
}
