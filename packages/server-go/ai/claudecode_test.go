package ai

import (
	"context"
	"strings"
	"testing"
	"time"
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

func TestWaitForMemory_UnmeasurableReturnsImmediately(t *testing.T) {
	// With an unmeasurable memory reader (returns <= 0), the gate should not wait.
	callCount := 0
	oldReader := memoryReader
	defer func() {
		memoryReader = oldReader
		spawnTimesMu.Lock()
		spawnTimes = []int64{}
		spawnTimesMu.Unlock()
	}()
	memoryReader = func() int64 {
		callCount++
		return -1 // unmeasurable
	}

	spawnTimesMu.Lock()
	spawnTimes = []int64{}
	spawnTimesMu.Unlock()

	ctx := context.Background()
	free := waitForMemory(ctx, "task1", "execute")
	if free != -1 {
		t.Errorf("waitForMemory returned %d, want -1", free)
	}
	if callCount != 1 {
		t.Errorf("memoryReader called %d times, want 1 (immediate return)", callCount)
	}
}

func TestWaitForMemory_SufficientMemoryReturnsImmediately(t *testing.T) {
	// With sufficient memory (>= reserve), the gate should not wait.
	callCount := 0
	oldReader := memoryReader
	defer func() {
		memoryReader = oldReader
		spawnTimesMu.Lock()
		spawnTimes = []int64{}
		spawnTimesMu.Unlock()
	}()

	spawnTimesMu.Lock()
	spawnTimes = []int64{}
	spawnTimesMu.Unlock()

	memoryReader = func() int64 {
		callCount++
		return 5000 // well above default 3072 MB reserve
	}

	ctx := context.Background()
	free := waitForMemory(ctx, "task2", "plan")
	if free != 5000 {
		t.Errorf("waitForMemory returned %d, want 5000", free)
	}
	if callCount != 1 {
		t.Errorf("memoryReader called %d times, want 1 (immediate return)", callCount)
	}
}

func TestWaitForMemory_WaitsUntilSufficientMemory(t *testing.T) {
	// With an injected memory reader returning 100 MB then 5000 MB, the gate
	// should wait one poll (5 seconds) and then proceed.
	reads := []int64{100, 5000}
	readIndex := 0
	oldReader := memoryReader
	defer func() {
		memoryReader = oldReader
		spawnTimesMu.Lock()
		spawnTimes = []int64{}
		spawnTimesMu.Unlock()
	}()

	spawnTimesMu.Lock()
	spawnTimes = []int64{}
	spawnTimesMu.Unlock()

	memoryReader = func() int64 {
		if readIndex < len(reads) {
			val := reads[readIndex]
			readIndex++
			return val
		}
		return reads[len(reads)-1]
	}

	// Use a small timeout for the test.
	oldGetWaitSecs := memorySpawnWaitSecsFn
	defer func() { memorySpawnWaitSecsFn = oldGetWaitSecs }()
	memorySpawnWaitSecsFn = func() time.Duration {
		return 10 * time.Second // allow plenty of time
	}

	ctx := context.Background()
	start := time.Now()
	free := waitForMemory(ctx, "task3", "execute")
	elapsed := time.Since(start)

	if free != 5000 {
		t.Errorf("waitForMemory returned %d, want 5000", free)
	}
	if readIndex != 2 {
		t.Errorf("memoryReader called %d times, want 2", readIndex)
	}
	// Should have waited roughly 5 seconds (one poll interval).
	if elapsed < 4*time.Second || elapsed > 8*time.Second {
		t.Errorf("elapsed time %.1fs, expected ~5s", elapsed.Seconds())
	}
}

func TestWaitForMemory_ProceedsAfterTimeout(t *testing.T) {
	// With a permanently low memory reader and a short timeout, the gate should
	// proceed after the timeout with a log message. Note: the poll interval is 5s,
	// so even with a 1s timeout, we wait until the first poll completes.
	oldReader := memoryReader
	defer func() {
		memoryReader = oldReader
		spawnTimesMu.Lock()
		spawnTimes = []int64{}
		spawnTimesMu.Unlock()
	}()

	spawnTimesMu.Lock()
	spawnTimes = []int64{}
	spawnTimesMu.Unlock()

	memoryReader = func() int64 {
		return 100 // always low
	}

	// Override wait timeout to a value less than the poll interval (5s).
	oldGetWaitSecs := memorySpawnWaitSecsFn
	defer func() { memorySpawnWaitSecsFn = oldGetWaitSecs }()
	memorySpawnWaitSecsFn = func() time.Duration {
		return 1 * time.Second // timeout < poll interval
	}

	ctx := context.Background()
	start := time.Now()
	free := waitForMemory(ctx, "task4", "plan")
	elapsed := time.Since(start)

	if free != 100 {
		t.Errorf("waitForMemory returned %d, want 100", free)
	}
	// Should have waited roughly 5 seconds (one full poll interval), then detected timeout.
	if elapsed < 4*time.Second || elapsed > 6*time.Second {
		t.Errorf("elapsed time %.1fs, expected ~5s (one poll interval)", elapsed.Seconds())
	}
}

func TestWaitForMemory_HonoursContextCancel(t *testing.T) {
	// If the context is cancelled while waiting, the gate should return
	// immediately without deadlocking.
	oldReader := memoryReader
	defer func() {
		memoryReader = oldReader
		spawnTimesMu.Lock()
		spawnTimes = []int64{}
		spawnTimesMu.Unlock()
	}()

	spawnTimesMu.Lock()
	spawnTimes = []int64{}
	spawnTimesMu.Unlock()

	memoryReader = func() int64 {
		return 100 // always low, would wait forever without cancel
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	free := waitForMemory(ctx, "task5", "execute")
	elapsed := time.Since(start)

	if free != 100 {
		t.Errorf("waitForMemory returned %d, want 100", free)
	}
	// Should have returned in roughly 500ms when cancelled.
	if elapsed < 400*time.Millisecond || elapsed > 1*time.Second {
		t.Errorf("elapsed time %.1fs, expected ~0.5s", elapsed.Seconds())
	}
}

func TestWaitForMemory_AccountsForYoungSessions(t *testing.T) {
	// With 12000 MB free and 8 sessions spawned 10s ago (young), the 9th should
	// wait because: need 3072 (reserve) + 1400*9 (expected sessions) = 15672 MB,
	// but only have 12000 MB.
	oldReader := memoryReader
	oldGetWaitSecs := memorySpawnWaitSecsFn
	oldTimeNano := timeSinceNanoFn

	defer func() {
		memoryReader = oldReader
		memorySpawnWaitSecsFn = oldGetWaitSecs
		timeSinceNanoFn = oldTimeNano
		spawnTimesMu.Lock()
		spawnTimes = []int64{}
		spawnTimesMu.Unlock()
	}()

	// Set up time: now is 1000, young sessions at time 990 (10s ago).
	now := int64(1000 * time.Second.Nanoseconds())
	youngSessionTime := int64(990 * time.Second.Nanoseconds())

	// Pre-populate with 8 young sessions.
	spawnTimesMu.Lock()
	spawnTimes = []int64{
		youngSessionTime, youngSessionTime, youngSessionTime, youngSessionTime,
		youngSessionTime, youngSessionTime, youngSessionTime, youngSessionTime,
	}
	spawnTimesMu.Unlock()

	callCount := 0
	memoryReader = func() int64 {
		callCount++
		if callCount == 1 {
			return 12000 // insufficient: need 15672, have 12000
		}
		// On second call, return enough.
		return 16000
	}

	timeSinceNanoFn = func() int64 {
		return now
	}

	memorySpawnWaitSecsFn = func() time.Duration {
		return 10 * time.Second
	}

	ctx := context.Background()
	start := time.Now()
	free := waitForMemory(ctx, "task-burst", "execute")
	elapsed := time.Since(start)

	if free != 16000 {
		t.Errorf("waitForMemory returned %d, want 16000", free)
	}
	if callCount < 2 {
		t.Errorf("memoryReader called %d times, want at least 2", callCount)
	}
	// Should have waited one poll interval (5s).
	if elapsed < 4*time.Second || elapsed > 8*time.Second {
		t.Errorf("elapsed time %.1fs, expected ~5s", elapsed.Seconds())
	}
}

func TestWaitForMemory_NoYoungSessionsProceedsImmediately(t *testing.T) {
	// With 12000 MB free and no young sessions, the spawn should proceed
	// immediately because: need 3072 (reserve) + 1400*1 = 4472 MB, have 12000 MB.
	oldReader := memoryReader
	oldTimeNano := timeSinceNanoFn

	defer func() {
		memoryReader = oldReader
		timeSinceNanoFn = oldTimeNano
		spawnTimesMu.Lock()
		spawnTimes = []int64{}
		spawnTimesMu.Unlock()
	}()

	callCount := 0
	memoryReader = func() int64 {
		callCount++
		return 12000
	}

	spawnTimesMu.Lock()
	spawnTimes = []int64{} // no young sessions
	spawnTimesMu.Unlock()

	timeSinceNanoFn = func() int64 {
		return int64(1000 * time.Second.Nanoseconds())
	}

	ctx := context.Background()
	free := waitForMemory(ctx, "task-clean", "execute")

	if free != 12000 {
		t.Errorf("waitForMemory returned %d, want 12000", free)
	}
	if callCount != 1 {
		t.Errorf("memoryReader called %d times, want 1 (immediate return)", callCount)
	}
}

func TestWaitForMemory_EnforcesMinimumGapBetweenSpawns(t *testing.T) {
	// Two back-to-back admissions should be at least MYEDITOR_SPAWN_MIN_GAP_SECS apart.
	oldReader := memoryReader
	oldTimeNano := timeSinceNanoFn

	defer func() {
		memoryReader = oldReader
		timeSinceNanoFn = oldTimeNano
		spawnTimesMu.Lock()
		spawnTimes = []int64{}
		spawnTimesMu.Unlock()
	}()

	memoryReader = func() int64 {
		return 12000 // always sufficient
	}

	// Simulate one spawn at t=1000.
	spawnTimesMu.Lock()
	spawnTimes = []int64{int64(1000 * time.Second.Nanoseconds())}
	spawnTimesMu.Unlock()

	now := int64(1002 * time.Second.Nanoseconds()) // now is t=1002 (2s later)
	callCount := 0

	timeSinceNanoFn = func() int64 {
		callCount++
		// First call (min gap check): return now (1002).
		// After gap wait completes, return now + gap time (1004).
		if callCount == 1 {
			return now
		}
		return int64(1004 * time.Second.Nanoseconds()) // skip to 1004 (gap satisfied)
	}

	ctx := context.Background()
	start := time.Now()
	free := waitForMemory(ctx, "task-gap", "execute")
	elapsed := time.Since(start)

	if free != 12000 {
		t.Errorf("waitForMemory returned %d, want 12000", free)
	}
	// Should have waited roughly 2 seconds for the gap to elapse.
	if elapsed < 1500*time.Millisecond || elapsed > 3*time.Second {
		t.Errorf("elapsed time %.1fs, expected ~2s for gap enforcement", elapsed.Seconds())
	}
}

func TestWaitForMemory_YoungSessionsAgeOut(t *testing.T) {
	// Sessions older than MYEDITOR_SPAWN_WARMUP_SECS should not count toward
	// the young sessions and should be pruned from tracking.
	oldReader := memoryReader
	oldTimeNano := timeSinceNanoFn

	defer func() {
		memoryReader = oldReader
		timeSinceNanoFn = oldTimeNano
		spawnTimesMu.Lock()
		spawnTimes = []int64{}
		spawnTimesMu.Unlock()
	}()

	callCount := 0
	memoryReader = func() int64 {
		callCount++
		return 12000
	}

	// Pre-populate with 8 sessions at time 1000, but we're now at time 1200 (200s later).
	// With default warmup of 120s, these should be aged out and not count.
	oldSessionTime := int64(1000 * time.Second.Nanoseconds())
	spawnTimesMu.Lock()
	spawnTimes = []int64{
		oldSessionTime, oldSessionTime, oldSessionTime, oldSessionTime,
		oldSessionTime, oldSessionTime, oldSessionTime, oldSessionTime,
	}
	spawnTimesMu.Unlock()

	now := int64(1200 * time.Second.Nanoseconds())
	timeSinceNanoFn = func() int64 {
		return now
	}

	ctx := context.Background()
	free := waitForMemory(ctx, "task-old", "execute")

	if free != 12000 {
		t.Errorf("waitForMemory returned %d, want 12000", free)
	}
	if callCount != 1 {
		t.Errorf("memoryReader called %d times, want 1", callCount)
	}
	// Check that old sessions were pruned and new one was added.
	spawnTimesMu.Lock()
	defer spawnTimesMu.Unlock()
	if len(spawnTimes) != 1 {
		t.Errorf("spawnTimes has %d entries after aging out, want 1 (the new spawn)", len(spawnTimes))
	}
}
