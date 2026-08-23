# Engine Architecture

Living reference for tech stack, module structure, and key patterns.
Update this when structural changes land. Do not let it drift from reality.

## Tech Stack

| Layer | Technology |
| --- | --- |
| Client | React + TypeScript, Vite, Tailwind CSS |
| Server | Go (`packages/server-go`), WebSocket-based |
| Desktop shell | Tauri (Rust, `packages/desktop-tauri`) |
| Shared types | TypeScript (`packages/shared`) |
| Database | SQLite via Go (`server-go/db/`) |
| AI routing | Go (`server-go/ai/`) — Anthropic, OpenAI-compat, Ollama, agent communication |
| Secret scanning | Go (`server-go/ai/`) — blocks secrets before send |

## Module Map

```
packages/
  client/           React UI (Vite, Tailwind)
    src/components/ Feature components (AI, FileTree, Editor, Terminal, …)
    src/store/      Zustand global state
    src/ws/         WebSocket client (bridge to Go server)
    src/test/       Vitest tests — 100% coverage enforced
  server-go/
    ai/             AI provider routing, session history, agent comms, secret scan, harness
    db/             SQLite persistence (sessions, usage, project direction)
    discord/        Discord control-plane bot
    fs/             File system tools (read, write, list, search)
    git/            Git operations (status, diff, commit, push, pull, branch)
    github/         GitHub Issues integration
    remote/         Remote/mobile access tunnel
    terminal/       Terminal process management
    vpn/            VPN/network utilities
    workspace/      Repository registry
    ws/             WebSocket handler — routes all client messages
  desktop-tauri/    Tauri shell wrapping the client build
  shared/           TypeScript types shared between client and other packages
```

## Key Patterns

- **WebSocket bridge**: every client action sends a JSON message to Go via WS; Go routes to the right handler and streams back events.
- **AI streaming**: response chunks arrive as partial text events over WS; client renders them incrementally.
- **Project direction**: Go persists a living project direction summary per-workspace in SQLite; always loaded when a new session starts.
- **Agent communication pool**: Go maintains a project-scoped in-process pool where the lead agent and worker teams can list peers, send focused messages, read inboxes, and await replies without forcing every role into one context window.
- **Runtime hierarchy (current intended architecture)**: Chatter surfaces (Discord, GitHub, editor chat) feed the Orchestrator. The Orchestrator is the durable project brain. A Scribe layer sits beneath it as the context/documentation fabric. Managers are persistent area owners. Workers are tiny-scope specialists created and retired as needed.
- **Secret scanning**: Go intercepts every outgoing AI message and blocks if a secret pattern is matched.
- **Custom tools**: `.engine/tools/<name>.json` defines project-specific agent tools; inputs passed as `INPUT_<NAME>` env vars to prevent injection.
- **Test coverage**: 100% client (Vitest), 100% Go (go test), Rust (cargo llvm-cov) — all enforced by completion gate.
- **Memory OS (always-on)**: append-only hash-chained ledger (`memory_ledger_events`), residual cognition graph (`memory_residual_nodes`, `memory_residual_edges`), deterministic snapshots (`memory_state_snapshots`), deterministic context compilation, and scribe snapshots under `.engine/memory/scribe/`.

## Orchestration Status

- The classic orchestrator loop in `packages/server-go/ai/orchestrator.go` is the only production project-control path.
- The event-driven orchestrator remains in-tree as development code, but it is intentionally disabled from the top-level `RunAutonomousProject` entrypoint because its current "teams" are heuristic step buckets rather than true manager/specialist members.

## Client & Shared Type Interfaces

For detailed documentation of client and shared types, see:
- [packages/client/README.md](../../../packages/client/README.md) — client module interfaces and structure
- [packages/shared/README.md](../../../packages/shared/README.md) — shared type definitions
- [.github/CLIENT_MODULE_REFERENCE.md](./../CLIENT_MODULE_REFERENCE.md) — quick reference of all exported types

