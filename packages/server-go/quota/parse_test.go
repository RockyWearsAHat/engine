package quota

import (
	"encoding/json"
	"fmt"
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
		{
			name: "comma-separated date format (Sep 4, 7pm)",
			in:   "Current session: 35% used · resets Sep 4, 7pm (America/Denver)",
			check: func(t *testing.T, s Snapshot) {
				if !s.Session.HasReset {
					t.Fatal("session reset not parsed")
				}
				want := time.Date(2026, time.September, 4, 19, 0, 0, 0, loc)
				if !s.Session.ResetsAt.Equal(want) {
					t.Errorf("session reset = %v, want %v", s.Session.ResetsAt, want)
				}
			},
		},
		{
			name: "comma-separated date with minutes (Sep 5, 3:45pm)",
			in:   "Current week (all models): 7% used · resets Sep 5, 3:45pm (America/Denver)",
			check: func(t *testing.T, s Snapshot) {
				if !s.Week.HasReset {
					t.Fatal("week reset not parsed")
				}
				want := time.Date(2026, time.September, 5, 15, 45, 0, 0, loc)
				if !s.Week.ResetsAt.Equal(want) {
					t.Errorf("week reset = %v, want %v", s.Week.ResetsAt, want)
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

// ── Test fixture parsing from testdata/usage-sample.txt ──
const fixtureUsageOutput = `You are currently using your subscription to power your Claude Code usage

Current session: 35% used · resets Sep 4, 7pm (America/Denver)
Current week (all models): 7% used · resets Sep 5, 3pm (America/Denver)
Current week (Fable): 4% used · resets Sep 5, 3pm (America/Denver)

What's contributing to your limits usage?
Approximate, based on local sessions on this machine — does not include other devices or claude.ai. Behaviors are independent characteristics, not a breakdown.

Last 24h · 29108 requests · 1308 sessions
  99% of your usage was while 4+ sessions ran in parallel
  Top MCP servers: dx 4%

Last 7d · 70105 requests · 2209 sessions
  87% of your usage was while 4+ sessions ran in parallel
  Top skills: /run 1%
  Top MCP servers: dx 1%`

func TestParseUsageFixtureLines(t *testing.T) {
	loc := denver(t)
	// Using Sep 4 at noon Denver time as the reference (when fixture was generated).
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, loc)

	s, err := ParseUsage(fixtureUsageOutput, now)
	if err != nil {
		t.Fatalf("ParseUsage: %v", err)
	}
	if !s.Ok {
		t.Fatalf("expected Ok snapshot, got err=%q", s.Err)
	}

	// Verify session parsing
	if s.Session.Percent != 35 {
		t.Errorf("session percent = %v, want 35", s.Session.Percent)
	}
	if !s.Session.HasReset {
		t.Error("session reset not parsed")
	}
	// "Sep 4, 7pm" should be today at 7pm (19:00)
	wantSessionReset := time.Date(2026, time.September, 4, 19, 0, 0, 0, loc)
	if s.Session.ResetsAt == nil || !s.Session.ResetsAt.Equal(wantSessionReset) {
		t.Errorf("session reset = %v, want %v", s.Session.ResetsAt, &wantSessionReset)
	}

	// Verify week parsing
	if s.Week.Percent != 7 {
		t.Errorf("week percent = %v, want 7", s.Week.Percent)
	}
	if !s.Week.HasReset {
		t.Error("week reset not parsed")
	}
	// "Sep 5, 3pm" should be tomorrow at 3pm (15:00)
	wantWeekReset := time.Date(2026, time.September, 5, 15, 0, 0, 0, loc)
	if s.Week.ResetsAt == nil || !s.Week.ResetsAt.Equal(wantWeekReset) {
		t.Errorf("week reset = %v, want %v", s.Week.ResetsAt, &wantWeekReset)
	}

	// Verify per-model parsing
	if len(s.PerModel) != 1 {
		t.Fatalf("per-model windows = %d, want 1", len(s.PerModel))
	}
	if s.PerModel[0].Name != "week:fable" || s.PerModel[0].Percent != 4 {
		t.Errorf("per-model[0] = %+v, want week:fable at 4%%", s.PerModel[0])
	}
}

// ── Edge case tests ──

func TestParseResetMidnightRollover(t *testing.T) {
	loc := denver(t)

	// Test: reset time at midnight (00:00)
	now := time.Date(2026, time.September, 4, 23, 30, 0, 0, loc)
	got, ok := parseReset("Sep 5 at 12:00am (America/Denver)", now)
	if !ok {
		t.Fatal("parseReset failed for midnight")
	}
	want := time.Date(2026, time.September, 5, 0, 0, 0, 0, loc)
	if got == nil || !got.Equal(want) {
		t.Errorf("midnight reset = %v, want %v", got, &want)
	}
}

func TestParseResetTimezoneParsing(t *testing.T) {
	// Test: various timezone names are parsed correctly.
	// Invalid timezones fall back gracefully to the reference timezone (now.Location()).
	cases := []struct {
		name   string
		tzStr  string
		wantOk bool
	}{
		{"Denver", "America/Denver", true},
		{"UTC", "UTC", true},
		{"London", "Europe/London", true},
		{"Tokyo", "Asia/Tokyo", true},
		{"InvalidFallsBackToNow", "Invalid/Timezone", true}, // Falls back to now's timezone
		{"Empty", "", true}, // Falls back to now's timezone
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loc := denver(t)
			now := time.Date(2026, time.September, 4, 12, 0, 0, 0, loc)

			var resetStr string
			if tc.tzStr != "" {
				resetStr = fmt.Sprintf("Sep 5 at 3pm (%s)", tc.tzStr)
			} else {
				resetStr = "Sep 5 at 3pm"
			}

			got, ok := parseReset(resetStr, now)
			if ok != tc.wantOk {
				if tc.wantOk {
					t.Errorf("parseReset(%q) failed, expected success", resetStr)
				} else {
					t.Errorf("parseReset(%q) succeeded, expected failure", resetStr)
				}
				return
			}
			if ok && (got == nil || got.IsZero()) {
				t.Errorf("parseReset returned nil or zero time")
			}
		})
	}
}

func TestParseResetRelativeTimeConversion(t *testing.T) {
	loc := denver(t)

	// Test: parse time-only format and convert to absolute UTC
	// "3pm" with no date should roll to today or tomorrow
	now := time.Date(2026, time.September, 4, 10, 0, 0, 0, loc)
	got, ok := parseReset("3pm (America/Denver)", now)
	if !ok {
		t.Fatal("parseReset failed for relative time")
	}
	// 3pm is in the future (now is 10am), so should be today
	want := time.Date(2026, time.September, 4, 15, 0, 0, 0, loc)
	if got == nil || !got.Equal(want) {
		t.Errorf("relative time = %v, want %v", got, &want)
	}

	// Test: when time is in the past, roll to tomorrow
	now = time.Date(2026, time.September, 4, 20, 0, 0, 0, loc)
	got, ok = parseReset("3pm (America/Denver)", now)
	if !ok {
		t.Fatal("parseReset failed")
	}
	want = time.Date(2026, time.September, 5, 15, 0, 0, 0, loc)
	if got == nil || !got.Equal(want) {
		t.Errorf("relative time past = %v, want %v", got, &want)
	}
}

func TestParseResetAbsoluteDateParsing(t *testing.T) {
	loc := denver(t)

	// Test: absolute date parsing with month name
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, loc)

	cases := []struct {
		name   string
		input  string
		want   time.Time
		wantOk bool
	}{
		{
			"Sep date",
			"Sep 5 at 3pm (America/Denver)",
			time.Date(2026, time.September, 5, 15, 0, 0, 0, loc),
			true,
		},
		{
			"Dec date rolls year forward",
			"Dec 25 at 9am (America/Denver)",
			time.Date(2026, time.December, 25, 9, 0, 0, 0, loc),
			true,
		},
		{
			"Jan date on Dec 31 rolls forward",
			"Jan 2 at 9am (America/Denver)",
			time.Date(2027, time.January, 2, 9, 0, 0, 0, loc),
			true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseReset(tc.input, now)
			if ok != tc.wantOk {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOk)
			}
			if ok && (got == nil || !got.Equal(tc.want)) {
				t.Errorf("got %v, want %v", got, &tc.want)
			}
		})
	}
}

