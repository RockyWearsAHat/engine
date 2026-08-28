package ai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/engine/server/quota"
)

// claudecodeIdleTimeout returns how long the provider waits with NO output from
// `claude -p` before treating the run as stalled, killing it, and surfacing an
// error so the orchestrator retries the step. Without this, a hung CLI (e.g. a
// backend outage mid-call) blocks the whole build forever with no error — the
// opposite of "intelligently keep trying". Override with
// ENGINE_CLAUDECODE_IDLE_TIMEOUT_SEC.
func claudecodeIdleTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv("ENGINE_CLAUDECODE_IDLE_TIMEOUT_SEC")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 180 * time.Second
}

// activityReader wraps an io.Reader and records the wall-clock time of the last
// non-empty read into last. The stall watchdog reads this to tell "working but
// quiet" from "hung".
type activityReader struct {
	r    io.Reader
	last *atomic.Int64
}

func (a *activityReader) Read(p []byte) (int, error) {
	n, err := a.r.Read(p)
	if n > 0 {
		a.last.Store(time.Now().UnixNano())
	}
	return n, err
}

// claudecodeProvider drives the Claude Code CLI (`claude -p`) as an inference
// backend.
//
// It is fundamentally different from the API providers (anthropic/openai/ollama):
// those run Engine's own agentic loop — Engine calls the model, parses tool
// calls, executes them, and feeds results back. Claude Code instead runs its
// OWN agentic loop with its own file/bash/edit tools against the project
// directory, and authenticates via the user's Claude subscription (Max/Pro)
// through `claude` login rather than an ANTHROPIC_API_KEY + API credits.
//
// So this provider does not execute Engine tools itself. It hands the task to
// Claude Code (scoped to ctx.ProjectPath), streams the transcript back through
// ctx.OnChunk + finalText, and records the tool calls Claude Code made so the
// completion summary still sees real activity. The work — files written, tests
// run, commits — happens inside Claude Code's loop on the same working tree.
type claudecodeProvider struct{}

// claudeBinary is the executable name/path for the Claude Code CLI.
// Injectable so tests can point it at a stub script.
var claudeBinary = "claude"

