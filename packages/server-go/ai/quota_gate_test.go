package ai

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/engine/server/quota"
)

// The single most important property of the wiring: with governance disabled,
// nothing about a claudecode run changes. Quota awareness is an optimisation,
// and an engine that behaves differently — or worse, refuses to build — because
// of it has made things worse, not better.
func TestQuotaDisabledChangesNothing(t *testing.T) {
	t.Setenv("ENGINE_QUOTA", "0")
	resetQuotaGateForTest()

	d := quotaBefore(t.Context(), RoleImplementer, "claude-opus-4-8", "/tmp/proj", []string{"PATH=/bin"})
	if !d.Allow {
		t.Fatal("a disabled gate must never block a dispatch")
	}
	if d.Model != "claude-opus-4-8" {
		t.Errorf("model = %q, want the requested model untouched", d.Model)
	}
	if d.Env != nil {
		t.Error("a disabled gate must not rewrite the environment")
	}
	if d.Governed {
		t.Error("Governed should be false when governance is off")
	}
}

// Observing a limit event must be a pure parse — no account resolution, no
// shelling out — because it happens inline on a live stream.
func TestObserveLimitEventDoesNotBuildTheGate(t *testing.T) {
	t.Setenv("ENGINE_QUOTA", "1")
	resetQuotaGateForTest()

	line := `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning","resetsAt":1787536800,"rateLimitType":"five_hour","isUsingOverage":false}}`
	ev, ok := observeLimitEvent("default", line)
	if !ok {
		t.Fatal("a real rate_limit_event did not parse")
	}
	if ev.Status != "allowed_warning" {
		t.Errorf("status = %q", ev.Status)
	}
	if gateBuiltForTest() {
		t.Error("observing built the gate — that would shell out to `claude auth status` mid-stream")
	}
}

// The gate may hold or cheapen the model routing chose. It must never upgrade
// it: asking for Sonnet and silently getting Opus is both a surprise and the
// exact opposite of spending less.
func TestModelRankOrdersByCost(t *testing.T) {
	tests := []struct {
		a, b string
		less bool
	}{
		{"claude-haiku-4-5", "claude-sonnet-4-5", true},
		{"claude-sonnet-4-5", "claude-opus-4-8", true},
		{"claude-haiku-4-5", "claude-opus-4-8", true},
		{"claude-opus-4-8", "claude-opus-4-8", false},
	}
	for _, tt := range tests {
		if got := modelRank(tt.a) < modelRank(tt.b); got != tt.less {
			t.Errorf("rank(%s) < rank(%s) = %v, want %v", tt.a, tt.b, got, tt.less)
		}
	}
	// An unknown model ranks highest so it is never treated as a downgrade
	// target — we cannot know it is weaker, so we do not assume it.
	if modelRank("some-future-model") <= modelRank("claude-opus-4-8") {
		t.Error("an unrecognised model must not be silently downgraded onto")
	}
}

func TestClaudeRunStatsCountsCacheReads(t *testing.T) {
	// Numbers taken from a real result event.
	s := claudeRunStats{
		InputTokens:         10,
		OutputTokens:        33,
		CacheCreationTokens: 8903,
		CacheReadTokens:     18140,
	}
	if got := s.TotalTokens(); got != 27086 {
		t.Errorf("total = %d, want 27086", got)
	}
	// Excluding cache reads would report this 27k-token run as 8.9k and make the
	// biggest quota driver — a bloated context replayed every turn — invisible.
	if s.TotalTokens() <= s.InputTokens+s.OutputTokens+s.CacheCreationTokens {
		t.Error("cache reads must be counted; they are cheaper per token, not free")
	}
}

