package ai

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ── Cost Accounting ───────────────────────────────────────────────────────────

// ModelCost holds per-token pricing for a model (in USD per 1M tokens).
type ModelCost struct {
	InputPer1M  float64
	OutputPer1M float64
}

// knownModelCosts maps model name prefixes to pricing.
// Values are approximate list prices as of 2026-04.
var knownModelCosts = map[string]ModelCost{
	"claude-opus":    {InputPer1M: 15.0, OutputPer1M: 75.0},
	"claude-sonnet":  {InputPer1M: 3.0, OutputPer1M: 15.0},
	"claude-haiku":   {InputPer1M: 0.80, OutputPer1M: 4.0},
	"gpt-4o":         {InputPer1M: 2.50, OutputPer1M: 10.0},
	"gpt-4o-mini":    {InputPer1M: 0.15, OutputPer1M: 0.60},
	"gpt-4.1":        {InputPer1M: 2.0, OutputPer1M: 8.0},
	"gpt-4.1-mini":   {InputPer1M: 0.40, OutputPer1M: 1.60},
	"gpt-4.1-nano":   {InputPer1M: 0.10, OutputPer1M: 0.40},
	"gpt-5":          {InputPer1M: 5.0, OutputPer1M: 20.0},
}

// EstimateCost returns the estimated USD cost for a given number of tokens.
// Returns 0 if the model is not in the pricing table (e.g. local Ollama).
func EstimateCost(model string, inputTokens, outputTokens int) float64 {
	model = strings.ToLower(model)
	for prefix, cost := range knownModelCosts {
		if strings.HasPrefix(model, prefix) {
			return float64(inputTokens)/1e6*cost.InputPer1M + float64(outputTokens)/1e6*cost.OutputPer1M
		}
	}
	return 0
}

// UsageRecord holds token and cost data for one model call.
type UsageRecord struct {
	Model        string
	InputTokens  int
	OutputTokens int
	CostUSD      float64
	Timestamp    time.Time
}

// SessionUsage accumulates token and cost data across the life of a chat session.
// Thread-safe.
type SessionUsage struct {
	mu      sync.Mutex
	records []UsageRecord
}

// Add records one model call's usage.
func (s *SessionUsage) Add(model string, inputTokens, outputTokens int) {
	cost := EstimateCost(model, inputTokens, outputTokens)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, UsageRecord{
		Model:        model,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		CostUSD:      cost,
		Timestamp:    time.Now(),
	})
}

// Totals returns the cumulative tokens and cost across all recorded calls.
func (s *SessionUsage) Totals() (inputTokens, outputTokens int, costUSD float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.records {
		inputTokens += r.InputTokens
		outputTokens += r.OutputTokens
		costUSD += r.CostUSD
	}
	return
}

// Summary returns a human-readable usage summary.
func (s *SessionUsage) Summary() string {
	in, out, cost := s.Totals()
	if in == 0 && out == 0 {
		return ""
	}
	return fmt.Sprintf("Tokens used: %d in / %d out | Estimated cost: $%.4f", in, out, cost)
}

// ── Retry/Backoff ─────────────────────────────────────────────────────────────

const (
	maxRetries     = 3
	baseBackoff    = 1 * time.Second
	maxBackoff     = 30 * time.Second
)

// isTransientHTTPError returns true for status codes that are safe to retry.
func isTransientHTTPError(status int) bool {
	return status == http.StatusTooManyRequests ||
		status == http.StatusServiceUnavailable ||
		status == http.StatusBadGateway ||
		status == http.StatusGatewayTimeout ||
		status == http.StatusInternalServerError
}

// retryBackoff computes the wait duration for attempt n (0-indexed) using
// exponential backoff with jitter, capped at maxBackoff.
func retryBackoff(attempt int) time.Duration {
	exp := time.Duration(math.Pow(2, float64(attempt))) * baseBackoff
	if exp > maxBackoff {
		exp = maxBackoff
	}
	return exp
}

// ── Tool Quarantine ───────────────────────────────────────────────────────────

// ToolQuarantine tracks consecutive failure counts per tool and quarantines
// tools that fail twice in a row. Quarantined tools return an error immediately
// rather than executing, and the user is notified via OnError.
//
// Thread-safe.
type ToolQuarantine struct {
	mu              sync.Mutex
	consecutiveFail map[string]int
	quarantined     map[string]bool
}

