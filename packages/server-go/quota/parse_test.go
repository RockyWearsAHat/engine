package quota

import (
	"testing"
	"time"
)

// realUsageOutput is the verbatim body of `claude -p "/usage"` captured from the
// live CLI (v2.1.241). Pinning the real text is the point: this parser's only
// risk is upstream rewording, so the test must fail loudly the day the format
// moves rather than pass against a hand-written idealisation of it.
const realUsageOutput = `You are currently using your subscription to power your Claude Code usage

Current session: 4% used · resets Aug 23 at 8pm (America/Denver)
Current week (all models): 14% used · resets Aug 29 at 3pm (America/Denver)
Current week (Fable): 0% used

What's contributing to your limits usage?
Approximate, based on local sessions on this machine — does not include other devices or claude.ai. Behaviors are independent characteristics, not a breakdown.

Last 24h · 6283 requests · 141 sessions
  65% of your usage was at >150k context
  46% of your usage came from subagent-heavy sessions
  25% of your usage was while 4+ sessions ran in parallel
  Top skills: /claude-api 7%, /frontend-design:frontend-design 5%
  Top subagents: general-purpose 1%
  Top plugins: frontend-design 5%
  Top MCP servers: dx 13%, claude-in-chrome 5%, sara 1%

Last 7d · 30923 requests · 269 sessions
  73% of your usage was at >150k context
  67% of your usage came from subagent-heavy sessions
  33% of your usage was while 4+ sessions ran in parallel
  21% of your usage came from sessions active for 8+ hours
  Top skills: /front-end-design 2%, /frontend-design:frontend-design 1%, /claude-api 1%, /caveman-mode 1%, /resolving-merge-conflicts 1%
  Top subagents: workflow-subagent 15%, general-purpose 6%, fork 2%
  Top plugins: frontend-design 2%
  Top MCP servers: dx 10%, claude-in-chrome 7%`

func denver(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	return loc
}

func TestParseUsageRealOutput(t *testing.T) {
	loc := denver(t)
	now := time.Date(2026, time.August, 23, 16, 0, 0, 0, loc)

	s, err := ParseUsage(realUsageOutput, now)
	if err != nil {
		t.Fatalf("ParseUsage: %v", err)
	}
	if !s.Ok {
		t.Fatalf("expected Ok snapshot, got err=%q", s.Err)
	}

	if s.PlanNote == "" {
		t.Error("PlanNote not captured — a silent fallback to API credits would be invisible")
	}

	if got, want := s.Session.Percent, 4.0; got != want {
		t.Errorf("session percent = %v, want %v", got, want)
	}
	if !s.Session.HasReset {
		t.Fatal("session reset not parsed")
	}
	wantSessionReset := time.Date(2026, time.August, 23, 20, 0, 0, 0, loc)
	if !s.Session.ResetsAt.Equal(wantSessionReset) {
		t.Errorf("session reset = %v, want %v", s.Session.ResetsAt, wantSessionReset)
	}

	if got, want := s.Week.Percent, 14.0; got != want {
		t.Errorf("week percent = %v, want %v", got, want)
	}
	wantWeekReset := time.Date(2026, time.August, 29, 15, 0, 0, 0, loc)
	if !s.Week.HasReset || !s.Week.ResetsAt.Equal(wantWeekReset) {
		t.Errorf("week reset = %v (has=%v), want %v", s.Week.ResetsAt, s.Week.HasReset, wantWeekReset)
	}

	if len(s.PerModel) != 1 {
		t.Fatalf("per-model windows = %d, want 1", len(s.PerModel))
	}
	if s.PerModel[0].Name != "week:fable" || s.PerModel[0].Percent != 0 {
		t.Errorf("per-model[0] = %+v, want week:fable at 0%%", s.PerModel[0])
	}
	// A per-model line prints no reset; that must read as "unknown", never as
	// the zero time (which would look like a window overdue since 1970).
	if s.PerModel[0].HasReset {
		t.Error("per-model window should have no reset time")
	}
}