func (p *claudecodeProvider) RunLoop(
	ctx *ChatContext,
	model, systemPrompt string,
	history []anthropicMessage,
	allToolCalls *[]ToolCall,
	finalText *strings.Builder,
) {
	prompt := flattenHistoryForCLI(history)
	if strings.TrimSpace(prompt) == "" {
		if ctx.OnError != nil {
			ctx.OnError("claudecode: empty prompt — nothing to send")
		}
		return
	}

	// Bridge ctx.Cancel into process termination. CommandContext kills the
	// process tree when runCtx is cancelled.
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if ctx.Cancel != nil {
		go func() {
			select {
			case <-ctx.Cancel:
				cancel()
			case <-runCtx.Done():
			}
		}()
	}

	// Quota gate. This is the point where the engine finds out whether the
	// subscription can actually pay for this run, WHICH account should, and how
	// cheaply the work can be done — before spending anything.
	//
	// A blocked verdict is surfaced as an error rather than a silent skip: the
	// orchestrator's retry path already knows how to back off and try again, and
	// a step that vanishes without explanation is the harder failure to debug.
	// The project path is the rateable unit: it is what SARA ships and what the
	// user forms an opinion about, so it is what a later rating is keyed on.
	dispatch := quotaBefore(runCtx, ctx.Role, model, ctx.ProjectPath, os.Environ())
	if !dispatch.Allow {
		if ctx.OnError != nil {
			ctx.OnError(fmt.Sprintf("claudecode: out of subscription quota — %s; retry in %s",
				dispatch.Reason, dispatch.RetryAfter.Round(time.Minute)))
		}
		return
	}
	// quotaAfter below closes the quota measurement bracket and owns the reading;
	// this only covers the error returns between here and there.
	defer releaseQuotaObservation(dispatch)
	if dispatch.Model != "" {
		model = dispatch.Model
	}

	// The dispatch verdict carries three ceilings and this call site read
	// exactly one of them. Model was applied above; SubagentFanout is applied
	// here. (MaxContextTokens binds earlier, where the prompt is assembled —
	// see governedContextBudget; by the time we are here the history has
	// already been trimmed to it.)
	// Hub identity before launch: agent_list on a peer must already show this
	// worker when the CLI comes up.
	registerChatAgent(ctx)
	args := buildClaudeArgs(ctx, model, systemPrompt, dispatch.SubagentFanout)
	if p := mcpConfigPathFromArgs(args); p != "" {
		defer os.Remove(p)
	}

	cmd := exec.CommandContext(runCtx, claudeBinary, args...)
	if strings.TrimSpace(ctx.ProjectPath) != "" {
		cmd.Dir = ctx.ProjectPath
	}
	// Account selection: same binary, different CLAUDE_CONFIG_DIR, different
	// quota pool. Nil Env inherits the ambient login unchanged, which is exactly
	// today's behaviour on a single-account machine.
	if dispatch.Env != nil {
		cmd.Env = dispatch.Env
	}
	cmd.Stdin = strings.NewReader(prompt)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		if ctx.OnError != nil {
			ctx.OnError("claudecode: stdout pipe: " + err.Error())
		}
		return
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		if ctx.OnError != nil {
			ctx.OnError("claudecode: failed to start `claude` (is the CLI installed and logged in?): " + err.Error())
		}
		return
	}

	// Stall watchdog: if the CLI produces no output for idleTimeout, kill it so
	// the orchestrator can retry rather than block the whole build forever.
	var lastActivity atomic.Int64
	lastActivity.Store(time.Now().UnixNano())
	var stalled atomic.Bool
	idle := claudecodeIdleTimeout()
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-t.C:
				if time.Since(time.Unix(0, lastActivity.Load())) > idle {
					stalled.Store(true)
					cancel() // kills the process via CommandContext
					return
				}
			}
		}
	}()

	stats := parseClaudeStreamWithStats(ctx, &activityReader{r: stdout, last: &lastActivity}, allToolCalls, finalText, dispatch.Account)

	err = cmd.Wait()

	// Record what this configuration actually cost and whether it worked, so the
	// ledger can recommend a cheaper one next time it is safe to. The bar is
	// deliberately coarse — finished, no error event, produced output — because
	// it only has to be applied consistently across configurations.
	// Subscription runs put total_cost_usd 0 on the wire; price the usage.
	if stats.CostUSD == 0 {
		stats.CostUSD = quota.CostUSD(model, stats.InputTokens, stats.OutputTokens, stats.CacheCreationTokens, stats.CacheReadTokens)
	}
	quotaAfter(dispatch, stats, err == nil && !stats.SawError && finalText.Len() > 0)
	if ctx.OnRunStats != nil {
		ctx.OnRunStats(RunStats{
			Model:            model,
			InputTokens:      stats.InputTokens,
			OutputTokens:     stats.OutputTokens,
			SubagentsSpawned: stats.SubagentsSpawned,
			Duration:         time.Since(dispatch.startedAt),
			Seen:             true,
		})
	}

	if stalled.Load() {
		if ctx.OnError != nil {
			ctx.OnError(fmt.Sprintf("claudecode: no output for %s — killed as stalled; orchestrator will retry", idle))
		}
		return
	}
	if err != nil {
		// A cancelled run (caller cancel) is expected, not an error to surface.
		if runCtx.Err() == context.Canceled {
			return
		}
		msg := "claudecode: `claude` exited with error: " + err.Error()
		if s := strings.TrimSpace(stderr.String()); s != "" {
			msg += "\n" + s
		}
		if ctx.OnError != nil {
			ctx.OnError(msg)
		}
	}
}

