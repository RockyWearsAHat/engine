# Event-Driven Orchestrator (Code-Truth Snapshot)

This file is intentionally limited to what exists in source today. If code and this document diverge, code is authoritative.

## Activation Route

- `RunProject` (`orchestrator.go`) is the only entry. It calls `ShouldRunEventOrchestrator`.
- Rule: `ENGINE_EVENT_ORCHESTRATOR=1/0` wins. Else serial **only** when `cfg.PlanSteps <= 2` (0 = unknown) **and** governor `MaxConcurrency < 2`. Everything else → event orchestrator. `TaskMode` no longer forces serial.
- SARA `/task` dispatches (`task_api.go`, `TaskMode: true`) therefore land here whenever the window has room. `task_api.go` passes `PlanSteps: 0` — step count is unknown at dispatch time, so the `PlanSteps <= 2` half of the serial rule is dormant for real task-mode calls until a caller supplies a count; with room the window decides alone. Log line: `parallel teams — task <slug>: plan step count unknown, quota tier T allows K concurrent teams — comms on` (`N plan step(s) known` only when a caller set `PlanSteps > 0`).
- Task mode inside the event path: intake/PRD skipped (existing `.engine` docs loaded into the brain), state file `.engine/brain-<taskSlug>.json` (whole-project runs keep `brain.json`).
- `RunEventOrchestratorAsState` adapts the brain to `*OrchestrationState`.

## Implemented Components

1. `event.go`
- Defines `EventType`, `Event`, and `EventBus`.
- `EventBus` supports `Subscribe`, `Emit`, and `Close`.

2. `orchestrator_brain.go`
- Defines persistent orchestration state (`OrchestrationBrain`, project requirements, team state).
- Includes readiness/dependency helpers used during dispatch and wait phases.

3. `team_dispatcher.go`
- Defines `TeamDispatcher` and team worker execution flow.
- Dispatches ready teams and tracks active workers.

4. `orchestrator_event.go`
- Defines `RunEventOrchestrator` and `EventOrchestrator`.
- Runs phases in this order: `phaseIntake`, `phasePlan`, `phaseDispatchTeams`, `phaseWaitTeams`, `phaseValidate`.
- Supports outer-loop replanning when validation feedback is not passing.
- Builds team slices with `createTeamsFromPlan`.

5. `orchestrator_intake.go`
- Implements intake artifacts and role handoff (`RoleGriller`, `RolePRDWriter`) used by event orchestration.

## Agent Roles (15 total)

Roles are specialized agent personas, each with a system prompt, pre-granted tools, and specific output format.

| Role | Purpose | Input | Output | Model |
|------|---------|-------|--------|-------|
| RoleInteractive | Default user-facing chat | User message + context | Helpful reply + tool results | Any |
| RolePlanner | Numbered implementation plans | Codebase + brief | Numbered steps only | Haiku+ |
| RoleScaffolder | Compile-valid file stubs | Plan step | File stubs (no logic) | Haiku |
| RoleImplementer | Production code for one module | File path + spec | Working code for spec | Haiku+ |
| RoleTester | Test writing + iteration | Failing test output | Fixed code + green tests | Haiku+ |
| RoleReviewer | Runtime quality review | Diff + screenshots | APPROVE / REJECT + findings | Haiku+ |
| RoleDocumenter | Documentation updates | Code changes | Updated WORKING_BEHAVIORS.md | Haiku |
| RoleIntaker | Structured project profile extraction | README / user message | JSON ProjectProfile | Haiku |
| RoleAutonomousBuilder | Headless scaffold+implement+validate | Project path + brief | Committed, working code | Haiku+ |
| RoleGriller | Design tree walkthrough | Brief | Structured design.md | Sonnet |
| RolePRDWriter | Design → vocabulary + PRD | design.md | vocabulary.md + prd.md | Sonnet |
| RoleModuleIndexer | Module/package strategic index | Codebase | modules.md table | Haiku |
| RoleArchitect | Ousterhout deep-modules review | Diff + files | APPROVE / REJECT + findings | Haiku+ |
| RoleRouter | Brief classifier (BUILD vs CHAT) | User message | One word: BUILD or CHAT | Haiku |
| RoleCoach | Rejected brief decomposer | Brief + REJECT feedback | Decomposed brief for retry | Sonnet |
| RoleDesignReviewer | Design consistency verification | 2 images + design.dx rules | JSON {pass, violations:[...]} | Haiku |