func TestParseUsageAttribution(t *testing.T) {
	loc := denver(t)
	now := time.Date(2026, time.August, 23, 16, 0, 0, 0, loc)
	s, err := ParseUsage(realUsageOutput, now)
	if err != nil {
		t.Fatalf("ParseUsage: %v", err)
	}

	if s.Last24h.Requests != 6283 || s.Last24h.Sessions != 141 {
		t.Errorf("24h counts = %d req / %d sessions, want 6283/141", s.Last24h.Requests, s.Last24h.Sessions)
	}
	if s.Last7d.Requests != 30923 || s.Last7d.Sessions != 269 {
		t.Errorf("7d counts = %d req / %d sessions, want 30923/269", s.Last7d.Requests, s.Last7d.Sessions)
	}

	for _, tc := range []struct {
		period Period
		key    string
		want   float64
	}{
		{s.Last24h, BehaviourHighContext, 65},
		{s.Last24h, BehaviourSubagentHeavy, 46},
		{s.Last24h, BehaviourParallelSessions, 25},
		{s.Last7d, BehaviourHighContext, 73},
		{s.Last7d, BehaviourSubagentHeavy, 67},
		{s.Last7d, BehaviourParallelSessions, 33},
		{s.Last7d, BehaviourLongSessions, 21},
	} {
		got, ok := tc.period.Behaviour(tc.key)
		if !ok {
			t.Errorf("%s/%s missing", tc.period.Span, tc.key)
			continue
		}
		if got != tc.want {
			t.Errorf("%s/%s = %v, want %v", tc.period.Span, tc.key, got, tc.want)
		}
	}

	// The 24h block has no long-session line; absent must be distinguishable
	// from zero, or the governor would "fix" a driver that was never reported.
	if _, ok := s.Last24h.Behaviour(BehaviourLongSessions); ok {
		t.Error("24h long_sessions should be absent, not zero")
	}

	if len(s.Last24h.TopMCPServers) != 3 {
		t.Fatalf("24h MCP servers = %d, want 3", len(s.Last24h.TopMCPServers))
	}
	if s.Last24h.TopMCPServers[0] != (Share{Name: "dx", Percent: 13}) {
		t.Errorf("top MCP server = %+v, want dx 13%%", s.Last24h.TopMCPServers[0])
	}
	// Names carrying slashes and colons must survive intact.
	if len(s.Last24h.TopSkills) != 2 || s.Last24h.TopSkills[1].Name != "/frontend-design:frontend-design" {
		t.Errorf("24h skills = %+v, want second entry /frontend-design:frontend-design", s.Last24h.TopSkills)
	}
	if len(s.Last7d.TopSkills) != 5 {
		t.Errorf("7d skills = %d, want 5", len(s.Last7d.TopSkills))
	}
	if len(s.Last7d.TopSubagents) != 3 || s.Last7d.TopSubagents[0].Name != "workflow-subagent" {
		t.Errorf("7d subagents = %+v", s.Last7d.TopSubagents)
	}
}

func TestParseUsageNoLimitsIsAnError(t *testing.T) {
	// The failure that matters: a wholesale format change must NOT parse as a
	// tidy 0% snapshot, because 0% means "spend freely".
	_, err := ParseUsage("Some completely different output\nwith no limits in it", time.Now())
	if err == nil {
		t.Fatal("expected an error when no limit lines are present")
	}
	s, _ := ParseUsage("nothing here", time.Now())
	if s.Ok {
		t.Fatal("snapshot must not be Ok when nothing parsed")
	}
	if s.Session.Percent != 0 || s.Week.Percent != 0 {
		t.Fatal("unparsed snapshot should carry zero values guarded by Ok=false")
	}
}

