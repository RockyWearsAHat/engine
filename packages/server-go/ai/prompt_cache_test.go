package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// ── buildSystemBlocks ─────────────────────────────────────────────────────────

func TestBuildSystemBlocks_EmptyPromptYieldsNoBlocks(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\t "} {
		if got := buildSystemBlocks(in); got != nil {
			t.Fatalf("buildSystemBlocks(%q) = %#v, want nil", in, got)
		}
	}
}

func TestBuildSystemBlocks_CarriesCacheBreakpoint(t *testing.T) {
	blocks := buildSystemBlocks("you are engine")
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	b := blocks[0]
	if b.Type != "text" || b.Text != "you are engine" {
		t.Fatalf("unexpected block %#v", b)
	}
	if b.CacheControl == nil || b.CacheControl.Type != "ephemeral" {
		t.Fatalf("want ephemeral cache breakpoint, got %#v", b.CacheControl)
	}
	if b.CacheControl.TTL != "" {
		t.Fatalf("want default TTL, got %q", b.CacheControl.TTL)
	}
}

// ── applyMessageCacheBreakpoint ───────────────────────────────────────────────

func TestApplyMessageCacheBreakpoint_NilAndEmpty(t *testing.T) {
	applyMessageCacheBreakpoint(nil) // must not panic

	req := &anthropicRequest{}
	applyMessageCacheBreakpoint(req)
	if len(req.Messages) != 0 {
		t.Fatalf("empty request gained messages: %#v", req.Messages)
	}
}

func TestApplyMessageCacheBreakpoint_StringContentBecomesMarkedBlock(t *testing.T) {
	req := &anthropicRequest{Messages: []anthropicMessage{
		{Role: "user", Content: "first"},
		{Role: "user", Content: "second"},
	}}
	applyMessageCacheBreakpoint(req)

	blocks, ok := req.Messages[1].Content.([]contentBlock)
	if !ok {
		t.Fatalf("last content not converted to blocks: %T", req.Messages[1].Content)
	}
	if len(blocks) != 1 || blocks[0].Text != "second" || blocks[0].Type != "text" {
		t.Fatalf("unexpected blocks %#v", blocks)
	}
	if blocks[0].CacheControl == nil {
		t.Fatal("last block is not marked")
	}
	// Earlier messages must be left alone — only the tail carries a breakpoint.
	if _, converted := req.Messages[0].Content.([]contentBlock); converted {
		t.Fatal("earlier message was rewritten")
	}
}

func TestApplyMessageCacheBreakpoint_BlankStringContentIsSkipped(t *testing.T) {
	req := &anthropicRequest{Messages: []anthropicMessage{{Role: "user", Content: "  "}}}
	applyMessageCacheBreakpoint(req)
	if _, converted := req.Messages[0].Content.([]contentBlock); converted {
		t.Fatal("blank content should not become a cached block")
	}
}

func TestApplyMessageCacheBreakpoint_MarksLastOfBlockSlice(t *testing.T) {
	req := &anthropicRequest{Messages: []anthropicMessage{{Role: "user", Content: []contentBlock{
		{Type: "text", Text: "a"},
		{Type: "text", Text: "b"},
	}}}}
	applyMessageCacheBreakpoint(req)

	blocks := req.Messages[0].Content.([]contentBlock)
	if blocks[0].CacheControl != nil {
		t.Fatal("non-final block was marked")
	}
	if blocks[1].CacheControl == nil || blocks[1].CacheControl.Type != "ephemeral" {
		t.Fatalf("final block not marked: %#v", blocks[1].CacheControl)
	}
}

func TestApplyMessageCacheBreakpoint_EmptyBlockSliceAndUnknownType(t *testing.T) {
	req := &anthropicRequest{Messages: []anthropicMessage{{Role: "user", Content: []contentBlock{}}}}
	applyMessageCacheBreakpoint(req)
	if got := req.Messages[0].Content.([]contentBlock); len(got) != 0 {
		t.Fatalf("empty block slice was modified: %#v", got)
	}

	req = &anthropicRequest{Messages: []anthropicMessage{{Role: "user", Content: 42}}}
	applyMessageCacheBreakpoint(req)
	if req.Messages[0].Content != 42 {
		t.Fatalf("unknown content type was rewritten: %#v", req.Messages[0].Content)
	}
}