func TestParseResetInvalidFormat(t *testing.T) {
	loc := denver(t)
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, loc)

	invalidCases := []string{
		"invalid time format",
		"25pm", // Invalid hour
		"3:75pm", // Invalid minute
		"99 at 3pm",
		"",
		"sometime next week",
	}

	for _, input := range invalidCases {
		t.Run(fmt.Sprintf("reject_%q", input), func(t *testing.T) {
			_, ok := parseReset(input, now)
			if ok {
				t.Errorf("parseReset should reject %q", input)
			}
		})
	}
}

// ── JSON shape verification tests ──

func TestWindowJSONMarshaling(t *testing.T) {
	loc := denver(t)
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, loc)

	s, err := ParseUsage(fixtureUsageOutput, now)
	if err != nil {
		t.Fatalf("ParseUsage: %v", err)
	}

	// Test Window JSON structure
	// Window.ResetsAt should marshal as JSON with hasReset bool
	sessionJSON, err := json.Marshal(s.Session)
	if err != nil {
		t.Fatalf("json.Marshal(session): %v", err)
	}

	var sessionData map[string]interface{}
	if err := json.Unmarshal(sessionJSON, &sessionData); err != nil {
		t.Fatalf("json.Unmarshal session: %v", err)
	}

	// Verify expected fields
	if _, hasResetsAt := sessionData["resetsAt"]; !hasResetsAt {
		t.Error("session JSON should have resetsAt field")
	}
	if _, hasReset := sessionData["hasReset"]; !hasReset {
		t.Error("session JSON should have hasReset field")
	}
	if percent, ok := sessionData["percent"]; !ok {
		t.Error("session JSON should have percent field")
	} else {
		if v, ok := percent.(float64); !ok || v != 35 {
			t.Errorf("percent = %v, want 35.0", percent)
		}
	}
}

