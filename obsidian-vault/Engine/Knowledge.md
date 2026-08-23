# Knowledge — Design Decisions & Discovered Constraints

Use this note to record thought processes, discovered constraints, and design rationale.
Add entries here when making non-obvious decisions so future sessions can reason from them.

## How to Use

- Add a `## Decision: <topic>` section for each meaningful design choice.
- Include: what was decided, why, what alternatives were rejected, and what tradeoffs exist.
- Link to related [[Engine/Progress Log]] entries or [[Engine/Architecture]] sections.

---

## Decision: WebSocket over REST for AI streaming

**Decided:** Use persistent WebSocket connections rather than HTTP streaming for all client-server communication.

**Why:** AI streaming requires low-latency bidirectional communication. WebSockets give us a single persistent channel for tool call results, AI chunks, and real-time file tree updates without polling.

**Rejected:** SSE (server-sent events) — unidirectional; REST — request/response model creates unnecessary round-trips for streaming.

---

## Decision: SQLite for persistence

**Decided:** SQLite embedded in the Go server, not a remote DB.

**Why:** Engine runs locally. No network dependency, zero setup, portable. The data (sessions, usage, project direction) is per-workspace and small enough for SQLite to handle trivially.

---

## Decision: Tauri for desktop shell (not Electron)

**Decided:** Tauri (Rust + WebView) over Electron.

**Why:** Far smaller bundle size, native WebView instead of bundled Chromium, better security posture, Rust for native code.

**Tradeoff:** More platform-specific surface area; WebView quirks differ from Chromium.

---

## Decision: Separate lint gate — gsh strict lint, not ESLint/Biome

**Decided:** No ESLint, Biome, or OXC installed. Lint runs via the `gsh strict lint` MCP tool.

**Why:** Keeps the dependency tree lean and avoids config sprawl. The MCP tool provides VS Code diagnostics directly.

---

## Decision: No workspace mirrors for autonomous project clones

**Decided:** Autonomous project clones must live in `~/.engine/projects` or `ENGINE_CLONES_DIR`, not under the Engine repo as `.engine/projects` or `packages/server-go/.engine/projects`. Workspace symlinks or migrated copies pointing at runtime clones should be removed immediately while preserving the real runtime directory.

**Why:** Workspace mirrors make VS Code and git show fake project state inside the Engine repo, which caused monitoring to inspect the wrong-looking path and hid the real runtime contract. They also risk Go workspace leakage and misleading dirty subproject status.

**Rejected alternatives:** Keeping symlinks for convenience; treating workspace-root `.engine/projects` as an acceptable alias.

**Tradeoffs:** Agents must use the real runtime path or configured clone dir explicitly, but monitoring and validation now reflect the environment Engine actually drives.

---

## Decision: Lead agent plus project-scoped peer communication

**Decided:** Keep one user-facing lead agent responsible for reporting to the user, while worker teams exchange focused messages through a project-scoped agent communication pool with four built-in tools: list peers, send a message, read inbox, and await a reply.

**Why:** The video guidance was correct that information dies when every fact climbs a strict hierarchy, but a completely flat team with no user-facing lead can become noisy. This design keeps the human interface simple while letting specialists ask each other narrow questions and preserve clean context windows.

**Rejected alternatives:** One giant orchestrator prompt with every role's context; isolated sub-agents that only report upward; exposing all peer chatter directly to the user.

**Tradeoffs:** The first implementation is in-process and project-scoped, so cross-device agent communication still needs a transport layer later. The tool contract is intentionally small so it can be backed by a network broker without changing role prompts.

---

## Decision: Memory OS as infrastructure (not prompt discipline)

**Decided:** Implement an always-on Memory OS in the server with an append-only hash-chained event ledger, a residual cognition graph, deterministic context compiler output, and shared/model-specific scribe snapshots.

**Why:** Finite model context windows make raw-history replay impossible at scale. The only reliable path is: persist all meaningful events losslessly, keep a weighted state graph for relevance, and compile deterministic context packs each turn so the model does zero manual bookkeeping.

**Rejected alternatives:** Relying on manually-authored summaries; storing only recent chat windows; embedding memory directives only in prompt text without backend persistence.

**Tradeoffs:** This increases DB schema and ingestion complexity, and scoring heuristics will need iteration. The first pass focuses on deterministic state recovery and hook coverage rather than a fully autonomous contradiction repair loop.

---

## Decision: Orchestrator Owns Project Control, Event Teams Disabled In Production

**Decided:** The classic orchestrator remains the only production control loop. The event-driven team runtime is disabled from the top-level `RunAutonomousProject` path until it implements the intended hierarchy: Chatter -> Orchestrator -> Scribe -> Managers -> Workers.

**Why:** Current event-mode "teams" are grouped plan buckets executed through the same autonomous-builder role, which is not the intended manager/member model. Leaving that path reachable causes architectural drift and teaches the wrong behavior.

**Rejected alternatives:** Keeping event mode as an opt-in production path; treating heuristic step buckets as real subordinate managers; documenting the current event model as if it already matched the target hierarchy.

**Tradeoffs:** This removes one experimental runtime path from normal use. The repo keeps the event orchestrator code for isolated iteration and tests, but all real project control now stays on the stronger classic loop until the layered hierarchy is implemented for real.

---
