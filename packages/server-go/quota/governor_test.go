package quota

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func at(hhmm string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", "2026-08-23 "+hhmm)
	if err != nil {
		panic(err)
	}
	return t
}

func TestPace(t *testing.T) {
	now := at("12:00")
	tests := []struct {
		name    string
		w       Window
		dur     time.Duration
		want    float64
		wantOk  bool
		epsilon float64
	}{
		{
			// Half the window gone, half the quota gone: exactly on pace.
			name:   "on pace",
			w:      Window{Percent: 50, ResetsAt: now.Add(150 * time.Minute), HasReset: true},
			dur:    SessionWindow,
			want:   1.0,
			wantOk: true,
		},
		{
			// Half the window gone, all the quota gone: will hit the wall.
			name:   "double pace",
			w:      Window{Percent: 100, ResetsAt: now.Add(150 * time.Minute), HasReset: true},
			dur:    SessionWindow,
			want:   2.0,
			wantOk: true,
		},
		{
			// Most of the window gone, barely used: will waste most of it.
			name:   "under pace wastes the window",
			w:      Window{Percent: 20, ResetsAt: now.Add(30 * time.Minute), HasReset: true},
			dur:    SessionWindow,
			want:   0.222,
			wantOk: true,
		},
		{
			name:   "no reset time is unknown, not zero",
			w:      Window{Percent: 50},
			dur:    SessionWindow,
			wantOk: false,
		},
		{
			// The guard that stops a fresh window producing an infinite pace.
			name:   "start of window is clamped",
			w:      Window{Percent: 1, ResetsAt: now.Add(SessionWindow), HasReset: true},
			dur:    SessionWindow,
			want:   0.5,
			wantOk: true,
		},
		{
			name:   "reset beyond the window length is not trusted",
			w:      Window{Percent: 50, ResetsAt: now.Add(9 * time.Hour), HasReset: true},
			dur:    SessionWindow,
			wantOk: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Pace(tt.w, tt.dur, now)
			if ok != tt.wantOk {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOk)
			}
			if !ok {
				return
			}
			eps := tt.epsilon
			if eps == 0 {
				eps = 0.01
			}
			if diff := got - tt.want; diff > eps || diff < -eps {
				t.Errorf("pace = %.3f, want %.3f", got, tt.want)
			}
		})
	}
}

func TestAssessTiers(t *testing.T) {
	now := at("12:00")
	// halfway through both windows, so pace == 2x utilization
	midSession := now.Add(150 * time.Minute)
	midWeek := now.Add(84 * time.Hour)

	snap := func(session, week float64) Snapshot {
		return Snapshot{
			Ok:      true,
			Session: Window{Name: "session", Percent: session, ResetsAt: midSession, HasReset: true},
			Week:    Window{Name: "week", Percent: week, ResetsAt: midWeek, HasReset: true},
		}
	}

	tests := []struct {
		name string
		s    Snapshot
		want Tier
	}{
		{"exhausted blocks", snap(100, 40), TierBlocked},
		{"nearly exhausted is critical", snap(96, 40), TierCritical},
		{"high utilization conserves", snap(85, 40), TierConserve},
		{"overspending pace conserves even when utilization is low", snap(70, 20), TierConserve},
		{"on pace is steady", snap(50, 30), TierSteady},
		{"well under pace expands", snap(20, 10), TierExpand},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Assess(tt.s, now)
			if got.Tier != tt.want {
				t.Errorf("tier = %s (%s), want %s", got.Tier, got.Reason, tt.want)
			}
		})
	}
}

// The single most important behaviour in the package: a reading we could not
// take must never be treated as a reading of zero.
func TestAssessUnknownIsNotEmpty(t *testing.T) {
	now := at("12:00")
	got := Assess(Snapshot{Ok: false, Err: "probe timed out"}, now)
	if got.Tier != TierSteady {
		t.Errorf("unknown tier = %s, want steady", got.Tier)
	}
	if got.Known {
		t.Error("Known should be false for an unreadable snapshot")
	}
	if got.Tier == TierExpand {
		t.Error("an unknown balance must never authorise expansion")
	}
	if !strings.Contains(got.Reason, "unknown") {
		t.Errorf("reason should say the state is unknown, got %q", got.Reason)
	}
}

