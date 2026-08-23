package ai

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestClaudeCodeProvider_Live drives the REAL Claude Code CLI end-to-end: it
// asks the provider to create a file in a temp project dir and verifies the
// file actually appears on disk, that text streamed back, and that tool calls
// were recorded. It authenticates via the local `claude` login (subscription),
// so it is gated behind ENGINE_LIVE_CLAUDECODE=1 to avoid running in CI or
// burning subscription usage unintentionally.
//
//	ENGINE_LIVE_CLAUDECODE=1 GOWORK=off go test ./ai/ -run TestClaudeCodeProvider_Live -v -timeout 300s
func TestClaudeCodeProvider_Live(t *testing.T) {
	if os.Getenv("ENGINE_LIVE_CLAUDECODE") != "1" {
		t.Skip("set ENGINE_LIVE_CLAUDECODE=1 to run the live Claude Code CLI test")
	}
	if _, err := exec.LookPath(claudeBinary); err != nil {
		t.Skipf("claude CLI not on PATH: %v", err)
	}

	dir := t.TempDir()
	const marker = "BUILT_BY_ENGINE_CLAUDECODE"

	var chunks strings.Builder
	var toolCalls []ToolCall
	var final strings.Builder
	var errs []string

	ctx := &ChatContext{
		ProjectPath: dir,
		SessionID:   "live-claudecode",
		Role:        RoleAutonomousBuilder,
		OnChunk:     func(s string, _ bool) { chunks.WriteString(s) },
		OnToolCall:  func(string, any) {},
		OnError:     func(s string) { errs = append(errs, s) },
	}
	history := []anthropicMessage{{
		Role: "user",
		Content: "Create a file named result.txt in the current directory whose entire contents are exactly: " +
			marker + "\nDo not create any other files. Then stop.",
	}}

	done := make(chan struct{})
	start := time.Now()
	go func() {
		defer close(done)
		(&claudecodeProvider{}).RunLoop(ctx, "claude-opus-4-8", "", history, &toolCalls, &final)
	}()
	select {
	case <-done:
	case <-time.After(280 * time.Second):
		t.Fatal("claudecode RunLoop did not finish within timeout")
	}
	t.Logf("run took %s", time.Since(start))

	if len(errs) > 0 {
		t.Logf("OnError messages: %v", errs)
	}

	// 1. The real artifact must exist on disk with the exact content.
	got, err := os.ReadFile(filepath.Join(dir, "result.txt"))
	if err != nil {
		entries, _ := os.ReadDir(dir)
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("result.txt not created by Claude Code: %v (dir contains: %v)", err, names)
	}
	if !strings.Contains(string(got), marker) {
		t.Fatalf("result.txt content = %q, want it to contain %q", string(got), marker)
	}

	// 2. The transcript streamed back through finalText / OnChunk.
	if strings.TrimSpace(final.String()) == "" {
		t.Error("expected non-empty final text from the run")
	}
	if chunks.Len() == 0 {
		t.Error("expected streamed chunks via OnChunk")
	}

	// 3. Claude Code performed real tool calls (e.g. Write).
	if len(toolCalls) == 0 {
		t.Error("expected at least one recorded tool call")
	}
	t.Logf("recorded %d tool calls; first: %s", len(toolCalls), firstToolName(toolCalls))
}

func firstToolName(tcs []ToolCall) string {
	if len(tcs) == 0 {
		return "(none)"
	}
	return tcs[0].Name
}