Reachable from `/task` POST with `role` field: "design-reviewer" → RoleDesignReviewer (strict JSON output, no tool calls, read-only).

## Plan Decomposition Gate (Slice J)

The planner phase now enforces task decomposition before dispatch:
- Each step must be ≤1 file cluster (detected by >3 verbs → rejected).
- Each step must have ≤1 acceptance check.
- Each step must be ≤30 min for haiku (single concern).
- Steps cannot use "and then" or "also" (compound steps rejected).
- `validatePlanDecomposition` (orchestrator_phases.go) inspects steps and counts action verbs.
- If a step violates decomposition: planner gets asked to split (max 2 passes).
- Log line: `plan: <n> steps, split <m>`. On second rejection, gate fails.
- Planner prompt (caveman style) explicitly constrains output and warns against compound steps.
- `buildPlanSplitPrompt` gives feedback on why a step was split, helps planner break it into smaller concerns.
- Test spec: plan with step "add migration and then wire UI and write docs" rejected, split into 3 clean steps; each step has acceptance non-empty.

## Current Structural Reality

- Comms hub: `AgentCommsForProject` per project (keyed by project path, no task slug). Orchestrator registers `lead`; `TeamDispatcher` registers every team id as `queued`; `claudecodeProvider.RunLoop` registers the worker (`registerChatAgent`) **before** the CLI starts, so `agent_list` on a peer already shows it. `registerChatAgent` always writes status `active` — it overwrites the dispatcher's `queued` on the same id. Intended: CLI start *is* the queued→active edge. Bridge tool calls self-register an *unknown* worker as `active`; a peer already in the hub keeps its status (`IsRegistered` guard in `ExecuteBridgeTool`).
- Model pin: `cfg.RequestedModel` (SARA's haiku/sonnet/opus tier) lands in `ChatContext.ModelOverride` at **one** seam per path — serial `stageChatContextCreation`, event `newPhaseChat`. `newPhaseChat` feeds every planner phase and `TeamWorker.runStep`, so the pin holds across the whole event run. Role/team env overrides still win over it (floor, not ceiling). Was dropped on the event path before; test `model_pin_event_test.go` guards both call sites.
- Comms on disk: every `Register` / `Send` mirrors the hub to `<project>/.engine/comms.json` (`comms-<slug>.json` when a hub is built with `NewAgentCommsHubAt(path, slug)`; the shared per-project hub has no slug). Shape: `{updatedAt, agents[], messages[]}` — peers sorted by id, messages by time. Best-effort write; disk trouble never breaks comms. This is the observable proof for "≥2 registered peers, ≥1 `msg-N` exchanged".
- MCP bridge (`ai/mcp_bridge.go`): `claude -p` workers get `--mcp-config <tmp.json> --allowedTools mcp__engine__*`. The config spawns `<server binary> mcp-bridge` (override `ENGINE_MCP_BRIDGE_CMD`) with env `ENGINE_MCP_PROJECT`, `ENGINE_MCP_AGENT`, `ENGINE_MCP_ROLE`, `ENGINE_MCP_ADDR`. Bridge = stdio JSON-RPC 2.0 (`initialize`, `tools/list`, `tools/call`, `ping`); each `tools/call` POSTs to the running Engine at `/mcp/tool`, which runs the tool in-process against the project hub. Empty addr = in-process. The POST client has **no timeout** — `agent_await` blocks as long as the worker asks; the bridge process dies with the CLI.
- Bridged tools: `agent_list agent_send agent_inbox agent_receive agent_await signal_done discord_post_progress discord_dm mesh_exec search_tools`. File/shell tools are not bridged — Claude Code has its own. Temp config removed after the run.
- `--disallowedTools Task` only when governor fanout == 0. Positive fanout = advisory budget in the system prompt.
- Teams are still step buckets: `createTeamsFromPlan` groups by title keyword (`db`, `frontend`, `api`, `general`); every member runs `RoleAutonomousBuilder`; `lead_planner.go` composition not wired (slice H).
- No mid-run member spawn / retire yet (slice H).

## Task API Surface (`task_api.go`)

How SARA hands one unit of work in and learns what happened. Rules:

- `POST /task` → 202 `{id,...}` at once. Orchestration runs in a goroutine. Planner never on the request path.
- `GET /task?id=` and `GET /task/<id>` → registry read under RLock. Never waits on a phase. Fields: `status`, `phase`, `stepsDone/stepsTotal`, `alive` (this process owns a goroutine for it), `firstProgressAt`, `lastTokenAt`, `lastToolAt` (evidence stamps from `OrchestratorConfig.OnActivity`), `model`, `tokensIn/tokensOut/subagentsSpawned` (from `OnRunStats`), `coached/escalated` (from `OnCoach`, slice I fills).
- Registry persisted to `<ENGINE_STATE_DIR>/tasks.json` (else `<project>/.engine/tasks.json`): on accept BEFORE plan, and on every note/plan/finish/stats change. Tmp+rename. Registry lock not held during write.
- Server start reloads it. Every row still `running` on reload is marked `failed` (terminal, `alive:false`, `lost:true`, dedupe key released) regardless of PID. SARA's gateway (`myeditor.mjs` asAgentEntry) treats status=failed + lost:true as "MyEditor forgot task", not a failure: engine marks item lastOutcome=lost, re-dispatches, no retry attempt spent. Nothing forgotten.
- Wake POST fires on EVERY terminal state — done, failed, canceled, panic. Target: caller `callbackUrl`, else `http://127.0.0.1:$SARA_ENGINE_WAKE_PORT/task-complete` (default 24777). Payload `{id, project, outcome, model, tokensIn, tokensOut, subagentsSpawned, coached, escalated, error}`. Fire-and-forget; GET-by-id stays truth.

## Builder Retry Rules (`orchestrator_phases.go`, `orchestrator.go`, `team_dispatcher.go`)

- Every builder attempt: fresh session, fresh `ChatContext`. Reviewer/critic notes ride in via `step.LastFeedback` as `REVIEWER NOTES` in the prompt, or via `CoachingBrief` when available.
- Every run logs phase `tokens`: `model in out elapsed`.
- Provider fault = provider reported usage (`RunStats.Seen`) AND `OutputTokens == 0` AND run ended inside `zeroOutputWindow` (1s). Backoff 1s / 5s / 15s, new run each. After the schedule: `ErrProviderZeroOutput`; outer loop calls `refundProviderAttempt` — `step.Attempts--`. Provider hiccups never retire a step (err-…002).
- Stub/ollama runs with no usage keep the plain "builder produced no output" path — counted as an attempt, no backoff.

## Reviewer REJECT Coaching (slice I: coaching, not escalation)

- `PlanStep.ReviewRejects` counts reviewer REJECT verdicts on this step (separate from `Attempts`).
- On 1st REJECT: `orchestratorCoachStep` spawns RoleCoach (one tier above worker: sonnet when worker is haiku). Coach reads rejection + acceptance criteria + original brief → outputs decomposed brief. `step.CoachingBrief` stores it. On next builder run, `buildStepPromptWithContext` uses `CoachingBrief` instead of `Body`. Re-run on same model. OnCoach fired: `coached=1, escalated=false`.
- On 2nd REJECT: Coach again with same flow; `LastFeedback` (reviewer notes) injected in the prompt. OnCoach fired: `coached=2, escalated=false`.
- On 3rd REJECT: Escalate model one tier (haiku→sonnet, sonnet→opus). OnCoach fired: `coached=3, escalated=true`. Allow one more attempt at the higher tier.
- If 4th attempt fails (rare; escalation worked but insufficient): step retired as failed. No further escalation.
- Tests: coaching invoked on REJECT → coach-rewritten brief fed to retry; 2nd REJECT → feedback injected; 3rd+ REJECT → escalated flag set; webhook payload carries coached/escalated.

## Team Member Communication

- After each plan step completes, `TeamWorker.reportStepToLead` sends a progress message to the lead via `AgentCommsHub.Send()`.
- Each message has format: `"Step N/total done: <step title>"` with subject `"progress"`.
- The lead agent can drain its inbox to track which steps have been completed.
- Team members registered as agents allow status queries and logging via the comms system.
- This enables the lead to maintain accurate `stepsDone` counters for task progress tracking.

## Validation Surface

The event orchestrator has direct unit coverage in:

- `mcp_bridge_test.go` (routing rule, brain namespacing, bridge wire, fake-claude → bridge → HTTP → hub)
- `model_pin_event_test.go` (RequestedModel → ModelOverride on planner phases and TeamWorker steps)
- `orchestrator_event_extra_test.go`
- `orchestrator_gap_extra_test.go`
- `orchestrator_run_extra_test.go`
- `team_dispatcher_test.go`
- `event_close_test.go`

These tests are the canonical behavioral spec for current orchestration behavior.

## Operator Notes

- Default on. Force: `ENGINE_EVENT_ORCHESTRATOR=1` (or `0` for serial). Old `USE_EVENT_ORCHESTRATOR` / `ENGINE_EXPERIMENTAL_EVENT_ORCHESTRATOR` are dead.
- Bridge callback addr defaults to `http://127.0.0.1:$PORT` (24444); override `ENGINE_MCP_ADDR`.
- Brain state: `.engine/brain.json` or `.engine/brain-<taskSlug>.json`.

## Quota Governance (quota/ + quota_gate.go)

- Objective: spend 100% of each Anthropic window by its reset. Under pace = wasted quota, over pace = 429s. `PaceTarget = 1.0`; `Assessment.Pace` = used fraction / elapsed fraction, `Ahead = 1 - pace/target`.
- Boost: when ahead of target, `paceConcurrency` raises `Plan.MaxConcurrency` and `Plan.SubagentFanout` by `1 + Ahead`, capped at `PaceBoostCap` (2x) × `Policy.MaxConcurrency` / `Policy.MaxSubagents`. Worker model is never touched by pace.
- Planner model: `Plan.PlannerModel` = `CheapModel` (haiku) unless `Ahead > PlannerSonnetAhead` (0.30), then `MidModel` (sonnet). `runPlannerPrePass` applies it downgrade-only via `modelRank`, and only for `claudecode`/`anthropic` planners; ollama/openai planners are untouched.
- Accounts: `quota.LoadAccounts` reads `$SARA_ACCOUNTS_FILE` (default `~/.sara-accounts.json`, shape `[{name, configDir}]`) first, falls back to `ENGINE_CLAUDE_ACCOUNTS`. Registry dedupes by `Identity.Key()` (org id or email), so the same login in two config dirs is one account.
- Quota snapshot truth: `-1` is unknown; real percentages are 0-100. Snapshot must build from latest local probe reading, never from a stale or failed pooled row. `GET /quota/snapshot` exports each account's real percentages from a live or cached probe; if the probe has never run, snapshot waits for it (bounded by the probe's timeout). If probe fails, returns `-1` with `ok:false`.
- Pooled snapshot merge rule: known beats unknown. When merging two snapshots of the same account, a reading with both session and week percentages >= 0 overrides a row with any `-1` value. When both readings are known, worst percent (highest usage) wins. `POST /quota/pooled` accepts merged snapshots; `GET /quota` and `/quota/snapshot` only return pooled rows when they carry a complete, known reading. A pooled row with session or week = -1 never produces a source="pooled" label or overrides a known local reading.
- Ledger usd: subscription runs report `total_cost_usd: 0`, so `quotaAfter` prices stream-json usage with `quota.CostUSD` (fable 10/50, opus 5/25, sonnet 3/15, haiku 1/5 per 1M; cache write 1.25x, cache read 0.1x input). `Outcome.USD` and `Stat.USD` are non-zero for any priced model; unknown models price 0.
- Tests: `quota/pooled_test.go`, `quota/governor_test.go`, `ai/quota_usd_test.go`.

