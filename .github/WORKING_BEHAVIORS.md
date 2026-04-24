# Working Behaviors (Executive Behavioral Contract)

Big rule: this file is deal between user and agent.
If code and this file fight, we fix code or fix file in same work session.

## Agreement

- User and agent agree this file is truth for expected runtime behavior.
- Agent must read this file before big test work or behavior refactor.
- Agent must tell user every time this file changes.
- If new behavior is not finished yet, mark line with (IN PROGRESS).
- When behavior is tested and done, remove (IN PROGRESS).

## Test Style Agreement

- One tiny smoke test per UI surface.
- Most tests must check real behavior:
  - key/mouse actions
  - websocket messages
  - store state changes
  - persisted bridge/config writes
  - emitted events and command dispatch
- No brittle copy/layout tests unless text is contract.

## Coverage Contract (Current)

- Client coverage gate comes from [packages/client/vitest.config.ts](packages/client/vitest.config.ts):
  - statements >= 100
  - branches >= 85
  - functions >= 100
  - lines >= 100
- Go coverage is tracked and reported each run.
- Go coverage target for current gate pass: >= 20.

## Behavior Contract By Surface

### App Shell

- Cmd/Ctrl+P opens command palette in file mode.
- Cmd/Ctrl+Shift+P opens command palette in command mode.
- Cmd/Ctrl+, opens preferences/settings.
- websocket approval.request shows approval modal.
- approval modal buttons send approval.respond allow true/false.

### AI Chat

- Send pushes chat websocket payload and local user message.
- Cmd/Ctrl+Enter sends chat input.
- While streaming, stop control appears and sends chat.stop.
- Failed assistant response can retry from latest user message.
- Tool badge can expand and show input/result payload.

### Agent Panel

- New session button sends session.create for active project.
- Clicking session sets active session and sends session.load.
- Streaming state shows current activity and recent tool calls.

### File Tree

- Search Enter sends file.search with query and active root.
- Search panel shows loading/error/results from store.
- Open editors section lives inside explorer surface.

### Preferences

- Tabs change active preferences section.
- Editor appearance changes persist with bridge.setEditorPreferences.
- Discord websocket messages hydrate and validate discord form state.

### Status Bar

- Listens to EDITOR_STATUS_EVENT for language/size/location.
- Markdown mode menu persists selection via bridge.

### Terminal

- Plus button creates terminal in active workspace cwd.
- Command launch creates terminal then sends command.
- websocket terminal output writes into active xterm instance.
- Close tab sends terminal close for that terminal id.

### Markdown Preview

- External links use bridge openExternal.
- Internal links stay local.
- Inline code and fenced code blocks render highlighted.
- Syntactical preview adds syntax annotations for heading/link/inline-code/code-block.

### Connections

- Pair-and-save requires host and workspace path.
- Pair code flow: pairConnectionCode then saveConnectionProfile.
- Forget all clears profiles and reloads.

## Delta Log

### 2026-04-23

- Rewrote whole contract in simple caveman English.
- Kept behavior list concrete and testable.
- Made agreement explicit: user + agent both follow this file.
