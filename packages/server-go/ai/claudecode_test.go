package ai

import (
	"strings"
	"testing"
)

func TestNewProviderForName_ClaudeCode(t *testing.T) {
	for _, name := range []string{"claudecode", "claude-code", "claude_code"} {
		if _, ok := newProviderForName(name).(*claudecodeProvider); !ok {
			t.Errorf("newProviderForName(%q) did not return *claudecodeProvider", name)
		}
	}
	// Sanity: a real API name must NOT route to the CLI provider.
	if _, ok := newProviderForName("anthropic").(*claudecodeProvider); ok {
		t.Error("anthropic incorrectly routed to claudecodeProvider")
	}
}

func TestInferredProviderForModel_ClaudeCodeSentinel(t *testing.T) {
	// The sentinel must route to the CLI provider, NOT the raw Anthropic API,
	// even though it starts with "claude".
	for _, m := range []string{"claude-code", "claudecode", "CLAUDE-CODE"} {
		if got := inferredProviderForModel(m); got != "claudecode" {
			t.Errorf("inferredProviderForModel(%q) = %q, want claudecode", m, got)
		}
	}
	// A real model id must still resolve to anthropic.
	if got := inferredProviderForModel("claude-opus-4-8"); got != "anthropic" {
		t.Errorf("inferredProviderForModel(claude-opus-4-8) = %q, want anthropic", got)
	}
}

func TestResolveProvider_ClaudeCode(t *testing.T) {
	if got := resolveProvider("claudecode", ""); got != "claudecode" {
		t.Errorf("resolveProvider(claudecode) = %q, want claudecode", got)
	}
	if got := resolveProvider("claude-code", ""); got != "claudecode" {
		t.Errorf("resolveProvider(claude-code) = %q, want claudecode", got)
	}
}

func TestBuildClaudeArgs(t *testing.T) {
	ctx := &ChatContext{ProjectPath: "/tmp/proj"}
	args := buildClaudeArgs(ctx, "claude-opus-4-8", "be a builder", -1)
	joined := strings.Join(args, " ")

	for _, want := range []string{
		"-p", "--output-format stream-json", "--verbose",
		"--permission-mode bypassPermissions",
		"--model claude-opus-4-8",
		"--append-system-prompt be a builder",
		"--add-dir /tmp/proj",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q missing %q", joined, want)
		}
	}
}

func TestBuildClaudeArgs_SentinelModelOmitted(t *testing.T) {
	ctx := &ChatContext{}
	args := buildClaudeArgs(ctx, "claude-code", "", -1)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--model") {
		t.Errorf("sentinel model should not produce --model flag; got %q", joined)
	}
	// Every session gets the one-item-one-worker directive appended, even
	// with an empty input system prompt — so --append-system-prompt is now
	// always present, and must carry that directive.
	if !strings.Contains(joined, "--append-system-prompt") {
		t.Errorf("the one-worker directive should still produce --append-system-prompt; got %q", joined)
	}
	if !strings.Contains(joined, "Do NOT spawn sub-agents or teams") {
		t.Errorf("missing one-item-one-worker directive; got %q", joined)
	}
	if strings.Contains(joined, "--add-dir") {
		t.Errorf("empty project path should not produce --add-dir; got %q", joined)
	}
}

// The default brief — every session, regardless of fanout tier — must tell
// the model to do the work itself rather than spinning up a team. This is
// what stopped a single dispatched item from fanning out into several
// concurrent `claude -p` processes.
func TestBuildClaudeArgs_OneWorkerDirectiveAlwaysPresent(t *testing.T) {
	ctx := &ChatContext{ProjectPath: "/tmp/proj"}
	for _, fanout := range []int{-1, 0, 1, 2} {
		joined := strings.Join(buildClaudeArgs(ctx, "claude-sonnet-4-5", "sys", fanout), " ")
		if !strings.Contains(joined, "Do the work yourself in this one session. Do NOT spawn sub-agents or teams.") {
			t.Errorf("fanout %d: missing one-item-one-worker directive; got %q", fanout, joined)
		}
	}
}