// A per-model weekly sub-limit can be the binding constraint while the overall
// account looks healthy.
func TestAssessPerModelWindowBinds(t *testing.T) {
	now := at("12:00")
	s := Snapshot{
		Ok:      true,
		Session: Window{Name: "session", Percent: 10, ResetsAt: now.Add(150 * time.Minute), HasReset: true},
		Week:    Window{Name: "week", Percent: 20, ResetsAt: now.Add(84 * time.Hour), HasReset: true},
		PerModel: []Window{
			{Name: "week:fable", Label: "Current week (Fable)", Percent: 97},
		},
	}
	got := Assess(s, now)
	if got.Tier != TierCritical {
		t.Errorf("tier = %s, want critical (per-model window at 97%%)", got.Tier)
	}
	if got.Binding != "week:fable" {
		t.Errorf("binding = %q, want week:fable", got.Binding)
	}
}

func TestPlanForCutsCheapLeversFirst(t *testing.T) {
	p := DefaultPolicy()
	conserve := planFor(TierConserve, p)
	expand := planFor(TierExpand, p)

	if conserve.MaxContextTokens >= expand.MaxContextTokens {
		t.Error("conserve should trim context before anything else")
	}
	if conserve.MaxConcurrency >= expand.MaxConcurrency {
		t.Error("conserve should reduce concurrency")
	}
	// The deliberate choice: quality-affecting levers move last, so a conserving
	// engine still runs the good model on a smaller context.
	if conserve.Model != p.HeavyModel {
		t.Errorf("conserve model = %q, want the heavy model — downgrading the model before trimming context saves less and hurts more", conserve.Model)
	}
	if crit := planFor(TierCritical, p); crit.Model != p.CheapModel {
		t.Errorf("critical model = %q, want the cheap model", crit.Model)
	}
	if blocked := planFor(TierBlocked, p); blocked.Allow {
		t.Error("blocked plan must not allow dispatch")
	}
}

// fakeRunner scripts CLI output per (account, subcommand). It is mutex-guarded
// because ProbeAll and Registry.Resolve call Run for several accounts at once,
// which is part of the Runner contract.
type fakeRunner struct {
	auth  map[string]string
	usage map[string]string
	errs  map[string]error

	mu    sync.Mutex
	calls map[string]int
}

func (f *fakeRunner) callCount(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[key]
}

func (f *fakeRunner) Run(_ context.Context, a Account, args ...string) (string, error) {
	kind := "usage"
	if len(args) > 0 && args[0] == "auth" {
		kind = "auth"
	}
	f.mu.Lock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[a.Name+":"+kind]++
	f.mu.Unlock()

	if err, ok := f.errs[a.Name+":"+kind]; ok {
		return "", err
	}
	if kind == "auth" {
		if v, ok := f.auth[a.Name]; ok {
			return v, nil
		}
		return "", fmt.Errorf("no auth scripted for %s", a.Name)
	}
	if v, ok := f.usage[a.Name]; ok {
		return v, nil
	}
	return "", fmt.Errorf("no usage scripted for %s", a.Name)
}

func authJSON(email, org string) string {
	return fmt.Sprintf(`{"loggedIn":true,"authMethod":"oauth","apiProvider":"anthropic","email":%q,"orgId":%q,"subscriptionType":"max"}`, email, org)
}

func usageJSON(sessionPct, weekPct int, sessionReset, weekReset string) string {
	text := fmt.Sprintf(`Current session: %d%% used · resets %s
Current week (all models): %d%% used · resets %s`, sessionPct, sessionReset, weekPct, weekReset)
	b, _ := jsonMarshalString(text)
	return fmt.Sprintf(`{"is_error":false,"num_turns":0,"total_cost_usd":0,"result":%s}`, b)
}

