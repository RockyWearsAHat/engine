# Engine — Architectural Reference

> **AI-native code editor.** AI is not bolted onto a text editor — it is the foundational architecture. Every feature is designed around AI-driven workflows.
>
> **Clarification:** Engine now uses a **first-party editor core**. **Tauri is only the desktop shell / process container** around the client and Go sidecar. It is not the editor engine.

---

## Table of Contents

1. [High-Level System Architecture](#1-high-level-system-architecture)
2. [Package Dependency Graph](#2-package-dependency-graph)
3. [UI Component Tree](#3-ui-component-tree)
4. [Chat & AI Tool Use Flow](#4-chat--ai-tool-use-flow)
5. [File & Git Operation Flow](#5-file--git-operation-flow)
6. [Terminal Data Flow](#6-terminal-data-flow)
7. [Desktop App Startup Sequence](#7-desktop-app-startup-sequence)
8. [Session & Database Model](#8-session--database-model)
9. [Build Pipeline](#9-build-pipeline)
10. [CI/CD Pipeline](#10-cicd-pipeline)
11. [Tech Stack](#11-tech-stack)
12. [Implementation Status](#12-implementation-status)
13. [Roadmap](#13-roadmap)
14. [Key Design Decisions](#14-key-design-decisions)

---

## 1. High-Level System Architecture

```mermaid
graph TB
    subgraph Desktop["🖥️ Desktop Shell — Tauri 2 (Rust container)"]
        subgraph UI["Web Frontend — React 18 + Vite + TypeScript"]
            Editor["Engine Editor\nFirst-party editor core"]
            Chat["AI Chat Panel"]
            FileTree["File Tree"]
            Terminal["Terminal (xterm.js)"]
            AgentPanel["Agent Monitor"]
            StatusBar["Status Bar"]
            Store["Zustand Store\nsessions · messages · files · git"]
            WSClient["WebSocket Client\n+ Platform Bridge"]
        end
        TauriIPC["Tauri IPC (Rust)\nopen_folder · tokens · window_*"]
    end

    subgraph Server["⚙️ Go Server Sidecar — engine-server :3000"]
        WSHub["WS Hub + Router\nws/handler.go"]
        DB["SQLite\ndb/db.go"]
        AI["AI Context\nai/context.go"]
        FS["File System\nfs/fs.go"]
        Git["Git + GitHub\ngit/git.go"]
        PTY["Terminal PTY\nterminal/manager.go"]
    end

    subgraph External["🌐 External"]
        Anthropic["Anthropic API\n(Claude)"]
        OpenAI["OpenAI API\n(GPT)"]
        GitHub["GitHub API\nIssues · PRs · Repos"]
        HostFS["Host File System\n+ Git Repo"]
    end

    Editor --> Store
    Chat --> Store
    FileTree --> Store
    Terminal --> Store
    AgentPanel --> Store
    StatusBar --> Store
    Store --> WSClient
    WSClient -->|"ws://localhost:3000/ws"| WSHub
    WSClient <-->|"Tauri IPC invoke()"| TauriIPC
    TauriIPC -->|"spawns & manages"| WSHub

    WSHub --> DB
    WSHub --> AI
    WSHub --> FS
    WSHub --> Git
    WSHub --> PTY

    AI --> Anthropic
    AI --> OpenAI
    Git --> GitHub
    FS --> HostFS
    PTY --> HostFS
```

---

## 2. Package Dependency Graph

```mermaid
graph LR
    subgraph Workspace["pnpm Workspace"]
        Shared["@engine/shared\nTypeScript types only\npackages/shared/"]
        Client["@engine/client\nReact + Vite\npackages/client/"]
        GoServer["engine-server\nGo binary\npackages/server-go/"]
        Tauri["desktop-tauri\nRust + Tauri 2\npackages/desktop-tauri/"]
    end

    Shared -->|"workspace:*"| Client
    Client -->|"built dist/ bundled into"| Tauri
    GoServer -->|"binary resource bundled into"| Tauri

    Scripts["scripts/\nbuild-go.mjs\nrun-cargo.mjs\nci-system-smoke.mjs"]
    Scripts -->|"builds"| GoServer
    Scripts -->|"runs"| Tauri
```

---

## 3. UI Component Tree

```mermaid
graph TD
    main["main.tsx\nReact entry"] --> App

    App["App.tsx\nRoot layout"] --> FileTree
    App --> EditorPanel
    App --> ChatPanel
    App --> StatusBar

    EditorPanel["Editor Panel"] --> Editor["Editor.tsx\nEngine Editor (first-party)\n• single editor surface\n• large-file path\n• multi-tab\n• dirty tracking"]
    EditorPanel --> TerminalComp["Terminal.tsx\nxterm.js\n• PTY over WS\n• resize support"]

    ChatPanel["Chat Panel"] --> AIChat["AIChat.tsx\n• message history\n• streaming display\n• tool call log\n• session selector\n• input composer"]
    ChatPanel --> AgentPanel["AgentPanel.tsx\n• live tool stream\n• active sessions\n• duration per call"]

    FileTree["FileTree.tsx\n• dir listing\n• file open\n• new / delete"]
    StatusBar["StatusBar.tsx\n• branch\n• connection\n• model"]

    App --> Store

    Store["Zustand Store\nstore/index.ts"] --> WSClient
    WSClient["ws/client.ts\nWebSocket protocol"] --> Bridge
    Bridge["bridge.ts\nPlatform abstraction\nTauri | Electron | Web"]
```

---

## 4. Chat & AI Tool Use Flow

```mermaid
sequenceDiagram
    actor User
    participant Chat as AIChat.tsx
    participant Store as Zustand Store
    participant WS as WS Client
    participant Hub as WS Handler (Go)
    participant DB as SQLite
    participant AI as ai/context.go
    participant LLM as Anthropic / OpenAI
    participant Tools as Tool Dispatch

    User->>Chat: types message
    Chat->>Store: add pending message
    Chat->>WS: send {type:"chat", sessionId, content}
    WS->>Hub: JSON over WebSocket

    Hub->>DB: load session + all prior messages
    DB-->>Hub: Session, Message[], ToolCall[]
    Hub->>AI: buildContext(messages, projectPath, gitStatus)
    AI->>LLM: POST /messages (full history + tools)

    loop Streaming response
        LLM-->>AI: stream token / tool_use event
        alt text token
            AI-->>Hub: stream chunk
            Hub-->>WS: {type:"stream", token}
            WS-->>Chat: append token to message
        else tool_call
            AI-->>Hub: tool call {name, input}
            Hub->>Tools: dispatch tool
            alt fs tool
                Tools->>Tools: fs/fs.go (read/write/search)
            else git tool
                Tools->>Tools: git/git.go (status/commit/push)
            else terminal tool
                Tools->>Tools: terminal/manager.go (run cmd)
            else github tool
                Tools->>Tools: git/git.go → GitHub API
            end
            Tools-->>Hub: result
            Hub->>DB: store tool_call record
            Hub->>AI: inject tool result
            Hub-->>WS: {type:"tool_result", name, result}
            WS-->>Chat: render tool call + result inline
        end
    end

    AI-->>Hub: final response complete
    Hub->>DB: store assistant message
    Hub-->>WS: {type:"done"}
    WS-->>Store: mark message complete
    Chat-->>User: full response rendered
```

---

## 5. File & Git Operation Flow

```mermaid
flowchart TD
    A1["User clicks file in FileTree"] -->|WS: list_files / read_file| FSGo
    A2["User saves in Editor"] -->|WS: write_file| FSGo
    A3["AI tool call: read_file"] -->|WS: read_file| FSGo
    A4["AI tool call: write_file"] -->|WS: write_file| FSGo
    A5["AI tool call: search_files"] -->|WS: search_files| FSGo

    FSGo["fs/fs.go\nReadDir · ReadFile\nWriteFile · SearchFiles"]
    FSGo -->|"reads / writes"| HostFS["Host File System"]
    FSGo -->|"FileNode[] / content"| Hub

    B1["Status bar refresh"] -->|WS: git_status| GitGo
    B2["AI tool call: git_status"] -->|WS: git_status| GitGo
    B3["AI tool call: git_commit"] -->|WS: git_commit| GitGo
    B4["AI tool call: github_issues"] -->|WS: github_issues| GitGo

    GitGo["git/git.go\ngit status · commit\npush · GitHub API"]
    GitGo -->|"shell out to git"| GitRepo["Git Repository"]
    GitGo -->|"HTTPS"| GitHub["GitHub API"]
    GitGo -->|"GitStatus / Issue[]"| Hub

    Hub["WS Handler\nroutes results"] --> WSClient["WS Client"]
    WSClient --> Store["Zustand Store"]
    Store --> FileTree["FileTree re-renders"]
    Store --> EditorTab["Editor tab state"]
    Store --> StatusBar["StatusBar updates"]
```

---

## 6. Terminal Data Flow

```mermaid
sequenceDiagram
    participant User
    participant xterm as xterm.js (Terminal.tsx)
    participant WS as WS Client
    participant Hub as WS Handler
    participant PTY as terminal/manager.go
    participant Shell as PTY Shell Process

    User->>xterm: opens terminal panel
    xterm->>WS: {type:"terminal_create", cols, rows}
    WS->>Hub: route message
    Hub->>PTY: CreateSession(cols, rows)
    PTY->>Shell: spawn /bin/bash (PTY)
    PTY-->>Hub: terminalId
    Hub-->>WS: {type:"terminal_created", id}
    WS-->>xterm: store terminalId

    loop User types
        User->>xterm: keypress
        xterm->>WS: {type:"terminal_input", id, data}
        WS->>Hub: route
        Hub->>PTY: Write(id, data)
        PTY->>Shell: stdin write
    end

    loop Shell output
        Shell->>PTY: stdout / stderr
        PTY->>Hub: ReadOutput(id)
        Hub->>WS: {type:"terminal_output", id, data}
        WS->>xterm: write(data)
        xterm-->>User: rendered output
    end

    Note over Hub,PTY: AI uses same path via run_terminal tool
    Note over Hub,PTY: AI reads output → observes → fixes → reruns
```

---

## 7. Desktop App Startup Sequence

```mermaid
sequenceDiagram
    actor User
    participant OS as macOS / Windows / Linux
    participant Tauri as Tauri Runtime (lib.rs)
    participant GoServer as engine-server (Go)
    participant React as React App
    participant WSHub as WS Hub

    User->>OS: launch Engine.app
    OS->>Tauri: start process

    Tauri->>Tauri: read ~/.engine/config\n(tokens, last project path, model)
    Tauri->>GoServer: spawn resources/engine-server\nenv: PROJECT_PATH, PORT=3000
    GoServer->>GoServer: init SQLite (~/.engine/state.db)
    GoServer->>WSHub: start WebSocket listener :3000
    GoServer-->>Tauri: ready signal

    Tauri->>React: load frontendDist/index.html
    React->>React: boot main.tsx → App.tsx
    React->>WSHub: connect ws://localhost:3000/ws

    React->>WSHub: list_sessions
    WSHub-->>React: Session[]
    React->>WSHub: git_status
    WSHub-->>React: GitStatus
    React->>WSHub: list_files (project root)
    WSHub-->>React: FileNode[]

    React-->>User: UI ready\nproject open, sessions loaded, AI ready
```

---

## 8. Session & Database Model

```mermaid
erDiagram
    sessions {
        string id PK "UUID"
        string projectPath
        string branchName
        string summary "AI-generated (planned)"
        int messageCount
        datetime createdAt
        datetime updatedAt
    }

    messages {
        string id PK "UUID"
        string sessionId FK
        string role "user | assistant"
        text content
        datetime createdAt
    }

    tool_calls {
        string id PK "UUID"
        string messageId FK
        string name "read_file | git_status | etc"
        json input
        json result
        boolean isError
        int durationMs
    }

    sessions ||--o{ messages : "has many"
    messages ||--o{ tool_calls : "triggered"
```

---

## 9. Build Pipeline

```mermaid
flowchart TD
    subgraph Dev["pnpm dev (no Tauri)"]
        D1["build-go.mjs --dev --run\n→ engine-server :3000"]
        D2["pnpm --filter client dev\n→ Vite HMR :5173"]
        D1 & D2 -->|concurrently| D3["Dev environment ready"]
    end

    subgraph DevTauri["pnpm dev:tauri"]
        T1["build-go.mjs (sidecar)"]
        T2["client dev :5173"]
        T3["run-cargo.mjs tauri dev\nloads devUrl: localhost:5173"]
        T1 & T2 --> T3
    end

    subgraph Prod["pnpm build:tauri (production)"]
        P1["pnpm --filter shared build\ntsc → shared/dist/"]
        P2["build-go.mjs\ngo build → engine-server binary"]
        P3["pnpm --filter client build\nvite build → client/dist/"]
        P4["run-cargo.mjs tauri build\nbundles everything"]
        P5["Output: .app / .exe / .deb / .AppImage\n(engine-server embedded as resource)"]

        P1 --> P2
        P2 --> P3
        P3 --> P4
        P4 --> P5
    end

    subgraph Smoke["pnpm smoke:system"]
        S1["ci-system-smoke.mjs"]
        S2["Launch built binary"]
        S3["Wait for server ready"]
        S4["Test WS connection"]
        S5["Test basic operations"]
        S6["Assert responses"]
        S7["Cleanup"]
        S1 --> S2 --> S3 --> S4 --> S5 --> S6 --> S7
    end
```

---

## 10. CI/CD Pipeline

```mermaid
flowchart TD
    Push["Push to main / dev\nor Pull Request"] --> Trigger

    Trigger["GitHub Actions\ncross-platform-validation.yml"]

    Trigger --> Ubuntu["ubuntu-latest"]
    Trigger --> Mac["macos-latest"]
    Trigger --> Win["windows-latest"]

    Ubuntu & Mac & Win --> Steps

    subgraph Steps["Each OS runs:"]
        direction TB
        C1["1. pnpm install"]
        C2["2. pnpm typecheck"]
        C3["3. pnpm build (web client)"]
        C4["4. go test ./..."]
        C5["5. go build ./... (server binary)"]
        C6["6. cargo tauri check"]
        C7["7. cargo tauri build --debug"]
        C8["8. pnpm smoke:system"]
        C1 --> C2 --> C3 --> C4 --> C5 --> C6 --> C7 --> C8
    end
```

---

## 11. Tech Stack

| Layer | Technology | Version | Why |
|---|---|---|---|
| Frontend framework | React | 18.3.1 | Component model for complex UI |
| Build tool | Vite | 6.2.6 | Fast HMR, native ESM |
| Language (frontend) | TypeScript | 5.8.0 | Type safety across WS protocol |
| State management | Zustand | 5.0.3 | No boilerplate, simple, scalable |
| Code editor component | Engine editor core | First-party | Single editor surface tuned directly for Engine and large-file control |
| Terminal component | xterm.js | 5.5.0 | Battle-tested PTY terminal |
| Styling | Tailwind CSS | 3.4.17 | Utility-first, no CSS file sprawl |
| Icons | lucide-react | 0.487.0 | Clean SVG icon set |
| Backend language | Go | 1.26.1 | Concurrency, fast startup, single binary |
| WebSocket server | gorilla/websocket | 1.5.3 | Proven WS library for Go |
| Database | SQLite (modernc) | 1.48.1 | Embedded, zero ops, persistent sessions |
| PTY | creack/pty | 1.1.24 | PTY creation for Unix/macOS |
| Desktop framework | Tauri | 2 | Rust shell, tiny bundle, no Node runtime |
| Desktop language | Rust | stable | Memory safe, Tauri ecosystem |
| AI APIs | Anthropic + OpenAI | — | Multi-provider (Claude + GPT) |
| Package manager | pnpm | 10.28.1 | Fast, workspace support |

---

## 12. Implementation Status

```mermaid
%%{init: {"themeVariables": {"fontSize": "14px"}}}%%
pie title Feature Completion
    "Done / Working" : 18
    "Partial / In Progress" : 6
    "Not Yet Built" : 8
```

### ✅ Done / Working

| Feature | Package |
|---|---|
| WebSocket server hub + message routing | `server-go/ws` |
| SQLite sessions / messages / tool_calls | `server-go/db` |
| File system read / write / list / search | `server-go/fs` |
| Git status, branch, commit info | `server-go/git` |
| Terminal PTY (Unix + Windows) | `server-go/terminal` |
| Anthropic (Claude) streaming + tool use | `server-go/ai` |
| OpenAI (GPT) streaming + tool use | `server-go/ai` |
| React UI scaffold + layout | `client` |
| First-party editor core (single text surface, large-file path) | `client/Editor` |
| xterm.js terminal (PTY over WS) | `client/Terminal` |
| File tree (listing + open) | `client/FileTree` |
| AI chat (history + streaming) | `client/AIChat` |
| Agent monitor (live tool call view) | `client/AgentPanel` |
| Zustand store (all UI state) | `client/store` |
| Platform bridge (Tauri + web) | `client/bridge` |
| Shared TypeScript type definitions | `shared` |
| Tauri IPC / server lifecycle / window mgmt | `desktop-tauri` |
| CI matrix (Ubuntu / macOS / Windows) | `.github/workflows` |

### 🔶 Partial / In Progress

| Feature | Gap |
|---|---|
| Git commit / push / pull UI flow | Needs end-to-end testing |
| GitHub Issues integration | API exists in Go; no live issue → AI trigger loop |
| Session summary auto-generation | Schema has `summary` field; not yet generated by AI |
| Agent orchestration | AgentPanel renders; multi-agent dispatch not wired |
| Error recovery / reconnect | Basic handling; edge cases not covered |
| Mobile / remote access | Web client works in theory; not hardened |

### ❌ Not Yet Built

| Feature | Priority |
|---|---|
| Project direction summarization (auto-maintained across sessions) | 🔴 HIGH |
| Live GitHub issue → AI notification + task pickup | 🔴 HIGH |
| Multi-agent orchestration (coordinator + workers) | 🔴 HIGH |
| Behavioral validation loop (AI runs app, observes, fixes, reruns) | 🔴 HIGH |
| Settings UI (model picker, token management in-app) | 🟡 MEDIUM |
| Authentication for remote access | 🟡 MEDIUM |
| Mobile-responsive UI layout | 🟡 MEDIUM |
| Extension / plugin system | 🟢 LOW |

---

## 13. Roadmap

```mermaid
gantt
    title Engine Development Roadmap
    dateFormat  YYYY-MM
    axisFormat  %b %Y

    section Phase 1 — Core Reliability
    WS error recovery + reconnect          :p1a, 2026-04, 2w
    Full git flow tested                   :p1b, after p1a, 2w
    Session summary auto-generation        :p1c, after p1b, 2w
    Project direction tracking in DB       :p1d, after p1c, 2w
    Settings panel (model, tokens)         :p1e, after p1d, 2w

    section Phase 2 — AI Intelligence
    Cross-session context injection        :p2a, after p1e, 3w
    GitHub issue live monitoring           :p2b, after p2a, 3w
    CI failure awareness + AI trigger      :p2c, after p2b, 2w
    Project direction auto-summarization   :p2d, after p2c, 3w

    section Phase 3 — Agent Orchestration
    Multi-agent session management         :p3a, after p2d, 4w
    Behavioral validation loop             :p3b, after p3a, 4w
    Work queue (issues + CI → tasks)       :p3c, after p3b, 3w

    section Phase 4 — Universal Access
    Remote server mode + HTTPS             :p4a, after p3c, 3w
    Authentication layer                   :p4b, after p4a, 2w
    Mobile-responsive client               :p4c, after p4b, 3w
```

---

## 14. Key Design Decisions

```mermaid
mindmap
  root((Engine))
    Go Server
      Single binary → easy Tauri bundle
      Goroutines → concurrent WS + PTY
      modernc SQLite → no CGO required
      Cross-compile → Windows / macOS / Linux
    Tauri not Electron
      Shell only — not the editor core
      10-50x smaller binary
      No bundled Node.js
      Rust memory safety
      Native OS dialogs + file picker
    WebSocket not REST
      Streaming AI tokens require it
      Terminal I/O is bidirectional
      Single connection for everything
      Real-time updates push from server
    SQLite not PostgreSQL
      Zero ops — embedded
      Travels with the project
      Fast enough for sessions + messages
    First-party Editor Core
      Single editor surface across file sizes
      Large-file behavior under our control
      Product-specific tuning over package defaults
    Zustand not Redux
      No boilerplate
      Natural with React hooks
      Sufficient at this scale
```

---

*Source of truth: this document + code. When they conflict, code wins.*
