package quota

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// The gauge exists so a supervisor OUTSIDE this process can see the fuel level.
// That means the numbers have to survive a JSON round-trip intact — a percentage
// that only renders correctly in a Go string is not a usable signal.
func TestStatusSurvivesJSONRoundTrip(t *testing.T) {
	now := at("12:00")
	r := &fakeRunner{
		auth: map[string]string{
			"work": authJSON("a@example.com", "org-1"),
			"side": authJSON("b@example.com", "org-2"),
		},
		usage: map[string]string{
			"work": usageJSON(92, 80, "2:30pm", "Aug 26 at 12:00pm"),
			"side": usageJSON(10, 15, "2:30pm", "Aug 26 at 12:00pm"),
		},
	}
	reg := NewRegistry(r, Account{Name: "work", ConfigDir: "/w"}, Account{Name: "side", ConfigDir: "/s"})
	reg.Resolve(context.Background())

	prober := NewProber(r, time.Minute)
	prober.SetClock(func() time.Time { return now })
	g := NewGovernor(reg, prober, DefaultPolicy())
	g.SetClock(func() time.Time { return now })

	raw, err := json.Marshal(g.Status(context.Background()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Status
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Accounts) != 2 {
		t.Fatalf("accounts = %d, want 2", len(got.Accounts))
	}

	byName := map[string]AccountStatus{}
	for _, a := range got.Accounts {
		byName[a.Name] = a
	}
	work, side := byName["work"], byName["side"]

	if !work.Ok || !side.Ok {
		t.Fatalf("both snapshots should have parsed: work.ok=%v side.ok=%v", work.Ok, side.Ok)
	}
	if work.Session.Percent != 92 {
		t.Errorf("work session = %v%%, want 92", work.Session.Percent)
	}
	if work.Week.Percent != 80 {
		t.Errorf("work week = %v%%, want 80", work.Week.Percent)
	}
	if side.Session.Percent != 10 {
		t.Errorf("side session = %v%%, want 10", side.Session.Percent)
	}
	// The tier is what a supervisor actually branches on, and it is the field most
	// likely to be lost in translation: Tier is an int with json:"-", so only
	// TierName crosses the wire.
	if work.Assessment.TierName == "" || side.Assessment.TierName == "" {
		t.Error("tier name did not survive the round trip — a supervisor cannot branch on an empty string")
	}
	if got.Plan.TierName == "" {
		t.Error("the plan's tier name did not survive the round trip")
	}
	if got.Plan.Account != "side" {
		t.Errorf("plan account = %q, want side", got.Plan.Account)
	}
	// Durations are json:"-" on Plan/Assessment precisely because a nanosecond
	// integer is unreadable; the string mirrors must be populated instead.
	if work.Session.ResetsAt == "" {
		t.Error("session reset time is missing from the JSON")
	}
	if work.ResetsIn == "" {
		t.Error("resetsIn is missing — a supervisor cannot tell when to check back")
	}
}

// The invariant that runs through the whole package: an unreadable account must
// report "unknown", never "0% used". A supervisor that reads a failed probe as an
// empty tank does the exact opposite of the right thing.
func TestStatusReportsUnknownRatherThanZero(t *testing.T) {
	now := at("12:00")
	r := &fakeRunner{
		auth: map[string]string{"solo": authJSON("a@example.com", "org-1")},
		errs: map[string]error{"solo:usage": errors.New("claude: command not found")},
	}
	reg := NewRegistry(r, Account{Name: "solo", ConfigDir: "/s"})
	reg.Resolve(context.Background())

	prober := NewProber(r, time.Minute)
	prober.SetClock(func() time.Time { return now })
	g := NewGovernor(reg, prober, DefaultPolicy())
	g.SetClock(func() time.Time { return now })

	st := g.Status(context.Background())
	if len(st.Accounts) != 1 {
		t.Fatalf("accounts = %d, want 1", len(st.Accounts))
	}
	a := st.Accounts[0]
	if a.Ok {
		t.Fatal("a failed probe must not report ok")
	}
	if a.Session.Known || a.Week.Known {
		t.Error("windows from a failed probe must be marked unknown")
	}
	if a.Error == "" {
		t.Error("the failure reason should be reported, not swallowed")
	}
	// And the plan must still let work through: not knowing the balance is not a
	// reason to stop building.
	if !st.Plan.Allow {
		t.Errorf("an unknown balance must not block dispatch: %s", st.Plan.Reason)
	}
	if st.Plan.TierName != TierSteady.String() {
		t.Errorf("tier = %s, want steady when the reading is unknown", st.Plan.TierName)
	}
}

// A disabled account (one sharing a quota pool with another) still appears in the
// gauge, with the reason — otherwise configuring two accounts that turn out to be
// the same account looks like it worked.
func TestStatusShowsDisabledAccounts(t *testing.T) {
	now := at("12:00")
	r := &fakeRunner{
		auth: map[string]string{
			"work": authJSON("a@example.com", "org-1"),
			"dupe": authJSON("a@example.com", "org-1"),
		},
		usage: map[string]string{
			"work": usageJSON(20, 20, "2:30pm", "Aug 26 at 12:00pm"),
			"dupe": usageJSON(20, 20, "2:30pm", "Aug 26 at 12:00pm"),
		},
	}
	reg := NewRegistry(r, Account{Name: "work", ConfigDir: "/w"}, Account{Name: "dupe", ConfigDir: "/d"})
	reg.Resolve(context.Background())

	prober := NewProber(r, time.Minute)
	prober.SetClock(func() time.Time { return now })
	g := NewGovernor(reg, prober, DefaultPolicy())
	g.SetClock(func() time.Time { return now })

	st := g.Status(context.Background())
	var disabled int
	for _, a := range st.Accounts {
		if a.Disabled {
			disabled++
			if a.DisabledReason == "" {
				t.Errorf("account %q is disabled with no reason given", a.Name)
			}
		}
	}
	if disabled != 1 {
		t.Errorf("disabled accounts = %d, want 1 (they share a quota pool)", disabled)
	}
}