// buildClaudeArgs assembles the `claude -p` invocation. The prompt itself is
// delivered over stdin (to avoid OS arg-length limits on long build briefs).
//
// --output-format stream-json --verbose : newline-delimited JSON events we parse.
// --permission-mode bypassPermissions   : headless autonomy — Claude Code runs
//
//	its file/bash/edit tools without prompting. Appropriate here because Engine
//	already scopes work to the project box and the user opted into autonomy.
//
// --add-dir <project> + cmd.Dir         : confine tool access to the project.
// --append-system-prompt <role prompt>  : inject Engine's role instructions on
//
//	top of Claude Code's own system prompt rather than replacing it.
//
// buildClaudeArgs assembles the CLI invocation for one run.
//
// subagentFanout is the governor's ceiling on how many subagents this session
// may spawn. It is enforced honestly rather than uniformly, because the CLI
// gives us exactly one hard control and no numeric one:
//
//   - fanout 0 is ENFORCED. The Task tool is the only way a Claude Code session
//     spawns a subagent, so disallowing it makes "no fanout" a fact about the
//     process rather than a request. This is the tier that matters: fanout drops
//     to 0 precisely when the quota window is nearly spent, and that is exactly
//     when a session quietly forking four more of itself does the most damage.
//   - fanout > 0 is ADVISORY. There is no --max-subagents flag, so the number is
//     stated in the system prompt and the model is asked to respect it. Said
//     plainly because it matters: above zero this is guidance, not a guarantee.
//     What makes it more than a wish is that claudeRunStats already harvests
//     subagent_stats.spawned from the result event, so overruns are observable
//     rather than invisible.
//   - A NEGATIVE fanout means "no opinion" and adds nothing to the invocation.
//     That is the ungoverned case, and it must stay distinguishable from 0,
//     which is a deliberate instruction to spawn nothing.
func buildClaudeArgs(ctx *ChatContext, model, systemPrompt string, subagentFanout int) []string {
	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", "bypassPermissions",
	}
	if m := strings.TrimSpace(model); m != "" && m != "claude-code" && m != "claudecode" {
		args = append(args, "--model", m)
	}
	// Comms bridge. Engine's agent_* / signal_done / discord_* / mesh_exec
	// tools reach the CLI as mcp__engine__*. Without this the worker is deaf.
	if path, err := writeMCPConfig(ctx); err == nil {
		args = append(args, "--mcp-config", path, "--allowedTools", "mcp__"+MCPBridgeServerName+"__*")
	} else if ctx.OnError != nil {
		ctx.OnError(mcpBridgeArgError(err))
	}
	// fanout 0 = no Task tool. Only then. Positive fanout keeps it.
	if subagentFanout == 0 {
		args = append(args, "--disallowedTools", "Task")
	} else if subagentFanout > 0 {
		systemPrompt = strings.TrimSpace(systemPrompt + fmt.Sprintf(
			"\n\nSUBAGENT BUDGET: spawn at most %d subagent(s) for this task. "+
				"The quota window is being actively managed; going over the budget "+
				"takes spend away from the rest of the work.", subagentFanout))
	}
	if sp := strings.TrimSpace(systemPrompt); sp != "" {
		args = append(args, "--append-system-prompt", sp)
	}
	if p := strings.TrimSpace(ctx.ProjectPath); p != "" {
		args = append(args, "--add-dir", p)
	}
	return args
}

