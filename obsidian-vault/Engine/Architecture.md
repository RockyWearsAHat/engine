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
| AI routing | Go (`server-go/ai/`) — Anthropic, OpenAI-compat, Ollama |
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
    ai/             AI provider routing, session history, secret scan, harness
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
- **Secret scanning**: Go intercepts every outgoing AI message and blocks if a secret pattern is matched.
- **Custom tools**: `.engine/tools/<name>.json` defines project-specific agent tools; inputs passed as `INPUT_<NAME>` env vars to prevent injection.
- **Test coverage**: 100% client (Vitest), 100% Go (go test), Rust (cargo llvm-cov) — all enforced by completion gate.

## Design Principles

1. AI-first — AI autonomously controls files, terminals, branches; no bolt-on.
2. Persistent context — session history, project direction, and memory outlive individual conversations.
3. Autonomous validation — AI runs the app, observes behavior, validates fixes.
4. External event awareness — GitHub Issues, CI failures, Discord commands trigger autonomous work.
5. Universal access — runs locally, accessible remotely from any device.