func jsonMarshalString(s string) (string, error) {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String(), nil
}

// The correctness point that makes multi-account support worth anything: two
// config directories logged into the SAME Anthropic account share one quota
// pool, and scheduling them as independent would double the headroom the engine
// believes it has.
func TestRegistryCollapsesSharedQuotaPools(t *testing.T) {
	r := &fakeRunner{auth: map[string]string{
		"work":  authJSON("a@example.com", "org-1"),
		"dupe":  authJSON("a@example.com", "org-1"),
		"side":  authJSON("b@example.com", "org-2"),
		"nolog": `{"loggedIn":false}`,
	}}
	reg := NewRegistry(r,
		Account{Name: "work", ConfigDir: "/tmp/work"},
		Account{Name: "dupe", ConfigDir: "/tmp/dupe"},
		Account{Name: "side", ConfigDir: "/tmp/side"},
		Account{Name: "nolog", ConfigDir: "/tmp/nolog"},
	)
	reg.Resolve(context.Background())

	usable := reg.Names()
	if len(usable) != 2 || usable[0] != "side" || usable[1] != "work" {
		t.Fatalf("usable = %v, want [side work]", usable)
	}
	dupe, _ := reg.Get("dupe")
	if !dupe.Disabled || !strings.Contains(dupe.DisabledReason, "shares a quota pool") {
		t.Errorf("dupe should be disabled for pool sharing, got disabled=%v reason=%q", dupe.Disabled, dupe.DisabledReason)
	}
	// Disabled accounts stay visible, so the operator can see why.
	if len(reg.All()) != 4 {
		t.Errorf("All() dropped accounts: %d, want 4", len(reg.All()))
	}
}

func TestAccountEnvReplacesConfigDir(t *testing.T) {
	a := Account{Name: "side", ConfigDir: "/tmp/side"}
	env := a.Env([]string{"PATH=/bin", "CLAUDE_CONFIG_DIR=/old", "HOME=/h"})
	var found int
	for _, kv := range env {
		if strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			found++
			if kv != "CLAUDE_CONFIG_DIR=/tmp/side" {
				t.Errorf("config dir = %q", kv)
			}
		}
	}
	if found != 1 {
		t.Errorf("CLAUDE_CONFIG_DIR appears %d times, want exactly 1 — a duplicate leaves resolution up to the OS", found)
	}
	// An account with no dir inherits, unchanged.
	amb := Account{Name: "default"}
	if got := amb.Env([]string{"CLAUDE_CONFIG_DIR=/old"}); got[0] != "CLAUDE_CONFIG_DIR=/old" {
		t.Errorf("ambient account should inherit, got %v", got)
	}
}

func TestGovernorPicksAccountWithHeadroom(t *testing.T) {
	now := at("12:00")
	sessionReset := "2:30pm"
	weekReset := "Aug 26 at 12:00pm"

	r := &fakeRunner{
		auth: map[string]string{
			"work": authJSON("a@example.com", "org-1"),
			"side": authJSON("b@example.com", "org-2"),
		},
		usage: map[string]string{
			"work": usageJSON(92, 80, sessionReset, weekReset),
			"side": usageJSON(10, 15, sessionReset, weekReset),
		},
	}
	reg := NewRegistry(r, Account{Name: "work", ConfigDir: "/w"}, Account{Name: "side", ConfigDir: "/s"})
	reg.Resolve(context.Background())

	prober := NewProber(r, time.Minute)
	prober.SetClock(func() time.Time { return now })
	g := NewGovernor(reg, prober, DefaultPolicy())
	g.SetClock(func() time.Time { return now })

	plan := g.Decide(context.Background())
	if !plan.Allow {
		t.Fatalf("expected dispatch to be allowed: %s", plan.Reason)
	}
	if plan.Account != "side" {
		t.Errorf("account = %q, want side (the one with headroom)", plan.Account)
	}
	if plan.ConfigDir != "/s" {
		t.Errorf("configDir = %q, want /s", plan.ConfigDir)
	}
	// Guard against passing vacuously: if neither account's usage parsed, both
	// would be Unknown/Steady and the alphabetical tie-break would also land on
	// "side". Assert the reading was real and the tiers actually differed.
	if !plan.Assessment.Known {
		t.Fatal("the chosen account's snapshot did not parse — this test would pass for the wrong reason")
	}
	if plan.Tier != TierExpand {
		t.Errorf("tier = %s, want expand for an account at 10%%/15%%", plan.Tier)
	}
	work := Assess(prober.mustCached(t, "work"), now)
	if work.Tier != TierConserve && work.Tier != TierCritical {
		t.Errorf("the busy account assessed as %s; expected it to be constrained", work.Tier)
	}
}

