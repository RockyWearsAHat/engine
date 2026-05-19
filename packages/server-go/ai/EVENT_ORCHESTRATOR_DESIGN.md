# Event-Driven Orchestrator Design

## Overview

The orchestrator has been refactored from a linear synchronous pipeline to an **event-driven brain with team-based parallel dispatch**. This enables:

- **Persistent orchestrator brain** knowing project state, requirements, and dependencies
- **Event inbox** for GitHub issues, Discord messages, completion signals, user redirects
- **Parallel team execution** with dependency chains (DB team, frontend team, API team, etc.)
- **Flexible team composition** — each team is a configurable agent pipeline (plan → build → test → review)

## Architecture

### Core Components

#### 1. **event.go** — Event Bus
- `EventType` constants: `EventDesignReady`, `EventPRDReady`, `EventPlanReady`, `EventTeamStarted`, `EventTeamDone`, `EventTeamFailed`, `EventValidationPassed`, `EventValidationFailed`, `EventUserRedirect`
- `Event` struct carries type, timestamp, payload (user-friendly message data), and teamID
- `EventBus` pub/sub: `Subscribe(eventType)` → `<-chan Event`, `Emit(event)` broadcasts to all subscribers
- Non-blocking emit with configurable buffer size to prevent slow subscribers from blocking dispatch

#### 2. **orchestrator_brain.go** — Persistent State
- `ProjectRequirements`: vocabulary, PRD, design, module index (all immutable once loaded)
- `TeamState`: tracks each team's id, role (db/frontend/api), assigned steps, status, dependencies, feedback
- `OrchestrationBrain`: holds requirements, plan, teams map, outer iteration count, lifecycle timestamps
- Thread-safe with RWMutex for parallel team reads
- **Key queries**:
  - `ReadyTeams()` → teams with no blocking dependencies
  - `TeamBlockedOn(teamID)` → list of unsatisfied dependency IDs
  - `AllTeamsDone()` → true if all teams are done/failed
  - `AnyTeamFailed()` → stops execution early
- Persists to `.engine/brain.json` after mutations

#### 3. **team_dispatcher.go** — Parallel Execution
- `TeamWorker`: represents a single team's goroutine
  - Runs assigned plan steps sequentially within the team
  - Max turns bounded by `OrchestratorStepMaxTurns` (24 by default)
  - Emits `EventTeamUpdated`, `EventTeamDone`, `EventTeamFailed` on state changes
  - Integration point: calls `cfg.chatFnFor()(ctx, prompt)` for each step (builder role with RoleAutonomousBuilder)
- `TeamDispatcher`: manages N concurrent teams
  - Max concurrency: 4 teams by default (configurable)
  - `DispatchTeam(teamID)` — starts a worker goroutine if dependencies satisfied
  - Emits `EventTeamStarted` when team begins
  - Tracks active workers in map; provides `ActiveTeams()` count

#### 4. **orchestrator_event.go** — Event Loop
- `EventOrchestrator`: main state machine
  - Subscribes to: `EventTeamDone`, `EventTeamFailed`, `EventUserRedirect`, `EventCancel`
  - Linear phases (for now):
    1. **phaseIntake()** → RoleGriller, RolePRDWriter → design.md, vocabulary.md, prd.md
    2. **phasePlan()** → RolePlanner → PlanStep[] in brain
    3. **phaseDispatchTeams()** → calls `dispatcher.DispatchTeam()` for all ready teams
    4. **phaseWaitTeams()** → event loop waiting for teams + handling redirects
    5. **phaseValidate()** → RoleReviewer → VALIDATION_PASSED or feedback
    6. Outer iteration loop: re-plan if validation fails
- **Redirect handling**: `EventUserRedirect` with message payload → updates brain.LastValidation for replanning
- **Team lifecycle**: 
  - On step completion, check dependencies and dispatch newly ready teams
  - On team failure, re-plan and retry
  - On all teams done, validate; on validation failure, feedback → re-plan

#### 5. **orchestrator.go** — Wrapper
- `RunAutonomousProject` now routes through new event orchestrator when `USE_EVENT_ORCHESTRATOR=1`
- `RunEventOrchestratorAsState(cfg)` wraps event orchestrator to return `*OrchestrationState` for backward compatibility
- Keeps old linear orchestrator intact for gradual migration

## Data Flow

