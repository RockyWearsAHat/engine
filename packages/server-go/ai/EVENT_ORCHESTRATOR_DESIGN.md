# Event-Driven Orchestrator (Code-Truth Snapshot)

This file is intentionally limited to what exists in source today. If code and this document diverge, code is authoritative.

## Activation Route

- `RunAutonomousProject` in `orchestrator.go` routes to event orchestration only when both `USE_EVENT_ORCHESTRATOR=1` and `ENGINE_EXPERIMENTAL_EVENT_ORCHESTRATOR=1` are set.
- `RunEventOrchestratorAsState` adapts event orchestration to `*OrchestrationState`.

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

- In current code, "teams" are not true manager-led specialist groups.
- `createTeamsFromPlan` groups consecutive steps by simple title heuristics (`db`, `frontend`, `api`, `general`).
- `TeamWorker.runStep` currently executes every team step with `RoleAutonomousBuilder`, regardless of `TeamState.Role`.
- That means the event path is currently a parallel step-bucket executor, not a real specialist-member system.
- `lead_planner.go` defines a richer specialist composition model, but it is not currently wired into `RunEventOrchestrator` or `createTeamsFromPlan`.

## Validation Surface

The event orchestrator has direct unit coverage in:

- `orchestrator_event_extra_test.go`
- `orchestrator_gap_extra_test.go`
- `orchestrator_run_extra_test.go`
- `team_dispatcher_test.go`
- `event_close_test.go`

These tests are the canonical behavioral spec for current orchestration behavior.

## Operator Notes

- Enable event orchestration with:

```bash
export USE_EVENT_ORCHESTRATOR=1
export ENGINE_EXPERIMENTAL_EVENT_ORCHESTRATOR=1
```

- Runtime brain state is persisted under the project `.engine` directory and can be inspected during execution.

## Known Drift From Intended Manager Model

- The event orchestrator does replan and iterate, but it does not yet behave like a durable project manager that dynamically creates, supervises, repairs, and retires true subordinate managers/workers.
- The current implementation preserves project state, but its execution units are still coarse plan buckets rather than capability-driven members.
- If you need the strongest existing project-control loop today, the classic orchestrator path remains the more complete implementation.
