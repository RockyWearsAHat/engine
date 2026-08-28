package quota

import "strings"

// Price table. USD per 1M tokens. Cache write = 1.25x input, cache read =
// 0.1x input (Anthropic standard). Subscription runs report total_cost_usd 0
// on the wire, so ledger rows carried usd 0 forever; this table fills the gap.
// Keyed by tier substring: "fable", "opus", "sonnet", "haiku". Unknown → 0.
type Price struct {
	InputPer1M  float64
	OutputPer1M float64
}

var priceTable = []struct {
	match string
	p     Price
}{
	{"fable", Price{10, 50}},
	{"mythos", Price{10, 50}},
	{"opus", Price{5, 25}},
	{"sonnet", Price{3, 15}},
	{"haiku", Price{1, 5}},
}

// PriceFor finds price by model tier or id substring. ok=false → unknown model.
func PriceFor(model string) (Price, bool) {
	m := strings.ToLower(strings.TrimSpace(model))
	for _, e := range priceTable {
		if strings.Contains(m, e.match) {
			return e.p, true
		}
	}
	return Price{}, false
}

// CostUSD prices one run from stream-json usage counters.
func CostUSD(model string, input, output, cacheWrite, cacheRead int64) float64 {
	p, ok := PriceFor(model)
	if !ok {
		return 0
	}
	in := p.InputPer1M / 1e6
	return float64(input)*in +
		float64(cacheWrite)*in*1.25 +
		float64(cacheRead)*in*0.10 +
		float64(output)*p.OutputPer1M/1e6
}