// claudeStreamEvent is the subset of Claude Code's stream-json event schema we
// consume. Each line of `claude -p --output-format stream-json` output is one
// such object. Unknown fields are ignored.
type claudeStreamEvent struct {
	Type    string `json:"type"` // "system" | "assistant" | "user" | "result" | "rate_limit_event" | ...
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"` // final text on the terminal "result" event
	Message struct {
		Content []claudeContentBlock `json:"content"`
	} `json:"message"`
	// Usage and CostUSD appear on the terminal "result" event. Field names
	// verified against CLI v2.1.241 output.
	Usage struct {
		InputTokens              int64 `json:"input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	} `json:"usage"`
	CostUSD      float64 `json:"total_cost_usd"`
	NumTurns     int     `json:"num_turns"`
	SubagentStat struct {
		Spawned int `json:"spawned"`
	} `json:"subagent_stats"`
}

// claudeRunStats is what one `claude -p` run actually consumed, harvested from
// the terminal result event. Fed to the efficiency ledger so the engine can
// learn which configurations deliver a result for the least quota.
type claudeRunStats struct {
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
	CostUSD             float64
	NumTurns            int
	SubagentsSpawned    int
	// SawError is true if the result event reported failure.
	SawError bool
	// LimitStatus is the last rate-limit status seen on this run.
	LimitStatus string
}

// TotalTokens is every token the run was billed for.
//
// Cache reads are included deliberately. They are cheaper per token, not free,
// and they are precisely what a bloated context spends — excluding them would
// make the single biggest quota driver invisible to the ledger and reward the
// exact behaviour we are trying to reduce.
func (s claudeRunStats) TotalTokens() int64 {
	return s.InputTokens + s.OutputTokens + s.CacheCreationTokens + s.CacheReadTokens
}

type claudeContentBlock struct {
	Type  string          `json:"type"` // "text" | "tool_use" | ...
	Text  string          `json:"text"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// parseClaudeStream reads newline-delimited JSON events from r, streaming
// assistant text through ctx.OnChunk + finalText and recording tool_use blocks
// into allToolCalls (and ctx.OnToolCall). The terminal "result" event supplies
// the final text only as a fallback when no assistant text streamed, and
// surfaces errors via ctx.OnError.
//
// Kept for callers that do not care what the run cost.
func parseClaudeStream(ctx *ChatContext, r io.Reader, allToolCalls *[]ToolCall, finalText *strings.Builder) {
	parseClaudeStreamWithStats(ctx, r, allToolCalls, finalText, "")
}

// parseClaudeStreamWithStats is parseClaudeStream plus the two things quota
// governance needs from a stream we are already reading:
//
//   - the rate_limit_event, which is the engine's fastest and only free signal
//     that the limit state changed under it. Claude Code emits one near the
//     start of every run, so this costs nothing beyond the parse.
//   - the token counts on the result event, which are what the efficiency ledger
//     accounts in.
//
// account names the Claude account this run used, so the observed limit state is
// attributed to the right quota pool; empty means the ambient login.
func parseClaudeStreamWithStats(ctx *ChatContext, r io.Reader, allToolCalls *[]ToolCall, finalText *strings.Builder, account string) claudeRunStats {
	var stats claudeRunStats
	sc := bufio.NewScanner(r)
	// Event lines can be large (full assistant messages); raise the cap well
	// above bufio's default 64KB token limit.
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var ev claudeStreamEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue // tolerate non-JSON or partial lines
		}

		switch ev.Type {
		case "rate_limit_event":
			lev, ok := observeLimitEvent(account, line)
			if !ok {
				continue
			}
			stats.LimitStatus = lev.Status
			// Surface only the states that change what the operator should do. An
			// "allowed" event arrives on every single run; reporting it would bury
			// the two that matter in noise.
			if ctx.OnError != nil {
				switch {
				case lev.Status == "rejected":
					ctx.OnError("claudecode: rate limited on " + lev.Type + describeReset(lev))
				case lev.UsingOverage:
					ctx.OnError("claudecode: this account is now spending PAID OVERAGE — the subscription is no longer flat-cost")
				case lev.Status == "allowed_warning":
					ctx.OnError("claudecode: approaching the " + lev.Type + " limit" + describeReset(lev))
				}
			}
		case "assistant":
			for _, blk := range ev.Message.Content {
				switch blk.Type {
				case "text":
					if blk.Text == "" {
						continue
					}
					finalText.WriteString(blk.Text)
					if ctx.OnChunk != nil {
						ctx.OnChunk(blk.Text, false)
					}
				case "tool_use":
					var input any
					if len(blk.Input) > 0 {
						_ = json.Unmarshal(blk.Input, &input)
					}
					if allToolCalls != nil {
						*allToolCalls = append(*allToolCalls, ToolCall{
							ID:    blk.ID,
							Name:  blk.Name,
							Input: input,
						})
					}
					if ctx.OnToolCall != nil {
						ctx.OnToolCall(blk.Name, input)
					}
				}
			}
		case "result":
			stats.InputTokens = ev.Usage.InputTokens
			stats.OutputTokens = ev.Usage.OutputTokens
			stats.CacheCreationTokens = ev.Usage.CacheCreationInputTokens
			stats.CacheReadTokens = ev.Usage.CacheReadInputTokens
			stats.CostUSD = ev.CostUSD
			stats.NumTurns = ev.NumTurns
			stats.SubagentsSpawned = ev.SubagentStat.Spawned
			stats.SawError = ev.IsError
			if ev.IsError && ctx.OnError != nil {
				detail := strings.TrimSpace(ev.Result)
				if detail == "" {
					detail = ev.Subtype
				}
				ctx.OnError("claudecode: run reported failure: " + detail)
			}
			// Assistant text already streamed into finalText; only fall back to
			// the result string if nothing streamed (e.g. tool-only turns).
			if finalText.Len() == 0 && strings.TrimSpace(ev.Result) != "" {
				finalText.WriteString(ev.Result)
				if ctx.OnChunk != nil {
					ctx.OnChunk(ev.Result, false)
				}
			}
		}
	}
	return stats
}

