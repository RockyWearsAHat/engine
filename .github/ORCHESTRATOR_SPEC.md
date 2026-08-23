# Engine Orchestrator Specification

## Purpose

This document defines how Engine autonomous orchestration currently works in code.

Primary code paths:
- packages/server-go/ai/orchestrator.go
- packages/server-go/ai/orchestrator_event.go
- packages/server-go/ai/orchestrator_brain.go
- packages/server-go/ai/team_dispatcher.go
- packages/server-go/ai/agent_comms.go
- packages/server-go/ai/context.go

## High-Level Model

Engine has two orchestration modes:

1. Classic orchestration loop
- Entry: RunAutonomousProject in orchestrator.go
- Flow: intake -> PRD -> plan -> execute step -> review step -> validate -> done
- Persistence: .engine/orchestration.json and .engine/plan.md
- Default mode unless both USE_EVENT_ORCHESTRATOR=1 and ENGINE_EXPERIMENTAL_EVENT_ORCHESTRATOR=1

2. Event-driven team orchestration
- Entry: RunEventOrchestrator or RunEventOrchestratorAsState in orchestrator_event.go
- Flow: intake -> plan -> dispatch multiple teams -> wait on events -> validate -> replan if needed
- Persistence: .engine/brain.json (via OrchestrationBrain)
- Enabled only when USE_EVENT_ORCHESTRATOR=1 and ENGINE_EXPERIMENTAL_EVENT_ORCHESTRATOR=1

## Teammate Communication (Independent Agents)

This is independent-agent communication, not child/subagent delegation.

### Communication hub

Implementation: agent_comms.go

- AgentCommsHub is a per-project in-memory communication pool.
- AgentCommsForProject(projectPath) returns the shared hub for that project.
- Agents are registered as peers with id, role, status, and updatedAt.
- Messages are explicit from one peer to another and queued in recipient inboxes.

Core objects:
- AgentPeer
- AgentMessage
- AgentCommsHub

Core hub operations:
- Register(id, role, status)
- List()
- Send(from, to, subject, body, replyTo)
- Inbox(agentID, consume)
- Await(agentID, messageID, replyTo, timeout)

### Tool surface exposed to agents

Implementation entry: aiExecuteTool in context.go

Available communication tools:
- agent_list
- agent_send
- agent_inbox
- agent_await

Behavior:
- agent_list returns currently registered peers.
- agent_send requires sender, recipient, and non-empty body.
- agent_inbox can optionally consume messages.
- agent_await polls until matching message or timeout.

### Registration and identity

- registerChatAgent initializes AgentComms on ChatContext when available.
- If AgentName is unset, it is derived from role.
- Team workers set AgentName to their team id so each team appears as an independent peer.

## Event-Driven Team Orchestration Details

Implementation: orchestrator_event.go, orchestrator_brain.go, team_dispatcher.go, event.go

### Runtime components

- EventOrchestrator: main event-loop coordinator.
- OrchestrationBrain: persistent shared state for requirements, plan, teams, and validation feedback.
- EventBus: pub/sub channel bus for phase/team/control events.
- TeamDispatcher: concurrent team execution manager.
- AgentCommsHub: teammate communication pool shared by lead and teams.

### Event types used

From event.go:
- Intake: design_ready, prd_ready
- Planning/team: plan_ready, team_started, team_updated, team_done, team_blocked, team_failed
- Validation: validation_started, validation_passed, validation_failed
- Control: user_redirect, cancel, pause, resume
- Terminal: project_done, project_failed, project_canceled

### Team lifecycle

1. Plan is converted into teams by createTeamsFromPlan.
2. Teams are stored in brain.Teams with status queued/running/blocked/done/failed.
3. phaseDispatchTeams dispatches ready teams (dependencies satisfied).
4. TeamDispatcher starts TeamWorker goroutines for dispatched teams.
5. Team workers emit team_updated/team_done/team_failed events.
6. phaseWaitTeams reacts to events, dispatches newly unblocked teams, and handles failure/cancel.

### Team execution behavior

In TeamWorker.runStep:
- Role set to autonomous builder.
- AgentName set to team ID.
- AgentComms attached to ChatContext.
- Prompt explicitly instructs use of agent_list, agent_send, agent_inbox, and agent_await.
- Step loop is bounded by max turns.

### Concurrency

- TeamDispatcher is configured with maxTeams and tracks active workers.
- Workers run in parallel goroutines.
- Dependencies gate readiness via brain.TeamBlockedOn and brain.ReadyTeams.

## Classic Loop Details

Implementation: orchestrator.go and orchestrator_phases.go

Safety controls:
- OrchestratorMaxOuterIterations (default 200)
- OrchestratorStepMaxTurns (default 24)
- OrchestratorMaxStepAttempts (default 5)

Classic persistence:
- .engine/orchestration.json
- .engine/plan.md

Classic control-plane handle:
- Stop
- Pause / Resume
- Redirect (single-use redirect message consumed at next step)

## Discord Control Integration

Implementation: packages/server-go/discord/control.go

Commands integrate with orchestrator handles:
- stop
- redirect
- plan
- orchestrators
- pause/resume propagation via applyPauseToOrchestrator

## Known Current Limitations (Important)

These are current implementation facts:

1. Event planner parsing is currently stubbed
- extractPlanFromContext in orchestrator_event.go currently returns an empty plan.
- This means event-mode team creation depends on future plan parsing implementation.

2. Event intake PRD writer role is initialized, but current phaseIntake sets requirements from docs and emits events rather than fully parsing a writer output pipeline in that function.

3. Event mode and classic mode do not persist to the same state files.
- Event mode uses .engine/brain.json
- Classic mode uses .engine/orchestration.json and .engine/plan.md

4. Event-mode teams are not true specialist members yet.
- `createTeamsFromPlan` groups plan steps by title heuristics rather than by an explicit manager-built specialist composition.
- `TeamWorker.runStep` executes all team work with `RoleAutonomousBuilder`, regardless of stored team role.
- The `lead_planner.go` specialist composition model exists in code, but it is not currently the runtime source for event-team creation.

5. Current "team" terminology is overloaded.
- Config-selected "team" means which orchestrator model to run.
- Event-mode "team" currently means a bucket of grouped plan steps.
- Neither surface currently equals a durable subordinate manager with its own capability-specific runtime contract.

## Teammate Communication Contract

When team mode is active, communication behavior is:

- Multiple independent team agents run concurrently.
- Each team has its own identity in the project communication pool.
- Teams can discover peers and exchange focused messages without routing every detail through one giant context window.
- The user still interacts through a lead orchestrator path; peer chatter is internal unless surfaced intentionally.

## Operational Summary

- If USE_EVENT_ORCHESTRATOR is unset: classic step-by-step orchestrator path runs.
- If USE_EVENT_ORCHESTRATOR=1 but ENGINE_EXPERIMENTAL_EVENT_ORCHESTRATOR is unset: classic step-by-step orchestrator path still runs.
- If both USE_EVENT_ORCHESTRATOR=1 and ENGINE_EXPERIMENTAL_EVENT_ORCHESTRATOR=1: event-driven team orchestration path runs and enables independent teammate communication lines through AgentCommsHub and agent_* tools.

This is the source-of-truth behavior as of current code.
