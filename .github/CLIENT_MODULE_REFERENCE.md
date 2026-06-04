# Client Module Type Reference

Documentation for public interfaces and types exported from Engine client packages.

## packages/client/src — Core Client Types

### bridge.ts
Platform abstraction for Electron, Tauri, and web APIs.

### connectionProfiles.ts
- `ConnectionProfile` — remote server connection (host, port, token, workspace path)

### editorPreferences.ts
- `EditorPreferences` — editor configuration (font, size, line height, tab size, word wrap)
- `MarkdownViewMode` — markdown view mode type ('text' | 'preview' | 'split' | 'syntactical')

### editorEvents.ts
Custom DOM events for editor state changes:
- `EditorStatusDetail` — current file status and editor state
- `CloseFileEventDetail` — file close event details
- `SaveFilesEventDetail` — file save event details  
- `RevealFileLocationDetail` — file location reveal details

## packages/client/src/ws — WebSocket Client Types

### client.ts
- `RemoteConfig` — remote server configuration (host, port, token)

## packages/shared/src — Shared Types

TypeScript types shared across client-server boundaries.

### Core Domain Types
- `Session` — project session (id, projectPath, branchName, createdAt, updatedAt, projectDirection, summary, messageCount)
- `Message` — chat message (id, sessionId, role, content, toolCalls, createdAt)
- `ToolCall` — AI tool invocation (id, name, input, result, isError)

### File System Types
- `FileNode` — file tree node (name, path, type, children, loaded, hasChildren, size, modified)
- `FileContent` — file buffer (path, content, language, size)

### Request & Search Types
- `ApprovalRequest` — user approval request (id, sessionId, kind, title, message, command)
- `SearchResult` — code search result (path, line, column, preview)

### Configuration Types
- `RuntimeConfig` — merged environment and storage config (githubToken, anthropicKey, openaiKey, modelProvider, model, clonesDir, contextMaxTokens, etc.)