func (p *Prober) mustCached(t *testing.T, account string) Snapshot {
	t.Helper()
	s, _ := p.Cached(account)
	if !s.Ok {
		t.Fatalf("no readable snapshot cached for %q: %s", account, s.Err)
	}
	return s
}

// A rejection observed on the wire outranks a cached percentage that still looks
// healthy — the server has already said no.
func TestGovernorHonoursObservedRejection(t *testing.T) {
	now := at("12:00")
	r := &fakeRunner{
		auth:  map[string]string{"solo": authJSON("a@example.com", "org-1")},
		usage: map[string]string{"solo": usageJSON(30, 30, "2:30pm", "Aug 26 at 12:00pm")},
	}
	reg := NewRegistry(r, Account{Name: "solo", ConfigDir: "/s"})
	reg.Resolve(context.Background())
	prober := NewProber(r, time.Minute)
	prober.SetClock(func() time.Time { return now })
	g := NewGovernor(reg, prober, DefaultPolicy())
	g.SetClock(func() time.Time { return now })

	if plan := g.Decide(context.Background()); !plan.Allow {
		t.Fatalf("baseline should allow: %s", plan.Reason)
	}

	line := `{"type":"rate_limit_event","status":"rejected","rateLimitType":"five_hour","resetsAt":"2026-08-23T14:30:00Z","isUsingOverage":false}`
	if _, ok := g.Observer().Observe("solo", line, now); !ok {
		t.Fatal("failed to parse the rate_limit_event")
	}

	plan := g.Decide(context.Background())
	if plan.Allow {
		t.Fatal("a rejected account must not be dispatched to")
	}
	if plan.RetryAfter <= 0 {
		t.Error("a blocked plan must carry a wait, so the caller sleeps instead of spinning")
	}
	if plan.RetryAfter > SessionWindow {
		t.Errorf("retryAfter = %s, longer than the window itself", plan.RetryAfter)
	}
}

// Paid overage looks healthy in the percentages and is not: the subscription has
// stopped being flat-cost. The objective says pull back, not lean in.
func TestGovernorPullsBackOnPaidOverage(t *testing.T) {
	now := at("12:00")
	r := &fakeRunner{
		auth: map[string]string{"solo": authJSON("a@example.com", "org-1")},
		// Low utilization — without the overage signal this would read as Expand.
		usage: map[string]string{"solo": usageJSON(15, 12, "2:30pm", "Aug 26 at 12:00pm")},
	}
	reg := NewRegistry(r, Account{Name: "solo", ConfigDir: "/s"})
	reg.Resolve(context.Background())
	prober := NewProber(r, time.Minute)
	prober.SetClock(func() time.Time { return now })
	g := NewGovernor(reg, prober, DefaultPolicy())
	g.SetClock(func() time.Time { return now })

	if plan := g.Decide(context.Background()); plan.Tier != TierExpand {
		t.Fatalf("baseline tier = %s, want expand — otherwise this test proves nothing", plan.Tier)
	}

	line := `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","rateLimitType":"five_hour","isUsingOverage":true}}`
	if _, ok := g.Observer().Observe("solo", line, now); !ok {
		t.Fatal("failed to parse the overage event")
	}

	plan := g.Decide(context.Background())
	if !plan.Allow {
		t.Error("overage should throttle, not stop — the work still needs doing")
	}
	if plan.Tier > TierConserve {
		t.Errorf("tier = %s while paying overage; want conserve or tighter", plan.Tier)
	}
	if !strings.Contains(plan.Reason, "OVERAGE") {
		t.Errorf("reason should name the overage plainly, got %q", plan.Reason)
	}
}

