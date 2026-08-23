package quota

import (
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// Observer ingests the `rate_limit_event` that Claude Code emits on the
// stream-json output the engine already consumes.
//
// # WHY BOTHER, GIVEN Probe EXISTS
//
// The probe is authoritative but periodic: between reads, the cached percentage
// ages, and a burst of parallel agents can cross a threshold in far less than
// one TTL. This event costs nothing extra — it arrives on runs already being
// made — and it carries the two facts a cached percentage cannot supply in time:
//
//   - "allowed_warning" / "rejected": the server's own opinion, right now. A
//     rejection here is ground truth that outranks any percentage we hold.
//   - resetsAt: when the window refills, which lets a blocked governor sleep
//     exactly long enough instead of retrying into a wall.
//
// What it does NOT carry is utilization — the SDK narrows the event to
// {status, resetsAt, rateLimitType, isUsingOverage} before it reaches stdout.
// So this is an alarm and a freshness hint, never a gauge. Wiring it up as a
// gauge is the tempting mistake: you would end up with a number that is always
// either 0 or 100 and a governor that oscillates between the two.
type Observer struct {
	mu     sync.RWMutex
	states map[string]LimitEvent
	// invalidate is called with an account name when its state changes, so the
	// next Probe re-reads instead of serving a snapshot we know is stale.
	invalidate func(account string)
}

// LimitEvent is the parsed form of one rate_limit_event.
type LimitEvent struct {
	// Status is "allowed", "allowed_warning" or "rejected".
	Status string `json:"status"`
	// Type is the window the event refers to, e.g. "five_hour", "seven_day".
	Type string `json:"rateLimitType"`
	// ResetsAt is when that window refills.
	ResetsAt time.Time `json:"-"`
	HasReset bool      `json:"-"`
	// UsingOverage reports that the account has spilled into paid overage —
	// worth surfacing loudly, because the subscription's "flat cost" stops being
	// flat at exactly this moment.
	UsingOverage bool `json:"isUsingOverage"`
	// OverageStatus is whether paid overage is available as a safety net at all
	// ("allowed" / "rejected"), and OverageDisabledReason says why not.
	//
	// This changes what running out MEANS, so it changes how hard to pace. With
	// overage available, hitting 100% costs money and the work continues. With it
	// disabled — the observed default here is "rejected"/"org_level_disabled" —
	// hitting 100% is a hard stop and every agent goes dark until the reset. The
	// governor pushes closer to the line only when there is a net below it.
	OverageStatus         string `json:"overageStatus,omitempty"`
	OverageDisabledReason string `json:"overageDisabledReason,omitempty"`
	// SeenAt is when we observed it.
	SeenAt time.Time `json:"seenAt"`
}

// OverageAvailable reports whether exceeding the limit would spill into paid
// overage rather than stopping the engine. False is the safe assumption: an
// account we have never observed is treated as having no net.
func (e LimitEvent) OverageAvailable() bool {
	return strings.EqualFold(e.OverageStatus, "allowed")
}

// Rejected reports whether the last event for an account was a rejection that
// has not yet reset.
func (e LimitEvent) Rejected(now time.Time) bool {
	if e.Status != StatusRejected {
		return false
	}
	// A rejection older than its own reset time is spent history, not state.
	if e.HasReset && !e.ResetsAt.After(now) {
		return false
	}
	return true
}

// Status values as emitted by the CLI.
const (
	StatusAllowed  = "allowed"
	StatusWarning  = "allowed_warning"
	StatusRejected = "rejected"
)

// NewObserver builds an empty observer.
func NewObserver() *Observer {
	return &Observer{states: map[string]LimitEvent{}}
}

// OnChange registers a callback fired when an account's limit status changes.
// Wire this to Prober.Invalidate so a status change forces a fresh read.
func (o *Observer) OnChange(fn func(account string)) {
	o.mu.Lock()
	o.invalidate = fn
	o.mu.Unlock()
}

// rawLimitInfo is the payload, verified against CLI v2.1.241 output:
//
//	{"type":"rate_limit_event","rate_limit_info":{
//	   "status":"allowed","resetsAt":1787536800,"rateLimitType":"five_hour",
//	   "overageStatus":"rejected","overageDisabledReason":"org_level_disabled",
//	   "isUsingOverage":false}}
type rawLimitInfo struct {
	Status                string `json:"status"`
	RateLimitTyp          string `json:"rateLimitType"`
	ResetsAt              any    `json:"resetsAt"`
	UsingOverage          bool   `json:"isUsingOverage"`
	OverageStatus         string `json:"overageStatus"`
	OverageDisabledReason string `json:"overageDisabledReason"`
}

// rawLimitEvent is the envelope. The payload is nested under "rate_limit_info",
// but older builds emitted it flat or under "rate_limit"; all three are accepted
// so a version skew degrades to "still works" rather than to silent blindness.
type rawLimitEvent struct {
	Type string `json:"type"`
	rawLimitInfo
	Info      *rawLimitInfo `json:"rate_limit_info"`
	RateLimit *rawLimitInfo `json:"rate_limit"`
}

// ParseLimitEvent parses one stream-json line into a LimitEvent.
//
// Returns ok=false for any line that is not a rate_limit_event, so callers can
// hand it every line of the stream without pre-filtering.
func ParseLimitEvent(line string, now time.Time) (LimitEvent, bool) {
	line = strings.TrimSpace(line)
	if line == "" || !strings.Contains(line, "rate_limit") {
		return LimitEvent{}, false
	}
	var raw rawLimitEvent
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return LimitEvent{}, false
	}
	if raw.Type != "rate_limit_event" {
		return LimitEvent{}, false
	}
	// Prefer the nested payload; fall back to the flat one.
	info := raw.rawLimitInfo
	if raw.Info != nil {
		info = *raw.Info
	} else if raw.RateLimit != nil {
		info = *raw.RateLimit
	}
	if info.Status == "" {
		return LimitEvent{}, false
	}
	ev := LimitEvent{
		Status:                info.Status,
		Type:                  info.RateLimitTyp,
		UsingOverage:          info.UsingOverage,
		OverageStatus:         info.OverageStatus,
		OverageDisabledReason: info.OverageDisabledReason,
		SeenAt:                now,
	}
	if t, ok := parseResetValue(info.ResetsAt); ok {
		ev.ResetsAt, ev.HasReset = t, true
	}
	return ev, true
}