```
GitHub Issue / Discord / User Prompt
    ↓
RunAutonomousProject (cfg)
    ↓ [if USE_EVENT_ORCHESTRATOR=1]
RunEventOrchestrator (cfg)
    ├─ Create brain from .engine/brain.json (or fresh)
    ├─ Create event bus
    ├─ Create team dispatcher
    ├─ Start event loop
    │
    ├─ Phase: Intake (grill, PRD)
    │  └─ Emit: EventDesignReady, EventPRDReady
    │
    ├─ Phase: Plan
    │  ├─ Call RolePlanner
    │  ├─ Create teams via createTeamsFromPlan() (heuristic: group steps by domain)
    │  └─ Emit: EventPlanReady
    │
    ├─ Dispatch Loop
    │  ├─ phaseDispatchTeams()
    │  │  └─ For each ready team: dispatcher.DispatchTeam(teamID)
    │  │     └─ Spawns worker goroutine → team.run()
    │  │        └─ For each assigned step:
    │  │           └─ TeamWorker.runStep(step)
    │  │              ├─ Create ChatContext with RoleAutonomousBuilder
    │  │              ├─ cfg.chatFnFor()(ctx, prompt) [bounded by MaxTurns]
    │  │              └─ Emit: EventTeamUpdated, EventTeamDone/Failed
    │  │
    │  ├─ phaseWaitTeams() [event loop]
    │  │  ├─ On EventTeamDone: check dependencies, dispatch ready teams
    │  │  ├─ On EventTeamFailed: mark team failed
    │  │  ├─ On EventUserRedirect: update brain.LastValidation
    │  │  └─ Exit when all teams done/failed
    │  │
    │  ├─ Validation
    │  │  ├─ phaseValidate() → RoleReviewer
    │  │  ├─ If passes: Emit EventProjectDone, return
    │  │  └─ If fails: update brain.LastValidation, loop to Plan
    │  │
    │  └─ Outer iteration loop until MaxOuterIterations or success
    │
    └─ Return: *OrchestrationBrain
```

## Team Grouping Heuristic

`createTeamsFromPlan()` uses `inferRoleFromStep()` to group consecutive similar steps:
- Steps with "db", "database", "schema" → "db" team
- Steps with "frontend", "ui", "component" → "frontend" team
- Steps with "api", "endpoint", "server" → "api" team
- Default → "general" team

Teams run **sequentially within their group** but **in parallel across groups**. Future refinements can split teams further or adjust grouping per project.

## Current Gaps & Future Work

### Phase 1: Event Infrastructure (✅ Complete)
- [x] Event types and EventBus
- [x] OrchestrationBrain with team state
- [x] TeamDispatcher for parallel dispatch
- [x] EventOrchestrator main loop

### Phase 2: Integration (In Progress)
- [ ] **Intake phase**: Fully invoke RoleGriller & RolePRDWriter via event callbacks
- [ ] **Plan parsing**: Extract JSON plan from planner output, populate PlanStep[] correctly
- [ ] **Team composition**: Refine grouping heuristic; allow project-level team config
- [ ] **Step execution**: Full ChatContext integration (tools, callbacks, signal_done detection)
- [ ] **Redirect routing**: Handle Discord `!redirect <msg>` → EventUserRedirect → brain.LastValidation

### Phase 3: Optimizations
- [ ] **Parallel turnaround**: Currently teams run sequentially (Max turns per step, then next step). Introduce asynchronous step dispatch so one team doesn't block others.
- [ ] **Model diversity**: Assign different models per team role (e.g., planner=qwen3.6:35b, builder=qwen2.5:7b for speed).
- [ ] **User feedback loop**: Discord commands (pause, resume, redirect, status) wire through event bus.
- [ ] **Dependency DAG**: Explicit user-defined or inferred team dependencies (e.g., frontend team waits for API team's schema endpoint).

## Testing

The event orchestrator is feature-flagged. To enable:
```bash
export USE_EVENT_ORCHESTRATOR=1
```

Then:
```bash
# Run a project with the new orchestrator
$ curl -X POST http://localhost:3000/orchestrate -d '{"owner":"repo-owner", "repo":"repo-name", "brief":"..."}'
```

Monitor events via:
```bash
tail -f .engine/brain.json  # Brain state updates
```

Check phase transitions:
```bash
echo "Team dispatch in action:"
grep "team_started\|team_done" .engine/brain.json
```

## Integration Checklist

- [ ] Test intake phase (design grill)
- [ ] Test plan parsing from planner output
- [ ] Test team dispatch (verify workers spawn)
- [ ] Test step execution (mock tool calls, verify signal_done)
- [ ] Test validation feedback loop
- [ ] Test user redirect via Discord
- [ ] Benchmark: measure parallel team speedup vs. sequential
- [ ] Document team configuration format (project-level overrides)