// describeReset renders " (resets 3:04PM)" when the event carried a reset time,
// and nothing when it did not — an absent reset must not print as 1970.
func describeReset(ev quota.LimitEvent) string {
	if !ev.HasReset {
		return ""
	}
	return " (resets " + ev.ResetsAt.Format(time.Kitchen) + ")"
}

// flattenHistoryForCLI renders the Engine message history into a single prompt
// string for `claude -p`. A single user turn is passed verbatim; multi-turn
// history is rendered as a labelled transcript so Claude Code sees the full
// conversation context.
func flattenHistoryForCLI(history []anthropicMessage) string {
	// Collect non-empty turns.
	type turn struct {
		role string
		text string
	}
	var turns []turn
	for _, m := range history {
		text := strings.TrimSpace(cliMessageText(m.Content))
		if text == "" {
			continue
		}
		role := "Human"
		if m.Role == "assistant" {
			role = "Assistant"
		}
		turns = append(turns, turn{role: role, text: text})
	}
	if len(turns) == 0 {
		return ""
	}
	if len(turns) == 1 {
		return turns[0].text
	}
	var b strings.Builder
	for i, t := range turns {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(t.role)
		b.WriteString(": ")
		b.WriteString(t.text)
	}
	return b.String()
}

// cliMessageText pulls plain text out of an anthropicMessage.Content value for
// the flattened CLI prompt. Content may be a plain string, a []contentBlock, or
// (after a JSON round-trip) a []any of maps. tool_use/tool_result blocks are
// summarised compactly so the CLI prompt still reflects that activity happened
// without dumping raw JSON. This is richer than the compaction-oriented
// extractMessageText in harness.go, which collapses non-text to a placeholder.
func cliMessageText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []contentBlock:
		var parts []string
		for _, blk := range c {
			if s := contentBlockText(blk.Type, blk.Text, blk.Content, blk.Name); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	case []any:
		var parts []string
		for _, raw := range c {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := m["type"].(string)
			text, _ := m["text"].(string)
			inner, _ := m["content"].(string)
			name, _ := m["name"].(string)
			if s := contentBlockText(typ, text, inner, name); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

// contentBlockText renders one content block to text for the flattened prompt.
func contentBlockText(typ, text, inner, name string) string {
	switch typ {
	case "text", "":
		return strings.TrimSpace(text)
	case "tool_result":
		return strings.TrimSpace(inner)
	case "tool_use":
		if name != "" {
			return fmt.Sprintf("[called tool: %s]", name)
		}
		return ""
	default:
		return strings.TrimSpace(text)
	}
}