// The result event's token counts must actually reach the stats, or the ledger
// learns from zeros and every configuration looks equally cheap.
func TestParseClaudeStreamHarvestsUsage(t *testing.T) {
	t.Setenv("ENGINE_QUOTA", "0")
	resetQuotaGateForTest()

	stream := strings.Join([]string{
		`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1787536800,"rateLimitType":"five_hour"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"ok"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"ok","num_turns":1,"total_cost_usd":0.0207,` +
			`"usage":{"input_tokens":10,"output_tokens":33,"cache_creation_input_tokens":8903,"cache_read_input_tokens":18140},` +
			`"subagent_stats":{"spawned":2}}`,
	}, "\n")

	ctx := &ChatContext{}
	var toolCalls []ToolCall
	var final strings.Builder
	stats := parseClaudeStreamWithStats(ctx, strings.NewReader(stream), &toolCalls, &final, "default")

	if stats.TotalTokens() != 27086 {
		t.Errorf("totalTokens = %d, want 27086", stats.TotalTokens())
	}
	if stats.NumTurns != 1 {
		t.Errorf("numTurns = %d, want 1", stats.NumTurns)
	}
	if stats.SubagentsSpawned != 2 {
		t.Errorf("subagentsSpawned = %d, want 2", stats.SubagentsSpawned)
	}
	if stats.CostUSD == 0 {
		t.Error("costUSD should have been captured")
	}
	if stats.SawError {
		t.Error("a successful run must not be recorded as an error")
	}
	if final.String() != "ok" {
		t.Errorf("final text = %q, want ok — the existing streaming behaviour must be unchanged", final.String())
	}
}

// An "allowed" event fires on every single run, and "allowed_warning" is the
// normal state of a fleet that targets 100% of its window — neither is an error.
// Only rejection and paid overage reach OnError.
func TestOnlyActionableLimitStatesAreSurfaced(t *testing.T) {
	t.Setenv("ENGINE_QUOTA", "0")
	resetQuotaGateForTest()

	tests := []struct {
		name      string
		info      string
		wantErrIn string
	}{
		{"allowed is silent", `{"status":"allowed","rateLimitType":"five_hour"}`, ""},
		{"warning is not an error", `{"status":"allowed_warning","rateLimitType":"five_hour"}`, ""},
		{"rejection is reported", `{"status":"rejected","rateLimitType":"five_hour"}`, "rate limited"},
		{"overage is reported", `{"status":"allowed","rateLimitType":"five_hour","isUsingOverage":true}`, "OVERAGE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var errs []string
			ctx := &ChatContext{OnError: func(e string) { errs = append(errs, e) }}
			var toolCalls []ToolCall
			var final strings.Builder
			line := `{"type":"rate_limit_event","rate_limit_info":` + tt.info + `}`
			parseClaudeStreamWithStats(ctx, strings.NewReader(line), &toolCalls, &final, "default")

			if tt.wantErrIn == "" {
				if len(errs) != 0 {
					t.Errorf("expected silence, got %v", errs)
				}
				return
			}
			if len(errs) != 1 {
				t.Fatalf("expected exactly one message, got %v", errs)
			}
			if !strings.Contains(errs[0], tt.wantErrIn) {
				t.Errorf("message %q does not mention %q", errs[0], tt.wantErrIn)
			}
		})
	}
}

// An absent reset time must not print as 1970.
func TestDescribeResetOmitsAbsentTimes(t *testing.T) {
	t.Setenv("ENGINE_QUOTA", "0")
	resetQuotaGateForTest()

	var errs []string
	ctx := &ChatContext{OnError: func(e string) { errs = append(errs, e) }}
	var toolCalls []ToolCall
	var final strings.Builder
	line := `{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","rateLimitType":"five_hour"}}`
	parseClaudeStreamWithStats(ctx, strings.NewReader(line), &toolCalls, &final, "default")

	if len(errs) != 1 {
		t.Fatalf("expected one message, got %v", errs)
	}
	if strings.Contains(errs[0], "resets") {
		t.Errorf("no reset time was present, but the message claims one: %q", errs[0])
	}
	if strings.Contains(errs[0], "1970") {
		t.Errorf("a zero time leaked into the message: %q", errs[0])
	}
}

