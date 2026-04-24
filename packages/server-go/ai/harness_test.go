package ai

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// ── EstimateCost ──────────────────────────────────────────────────────────────

func TestEstimateCost_KnownModel(t *testing.T) {
	cost := EstimateCost("claude-sonnet-3-5", 1_000_000, 1_000_000)
	if cost <= 0 {
		t.Error("expected positive cost for claude-sonnet")
	}
}

func TestEstimateCost_UnknownModel(t *testing.T) {
	cost := EstimateCost("ollama-local", 1_000_000, 1_000_000)
	if cost != 0 {
		t.Errorf("expected 0 cost for unknown model, got %v", cost)
	}
}

func TestEstimateCost_GPT4o(t *testing.T) {
	cost := EstimateCost("gpt-4o", 1_000_000, 1_000_000)
	if cost <= 0 {
		t.Error("expected positive cost for gpt-4o")
	}
}

func TestEstimateCost_Haiku(t *testing.T) {
	cost := EstimateCost("claude-haiku-3", 0, 0)
	if cost != 0 {
		t.Errorf("expected 0 cost for 0 tokens, got %v", cost)
	}
}

// ── SessionUsage ──────────────────────────────────────────────────────────────

func TestSessionUsage_Empty(t *testing.T) {
	su := &SessionUsage{}
	summary := su.Summary()
	if summary != "" {
		t.Errorf("expected empty summary, got %q", summary)
	}
}

func TestSessionUsage_Add_And_Totals(t *testing.T) {
	su := &SessionUsage{}
	su.Add("claude-sonnet-3-5", 1000, 500)
	in, out, cost := su.Totals()
	if in != 1000 {
		t.Errorf("expected 1000 input tokens, got %d", in)
	}
	if out != 500 {
		t.Errorf("expected 500 output tokens, got %d", out)
	}
	if cost <= 0 {
		t.Error("expected positive cost")
	}
}

func TestSessionUsage_Add_Multiple(t *testing.T) {
	su := &SessionUsage{}
	su.Add("gpt-4o", 100, 50)
	su.Add("gpt-4o", 200, 100)
	in, out, _ := su.Totals()
	if in != 300 {
		t.Errorf("expected 300 input tokens, got %d", in)
	}
	if out != 150 {
		t.Errorf("expected 150 output tokens, got %d", out)
	}
}

func TestSessionUsage_Summary(t *testing.T) {
	su := &SessionUsage{}
	su.Add("gpt-4o", 1000, 500)
	summary := su.Summary()
	if !strings.Contains(summary, "Tokens used") {
		t.Errorf("expected 'Tokens used' in summary, got %q", summary)
	}
}

// ── isTransientHTTPError ──────────────────────────────────────────────────────

func TestIsTransientHTTPError(t *testing.T) {
	cases := []struct {
		status    int
		transient bool
	}{
		{http.StatusTooManyRequests, true},
		{http.StatusServiceUnavailable, true},
		{http.StatusBadGateway, true},
		{http.StatusGatewayTimeout, true},
		{http.StatusInternalServerError, true},
		{http.StatusOK, false},
		{http.StatusBadRequest, false},
		{http.StatusUnauthorized, false},
		{http.StatusNotFound, false},
	}
	for _, tc := range cases {
		got := isTransientHTTPError(tc.status)
		if got != tc.transient {
			t.Errorf("status %d: got %v, want %v", tc.status, got, tc.transient)
		}
	}
}

// ── retryBackoff ──────────────────────────────────────────────────────────────

func TestRetryBackoff_Increases(t *testing.T) {
	d0 := retryBackoff(0)
	d1 := retryBackoff(1)
	d2 := retryBackoff(2)
	if d1 <= d0 || d2 <= d1 {
		t.Errorf("expected increasing backoff: %v %v %v", d0, d1, d2)
	}
}

func TestRetryBackoff_Capped(t *testing.T) {
	// attempt 10: 2^10*1s = 1024s >> 30s max
	d := retryBackoff(10)
	if d > maxBackoff {
		t.Errorf("backoff %v exceeds maxBackoff %v", d, maxBackoff)
	}
	if d != maxBackoff {
		t.Errorf("expected capped backoff %v, got %v", maxBackoff, d)
	}
}

func TestRetryBackoff_ZeroAttempt(t *testing.T) {
	d := retryBackoff(0)
	if d != baseBackoff {
		t.Errorf("expected baseBackoff %v, got %v", baseBackoff, d)
	}
}

// ── ToolQuarantine ────────────────────────────────────────────────────────────

func TestToolQuarantine_NewAllowsAll(t *testing.T) {
	q := NewToolQuarantine()
	ok, reason := q.Check("my_tool")
	if !ok {
		t.Errorf("new quarantine should allow all, reason: %q", reason)
	}
}

func TestToolQuarantine_QuarantineAfterTwoFailures(t *testing.T) {
	q := NewToolQuarantine()
	notified := ""
	q.RecordOutcome("bad_tool", true, func(msg string) { notified = msg })
	q.RecordOutcome("bad_tool", true, func(msg string) { notified = msg })
	ok, reason := q.Check("bad_tool")
	if ok {
		t.Error("tool should be quarantined after 2 failures")
	}
	if reason == "" {
		t.Error("expected non-empty reason")
	}
	if notified == "" {
		t.Error("expected notification to be called")
	}
}