func TestWindowStatusJSONShape(t *testing.T) {
	loc := denver(t)
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, loc)

	s, err := ParseUsage(fixtureUsageOutput, now)
	if err != nil {
		t.Fatalf("ParseUsage: %v", err)
	}

	// Convert Window to WindowStatus (as done in governor.go)
	sessionStatus := windowStatus(s.Session, s.Ok, now)

	sessionJSON, err := json.Marshal(sessionStatus)
	if err != nil {
		t.Fatalf("json.Marshal(windowStatus): %v", err)
	}

	var statusData map[string]interface{}
	if err := json.Unmarshal(sessionJSON, &statusData); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	// Verify WindowStatus JSON fields
	if _, hasResetsAt := statusData["resetsAt"]; !hasResetsAt {
		t.Error("WindowStatus JSON should have resetsAt field")
	}
	if _, hasResetsIn := statusData["resetsIn"]; !hasResetsIn {
		t.Error("WindowStatus JSON should have resetsIn field")
	}
	if known, ok := statusData["known"]; !ok {
		t.Error("WindowStatus JSON should have known field")
	} else {
		if v, ok := known.(bool); !ok || !v {
			t.Errorf("known = %v, want true", known)
		}
	}
}

func TestSnapshotAccountJSONShape(t *testing.T) {
	loc := denver(t)
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, loc)

	s, err := ParseUsage(fixtureUsageOutput, now)
	if err != nil {
		t.Fatalf("ParseUsage: %v", err)
	}

	// Create a SnapshotAccount (as done in pooled.go)
	acct := Account{Name: "test"}
	policy := Policy{}
	snapAcct := snapshotAccount(acct, s, policy, now)

	snapJSON, err := json.Marshal(snapAcct)
	if err != nil {
		t.Fatalf("json.Marshal(SnapshotAccount): %v", err)
	}

	var acctData map[string]interface{}
	if err := json.Unmarshal(snapJSON, &acctData); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	// Verify SnapshotAccount JSON fields
	if _, hasResetAt := acctData["resetAt"]; !hasResetAt {
		t.Error("SnapshotAccount JSON should have resetAt field")
	}
	if _, hasSessionResetAt := acctData["sessionResetAt"]; !hasSessionResetAt {
		t.Error("SnapshotAccount JSON should have sessionResetAt field")
	}
	if _, hasWeekResetAt := acctData["weekResetAt"]; !hasWeekResetAt {
		t.Error("SnapshotAccount JSON should have weekResetAt field")
	}

	// Verify resetAt is RFC3339 formatted when present
	if resetAtStr, ok := acctData["resetAt"].(string); ok && resetAtStr != "" {
		if _, err := time.Parse(time.RFC3339, resetAtStr); err != nil {
			t.Errorf("resetAt is not RFC3339: %q (%v)", resetAtStr, err)
		}
	}
}

