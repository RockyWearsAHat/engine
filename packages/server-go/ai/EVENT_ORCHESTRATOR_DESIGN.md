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

## Quota Governance (quota/ + quota_gate.go)

- Objective: spend 100% of each Anthropic window by its reset. Under pace = wasted quota, over pace = 429s. `PaceTarget = 1.0`; `Assessment.Pace` = used fraction / elapsed fraction, `Ahead = 1 - pace/target`.
- Boost: when ahead of target, `paceConcurrency` raises `Plan.MaxConcurrency` and `Plan.SubagentFanout` by `1 + Ahead`, capped at 2x policy and never above `Policy.MaxConcurrency` / `Policy.MaxSubagents`. Worker model is never touched by pace.
- Planner model: `Plan.PlannerModel` = `CheapModel` (haiku) unless `Ahead > PlannerSonnetAhead` (0.30), then `MidModel` (sonnet). `runPlannerPrePass` applies it downgrade-only via `modelRank`, and only for `claudecode`/`anthropic` planners; ollama/openai planners are untouched.
- Accounts: `quota.LoadAccounts` reads `$SARA_ACCOUNTS_FILE` (default `~/.sara-accounts.json`, shape `[{name, configDir}]`) first, falls back to `ENGINE_CLAUDE_ACCOUNTS`. Registry dedupes by `Identity.Key()` (org id or email), so the same login in two config dirs is one account.
- Pooled fleet snapshot: quota is per Anthropic account, not per box. `GET /quota/snapshot` exports `{machine, accounts:[{key,name,sessionPct,weekPct,pacePct,resetAt,maxConcurrency}], maxConcurrency, generatedAt}`. `POST /quota/pooled` accepts a snapshot merged across machines (`MergeSnapshots`: dedupe by key, worst pct, lowest grant). Governor reads pooled rows instead of probing locally while the snapshot is under `PooledFresh` (2 min); stale = local probe. `AccountStatus.Source` says which.
- Ledger usd: subscription runs report `total_cost_usd: 0`, so `quotaAfter` prices stream-json usage with `quota.CostUSD` (fable 10/50, opus 5/25, sonnet 3/15, haiku 1/5 per 1M; cache write 1.25x, cache read 0.1x input). `Outcome.USD` and `Stat.USD` are non-zero for any priced model; unknown models price 0.
- Tests: `quota/pooled_test.go`, `quota/governor_test.go`, `ai/quota_usd_test.go`.

## Known Drift From Intended Manager Model

- The event orchestrator does replan and iterate, but it does not yet behave like a durable project manager that dynamically creates, supervises, repairs, and retires true subordinate managers/workers.
- The current implementation preserves project state, but its execution units are still coarse plan buckets rather than capability-driven members.
- If you need the strongest existing project-control loop today, the classic orchestrator path remains the more complete implementation.