func TestParseUsageTolerantVariants(t *testing.T) {
	loc := denver(t)
	now := time.Date(2026, time.August, 23, 16, 0, 0, 0, loc)

	cases := []struct {
		name    string
		in      string
		check   func(*testing.T, Snapshot)
		wantErr bool
	}{
		{
			name: "minutes in reset time",
			in:   "Current session: 7% used · resets Aug 23 at 7:59pm (America/Denver)",
			check: func(t *testing.T, s Snapshot) {
				want := time.Date(2026, time.August, 23, 19, 59, 0, 0, loc)
				if !s.Session.ResetsAt.Equal(want) {
					t.Errorf("reset = %v, want %v", s.Session.ResetsAt, want)
				}
			},
		},
		{
			name: "no reset clause at all",
			in:   "Current session: 12% used",
			check: func(t *testing.T, s Snapshot) {
				if s.Session.Percent != 12 || s.Session.HasReset {
					t.Errorf("got %+v, want 12%% with no reset", s.Session)
				}
			},
		},
		{
			name: "fractional percent",
			in:   "Current week (all models): 3.5% used",
			check: func(t *testing.T, s Snapshot) {
				if s.Week.Percent != 3.5 {
					t.Errorf("week = %v, want 3.5", s.Week.Percent)
				}
			},
		},
		{
			name: "sonnet-only weekly sub-limit",
			in:   "Current week (Sonnet only): 22% used · resets Aug 29 at 3pm (America/Denver)",
			check: func(t *testing.T, s Snapshot) {
				if len(s.PerModel) != 1 || s.PerModel[0].Name != "week:sonnet only" {
					t.Fatalf("per-model = %+v", s.PerModel)
				}
				if w, ok := s.ModelWindow("sonnet"); !ok || w.Percent != 22 {
					t.Errorf("ModelWindow(sonnet) = %+v ok=%v", w, ok)
				}
			},
		},
		{
			name: "unknown behaviour phrasing is kept raw, not dropped",
			in: "Current session: 5% used\n" +
				"Last 24h · 10 requests · 2 sessions\n" +
				"  40% of your usage came from something we have never seen\n",
			check: func(t *testing.T, s Snapshot) {
				if len(s.Last24h.RawBehaviours) != 1 {
					t.Fatalf("raw behaviours = %+v", s.Last24h.RawBehaviours)
				}
				if len(s.Last24h.Behaviours) != 0 {
					t.Errorf("unknown phrasing should map to no key, got %+v", s.Last24h.Behaviours)
				}
			},
		},
		{
			name: "thousands separators in counts",
			in: "Current session: 5% used\n" +
				"Last 7d · 30,923 requests · 1,269 sessions\n",
			check: func(t *testing.T, s Snapshot) {
				if s.Last7d.Requests != 30923 || s.Last7d.Sessions != 1269 {
					t.Errorf("counts = %d/%d", s.Last7d.Requests, s.Last7d.Sessions)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := ParseUsage(tc.in, now)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseUsage: %v", err)
			}
			tc.check(t, s)
		})
	}
}

func TestParseResetRollsForward(t *testing.T) {
	loc := denver(t)

	// A time-only reset already past today means tomorrow.
	now := time.Date(2026, time.August, 23, 21, 0, 0, 0, loc)
	got, ok := parseReset("8pm (America/Denver)", now)
	if !ok {
		t.Fatal("parseReset failed")
	}
	want := time.Date(2026, time.August, 24, 20, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}

	// A dated reset in January read on New Year's Eve belongs to next year.
	now = time.Date(2026, time.December, 31, 23, 0, 0, 0, loc)
	got, ok = parseReset("Jan 2 at 9am (America/Denver)", now)
	if !ok {
		t.Fatal("parseReset failed")
	}
	if got.Year() != 2027 || got.Month() != time.January || got.Day() != 2 {
		t.Errorf("got %v, want 2027-01-02", got)
	}

	// Garbage must not become a confident wrong instant.
	if _, ok := parseReset("sometime soon", time.Now()); ok {
		t.Error("expected parseReset to reject unparseable input")
	}
}

func TestSnapshotTightestAndSummary(t *testing.T) {
	loc := denver(t)
	now := time.Date(2026, time.August, 23, 16, 0, 0, 0, loc)
	s, err := ParseUsage(realUsageOutput, now)
	if err != nil {
		t.Fatalf("ParseUsage: %v", err)
	}
	// Week at 14% binds before session at 4%.
	if got := s.Tightest(); got.Name != "week" {
		t.Errorf("Tightest = %s, want week", got.Name)
	}
	if s.Week.Remaining() != 0.86 {
		t.Errorf("week remaining = %v, want 0.86", s.Week.Remaining())
	}
	if got := s.Summary(); got == "" {
		t.Error("Summary should render")
	}
}
