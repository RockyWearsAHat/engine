package quota

import (
	"testing"
	"time"
)

// realLimitEvent is a verbatim rate_limit_event line captured from Claude Code
// CLI v2.1.241 (`claude -p ... --output-format stream-json --verbose`). Pinned
// so a schema change upstream fails this test instead of silently turning the
// engine's fastest limit signal into a no-op.
const realLimitEvent = `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1787536800,"rateLimitType":"five_hour","overageStatus":"rejected","overageDisabledReason":"org_level_disabled","isUsingOverage":false},"uuid":"789203d9-7c06-4327-984e-4200035c0f4e","session_id":"7979520e-1a74-4fd4-9885-67e81a61eb86"}`

func TestParseRealLimitEvent(t *testing.T) {
	now := at("12:00")
	ev, ok := ParseLimitEvent(realLimitEvent, now)
	if !ok {
		t.Fatal("failed to parse a real rate_limit_event — the payload key is rate_limit_info")
	}
	if ev.Status != StatusAllowed {
		t.Errorf("status = %q, want allowed", ev.Status)
	}
	if ev.Type != "five_hour" {
		t.Errorf("type = %q, want five_hour", ev.Type)
	}
	if !ev.HasReset {
		t.Fatal("resetsAt (a unix epoch number) did not parse")
	}
	if got := ev.ResetsAt.Unix(); got != 1787536800 {
		t.Errorf("resetsAt = %d, want 1787536800", got)
	}
	if ev.UsingOverage {
		t.Error("isUsingOverage was false in the payload")
	}
	// The consequential one: this account has NO overage net, so exhausting the
	// window is a hard stop rather than a billing event.
	if ev.OverageAvailable() {
		t.Error("overageStatus was \"rejected\" — there is no safety net below the limit")
	}
	if ev.OverageDisabledReason != "org_level_disabled" {
		t.Errorf("overageDisabledReason = %q", ev.OverageDisabledReason)
	}
}

func TestParseLimitEventShapes(t *testing.T) {
	now := at("12:00")
	tests := []struct {
		name       string
		line       string
		wantOk     bool
		wantStatus string
		wantReset  bool
	}{
		{
			name:       "flat legacy shape still parses",
			line:       `{"type":"rate_limit_event","status":"rejected","rateLimitType":"seven_day","resetsAt":"2026-08-23T14:30:00Z"}`,
			wantOk:     true,
			wantStatus: StatusRejected,
			wantReset:  true,
		},
		{
			name:       "rate_limit nesting still parses",
			line:       `{"type":"rate_limit_event","rate_limit":{"status":"allowed_warning","rateLimitType":"five_hour","resetsAt":1787536800}}`,
			wantOk:     true,
			wantStatus: StatusWarning,
			wantReset:  true,
		},
		{
			name:   "a result event is not a limit event",
			line:   `{"type":"result","subtype":"success","result":"ok"}`,
			wantOk: false,
		},
		{
			name:   "an assistant event mentioning rate_limit in text is not one either",
			line:   `{"type":"assistant","message":{"content":[{"type":"text","text":"the rate_limit_info was fine"}]}}`,
			wantOk: false,
		},
		{
			name:   "malformed JSON is ignored, not fatal",
			line:   `{"type":"rate_limit_event","rate_limit_info":{`,
			wantOk: false,
		},
		{
			name:   "an event with no status carries no information",
			line:   `{"type":"rate_limit_event","rate_limit_info":{"rateLimitType":"five_hour"}}`,
			wantOk: false,
		},
		{
			name:       "a missing reset is absent, not epoch zero",
			line:       `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}}`,
			wantOk:     true,
			wantStatus: StatusAllowed,
			wantReset:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, ok := ParseLimitEvent(tt.line, now)
			if ok != tt.wantOk {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOk)
			}
			if !ok {
				return
			}
			if ev.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", ev.Status, tt.wantStatus)
			}
			if ev.HasReset != tt.wantReset {
				t.Errorf("hasReset = %v, want %v", ev.HasReset, tt.wantReset)
			}
			if !tt.wantReset && !ev.ResetsAt.IsZero() {
				t.Error("an absent reset must not become a real timestamp")
			}
		})
	}
}

// The cache must only be dropped on a real transition. Every run emits an
// "allowed" event; invalidating on each one would turn the TTL into a no-op and
// spawn a probe per dispatch.
func TestObserverInvalidatesOnlyOnTransition(t *testing.T) {
	now := at("12:00")
	o := NewObserver()
	var drops int
	o.OnChange(func(string) { drops++ })

	allowed := `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","rateLimitType":"five_hour"}}`
	for i := 0; i < 5; i++ {
		o.Observe("solo", allowed, now)
	}
	if drops != 1 {
		t.Errorf("drops = %d after five identical events, want 1 (the first sighting)", drops)
	}

	warn := `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning","rateLimitType":"five_hour"}}`
	o.Observe("solo", warn, now)
	if drops != 2 {
		t.Errorf("drops = %d after a status change, want 2", drops)
	}
	if !o.Warned("solo") {
		t.Error("expected the warning to be recorded")
	}
}

// A rejection expires when its own window resets — otherwise the engine stays
// blocked forever on a stale event.
func TestRejectionExpiresAtItsReset(t *testing.T) {
	now := at("12:00")
	o := NewObserver()
	line := `{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","rateLimitType":"five_hour","resetsAt":"2026-08-23T14:30:00Z"}}`
	if _, ok := o.Observe("solo", line, now); !ok {
		t.Fatal("parse failed")
	}
	if !o.Rejected("solo", now) {
		t.Error("should be rejected before the reset")
	}
	if o.Rejected("solo", now.Add(3*time.Hour)) {
		t.Error("a rejection past its own reset time is history, not state")
	}
}

func TestObserverUnknownAccountAssumesNoNet(t *testing.T) {
	o := NewObserver()
	if o.OverageAvailable("never-seen") {
		t.Error("an unobserved account must not be assumed to have an overage net")
	}
	if o.Rejected("never-seen", at("12:00")) {
		t.Error("an unobserved account is not rejected")
	}
}
