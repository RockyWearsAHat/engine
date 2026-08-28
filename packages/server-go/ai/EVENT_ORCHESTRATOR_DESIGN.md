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

## Current Structural Reality

- Comms hub: `AgentCommsForProject` per project (keyed by project path, no task slug). Orchestrator registers `lead`; `TeamDispatcher` registers every team id; `claudecodeProvider.RunLoop` registers the worker (`registerChatAgent`) **before** the CLI starts, so `agent_list` on a peer already shows it. Bridge tool calls self-register an *unknown* worker as `active`; a peer already in the hub keeps whatever status the dispatcher set (`IsRegistered` guard in `ExecuteBridgeTool`).
- Comms on disk: every `Register` / `Send` mirrors the hub to `<project>/.engine/comms.json` (`comms-<slug>.json` when a hub is built with `NewAgentCommsHubAt(path, slug)`; the shared per-project hub has no slug). Shape: `{updatedAt, agents[], messages[]}` — peers sorted by id, messages by time. Best-effort write; disk trouble never breaks comms. This is the observable proof for "≥2 registered peers, ≥1 `msg-N` exchanged".
- MCP bridge (`ai/mcp_bridge.go`): `claude -p` workers get `--mcp-config <tmp.json> --allowedTools mcp__engine__*`. The config spawns `<server binary> mcp-bridge` (override `ENGINE_MCP_BRIDGE_CMD`) with env `ENGINE_MCP_PROJECT`, `ENGINE_MCP_AGENT`, `ENGINE_MCP_ROLE`, `ENGINE_MCP_ADDR`. Bridge = stdio JSON-RPC 2.0 (`initialize`, `tools/list`, `tools/call`, `ping`); each `tools/call` POSTs to the running Engine at `/mcp/tool`, which runs the tool in-process against the project hub. Empty addr = in-process. The POST client has **no timeout** — `agent_await` blocks as long as the worker asks; the bridge process dies with the CLI.
- Bridged tools: `agent_list agent_send agent_inbox agent_receive agent_await signal_done discord_post_progress discord_dm mesh_exec search_tools`. File/shell tools are not bridged — Claude Code has its own. Temp config removed after the run.
- `--disallowedTools Task` only when governor fanout == 0. Positive fanout = advisory budget in the system prompt.
- Teams are still step buckets: `createTeamsFromPlan` groups by title keyword (`db`, `frontend`, `api`, `general`); every member runs `RoleAutonomousBuilder`; `lead_planner.go` composition not wired (slice H).
- No mid-run member spawn / retire yet (slice H).

## Validation Surface

The event orchestrator has direct unit coverage in:

- `mcp_bridge_test.go` (routing rule, brain namespacing, bridge wire, fake-claude → bridge → HTTP → hub)
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

## Known Drift From Intended Manager Model

- The event orchestrator does replan and iterate, but it does not yet behave like a durable project manager that dynamically creates, supervises, repairs, and retires true subordinate managers/workers.
- The current implementation preserves project state, but its execution units are still coarse plan buckets rather than capability-driven members.
- If you need the strongest existing project-control loop today, the classic orchestrator path remains the more complete implementation.
