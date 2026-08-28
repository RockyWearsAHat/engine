package ai

import (
	"strings"
	"testing"

	"github.com/engine/server/quota"
)

// Subscription runs carry total_cost_usd 0 on the wire. Usage counters must
// still price to a non-zero usd so ledger rows stop reading $0.
func TestFakeUsageEventPricesToNonZeroUSD(t *testing.T) {
	t.Setenv("ENGINE_QUOTA", "0")
	resetQuotaGateForTest()

	stream := strings.Join([]string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"ok"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"ok","num_turns":1,"total_cost_usd":0,` +
			`"usage":{"input_tokens":1200,"output_tokens":300,"cache_creation_input_tokens":8000,"cache_read_input_tokens":20000}}`,
	}, "\n")
	ctx := &ChatContext{}
	var calls []ToolCall
	var final strings.Builder
	stats := parseClaudeStreamWithStats(ctx, strings.NewReader(stream), &calls, &final, "default")
	if stats.CostUSD != 0 {
		t.Fatalf("wire usd = %v, test wants 0 to exercise the price table", stats.CostUSD)
	}
	usd := quota.CostUSD("haiku", stats.InputTokens, stats.OutputTokens, stats.CacheCreationTokens, stats.CacheReadTokens)
	if usd <= 0 {
		t.Fatalf("priced usd = %v, want > 0", usd)
	}
	// 1200*1 + 8000*1.25 + 20000*0.1 + 300*5 per 1M = 0.0012+0.01+0.002+0.0015
	if usd < 0.0146 || usd > 0.0148 {
		t.Errorf("usd = %v, want ~0.0147", usd)
	}
}