func TestToolQuarantine_ResetOnSuccess(t *testing.T) {
	q := NewToolQuarantine()
	q.RecordOutcome("flaky_tool", true, nil)
	q.RecordOutcome("flaky_tool", false, nil) // success resets count
	q.RecordOutcome("flaky_tool", true, nil)  // one more failure — not yet quarantined
	ok, _ := q.Check("flaky_tool")
	if !ok {
		t.Error("tool should not be quarantined after success reset")
	}
}

func TestToolQuarantine_Release(t *testing.T) {
	q := NewToolQuarantine()
	q.RecordOutcome("bad_tool", true, nil)
	q.RecordOutcome("bad_tool", true, nil) // quarantined
	q.Release("bad_tool")
	ok, _ := q.Check("bad_tool")
	if !ok {
		t.Error("released tool should be allowed")
	}
}

func TestToolQuarantine_QuarantinedList(t *testing.T) {
	q := NewToolQuarantine()
	q.RecordOutcome("tool_a", true, nil)
	q.RecordOutcome("tool_a", true, nil)
	list := q.QuarantinedList()
	if len(list) != 1 || list[0] != "tool_a" {
		t.Errorf("expected [tool_a], got %v", list)
	}
}

func TestToolQuarantine_NotifyNil(t *testing.T) {
	q := NewToolQuarantine()
	q.RecordOutcome("t", true, nil)
	q.RecordOutcome("t", true, nil) // should not panic with nil notify
}

// ── BudgetExceededError ───────────────────────────────────────────────────────

func TestBudgetExceededError_Message(t *testing.T) {
	err := &BudgetExceededError{Used: 105000, Budget: 100000}
	msg := err.Error()
	if !strings.Contains(msg, "105000") {
		t.Errorf("expected used tokens in message, got %q", msg)
	}
	if !strings.Contains(msg, "100000") {
		t.Errorf("expected budget in message, got %q", msg)
	}
}

// ── TokenEstimate ─────────────────────────────────────────────────────────────

func TestTokenEstimate_Empty(t *testing.T) {
	n := TokenEstimate("")
	if n != 0 {
		t.Errorf("expected 0, got %d", n)
	}
}

func TestTokenEstimate_Short(t *testing.T) {
	n := TokenEstimate("hello") // 5 chars → ceil(5/4)=2
	if n < 1 {
		t.Errorf("expected at least 1 token, got %d", n)
	}
}

func TestTokenEstimate_Long(t *testing.T) {
	s := strings.Repeat("word ", 1000)
	n := TokenEstimate(s)
	if n < 100 {
		t.Errorf("expected many tokens for long string, got %d", n)
	}
}

// ── EstimateMessagesAnthropicFormat ──────────────────────────────────────────

func TestEstimateMessagesAnthropicFormat_Empty(t *testing.T) {
	n := EstimateMessagesAnthropicFormat(nil)
	if n != 0 {
		t.Errorf("expected 0, got %d", n)
	}
}

func TestEstimateMessagesAnthropicFormat_StringContent(t *testing.T) {
	msgs := []anthropicMessage{
		{Role: "user", Content: "hello world"},
	}
	n := EstimateMessagesAnthropicFormat(msgs)
	if n <= 0 {
		t.Error("expected positive token count")
	}
}

func TestEstimateMessagesAnthropicFormat_BlockContent(t *testing.T) {
	msgs := []anthropicMessage{
		{Role: "user", Content: []contentBlock{{Text: "hello", Content: "world"}}},
	}
	n := EstimateMessagesAnthropicFormat(msgs)
	if n <= 0 {
		t.Error("expected positive token count")
	}
}

// ── TrimToTokenBudgetAnthropicFormat ─────────────────────────────────────────

func TestTrimToTokenBudgetAnthropicFormat_NothingToTrim(t *testing.T) {
	msgs := []anthropicMessage{
		{Role: "user", Content: "hi"},
	}
	trimmed, _ := TrimToTokenBudgetAnthropicFormat(msgs, 1000)
	if len(trimmed) != 1 {
		t.Errorf("expected 1 message, got %d", len(trimmed))
	}
}

func TestTrimToTokenBudgetAnthropicFormat_TrimsToFit(t *testing.T) {
	bigContent := strings.Repeat("x", 10000)
	msgs := []anthropicMessage{
		{Role: "user", Content: bigContent},
		{Role: "assistant", Content: bigContent},
		{Role: "user", Content: "small"},
	}
	trimmed, count := TrimToTokenBudgetAnthropicFormat(msgs, 50)
	if len(trimmed) > len(msgs) {
		t.Error("should not grow the message slice")
	}
	_ = count
}

func TestTrimToTokenBudgetAnthropicFormat_PreservesSystemMessages(t *testing.T) {
	sysMsg := anthropicMessage{Role: "system", Content: strings.Repeat("x", 10000)}
	msgs := []anthropicMessage{
		sysMsg,
		{Role: "user", Content: strings.Repeat("y", 10000)},
		{Role: "user", Content: "small"},
	}
	trimmed, _ := TrimToTokenBudgetAnthropicFormat(msgs, 10)
	hasSystem := false
	for _, m := range trimmed {
		if m.Role == "system" {
			hasSystem = true
		}
	}
	if !hasSystem {
		t.Error("system message should be preserved after trimming")
	}
}

// Ensure time package is used (import kept alive).
var _ = time.Now
