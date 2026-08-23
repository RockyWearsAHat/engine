# Boundary Contracts

This file records side-effect boundaries that are intentional and part of product behavior.

Rules for entries:
- Each entry has a `contract:` tag consumed by the quality scanner.
- Each entry states durable side effects and the expected failure behavior.
- These tags do not disable rules globally; they only document approved boundaries.

## Workspace Contracts

contract: packages/client/src/connectionProfiles.ts::deleteconnectionprofile
Side effects: updates persisted connection-profile JSON on disk.
Failure contract: returns filesystem/serialization errors to caller; no silent fallback.

contract: packages/desktop-tauri/src-tauri/src/lib.rs::run
Side effects: initializes runtime config and may persist local server token/config.
Failure contract: bubbles startup failures so desktop boot halts loudly.

contract: packages/server-go/ai/context.go::chat
Side effects: writes session/message/tool telemetry state.
Failure contract: propagates persistence failures to upstream request handling.

contract: packages/server-go/ai/context.go::executetoolfortest
Side effects: may execute mutating tool paths for test harnesses.
Failure contract: returns tool execution errors without swallowing.

contract: packages/server-go/ai/orchestrator.go::runautonomousproject
Side effects: persists orchestration state snapshots and progress records.
Failure contract: stops orchestration and returns durable-state errors.

contract: packages/server-go/ai/orchestrator_brain.go::updaterequirements
Side effects: mutates staged requirements and persists state.
Failure contract: returns persistence errors; no partial silent commit.

contract: packages/server-go/ai/orchestrator_brain.go::updateplan
Side effects: mutates staged plan and persists state.
Failure contract: returns persistence errors; no partial silent commit.

contract: packages/server-go/ai/orchestrator_brain.go::addteam
Side effects: mutates team configuration and persists state.
Failure contract: returns persistence errors; caller remains authoritative.

contract: packages/server-go/ai/orchestrator_brain.go::updateteamstatus
Side effects: mutates team status and persists state snapshots.
Failure contract: returns persistence errors and does not silently discard updates.

contract: packages/server-go/ai/orchestrator_brain.go::updateteamfeedback
Side effects: mutates team feedback and persists state snapshots.
Failure contract: returns persistence errors and does not silently discard updates.

contract: packages/server-go/ai/orchestrator_brain.go::markcompleted
Side effects: marks orchestration complete and persists final state.
Failure contract: returns persistence errors to caller.

contract: packages/server-go/ai/orchestrator_event.go::runeventorchestrator
Side effects: emits progress artifacts and may write planning docs.
Failure contract: exits with explicit error when phase persistence fails.

contract: packages/server-go/ai/orchestrator_event.go::runeventorchestratorasstate
Side effects: orchestrates event loop and persists resulting state.
Failure contract: returns event-loop write failures to caller.

contract: packages/server-go/ai/repair_loop.go::executerepairloop
Side effects: persists working-state snapshots for repair iterations.
Failure contract: returns snapshot/persistence errors.

contract: packages/server-go/ai/session_summary.go::ensureprojectdirection
Side effects: upserts project direction in persistent store.
Failure contract: returns storage errors, no silent fallback summary.

contract: packages/server-go/ai/session_summary.go::buildinitialsessionsummary
Side effects: may persist inferred project direction while summarizing.
Failure contract: returns context/persistence errors to caller.

contract: packages/server-go/ai/session_summary.go::buildworkspacepromptcontext
Side effects: may persist inferred project direction during context bootstrap.
Failure contract: returns context/persistence errors to caller.

contract: packages/server-go/ai/test_loop.go::completetestrun
Side effects: appends learning events and updates run-state bookkeeping.
Failure contract: returns persistence errors for durable test outcomes.

contract: packages/server-go/ai/test_loop.go::reporttestresult
Side effects: records result and may finalize run state.
Failure contract: returns persistence errors; no silent downgrade.

contract: packages/server-go/db/db.go::withproject
Side effects: initializes per-project database and runs migrations.
Failure contract: returns init/migration errors and aborts setup.

contract: packages/server-go/discord/service.go::newservice
Side effects: loads/persists enrollment state and session metadata.
Failure contract: returns load/save errors to startup caller.

contract: packages/server-go/discord/service.go::start
Side effects: reloads state and may persist enrollment/session changes.
Failure contract: surfaces reload/persistence failures.

contract: packages/server-go/discord/service.go::autoenrollproject
Side effects: mutates enrollment registry and persists result.
Failure contract: returns enrollment persistence errors.

contract: packages/server-go/discord/service.go::validate
Side effects: validates runtime state and may reload/persist session state.
Failure contract: surfaces validation and persistence errors.

contract: packages/server-go/discord/service.go::reload
Side effects: reloads discord state and persists normalized control-channel state.
Failure contract: returns reload/persistence errors.

contract: packages/server-go/github/identity.go::set
Side effects: persists GitHub identity/token state.
Failure contract: returns storage errors and keeps prior state unchanged.

contract: packages/server-go/github/webhook.go::servehttp
Side effects: writes protocol response codes/body only.
Failure contract: request fails with explicit HTTP status path.

contract: packages/server-go/quality/report.go::scanproject
Side effects: refreshes quality project index cache on changed sources.
Failure contract: returns scan/index errors.

contract: packages/server-go/quality/report.go::scanprojectwithprogress
Side effects: refreshes quality project index cache and emits progress.
Failure contract: returns scan/index errors.

contract: packages/server-go/quality/report.go::refreshprojectindex
Side effects: rebuilds and persists quality index artifacts.
Failure contract: returns rebuild/persist errors.

contract: packages/server-go/remote/auth.go::newauthmanager
Side effects: provisions token store bootstrap files.
Failure contract: returns bootstrap write errors.

contract: packages/server-go/remote/auth.go::issuetoken
Side effects: writes protocol response and signs/encodes tokens.
Failure contract: returns token issuance errors.

contract: packages/server-go/remote/auth.go::validatetoken
Side effects: writes protocol response for auth failures/success.
Failure contract: returns validation errors.

contract: packages/server-go/remote/pairing.go::boundary
Side effects: issues and validates pairing tokens used for remote onboarding.
Failure contract: pairing failures return explicit auth/validation errors.

contract: packages/server-go/remote/tls.go::boundary
Side effects: manages TLS material loading/refresh for remote endpoints.
Failure contract: TLS load/refresh failures return explicit startup/runtime errors.

contract: packages/server-go/remote/keychain.go::set
Side effects: mutates persisted keychain entries.
Failure contract: returns save errors; no silent mutation loss.

contract: packages/server-go/remote/keychain.go::delete
Side effects: mutates persisted keychain entries.
Failure contract: returns save errors; no silent mutation loss.

contract: packages/server-go/vpn/identity.go::loadorcreateidentity
Side effects: may create and persist vpn identity file.
Failure contract: returns filesystem/serialization errors.

contract: packages/server-go/vpn/trust.go::adddevice
Side effects: mutates trusted-device registry and saves state.
Failure contract: returns save errors.

contract: packages/server-go/vpn/trust.go::removedevice
Side effects: mutates trusted-device registry and saves state.
Failure contract: returns save errors.

contract: packages/server-go/workspace/registry.go::addtoregistry
Side effects: mutates workspace registry and persists state.
Failure contract: returns save errors.

contract: packages/server-go/workspace/registry.go::removefromregistry
Side effects: mutates workspace registry and persists state.
Failure contract: returns save errors.

contract: packages/server-go/ws/handler.go::servews
Side effects: mutates workspace/git/session state through dispatched commands.
Failure contract: returns command failure to websocket client with explicit error payload.