// parseResetValue accepts the two encodings seen in the wild: a unix epoch
// (seconds, sometimes as a JSON number, sometimes as a string) and RFC3339.
func parseResetValue(v any) (time.Time, bool) {
	switch t := v.(type) {
	case float64:
		if t <= 0 {
			return time.Time{}, false
		}
		// Milliseconds if it is far too large to be seconds.
		if t > 1e11 {
			return time.UnixMilli(int64(t)).Local(), true
		}
		return time.Unix(int64(t), 0).Local(), true
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return time.Time{}, false
		}
		if ts, err := time.Parse(time.RFC3339, s); err == nil {
			return ts.Local(), true
		}
		var n float64
		if err := json.Unmarshal([]byte(s), &n); err == nil {
			return parseResetValue(n)
		}
	}
	return time.Time{}, false
}

// Observe records an event line for an account. Safe to call on every line of
// every stream; non-events are ignored.
//
// Returns the parsed event and whether the line was one.
func (o *Observer) Observe(account, line string, now time.Time) (LimitEvent, bool) {
	ev, ok := ParseLimitEvent(line, now)
	if !ok {
		return LimitEvent{}, false
	}
	o.Record(account, ev)
	return ev, true
}

// Record stores an already-parsed event.
func (o *Observer) Record(account string, ev LimitEvent) {
	if account == "" {
		account = DefaultAccountName
	}
	o.mu.Lock()
	prev, had := o.states[account]
	o.states[account] = ev
	fn := o.invalidate
	o.mu.Unlock()

	// Only invalidate on a real transition. Every run emits an "allowed" event,
	// and dropping the cache on each one would turn the TTL into a no-op and
	// spawn a probe per dispatch — the exact waste the cache exists to avoid.
	if fn != nil && (!had || prev.Status != ev.Status) {
		fn(account)
	}
}

// Last returns the most recent event for an account.
func (o *Observer) Last(account string) (LimitEvent, bool) {
	if account == "" {
		account = DefaultAccountName
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	ev, ok := o.states[account]
	return ev, ok
}

// Rejected reports whether an account is currently known-rejected.
func (o *Observer) Rejected(account string, now time.Time) bool {
	ev, ok := o.Last(account)
	return ok && ev.Rejected(now)
}

// Warned reports whether the last event carried the server's warning threshold.
func (o *Observer) Warned(account string) bool {
	ev, ok := o.Last(account)
	return ok && ev.Status == StatusWarning
}

// UsingOverage reports whether the account is spending paid overage. The
// subscription stops being free at this point, so it is worth escalating rather
// than absorbing quietly.
func (o *Observer) UsingOverage(account string) bool {
	ev, ok := o.Last(account)
	return ok && ev.UsingOverage
}

// OverageAvailable reports whether the account has a paid-overage safety net
// below the limit. Unknown accounts report false — assuming a net that is not
// there is the expensive direction to be wrong in.
func (o *Observer) OverageAvailable(account string) bool {
	ev, ok := o.Last(account)
	return ok && ev.OverageAvailable()
}

// ResetsAt returns the reset time from the last event, when it carried one.
func (o *Observer) ResetsAt(account string) (time.Time, bool) {
	ev, ok := o.Last(account)
	if !ok || !ev.HasReset {
		return time.Time{}, false
	}
	return ev.ResetsAt, true
}
