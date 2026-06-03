# Progress Log

Use this note for human-authored progress updates and design decisions.

Tip: insert [[Templates/Progress Entry Template]] for each new entry.

---

# Progress Entry — 2026-05-18

## Goal

Implement the video's multi-agent communication lesson in Engine: one lead agent should report to the user while worker teams coordinate directly through simple tools.

## Change

Added a project-scoped agent communication pool in the Go AI subsystem, four built-in peer tools (`agent_list`, `agent_send`, `agent_inbox`, `agent_await`), lead/worker role prompt rules, and team-dispatcher wiring so autonomous teams enter the pool with their team identity.

## Validation

Ran focused Go tests for agent comms, team dispatcher wiring, and role prompt/tool contracts: `cd packages/server-go && GOWORK=off go test ./ai -run 'TestAgentComms|TestTeamDispatcher|TestBuildRoleSystemPrompt_Interactive_ContainsLeadDelegationRule|TestRoleBootstrapTools_AutonomousBuilder_HasAgentCommunication|TestBuildRoleSystemPrompt_AutonomousBuilder_ContainsTeamCommunicationRules' -count=1`.

## Decisions

| Decision | Why | Tradeoff |
| --- | --- | --- |
| Use one lead agent plus peer comms | Keeps user communication simple while preventing worker-level knowledge loss | Cross-device transport is future work |
| Keep the tool surface to four primitives | Mirrors the video's simple message-pool shape and reduces agent tool confusion | More advanced routing must build on these primitives |

## Thought Process

The video's strongest lesson was not “more agents,” but better communication topology: focused context windows, direct peer exchange, and validation/check-in between specialists. Engine already had role prompts and team dispatch, so the missing piece was a concrete communication substrate instead of another orchestration prompt.

## Links

- Related: [[Engine/Knowledge]]
- Behaviors changed: [[Engine/Working Behaviors]]

---

# Progress Entry — 2026-06-02

## Goal

Implement the Memory OS foundation so meaningful memory is persisted losslessly, residual attention state updates continuously, and context assembly is deterministic with zero manual model bookkeeping.

## Change

Added a new persistence and cognition layer in Go:
- DB schema + APIs for append-only hash-chained ledger events, residual graph nodes/edges, and memory snapshots.
- AI ingestion and scoring pipeline to convert chat/tool/validation lifecycle events into residual graph updates.
- Deterministic memory context compiler injected into interactive prompt assembly.
- Shared + model-specific scribe snapshots written to DB and `.engine/memory/scribe/` markdown artifacts.
- Chat lifecycle hooks for user, tool start/result, and assistant completion events.

## Validation

Ran focused passing tests:
- `go test ./packages/server-go/db -run 'TestMemoryLedgerAppendAndVerify|TestMemoryResidualNodesAndSnapshotsRoundTrip' -count=1`
- `go test ./packages/server-go/ai -run 'TestIngestMemoryEventAndCompileContext|TestPersistScribeSnapshotWritesSharedAndModelNotes' -count=1`
- `go test ./packages/server-go/ai -run 'TestBuildSelectiveContextPromptIncludesWeightedHistoryAndFocus' -count=1`

## Decisions

| Decision | Why | Tradeoff |
| --- | --- | --- |
| Store memory as immutable event ledger + graph state | Enables replayability and deterministic reconstruction | More schema complexity and ingestion surface |
| Compile deterministic memory context server-side | Prevents prompt drift and manual memory notes | Requires ongoing scoring calibration |
| Keep model-specific and shared scribe snapshots | Preserves per-model nuance while maintaining project-wide ground truth | Slight storage overhead |

## Thought Process

The key shift was treating memory as backend state infrastructure instead of model prompt behavior. The model should consume compiled state, not author it by hand each turn. This keeps continuity reliable across long-running sessions and context compaction.

## Links

- Related: [[Engine/Architecture]]
- Related: [[Engine/Knowledge]]
