package ai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/engine/server/quota"
)

// memoryReader is the injected function to read free commit memory in MB.
// Package-level var so tests can inject a mock. By default, it calls freeCommitMB.
// Initialized in init() to avoid build-tag issues.
var memoryReader func() int64

// memorySpawnWaitSecsFn is the injected function to read the spawn wait timeout.
// Package-level var so tests can inject a mock. By default, it reads the env var.
var memorySpawnWaitSecsFn func() time.Duration

// spawnTimes tracks recent spawn times (in nanoseconds since epoch) for admission gating.
// Protected by spawnTimesMu. Oldest entries are pruned as they age past warmupSecs.
var spawnTimes []int64
var spawnTimesMu sync.Mutex

// timeSinceNanoFn is the injected function to read current time in nanoseconds.
// Package-level var so tests can inject a mock. By default, it calls time.Now().UnixNano().
var timeSinceNanoFn func() int64

func init() {
	memoryReader = freeCommitMB
	memorySpawnWaitSecsFn = memorySpawnWaitSecsEnv
	timeSinceNanoFn = func() int64 { return time.Now().UnixNano() }
}

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

// memoryReserveMB returns the minimum free commit memory (in MB) required before
// spawning a new claude session. If free memory drops below this, the spawn gate
// will wait (up to memorySpawnWaitSecs) before proceeding. Override with
// MYEDITOR_MEMORY_RESERVE_MB.
func memoryReserveMB() int64 {
	if v := strings.TrimSpace(os.Getenv("MYEDITOR_MEMORY_RESERVE_MB")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return 3072
}

// memorySpawnWaitSecsEnv returns the maximum time (in seconds) to wait for free
// memory to become available before spawning a claude session anyway, reading from
// the MYEDITOR_SPAWN_WAIT_SECS environment variable.
func memorySpawnWaitSecsEnv() time.Duration {
	if v := strings.TrimSpace(os.Getenv("MYEDITOR_SPAWN_WAIT_SECS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 600 * time.Second
}

// memorySpawnWaitSecs returns the maximum time to wait for memory using the
// injected memorySpawnWaitSecsFn (for testability).
func memorySpawnWaitSecs() time.Duration {
	return memorySpawnWaitSecsFn()
}

// expectedSessionMB returns the expected memory usage (in MB) for a single
// spawned session. This is used to account for sessions that have been admitted
// recently but have not yet grown to their full size. Override with
// MYEDITOR_SESSION_EXPECTED_MB.
func expectedSessionMB() int64 {
	if v := strings.TrimSpace(os.Getenv("MYEDITOR_SESSION_EXPECTED_MB")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return 1400
}

// expectedSpawnMinGapSecs returns the minimum number of seconds between spawns
// to allow memory measurements to stabilize. Override with
// MYEDITOR_SPAWN_MIN_GAP_SECS.
func expectedSpawnMinGapSecs() time.Duration {
	if v := strings.TrimSpace(os.Getenv("MYEDITOR_SPAWN_MIN_GAP_SECS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 4 * time.Second
}

// expectedSpawnWarmupSecs returns the duration (in seconds) for which a spawned
// session is considered "young" and counted toward the memory headroom requirement.
// Sessions older than this age are not counted. Override with
// MYEDITOR_SPAWN_WARMUP_SECS.
func expectedSpawnWarmupSecs() time.Duration {
	if v := strings.TrimSpace(os.Getenv("MYEDITOR_SPAWN_WARMUP_SECS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 120 * time.Second
}

// waitForMemory is the admission gate before spawning a claude session. It gates
// on both current free memory and the expected growth from recently admitted sessions,
// enforces a minimum gap between admissions, and polls until memory is sufficient.
// Returns the final free memory observed.
//
//   - If memoryReader() returns <= 0 (unmeasurable), does not wait.
//   - Enforces a minimum gap between spawns (MYEDITOR_SPAWN_MIN_GAP_SECS, default 4s).
//     The wait honors cancellation and counts toward the overall timeout.
//   - Accounts for sessions spawned in the last MYEDITOR_SPAWN_WARMUP_SECS (default 120s):
//     the test is free - reserve >= expectedSessionMB * (1 + youngSessions).
//     This ensures the gate reserves memory for sessions still growing.
//   - If memory test fails, polls every 5 seconds up to memorySpawnWaitSecs(),
//     logging the reason at first deferral and upon proceeding or timeout.
//   - Respects ctx.Cancel so a cancelled task does not sit in the loop.
//   - Never deadlocks a task; the stall clock only starts after the gate returns.
func waitForMemory(ctx context.Context, taskID, phase string) int64 {
	reserve := memoryReserveMB()
	expectedMB := expectedSessionMB()
	minGap := expectedSpawnMinGapSecs()
	warmupSecs := expectedSpawnWarmupSecs()
	maxWait := memorySpawnWaitSecs()
	pollInterval := 5 * time.Second

	// Enforce minimum gap between spawns. This wait is part of the overall
	// admission timeout, not an additional delay.
	now := timeSinceNanoFn()
	lastSpawnNano := int64(0)
	spawnTimesMu.Lock()
	if len(spawnTimes) > 0 {
		lastSpawnNano = spawnTimes[len(spawnTimes)-1]
	}
	spawnTimesMu.Unlock()

	start := time.Now()
	gapWaitDone := false
	deferralLogged := false
	var deferralReason string

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		// Check if we need to wait for the minimum gap to pass.
		if !gapWaitDone {
			timeSinceLastSpawn := time.Duration(now - lastSpawnNano)
			if timeSinceLastSpawn < minGap {
				timeToWait := minGap - timeSinceLastSpawn
				select {
				case <-ctx.Done():
					// Task was cancelled; proceed anyway so the task doesn't hang.
					if !deferralLogged {
						log.Printf("session spawn context cancelled after %.0fs wait (gap enforcement)", time.Since(start).Seconds())
						deferralLogged = true
					}
					gapWaitDone = true
				case <-time.After(timeToWait):
					// Gap enforcement satisfied; update now and move on.
					now = timeSinceNanoFn()
					gapWaitDone = true
				}
				continue
			}
			gapWaitDone = true
		}

		free := memoryReader()

		// If unmeasurable (returns <= 0), do not wait — let the task proceed.
		if free <= 0 {
			// Record this spawn before returning.
			spawnTimesMu.Lock()
			spawnTimes = append(spawnTimes, now)
			spawnTimesMu.Unlock()
			return free
		}

		// Prune old spawn times from the tracking list (older than warmupSecs).
		cutoffNano := now - warmupSecs.Nanoseconds()
		spawnTimesMu.Lock()
		for len(spawnTimes) > 0 && spawnTimes[0] < cutoffNano {
			spawnTimes = spawnTimes[1:]
		}
		youngSessions := int64(len(spawnTimes))
		spawnTimesMu.Unlock()

		// Calculate the memory requirement: we need to reserve for this spawn
		// (expectedMB) plus all young sessions still growing (youngSessions * expectedMB).
		requiredFree := reserve + expectedMB*(1+youngSessions)
		sufficientMemory := free >= requiredFree

		// If sufficient memory, proceed.
		if sufficientMemory {
			if deferralLogged {
				elapsed := time.Since(start).Seconds()
				log.Printf("session spawn resumed task=%s after %.0fs (free commit %d MB; %s)", taskID, elapsed, free, deferralReason)
			}
			// Record this spawn time before returning.
			spawnTimesMu.Lock()
			spawnTimes = append(spawnTimes, now)
			spawnTimesMu.Unlock()
			return free
		}

		// Memory is insufficient. Log deferral on first check.
		if !deferralLogged {
			deferralReason = fmt.Sprintf("(%d young sessions, need %d MB headroom, have %d MB)", youngSessions, requiredFree-reserve, free)
			log.Printf("session spawn deferred task=%s phase=%s: %s", taskID, phase, deferralReason)
			deferralLogged = true
		}

		// Check timeout.
		if time.Since(start) > maxWait {
			log.Printf("session spawn proceeding after %.0fs wait: free commit still %d MB (timeout)", time.Since(start).Seconds(), free)
			// Record this spawn time before returning.
			spawnTimesMu.Lock()
			spawnTimes = append(spawnTimes, now)
			spawnTimesMu.Unlock()
			return free
		}

		// Wait for next poll or context cancellation.
		select {
		case <-ctx.Done():
			// Task was cancelled; proceed anyway so the task doesn't hang.
			if deferralLogged {
				log.Printf("session spawn context cancelled after %.0fs wait", time.Since(start).Seconds())
			}
			// Record this spawn time before returning.
			spawnTimesMu.Lock()
			spawnTimes = append(spawnTimes, now)
			spawnTimesMu.Unlock()
			return free
		case <-ticker.C:
			// Update the current time for the next iteration.
			now = timeSinceNanoFn()
			// Continue polling.
		}
	}
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
	//
	// Task mode (single worklist item) runs one session per iteration with no
	// subagent fan-out. The goal is one process per item, memory-efficiently,
	// not concurrent teams. Override the governor's ceiling regardless of tier.
	subagentFanout := dispatch.SubagentFanout
	if strings.TrimSpace(ctx.TaskID) != "" {
		subagentFanout = 0
	}
	// Hub identity before launch: agent_list on a peer must already show this
	// worker when the CLI comes up.
	registerChatAgent(ctx)
	args := buildClaudeArgs(ctx, model, systemPrompt, subagentFanout)
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
	setProcGroup(cmd)
	// CommandContext's default cancellation only ever kills this one PID
	// (Kill()/TerminateProcess). That leaves the engine-server.exe MCP bridge
	// child (and anything the CLI itself spawned) running and holding memory
	// after the run this process belonged to is done — measured as 3
	// in-flight tasks becoming 14 live processes on the box. Override Cancel
	// to kill the whole tree instead, on stall, cancellation, and task stop.
	cmd.Cancel = func() error { return killProcessTree(cmd) }
	cmd.WaitDelay = 5 * time.Second

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		if ctx.OnError != nil {
			ctx.OnError("claudecode: stdout pipe: " + err.Error())
		}
		return
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr

	// Memory admission gate: wait if free commit is below the reserve threshold.
	// This prevents the spawn loop from starving the box with dozens of sessions.
	phase := claudeSessionPhase(ctx.Role)
	taskID := ctx.TaskID
	waitForMemory(runCtx, taskID, phase)

	if err := cmd.Start(); err != nil {
		if ctx.OnError != nil {
			ctx.OnError("claudecode: failed to start `claude` (is the CLI installed and logged in?): " + err.Error())
		}
		return
	}

	// Spawn accounting: the data set used to correlate the box's `[supervisor]
	// exited with code -1` crashes with commit-memory exhaustion. taskID keys
	// the registry that lets a restart find and kill exactly this task's
	// sessions (see session_registry.go); phase distinguishes the plan
	// session (bounded, must not outlive planPhaseTimeout) from execute.
	pid := cmd.Process.Pid
	spawnedAt := time.Now()

	// On Windows, create a job object for this process so the entire tree
	// (including engine-server.exe MCP bridge) is automatically killed when
	// the job is closed. On Unix, process groups (set by setProcGroup) handle this.
	if _, err := CreateJobForProcess(pid); err != nil {
		if ctx.OnError != nil {
			ctx.OnError("claudecode: failed to create job object: " + err.Error())
		}
	}

	RegisterSession(taskID, pid, phase)
	taskSessions := LiveTaskSessionCount(taskID)
	log.Printf("session spawn task=%s phase=%s pid=%d live=%d trees=%d taskSessions=%d freeCommitMB=%d",
		taskID, phase, pid, LiveSessionCount(), LiveJobObjectCount(), taskSessions, freeCommitMB())
	// exitStats is filled from the result event once the stream has been read;
	// a session that dies before it gets there exits with zero turns and its
	// wall clock, which is itself the telemetry (see sessionExitLogLine).
	var exitStats claudeRunStats
	defer func() {
		UnregisterSession(taskID, pid)
		log.Print(sessionExitLogLine(taskID, phase, pid, exitStats, time.Since(spawnedAt)))
	}()

	// Stall watchdog: if the CLI produces no output for idleTimeout, kill it so
	// the orchestrator can retry rather than block the whole build forever.
	// Initialize the activity timestamp AFTER the process starts so the stall timer
	// only counts actual run time, not the memory wait time.
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
	exitStats = stats

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
	// One item = one worker is the default for every session, regardless of
	// fanout: say so in the brief, not just in the toolset, because the model
	// can be talked into "coordinating a team" narratively (structuring the
	// work that way, shelling out to other tools) even where the Task tool
	// itself is not blocked.
	systemPrompt = strings.TrimSpace(systemPrompt +
		"\n\nDo the work yourself in this one session. Do NOT spawn sub-agents or teams.")
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
	// Resumption. The CLI keeps the full conversation for a session id, so
	// --resume continues the interrupted run's own reasoning and file context
	// instead of paying for a cold re-read of work already done. Empty is the
	// normal case: a fresh session.
	if rs := strings.TrimSpace(ctx.ResumeSessionID); rs != "" {
		args = append(args, "--resume", rs)
	}
	return args
}

// claudeSessionPhase names the phase a session belongs to, from the role that
// opened it. Two names only, because that is the distinction the session
// registry and the task record both care about: the bounded planning session
// versus the long execution one.
func claudeSessionPhase(role AgentRole) string {
	if role == RolePlanner {
		return "plan"
	}
	return "execute"
}

// claudeStreamEvent is the subset of Claude Code's stream-json event schema we
// consume. Each line of `claude -p --output-format stream-json` output is one
// such object. Unknown fields are ignored.
type claudeStreamEvent struct {
	Type    string `json:"type"` // "system" | "assistant" | "user" | "result" | "rate_limit_event" | ...
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
	// SessionID is the CLI's own conversation id. It is on the opening
	// system/init event and again on the terminal result event, and it is the
	// only handle `claude --resume` accepts — so it is what turns an
	// interrupted run into a resumable one rather than a re-run from scratch.
	SessionID string `json:"session_id"`
	Result    string `json:"result"` // final text on the terminal "result" event
	Message   struct {
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
	DurationMs   int64   `json:"duration_ms"`
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
	// DurationMs is the CLI's own wall-clock figure from the result event; 0
	// when the run never reached one (killed, stalled, crashed).
	DurationMs       int64
	SubagentsSpawned int
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

	// The session id repeats on several events in one run (init, then result).
	// The callback is a "this run is now resumable, here is its handle" signal,
	// so it fires on the first non-empty id and never again: a caller that
	// persists on every call would write the same row once per event.
	sessionReported := false
	reportSession := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || sessionReported {
			return
		}
		sessionReported = true
		if ctx.OnClaudeSession != nil {
			ctx.OnClaudeSession(id)
		}
	}

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
		case "system", "init":
			// The opening event of a run. Its only job here is the session id:
			// reporting it now (rather than waiting for the result event) is
			// what makes a run that never reaches a result — killed, stalled,
			// engine restarted — resumable at all.
			reportSession(ev.SessionID)
		case "rate_limit_event":
			lev, ok := observeLimitEvent(account, line)
			if !ok {
				continue
			}
			stats.LimitStatus = lev.Status
			// Surface only the states that change what the operator should do. An
			// "allowed" event arrives on every single run; reporting it would bury
			// the two that matter in noise.
			switch {
			case lev.Status == "rejected":
				if ctx.OnError != nil {
					ctx.OnError("claudecode: rate limited on " + lev.Type + describeReset(lev))
				}
			case lev.UsingOverage:
				if ctx.OnError != nil {
					ctx.OnError("claudecode: this account is now spending PAID OVERAGE — the subscription is no longer flat-cost")
				}
			case lev.Status == "allowed_warning":
				// Not an error. The run is still allowed; the window is merely
				// high (the fleet targets 100% at reset, so this is the normal
				// state for most of the week). Routing it to OnError made every
				// step of a task look failed and got the task cancelled.
				log.Printf("claudecode: %s window high%s (run still allowed)", lev.Type, describeReset(lev))
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
			reportSession(ev.SessionID)
			stats.InputTokens = ev.Usage.InputTokens
			stats.OutputTokens = ev.Usage.OutputTokens
			stats.CacheCreationTokens = ev.Usage.CacheCreationInputTokens
			stats.CacheReadTokens = ev.Usage.CacheReadInputTokens
			stats.CostUSD = ev.CostUSD
			stats.NumTurns = ev.NumTurns
			stats.DurationMs = ev.DurationMs
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

// sessionExitLogLine is the `session exit` accounting line, with what the
// session actually spent appended: turns and duration always, tokens when the
// result event carried usage.
//
// duration_ms prefers the CLI's own figure — it excludes process start-up and
// our teardown, so it is the number to compare against the model's turns —
// and falls back to wall clock since spawn when the run never produced a
// result event, because a killed or stalled session is precisely the one whose
// duration the budget telemetry most needs to see.
func sessionExitLogLine(taskID, phase string, pid int, stats claudeRunStats, wall time.Duration) string {
	durationMs := stats.DurationMs
	if durationMs <= 0 {
		durationMs = wall.Milliseconds()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "session exit task=%s phase=%s pid=%d turns=%d duration_ms=%d",
		taskID, phase, pid, stats.NumTurns, durationMs)
	if stats.InputTokens > 0 || stats.OutputTokens > 0 {
		fmt.Fprintf(&b, " tokens_in=%d tokens_out=%d", stats.InputTokens, stats.OutputTokens)
	}
	fmt.Fprintf(&b, " live=%d trees=%d freeCommitMB=%d", LiveSessionCount(), LiveJobObjectCount(), freeCommitMB())
	return b.String()
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