// Between comparable accounts, spend the one whose quota expires soonest —
// unspent capacity does not roll over.
func TestScorePrefersSoonerReset(t *testing.T) {
	soon := Assessment{Account: "soon", Tier: TierSteady, Known: true, Headroom: 0.5, ResetsIn: 1 * time.Hour}
	later := Assessment{Account: "later", Tier: TierSteady, Known: true, Headroom: 0.5, ResetsIn: 90 * time.Hour}
	if Score(soon) <= Score(later) {
		t.Error("the account whose window resets sooner should be spent first")
	}

	// But headroom still dominates timing.
	roomy := Assessment{Account: "roomy", Tier: TierSteady, Known: true, Headroom: 0.9, ResetsIn: 90 * time.Hour}
	tight := Assessment{Account: "tight", Tier: TierSteady, Known: true, Headroom: 0.1, ResetsIn: 1 * time.Hour}
	if Score(roomy) <= Score(tight) {
		t.Error("headroom must outrank reset urgency")
	}

	if Score(Assessment{Tier: TierBlocked}) >= 0 {
		t.Error("a blocked account must score below every usable one")
	}
}

func TestGovernorWithNoAccountsStillRuns(t *testing.T) {
	reg := NewRegistry(&fakeRunner{})
	g := NewGovernor(reg, NewProber(&fakeRunner{}, time.Minute), DefaultPolicy())
	plan := g.Decide(context.Background())
	if !plan.Allow {
		t.Fatal("no configured accounts must not stop the engine")
	}
	if plan.Tier != TierSteady {
		t.Errorf("tier = %s, want steady", plan.Tier)
	}
}

func TestProberFailureIsUnknownNotEmpty(t *testing.T) {
	r := &fakeRunner{errs: map[string]error{"solo:usage": fmt.Errorf("boom")}}
	p := NewProber(r, time.Minute)
	s := p.Probe(context.Background(), Account{Name: "solo"})
	if s.Ok {
		t.Fatal("a failed probe must not report Ok")
	}
	if s.Err == "" {
		t.Error("a failed probe must carry the reason")
	}
	if s.Session.Percent != 0 {
		t.Error("expected zero values, but the point is that Ok=false makes them unreadable")
	}
	// And that unknown state must not authorise expansion.
	if Assess(s, at("12:00")).Tier == TierExpand {
		t.Error("an unreadable probe must never expand")
	}
}

func TestProberCachesAndSingleFlights(t *testing.T) {
	now := at("12:00")
	r := &fakeRunner{usage: map[string]string{"solo": usageJSON(30, 30, "2:30pm", "Aug 26 at 12:00pm")}}
	p := NewProber(r, 90*time.Second)
	p.SetClock(func() time.Time { return now })
	a := Account{Name: "solo"}

	for i := 0; i < 5; i++ {
		if s := p.Probe(context.Background(), a); !s.Ok {
			t.Fatalf("probe %d failed: %s", i, s.Err)
		}
	}
	if got := r.callCount("solo:usage"); got != 1 {
		t.Errorf("ran the CLI %d times, want 1 — the cache is what makes the governor cheap to consult", got)
	}

	p.Invalidate("solo")
	p.Probe(context.Background(), a)
	if got := r.callCount("solo:usage"); got != 2 {
		t.Errorf("after invalidate: %d calls, want 2", got)
	}
}