// The regression this guards: req.Messages shares a backing array with the
// caller's live history. Marking in place would persist breakpoints into stored
// history and add one more every turn, eventually breaking the four-per-request
// limit.
func TestApplyMessageCacheBreakpoint_DoesNotMutateCallerHistory(t *testing.T) {
	history := []anthropicMessage{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: []contentBlock{{Type: "text", Text: "two"}}},
	}
	req := &anthropicRequest{Messages: history}
	applyMessageCacheBreakpoint(req)

	if history[1].Content.([]contentBlock)[0].CacheControl != nil {
		t.Fatal("caller history was mutated in place")
	}
	if req.Messages[0].Content != "one" {
		t.Fatalf("copied request lost earlier content: %#v", req.Messages[0].Content)
	}
}

// Applying twice must still leave exactly one breakpoint in the history, which
// is what keeps a long session under the four-breakpoint ceiling.
func TestApplyMessageCacheBreakpoint_RepeatedTurnsDoNotAccumulate(t *testing.T) {
	history := []anthropicMessage{{Role: "user", Content: "one"}}

	for turn := 0; turn < 3; turn++ {
		req := &anthropicRequest{Messages: history}
		applyMessageCacheBreakpoint(req)
		if n := countBreakpoints(t, req.Messages); n != 1 {
			t.Fatalf("turn %d: %d breakpoints, want 1", turn, n)
		}
	}
	if countBreakpoints(t, history) != 0 {
		t.Fatal("breakpoints leaked back into the stored history")
	}
}

func countBreakpoints(t *testing.T, msgs []anthropicMessage) int {
	t.Helper()
	n := 0
	for _, m := range msgs {
		blocks, ok := m.Content.([]contentBlock)
		if !ok {
			continue
		}
		for _, b := range blocks {
			if b.CacheControl != nil {
				n++
			}
		}
	}
	return n
}

// ── wire format ───────────────────────────────────────────────────────────────

func TestAnthropicRequest_SerialisesCacheControl(t *testing.T) {
	req := anthropicRequest{
		Model:     "claude-opus-5",
		MaxTokens: 8192,
		System:    buildSystemBlocks("stable prefix"),
		Messages:  []anthropicMessage{{Role: "user", Content: "hello"}},
		Tools:     []anthropicTool{{Name: "read_file", Description: "d", InputSchema: map[string]any{}}},
		Stream:    true,
	}
	applyMessageCacheBreakpoint(&req)

	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(raw)

	// System must render as an array of blocks, not a bare string; only the
	// array form can carry a breakpoint.
	if !strings.Contains(got, `"system":[{"type":"text","text":"stable prefix"`) {
		t.Fatalf("system not rendered as blocks: %s", got)
	}
	if n := strings.Count(got, `"cache_control":{"type":"ephemeral"}`); n != 2 {
		t.Fatalf("want 2 breakpoints (system + last message), got %d in %s", n, got)
	}
	// A tool with no breakpoint must not emit an empty cache_control key.
	if strings.Contains(got, `"input_schema":{},"cache_control":null`) {
		t.Fatalf("nil cache_control leaked into the payload: %s", got)
	}
}

func TestAnthropicRequest_OmitsEmptySystem(t *testing.T) {
	raw, err := json.Marshal(anthropicRequest{Model: "m", System: buildSystemBlocks("")})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), `"system"`) {
		t.Fatalf("empty system should be omitted, got %s", raw)
	}
}

func TestEphemeralCache_IsDefaultTTL(t *testing.T) {
	cc := ephemeralCache()
	if cc.Type != "ephemeral" || cc.TTL != "" {
		t.Fatalf("unexpected %#v", cc)
	}
	raw, _ := json.Marshal(cc)
	if string(raw) != `{"type":"ephemeral"}` {
		t.Fatalf("wire form = %s", raw)
	}
}

// ── usage counters ────────────────────────────────────────────────────────────

func TestTokenCountFromUsage_ReadsCacheCounters(t *testing.T) {
	usageMap := map[string]any{
		"input_tokens":                float64(10),
		"output_tokens":               float64(5),
		"cache_creation_input_tokens": float64(2048),
		"cache_read_input_tokens":     float64(4096),
	}
	if got := tokenCountFromUsage(usageMap, "cache_creation_input_tokens"); got != 2048 {
		t.Fatalf("cache_creation = %d", got)
	}
	if got := tokenCountFromUsage(usageMap, "cache_read_input_tokens"); got != 4096 {
		t.Fatalf("cache_read = %d", got)
	}
	if got := tokenCountFromUsage(usageMap, "absent"); got != 0 {
		t.Fatalf("absent key = %d, want 0", got)
	}
}