func TestOmitResetFieldsWhenUnknown(t *testing.T) {
	loc := denver(t)

	// Parse output with no reset times
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, loc)
	noResetOutput := "Current session: 25% used\nCurrent week (all models): 12% used"

	s, err := ParseUsage(noResetOutput, now)
	if err != nil {
		t.Fatalf("ParseUsage: %v", err)
	}

	// Verify HasReset is false
	if s.Session.HasReset {
		t.Error("session should have HasReset=false when no reset was parsed")
	}
	if s.Week.HasReset {
		t.Error("week should have HasReset=false when no reset was parsed")
	}

	// Verify ResetsAt is nil when unknown
	if s.Session.ResetsAt != nil {
		t.Error("session ResetsAt should be nil when no reset was parsed")
	}
	if s.Week.ResetsAt != nil {
		t.Error("week ResetsAt should be nil when no reset was parsed")
	}

	// Verify Window JSON omits resetsAt when nil (using omitempty tag)
	sessionJSON, _ := json.Marshal(s.Session)
	var sessionData map[string]interface{}
	json.Unmarshal(sessionJSON, &sessionData)

	// When ResetsAt is nil, the JSON key should be absent
	if _, hasResetsAt := sessionData["resetsAt"]; hasResetsAt {
		t.Error("Window JSON should omit resetsAt when ResetsAt is nil")
	}

	// WindowStatus should also omit resetsAt when HasReset=false
	sessionStatus := windowStatus(s.Session, s.Ok, now)
	statusJSON, _ := json.Marshal(sessionStatus)
	var statusData map[string]interface{}
	json.Unmarshal(statusJSON, &statusData)

	if _, hasResetsAt := statusData["resetsAt"]; hasResetsAt {
		if v, ok := statusData["resetsAt"].(string); ok && v != "" {
			t.Error("WindowStatus JSON should omit resetsAt when HasReset=false")
		}
	}
}