// The subagent ceiling has to reach the process, not just the /quota response.
func TestBuildClaudeArgs_SubagentFanout(t *testing.T) {
	ctx := &ChatContext{ProjectPath: "/tmp/proj"}

	// Zero is the tier where it matters most, and it is the one case the CLI
	// lets us make binding: no Task tool, no subagents, full stop.
	zero := strings.Join(buildClaudeArgs(ctx, "claude-sonnet-4-5", "sys", 0), " ")
	if !strings.Contains(zero, "--disallowedTools Task") {
		t.Errorf("fanout 0 must disallow the Task tool; got %q", zero)
	}

	// Above zero the CLI has no numeric control, so the budget is stated to the
	// model. Assert it is stated — and that the Task tool is NOT disallowed,
	// since the whole point is that some fanout is permitted.
	two := strings.Join(buildClaudeArgs(ctx, "claude-sonnet-4-5", "sys", 2), " ")
	if !strings.Contains(two, "at most 2 subagent") {
		t.Errorf("fanout 2 should state the budget in the system prompt; got %q", two)
	}
	if strings.Contains(two, "--disallowedTools") {
		t.Errorf("a positive fanout must not disallow Task; got %q", two)
	}

	// Ungoverned adds nothing either way.
	none := strings.Join(buildClaudeArgs(ctx, "claude-sonnet-4-5", "sys", -1), " ")
	if strings.Contains(none, "--disallowedTools") || strings.Contains(none, "at most") {
		t.Errorf("negative fanout should add no subagent controls; got %q", none)
	}
}

func TestBuildClaudeArgs_TaskModeOverridesFanout(t *testing.T) {
	// Task mode (single worklist item) must force fanout=0 regardless of
	// governor's ceiling, to keep one process per item, not concurrent teams.
	ctx := &ChatContext{ProjectPath: "/tmp/proj", TaskID: "item-123"}

	// Even a high fanout (2) should be overridden to 0 in task mode
	// by the caller (claudecode.RunLoop), but test the override works.
	// The override happens before buildClaudeArgs is called, so this test
	// just verifies that fanout 0 produces the right args.
	zero := strings.Join(buildClaudeArgs(ctx, "claude-sonnet-4-5", "sys", 0), " ")
	if !strings.Contains(zero, "--disallowedTools Task") {
		t.Errorf("task mode with fanout 0 must disallow Task tool; got %q", zero)
	}
}

