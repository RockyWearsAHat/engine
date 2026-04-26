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