func TestFixtureWeekResetCarriedInPooledSnapshot(t *testing.T) {
	// Test: week reset time is parsed, carried through pooled snapshot wire format,
	// and reconstructed correctly in UTC. This verifies both the parsing
	// (fixture → Snapshot) and the pooling round-trip (Snapshot → SnapshotAccount → Snapshot).
	loc := denver(t)
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, loc)

	s, err := ParseUsage(fixtureUsageOutput, now)
	if err != nil {
		t.Fatalf("ParseUsage: %v", err)
	}
	if !s.Ok {
		t.Fatalf("expected Ok snapshot, got err=%q", s.Err)
	}

	// Verify week reset is parsed
	if !s.Week.HasReset {
		t.Fatal("week.HasReset should be true")
	}
	if s.Week.ResetsAt == nil {
		t.Fatal("week.ResetsAt should not be nil")
	}

	// Verify week reset time is correct in Denver time (3pm = 15:00)
	wantWeekResetDenver := time.Date(2026, time.September, 5, 15, 0, 0, 0, loc)
	if !s.Week.ResetsAt.Equal(wantWeekResetDenver) {
		t.Errorf("week.ResetsAt (Denver) = %v, want %v", s.Week.ResetsAt, wantWeekResetDenver)
	}

	// Verify week reset time is correct in UTC (3pm Denver + 6 hours = 9pm UTC = 21:00 UTC)
	wantWeekResetUTC := time.Date(2026, time.September, 5, 21, 0, 0, 0, time.UTC)
	if !s.Week.ResetsAt.UTC().Equal(wantWeekResetUTC) {
		t.Errorf("week.ResetsAt (UTC) = %v, want %v", s.Week.ResetsAt.UTC(), wantWeekResetUTC)
	}

	// Verify session reset time is correct in UTC (7pm Denver + 6 hours = 1am next day UTC = 01:00 UTC Sep 5)
	if !s.Session.HasReset {
		t.Fatal("session.HasReset should be true")
	}
	if s.Session.ResetsAt == nil {
		t.Fatal("session.ResetsAt should not be nil")
	}
	wantSessionResetUTC := time.Date(2026, time.September, 5, 1, 0, 0, 0, time.UTC)
	if !s.Session.ResetsAt.UTC().Equal(wantSessionResetUTC) {
		t.Errorf("session.ResetsAt (UTC) = %v, want %v", s.Session.ResetsAt.UTC(), wantSessionResetUTC)
	}

	// Test pooled snapshot wire format: convert to SnapshotAccount and back.
	// This simulates the /quota/snapshot endpoint serializing to JSON and another
	// box deserializing it.
	policy := DefaultPolicy()
	sa := snapshotAccount(Account{Name: "test", Identity: Identity{Email: "test@example.com"}}, s, policy, now)

	// Verify SnapshotAccount carries both resets in RFC3339 format
	if sa.SessionResetAt == "" {
		t.Error("SnapshotAccount.SessionResetAt should not be empty")
	}
	if sa.WeekResetAt == "" {
		t.Error("SnapshotAccount.WeekResetAt should not be empty")
	}

	// Reconstruct Snapshot from SnapshotAccount (simulating deserialization from /quota/snapshot)
	s2 := sa.toSnapshot("test", now)

	// Verify reconstructed week reset is present and correct in UTC
	if !s2.Week.HasReset {
		t.Fatal("reconstructed week.HasReset should be true")
	}
	if s2.Week.ResetsAt == nil {
		t.Fatal("reconstructed week.ResetsAt should not be nil")
	}
	if !s2.Week.ResetsAt.UTC().Equal(wantWeekResetUTC) {
		t.Errorf("reconstructed week.ResetsAt (UTC) = %v, want %v", s2.Week.ResetsAt.UTC(), wantWeekResetUTC)
	}

	// Verify reconstructed session reset is present and correct in UTC
	if !s2.Session.HasReset {
		t.Fatal("reconstructed session.HasReset should be true")
	}
	if s2.Session.ResetsAt == nil {
		t.Fatal("reconstructed session.ResetsAt should not be nil")
	}
	if !s2.Session.ResetsAt.UTC().Equal(wantSessionResetUTC) {
		t.Errorf("reconstructed session.ResetsAt (UTC) = %v, want %v", s2.Session.ResetsAt.UTC(), wantSessionResetUTC)
	}
}