## Memory and Documentation (SLICE K)

Memory-of-work lives in dx documents, rewritten live, never appended.

- **Documenter role**: After every completed build step, `DocumenterStep` searches the project's index.dx for blocks answering the step title/body. Rewrites each hit (never appends) with current truth: what was true, what changed, how it verifies. Include a ::code run block with reads=/writes= when verification needed. Never timeline, never "as of". If no index.dx, skip with one log line.
- **memory_os.go read/write**: `ReadMemoryFromDx` (sparse recall, ≤6 blocks, never whole docs) and `WriteMemoryToDx` (fallback to old store for migration). `buildDeterministicMemoryContext` appends dx recall to VerifiedFacts. Logs "recall: k blocks, n chars".
- **Planner brief context**: Built from top-k dx_search blocks (k≤6 max); planner never sees whole documents, only blocks. Doc retrieval already integrated in ComposeDocContextFocused.
- **Verified blocks cache**: When a block's ::code run verdict is PASS, dx caches it; planner reads verdict before re-run (unchanged inputs already proven).

## Known Drift From Intended Manager Model

- The event orchestrator does replan and iterate, but it does not yet behave like a durable project manager that dynamically creates, supervises, repairs, and retires true subordinate managers/workers.
- The current implementation preserves project state, but its execution units are still coarse plan buckets rather than capability-driven members.
- If you need the strongest existing project-control loop today, the classic orchestrator path remains the more complete implementation.