// Test that the snapshot cache returns immediately without blocking on probe.
func TestSnapshotCacheReturnsImmediately(t *testing.T) {
	// Create a cache with a long refresh interval so first refresh is still in progress.
	now := time.Now()
	cache := newSnapshotCache(10*time.Second, func() time.Time { return now })

	// Create a mock refresh function that sleeps 3 seconds.
	started := make(chan struct{}, 1)
	refreshFn := func(ctx context.Context) quota.PooledSnapshot {
		started <- struct{}{}
		time.Sleep(3 * time.Second) // Simulate slow probe
		return quota.PooledSnapshot{
			Machine:     "test",
			GeneratedAt: now,
			Accounts:    []quota.SnapshotAccount{{Name: "account1"}},
		}
	}

	cache.start(refreshFn)
	defer cache.stop()

	// Wait for refresh to start, then immediately request the snapshot.
	<-started
	start := time.Now()
	snap, stale := cache.get()
	elapsed := time.Since(start)

	// The get() should return in << 100ms, not wait for the 3 second refresh.
	if elapsed > 100*time.Millisecond {
		t.Errorf("get() took %v, expected << 100ms", elapsed)
	}

	// Before the first refresh completes, the snapshot is stale.
	if !stale {
		t.Error("snapshot should be stale before first refresh completes")
	}

	// The snapshot itself should be minimal (never refreshed).
	if snap.Machine != "" {
		t.Errorf("machine = %q, expected empty before refresh", snap.Machine)
	}
}

// Test that after refresh, the snapshot is returned with correct stale flag.
func TestSnapshotCacheMarksFreshAndStale(t *testing.T) {
	baseTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	currentTime := baseTime
	now := func() time.Time { return currentTime }
	cache := newSnapshotCache(30*time.Second, now)

	// Refresh immediately with a known snapshot.
	started := make(chan struct{}, 1)
	refreshFn := func(ctx context.Context) quota.PooledSnapshot {
		started <- struct{}{}
		return quota.PooledSnapshot{
			Machine:     "test-box",
			GeneratedAt: currentTime,
			Accounts:    []quota.SnapshotAccount{{Name: "acc1", SessionPct: 50}},
		}
	}

	cache.start(refreshFn)
	defer cache.stop()

	// First refresh runs immediately; wait for it.
	<-started
	time.Sleep(10 * time.Millisecond)

	// Fresh snapshot (< 30s old) should not be stale.
	currentTime = baseTime.Add(15 * time.Second)
	snap, stale := cache.get()
	if stale {
		t.Error("snapshot 15s old should not be stale (interval=30s)")
	}
	if snap.Machine != "test-box" {
		t.Errorf("machine = %q, want test-box", snap.Machine)
	}

	// Stale snapshot (> 2×30s = 60s old) should be marked stale.
	currentTime = baseTime.Add(70 * time.Second)
	snap, stale = cache.get()
	if !stale {
		t.Error("snapshot 70s old should be stale (2× interval = 60s)")
	}
	// Data is still there, just marked stale so caller knows it's old.
	if snap.Machine != "test-box" {
		t.Errorf("stale snapshot still has data: machine = %q", snap.Machine)
	}
}

// Test that the refresh loop stops cleanly with no goroutine leak.
func TestSnapshotCacheStopsCleanly(t *testing.T) {
	cache := newSnapshotCache(10*time.Millisecond, time.Now)

	tick := 0
	refreshFn := func(ctx context.Context) quota.PooledSnapshot {
		tick++
		return quota.PooledSnapshot{Machine: "test", GeneratedAt: time.Now()}
	}

	cache.start(refreshFn)

	// Let a refresh or two run.
	time.Sleep(50 * time.Millisecond)
	beforeTick := tick

	// Stop should block until the goroutine exits.
	cache.stop()

	// After a short delay, if the goroutine is still running it would increment tick.
	time.Sleep(50 * time.Millisecond)
	afterTick := tick

	// tick might have incremented once more before the stop was processed,
	// but not significantly more (no indefinite loop).
	if afterTick > beforeTick+2 {
		t.Errorf("too many ticks after stop: %d -> %d", beforeTick, afterTick)
	}
}

// Test that QuotaSnapshot returns the cache with stale flag set.
func TestQuotaSnapshotReturnsStaleFlag(t *testing.T) {
	t.Setenv("ENGINE_QUOTA", "0")
	resetQuotaGateForTest()

	// With governance disabled, QuotaSnapshot returns false OK.
	snap, ok := QuotaSnapshot(context.Background())
	if ok {
		t.Fatal("QuotaSnapshot should return false when governance is disabled")
	}
	if snap.Stale {
		t.Error("disabled snapshot should not have stale flag set (only enabled does)")
	}
}