### packages/client/src (Platform Bridge Layer)

**`bridge.ts`**  
Platform abstraction that unifies Electron IPC, Tauri commands, and plain web APIs. Exports methods for project access, GitHub tokens, local server health checks, and preference persistence.

**`connectionProfiles.ts`**  
Connection profile management for remote servers.
- `ConnectionProfile` interface — stores remote server connection details (host, port, token, workspace path, name). Manages localStorage persistence of multiple profiles.

**`editorPreferences.ts`**  
Editor configuration and font settings.
- `EditorPreferences` interface — editor settings (font, font size, line height, tab size, word wrap, markdown view mode).
- `MarkdownViewMode` type — supports 'text', 'preview', 'split', 'syntactical' modes.

**`editorEvents.ts`**  
Custom DOM events for editor state synchronization across components.
- `EditorStatusDetail` — current file, language, syntax status, dirty flag.
- `CloseFileEventDetail` — path of file being closed.
- `SaveFilesEventDetail` — array of file paths to save.
- `RevealFileLocationDetail` — path, line, optional column for file location reveal.

### packages/client/src/ws (WebSocket Client)

**`client.ts`**  
WebSocket client for communication with Go backend.
- `RemoteConfig` interface — host, port, and token for connecting to a remote Engine server. Manages WebSocket lifecycle and message routing.

### packages/shared/src (Type Definitions)

Shared TypeScript types used across client and server communication boundaries:

**Session & Message Types**
- `Session` — Project session metadata: id, projectPath, branchName, createdAt, updatedAt, projectDirection, summary, messageCount.
- `Message` — Chat message record: id, sessionId, role ('user' or 'assistant'), content, optional toolCalls array, createdAt.
- `ToolCall` — AI tool invocation: id, name, input object, optional result, optional isError flag.

**File System Types**
- `FileNode` — File tree representation: name, path, type ('file' or 'directory'), optional children array, loaded flag, hasChildren flag, size, modified timestamp.
- `FileContent` — File buffer: path, content string, language (for syntax highlighting), size in bytes.

**Request & Search Types**
- `ApprovalRequest` — Pending user approval for dangerous operations: id, sessionId, kind ('shell' or 'git_commit'), title, message, command.
- `SearchResult` — Code search result: path, line, optional column, preview text.

**Configuration Types**
- `RuntimeConfig` — Merged environment and storage configuration: GitHub token/owner/repo, Anthropic/OpenAI keys, model provider, Ollama base URL, model name, active team, clones directory, context token limits.

## Memory OS Module Surface

- `packages/server-go/db/db.go`
  - `AppendMemoryLedgerEvent` / `GetMemoryLedgerEvents` / `VerifyMemoryLedgerChain`
  - `UpsertMemoryResidualNode` / `UpsertMemoryResidualEdge` / `ListTopMemoryResidualNodes`
  - `SaveMemoryStateSnapshot` / `LoadLatestMemoryStateSnapshot`
- `packages/server-go/ai/memory_os.go`
  - Event ingestion from chat/tool/assistant lifecycle
  - Residual graph updates with composite scoring
  - Deterministic context compiler for prompt assembly
  - Shared + model-specific scribe snapshot persistence
- `packages/server-go/ai/context.go`
  - Hooks for `user_message`, `tool_call_started`, `tool_call_result`, `assistant_message`
  - Injects deterministic memory context into interactive prompt context
  - Persists scribe snapshots after assistant completion

## Design Principles

1. AI-first — AI autonomously controls files, terminals, branches; no bolt-on.
2. Persistent context — session history, project direction, and memory outlive individual conversations.
3. Autonomous validation — AI runs the app, observes behavior, validates fixes.
4. External event awareness — GitHub Issues, CI failures, Discord commands trigger autonomous work.
5. Universal access — runs locally, accessible remotely from any device.