func TestFlattenHistoryForCLI(t *testing.T) {
	// Single turn → verbatim.
	single := []anthropicMessage{{Role: "user", Content: "build a thing"}}
	if got := flattenHistoryForCLI(single); got != "build a thing" {
		t.Errorf("single-turn = %q, want verbatim", got)
	}

	// Multi-turn → labelled transcript, empty turns dropped.
	multi := []anthropicMessage{
		{Role: "user", Content: "do step 1"},
		{Role: "assistant", Content: "done step 1"},
		{Role: "user", Content: ""}, // dropped
		{Role: "user", Content: "now step 2"},
	}
	got := flattenHistoryForCLI(multi)
	for _, want := range []string{"Human: do step 1", "Assistant: done step 1", "Human: now step 2"} {
		if !strings.Contains(got, want) {
			t.Errorf("multi-turn missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Human: \n") || strings.HasSuffix(got, "Human: ") {
		t.Errorf("empty turn was not dropped: %q", got)
	}

	if flattenHistoryForCLI(nil) != "" {
		t.Error("nil history should flatten to empty string")
	}
}

func TestCliMessageText_Blocks(t *testing.T) {
	// []contentBlock with text + tool_result + tool_use.
	blocks := []contentBlock{
		{Type: "text", Text: "hello"},
		{Type: "tool_result", Content: "exit 0"},
		{Type: "tool_use", Name: "write_file"},
	}
	got := cliMessageText(blocks)
	for _, want := range []string{"hello", "exit 0", "[called tool: write_file]"} {
		if !strings.Contains(got, want) {
			t.Errorf("cliMessageText blocks missing %q in %q", want, got)
		}
	}

	// []any (post-JSON shape).
	anyBlocks := []any{
		map[string]any{"type": "text", "text": "world"},
		map[string]any{"type": "tool_use", "name": "shell"},
	}
	got = cliMessageText(anyBlocks)
	if !strings.Contains(got, "world") || !strings.Contains(got, "[called tool: shell]") {
		t.Errorf("cliMessageText []any = %q", got)
	}

	if cliMessageText("plain") != "plain" {
		t.Error("string content should pass through")
	}
}

// realStreamSample is captured verbatim from `claude -p --output-format stream-json --verbose`.
const realStreamSample = `{"type":"system","subtype":"init","cwd":"/tmp","session_id":"abc","model":"claude-opus-4-8"}
{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}}
{"type":"assistant","message":{"model":"claude-opus-4-8","role":"assistant","content":[{"type":"text","text":"Creating the file."},{"type":"tool_use","id":"toolu_1","name":"Write","input":{"file_path":"hello.txt","content":"hi"}}]},"session_id":"abc"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":" Done."}]},"session_id":"abc"}
{"type":"result","subtype":"success","is_error":false,"num_turns":2,"result":"Creating the file. Done.","session_id":"abc"}`

func TestParseClaudeStream_Success(t *testing.T) {
	var chunks []string
	var toolCalls []ToolCall
	var final strings.Builder
	ctx := &ChatContext{
		OnChunk:    func(s string, _ bool) { chunks = append(chunks, s) },
		OnToolCall: func(name string, _ any) {},
		OnError:    func(string) { t.Error("unexpected OnError on success stream") },
	}

	parseClaudeStream(ctx, strings.NewReader(realStreamSample), &toolCalls, &final)

	if got := final.String(); got != "Creating the file. Done." {
		t.Errorf("final text = %q, want %q", got, "Creating the file. Done.")
	}
	if len(chunks) == 0 {
		t.Error("expected streamed chunks via OnChunk")
	}
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].Name != "Write" || toolCalls[0].ID != "toolu_1" {
		t.Errorf("tool call = %+v, want Write/toolu_1", toolCalls[0])
	}
	if toolCalls[0].Input == nil {
		t.Error("expected tool call input to be parsed")
	}
}

func TestParseClaudeStream_ErrorResult(t *testing.T) {
	stream := `{"type":"result","subtype":"error_max_turns","is_error":true,"result":"hit the limit"}`
	var errMsg string
	var toolCalls []ToolCall
	var final strings.Builder
	ctx := &ChatContext{
		OnChunk: func(string, bool) {},
		OnError: func(s string) { errMsg = s },
	}
	parseClaudeStream(ctx, strings.NewReader(stream), &toolCalls, &final)
	if !strings.Contains(errMsg, "hit the limit") {
		t.Errorf("expected error surfaced via OnError, got %q", errMsg)
	}
}

func TestParseClaudeStream_ResultFallbackText(t *testing.T) {
	// Tool-only turn: no assistant text streamed; result string is the fallback.
	stream := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t","name":"Bash","input":{}}]}}
{"type":"result","subtype":"success","is_error":false,"result":"All set."}`
	var toolCalls []ToolCall
	var final strings.Builder
	ctx := &ChatContext{OnChunk: func(string, bool) {}, OnError: func(string) {}}
	parseClaudeStream(ctx, strings.NewReader(stream), &toolCalls, &final)
	if final.String() != "All set." {
		t.Errorf("expected result fallback text, got %q", final.String())
	}
}

func TestParseClaudeStream_ToleratesGarbageLines(t *testing.T) {
	stream := "not json\n\n{\"type\":\"assistant\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"ok\"}]}}\ngarbage{"
	var toolCalls []ToolCall
	var final strings.Builder
	ctx := &ChatContext{OnChunk: func(string, bool) {}, OnError: func(string) {}}
	parseClaudeStream(ctx, strings.NewReader(stream), &toolCalls, &final)
	if final.String() != "ok" {
		t.Errorf("expected to skip garbage and parse valid line, got %q", final.String())
	}
}

// The session id is the whole basis of resumption, and it arrives more than
// once per run (init, then result). It must be reported on the first sighting
// and never again: the callback persists to disk, so firing per event would
// rewrite the same row for every event carrying an id.
func TestParseClaudeStream_ReportsSessionIDOnce(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"sess-abc"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}`,
		`{"type":"result","subtype":"success","session_id":"sess-abc","result":"done"}`,
	}, "\n")

	var seen []string
	var final strings.Builder
	ctx := &ChatContext{
		OnClaudeSession: func(id string) { seen = append(seen, id) },
		OnError:         func(string) { t.Error("unexpected OnError") },
	}
	parseClaudeStream(ctx, strings.NewReader(stream), nil, &final)

	if len(seen) != 1 {
		t.Fatalf("OnClaudeSession fired %d time(s) (%v), want exactly 1", len(seen), seen)
	}
	if seen[0] != "sess-abc" {
		t.Errorf("session id = %q, want sess-abc", seen[0])
	}
}