// NewToolQuarantine creates a ready-to-use quarantine tracker.
func NewToolQuarantine() *ToolQuarantine {
	return &ToolQuarantine{
		consecutiveFail: make(map[string]int),
		quarantined:     make(map[string]bool),
	}
}

// Check returns (allowed bool, reason string).
// If the tool is quarantined, allowed=false and reason explains why.
func (q *ToolQuarantine) Check(toolName string) (bool, string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.quarantined[toolName] {
		return false, fmt.Sprintf("Tool '%s' is quarantined after 2 consecutive failures. It requires user review before use.", toolName)
	}
	return true, ""
}

// RecordOutcome records whether a tool call succeeded or failed.
// If a tool fails twice in a row, it is quarantined and the provided notify
// function is called with a user-facing message.
func (q *ToolQuarantine) RecordOutcome(toolName string, isError bool, notify func(string)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if isError {
		q.consecutiveFail[toolName]++
		if q.consecutiveFail[toolName] >= 2 && !q.quarantined[toolName] {
			q.quarantined[toolName] = true
			if notify != nil {
				notify(fmt.Sprintf("⚠️ Tool '%s' quarantined: failed %d times in a row. Human review required before it can run again.", toolName, q.consecutiveFail[toolName]))
			}
		}
	} else {
		// Reset on success.
		q.consecutiveFail[toolName] = 0
	}
}

// Release removes a tool from quarantine (called by user approval flow).
func (q *ToolQuarantine) Release(toolName string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.quarantined, toolName)
	delete(q.consecutiveFail, toolName)
}

// QuarantinedList returns the names of all currently quarantined tools.
func (q *ToolQuarantine) QuarantinedList() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]string, 0, len(q.quarantined))
	for name := range q.quarantined {
		out = append(out, name)
	}
	return out
}

// ── Token Budget ──────────────────────────────────────────────────────────────

// DefaultTokenBudget is the maximum number of input tokens per Chat() call.
// Derived from typical context windows: 100k leaves headroom for output.
const DefaultTokenBudget = 100_000

// BudgetExceededError is returned when a conversation exceeds the token budget.
type BudgetExceededError struct {
	Used   int
	Budget int
}

func (e *BudgetExceededError) Error() string {
	return fmt.Sprintf("token budget exceeded: %d tokens used, budget is %d", e.Used, e.Budget)
}

// TokenEstimate approximates the number of tokens in a string.
// Uses the heuristic: 1 token ≈ 4 characters (works within 10% for English).
func TokenEstimate(s string) int {
	return (len(s) + 3) / 4
}

// EstimateMessagesAnthropicFormat approximates total tokens for a slice of anthropic chat messages.
func EstimateMessagesAnthropicFormat(messages []anthropicMessage) int {
	total := 0
	for _, m := range messages {
		// Extract text content from message
		if contentStr, ok := m.Content.(string); ok {
			total += TokenEstimate(contentStr)
		} else if contentBlocks, ok := m.Content.([]contentBlock); ok {
			for _, block := range contentBlocks {
				total += TokenEstimate(block.Text)
				total += TokenEstimate(block.Content)
			}
		}
		total += 4 // per-message overhead (role, formatting)
	}
	return total
}

// TrimToTokenBudgetAnthropicFormat removes the oldest non-system messages from messages until the
// estimated token count is within budget. System messages (role=="system") are
// always preserved. Returns the trimmed slice and the estimated token count after trimming.
func TrimToTokenBudgetAnthropicFormat(messages []anthropicMessage, budget int) ([]anthropicMessage, int) {
	for {
		estimated := EstimateMessagesAnthropicFormat(messages)
		if estimated <= budget {
			return messages, estimated
		}
		// Find the oldest non-system message and remove it.
		removed := false
		for i, m := range messages {
			if m.Role != "system" {
				messages = append(messages[:i], messages[i+1:]...)
				removed = true
				break
			}
		}
		if !removed {
			// Only system messages remain — can't trim further.
			return messages, EstimateMessagesAnthropicFormat(messages)
		}
	}
}
