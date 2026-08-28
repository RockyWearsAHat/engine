# Event-Driven Orchestrator (Code-Truth Snapshot)

This file is intentionally limited to what exists in source today. If code and this document diverge, code is authoritative.

## Activation Route

- `RunAutonomousProject` in `orchestrator.go` asks `ShouldRunEventOrchestrator`. Default on (`eventOrchestratorDefault = true`); `ENGINE_EVENT_ORCHESTRATOR=1/0` forces either way. Task mode (`cfg.TaskMode`, every SARA dispatch) runs the classic serial loop.
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

## Task API Surface (`task_api.go`)

How SARA hands one unit of work in and learns what happened. Rules:

- `POST /task` → 202 `{id,...}` at once. Orchestration runs in a goroutine. Planner never on the request path.
- `GET /task?id=` and `GET /task/<id>` → registry read under RLock. Never waits on a phase. Fields: `status`, `phase`, `stepsDone/stepsTotal`, `alive` (this process owns a goroutine for it), `firstProgressAt`, `lastTokenAt`, `lastToolAt` (evidence stamps from `OrchestratorConfig.OnActivity`), `model`, `tokensIn/tokensOut/subagentsSpawned` (from `OnRunStats`), `coached/escalated` (from `OnCoach`, slice I fills).
- Registry persisted to `<ENGINE_STATE_DIR>/tasks.json` (else `<project>/.engine/tasks.json`): on accept BEFORE plan, and on every note/plan/finish/stats change. Tmp+rename. Registry lock not held during write.
- Server start reloads it. Every row still `running` on reload is marked `failed` (terminal, `alive:false`, `lost:true`, dedupe key released) regardless of PID. SARA's gateway (`myeditor.mjs` asAgentEntry) treats status=failed + lost:true as "MyEditor forgot task", not a failure: engine marks item lastOutcome=lost, re-dispatches, no retry attempt spent. Nothing forgotten.
- Wake POST fires on EVERY terminal state — done, failed, canceled, panic. Target: caller `callbackUrl`, else `http://127.0.0.1:$SARA_ENGINE_WAKE_PORT/task-complete` (default 24777). Payload `{id, project, outcome, model, tokensIn, tokensOut, subagentsSpawned, coached, escalated, error}`. Fire-and-forget; GET-by-id stays truth.

## Builder Retry Rules (`orchestrator_phases.go`, `orchestrator.go`)

- Every builder attempt: fresh session, fresh `ChatContext`. Reviewer/critic notes ride in via `step.LastFeedback` as `REVIEWER NOTES` in the prompt.
- Every run logs phase `tokens`: `model in out elapsed`.
- Provider fault = provider reported usage (`RunStats.Seen`) AND `OutputTokens == 0` AND run ended inside `zeroOutputWindow` (1s). Backoff 1s / 5s / 15s, new run each. After the schedule: `ErrProviderZeroOutput`; outer loop calls `refundProviderAttempt` — `step.Attempts--`. Provider hiccups never retire a step (err-…002).
- Stub/ollama runs with no usage keep the plain "builder produced no output" path — counted as an attempt, no backoff.
- Tests: `zero_output_retry_test.go`, `task_api_persist_test.go`.

## Validation Surface

The event orchestrator has direct unit coverage in:

- `orchestrator_event_extra_test.go`
- `orchestrator_gap_extra_test.go`
- `orchestrator_run_extra_test.go`
- `team_dispatcher_test.go`
- `event_close_test.go`

These tests are the canonical behavioral spec for current orchestration behavior.

## Operator Notes

- Force event orchestration on/off with `ENGINE_EVENT_ORCHESTRATOR=1` / `=0`. Unset = derived (on, unless task mode or single team).

- Runtime brain state is persisted under the project `.engine` directory and can be inspected during execution.

## Quota Governance (quota/ + quota_gate.go)

- Objective: spend 100% of each Anthropic window by its reset. Under pace = wasted quota, over pace = 429s. `PaceTarget = 1.0`; `Assessment.Pace` = used fraction / elapsed fraction, `Ahead = 1 - pace/target`.
- Boost: when ahead of target, `paceConcurrency` raises `Plan.MaxConcurrency` and `Plan.SubagentFanout` by `1 + Ahead`, capped at `PaceBoostCap` (2x) × `Policy.MaxConcurrency` / `Policy.MaxSubagents`. Worker model is never touched by pace.
- Planner model: `Plan.PlannerModel` = `CheapModel` (haiku) unless `Ahead > PlannerSonnetAhead` (0.30), then `MidModel` (sonnet). `runPlannerPrePass` applies it downgrade-only via `modelRank`, and only for `claudecode`/`anthropic` planners; ollama/openai planners are untouched.
- Accounts: `quota.LoadAccounts` reads `$SARA_ACCOUNTS_FILE` (default `~/.sara-accounts.json`, shape `[{name, configDir}]`) first, falls back to `ENGINE_CLAUDE_ACCOUNTS`. Registry dedupes by `Identity.Key()` (org id or email), so the same login in two config dirs is one account.
- Pooled fleet snapshot: quota is per Anthropic account, not per box. `GET /quota/snapshot` exports `{machine, accounts:[{key,name,sessionPct,weekPct,pacePct,resetAt,maxConcurrency}], maxConcurrency, generatedAt}`. `POST /quota/pooled` accepts a snapshot merged across machines (`MergeSnapshots`: dedupe by key, worst pct, lowest grant). Governor reads pooled rows instead of probing locally while the snapshot is under `PooledFresh` (2 min); stale = local probe. `AccountStatus.Source` says which.
- Ledger usd: subscription runs report `total_cost_usd: 0`, so `quotaAfter` prices stream-json usage with `quota.CostUSD` (fable 10/50, opus 5/25, sonnet 3/15, haiku 1/5 per 1M; cache write 1.25x, cache read 0.1x input). `Outcome.USD` and `Stat.USD` are non-zero for any priced model; unknown models price 0.
- Tests: `quota/pooled_test.go`, `quota/governor_test.go`, `ai/quota_usd_test.go`.

## Known Drift From Intended Manager Model

- The event orchestrator does replan and iterate, but it does not yet behave like a durable project manager that dynamically creates, supervises, repairs, and retires true subordinate managers/workers.
- The current implementation preserves project state, but its execution units are still coarse plan buckets rather than capability-driven members.
- If you need the strongest existing project-control loop today, the classic orchestrator path remains the more complete implementation.