// A run that reaches the result event without an init event still has to yield
// its session id — that is the only handle a repair gets.
func TestParseClaudeStream_SessionIDFromResultEvent(t *testing.T) {
	var seen []string
	var final strings.Builder
	ctx := &ChatContext{OnClaudeSession: func(id string) { seen = append(seen, id) }}
	parseClaudeStream(ctx,
		strings.NewReader(`{"type":"result","subtype":"success","session_id":"sess-late","result":"ok"}`),
		nil, &final)

	if len(seen) != 1 || seen[0] != "sess-late" {
		t.Fatalf("session ids = %v, want [sess-late]", seen)
	}
}

// An empty session id is not a session. Reporting one would put a resume
// handle on the task record that --resume cannot use.
func TestParseClaudeStream_EmptySessionIDNotReported(t *testing.T) {
	var final strings.Builder
	ctx := &ChatContext{
		OnClaudeSession: func(id string) { t.Fatalf("reported empty session id %q", id) },
	}
	parseClaudeStream(ctx,
		strings.NewReader(`{"type":"system","subtype":"init","session_id":""}`+"\n"+
			`{"type":"result","subtype":"success","result":"ok"}`),
		nil, &final)
}

// A nil OnClaudeSession is the normal case outside task mode.
func TestParseClaudeStream_NilSessionCallbackIsSafe(t *testing.T) {
	var final strings.Builder
	ctx := &ChatContext{}
	parseClaudeStream(ctx,
		strings.NewReader(`{"type":"system","subtype":"init","session_id":"sess-1"}`),
		nil, &final)
}

func TestBuildClaudeArgs_ResumeSessionID(t *testing.T) {
	ctx := &ChatContext{ProjectPath: "/tmp/proj", ResumeSessionID: "sess-xyz"}
	joined := strings.Join(buildClaudeArgs(ctx, "", "be a builder", -1), " ")
	if !strings.Contains(joined, "--resume sess-xyz") {
		t.Errorf("args %q missing --resume sess-xyz", joined)
	}

	fresh := strings.Join(buildClaudeArgs(&ChatContext{ProjectPath: "/tmp/proj"}, "", "be a builder", -1), " ")
	if strings.Contains(fresh, "--resume") {
		t.Errorf("fresh run must not resume: %q", fresh)
	}
}

// Resumption is for the session that holds the half-finished work. A planner
// context must stay cold, or a repair forks the interrupted conversation into
// re-planning instead of finishing.
func TestStageChatContextCreation_ResumeOnlyForBuilder(t *testing.T) {
	cfg := OrchestratorConfig{ProjectPath: "/tmp/proj", ResumeSessionID: "sess-resume"}
	noop := func(string, bool) {}
	builder := stageChatContextCreation(cfg, "s1", RoleAutonomousBuilder, nil, noop, func(string) {}, func(string, any) {}, func(string, any, bool) {})
	if builder.ResumeSessionID != "sess-resume" {
		t.Errorf("builder ResumeSessionID = %q, want sess-resume", builder.ResumeSessionID)
	}
	planner := stageChatContextCreation(cfg, "s2", RolePlanner, nil, noop, func(string) {}, func(string, any) {}, func(string, any, bool) {})
	if planner.ResumeSessionID != "" {
		t.Errorf("planner ResumeSessionID = %q, want empty", planner.ResumeSessionID)
	}
}

// The provider reports a bare session id; the phase it belongs to is added
// where the role is known.
func TestStageChatContextCreation_ClaudeSessionCarriesPhase(t *testing.T) {
	var got []string
	cfg := OrchestratorConfig{
		OnClaudeSession: func(phase, id string) { got = append(got, phase+"="+id) },
	}
	noop := func(string, bool) {}
	for _, tc := range []struct {
		role AgentRole
		want string
	}{
		{RolePlanner, "plan=s"},
		{RoleAutonomousBuilder, "execute=s"},
	} {
		ctx := stageChatContextCreation(cfg, "sid", tc.role, nil, noop, func(string) {}, func(string, any) {}, func(string, any, bool) {})
		ctx.OnClaudeSession("s")
		if got[len(got)-1] != tc.want {
			t.Errorf("role %v reported %q, want %q", tc.role, got[len(got)-1], tc.want)
		}
	}
}
