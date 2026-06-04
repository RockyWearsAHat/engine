# packages/client - Engine React UI

Client-side React application built with Vite, TypeScript, and Tailwind CSS.

## Core Modules

### Platform Bridge (`src/bridge.ts`)
Unified abstraction for Electron IPC, Tauri commands, and web APIs. Ensures platform-agnostic client code.

### Connection Profiles (`src/connectionProfiles.ts`)
Manages remote server connections.

**Exported Types:**
- `ConnectionProfile` — remote server connection metadata

### Editor Preferences (`src/editorPreferences.ts`)
Editor configuration management.

**Exported Types:**
- `EditorPreferences` — editor settings and configuration
- `MarkdownViewMode` — markdown display mode type

### Editor Events (`src/editorEvents.ts`)
Custom DOM events for editor state changes and synchronization.

**Exported Types:**
- `EditorStatusDetail` — editor status with current file info
- `CloseFileEventDetail` — file close event
- `SaveFilesEventDetail` — file save event
- `RevealFileLocationDetail` — file location navigation

### WebSocket Client (`src/ws/client.ts`)
WebSocket connection to Engine Go server.

**Exported Types:**
- `RemoteConfig` — remote server connection configuration

## Architecture

The client is organized into:
- `src/components/` — Feature components (AI chat, file tree, editor, terminal, etc.)
- `src/store/` — Zustand global state management
- `src/ws/` — WebSocket client and message handling
- `src/test/` — Vitest integration and unit tests (100% coverage enforced)

All state is synchronized with the Go server via WebSocket. The server handles project metadata, session persistence, file I/O, and AI provider routing.

## Testing

```bash
pnpm --filter @engine/client test:run
pnpm --filter @engine/client test:coverage
```

100% code coverage is enforced by the completion gate.
