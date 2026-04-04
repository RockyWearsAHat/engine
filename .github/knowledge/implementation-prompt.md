# MyEditor — Full Implementation Prompt

**Copy and paste this entire prompt to a fresh agent session to implement MyEditor.**

---

## Context

You are implementing **MyEditor** — an AI-native code editor where the AI *is* the interface. This is not VS Code with a chat panel bolted on. The AI controls the editor entirely. Users talk to the AI; the AI does everything.

The project already has a working foundation. Your job is to extend it to full capability. Read this entire prompt before touching a single file.

### What already exists (do not re-implement):
- `packages/server/src/ai/context.ts` — Claude API chat loop with tool use
- `packages/server/src/fs/index.ts` — read_file, write_file, list_directory, search_files
- `packages/server/src/git/index.ts` — git_status, git_diff, git_log, git_commit
- `packages/server/src/terminal/manager.ts` — PTY terminal sessions via node-pty
- `packages/server/src/db/index.ts` — SQLite persistence (sessions, messages, tool_log)
- `packages/server/src/ws/handler.ts` — WebSocket server routing all client messages
- `packages/shared/src/index.ts` — all TypeScript types shared between client/server
- `packages/client/src/` — React UI with Editor, FileTree, AIChat, Terminal, StatusBar, AgentPanel components
- `packages/desktop/src/` — Electron wrapper

### The master tool manifest:
`.github/knowledge/tool-manifest.md` — read this file first. It lists all ~113 tools with implementation status. Implement in the priority order defined in Phase 1 → Phase 2 → Phase 3 of that document.

---

## Architecture Rules

These are non-negotiable. Read them before writing any code.

### 1. Tools live in `packages/server/src/tools/`
Each tool category gets its own file:
- `packages/server/src/tools/fs.ts` — all filesystem tools
- `packages/server/src/tools/git.ts` — all git tools  
- `packages/server/src/tools/shell.ts` — shell + process management
- `packages/server/src/tools/session.ts` — session memory + project direction
- `packages/server/src/tools/github.ts` — GitHub API tools
- `packages/server/src/tools/ai.ts` — AI orchestration tools (subagent, summarize)
- `packages/server/src/tools/web.ts` — web fetch + search
- `packages/server/src/tools/testing.ts` — test/build/lint runners
- `packages/server/src/tools/registry.ts` — central registry (update this with every new tool)

The existing `packages/server/src/ai/context.ts` `TOOLS` array and `executeTool` switch should be refactored to import from these modules. Do not keep all tools as inline definitions in one 400-line file.

### 2. All tools return a typed result
Every tool implementation returns `{ result: string | object; isError: boolean }`. The result is either a human-readable string summary or a structured JSON object. Never return raw binary or unbounded data.

### 3. Workspace confinement is enforced at the tool layer
`packages/server/src/tools/sandbox.ts` must contain a `assertWithinWorkspace(path, projectRoot)` function. Every filesystem and shell tool calls this. Violations throw a typed `WorkspaceEscapeError` that surfaces to the AI as `isError: true`.

### 4. Dangerous tools require approval
Add a `requiresApproval: boolean` flag to `ToolMeta` in the registry. On the server, tool calls with `requiresApproval: true` must pause the AI, send a `{ type: 'tool.approval_required', toolName, input, approvalId }` message to the client, and wait for a `{ type: 'tool.approve', approvalId }` or `{ type: 'tool.deny', approvalId }` before proceeding.

Dangerous tools: `shell` (destructive commands), `git_push`, `git_reset`, `git_branch_delete`, `process_kill`, `delete_file`, `move_file` (outside workspace), `github_issues_create`.

### 5. Websocket protocol must be extended for every new capability
All new tool results and async events must have corresponding entries in `packages/shared/src/index.ts` `ServerMessage` and `ClientMessage` union types. Shared types are the contract — the client and server are fully typed against them.

### 6. The AI system prompt must reference all tools
`packages/server/src/ai/context.ts` has a system prompt. It must list every registered tool, give guidance on when to use it, and include the project direction + session summary in every message. The system prompt is not static text — it's built dynamically at the start of each chat call from live data.

### 7. Never swallow errors
All tool catch blocks must return `{ result: errorMessage, isError: true }`. The AI sees the error message and can reason about it. Log all tool errors to `tool_log` in the database with `is_error = 1`.

---

## Phase 1 Implementation — Core Agent Capability

Implement these in order. Each is a blocker for the next phase.

### 1.1 Upgrade `read_file` to support line ranges

**File:** `packages/server/src/tools/fs.ts`

Add `startLine?: number` and `endLine?: number` to the tool input schema. When provided, slice the file content. Return the sliced content with a header `// Lines N-M of /path/to/file`. Hard error on files over 2MB unless a range is specified:

```
{ result: "File too large (8.4MB). Specify startLine/endLine to read a range.", isError: true }
```

Hard error on binary files (detect by extension from the existing `BINARY_EXTENSIONS` set in `fs/index.ts`):
```
{ result: "Binary file — cannot read as text.", isError: true }
```

### 1.2 Add `find_files` tool

**File:** `packages/server/src/tools/fs.ts`

```typescript
// Input: { pattern: string, directory?: string, maxResults?: number }
// Uses: glob matching against the file tree, or ripgrep --files with -g pattern
// Returns: array of matching paths relative to project root
// Example: find_files({ pattern: "**/*.test.ts" }) → ["src/utils.test.ts", ...]
```

Use ripgrep's `--files -g <pattern>` mode for performance. Respect the IGNORED directories.

### 1.3 Add `get_diagnostics` tool

**File:** `packages/server/src/tools/testing.ts`

This is the equivalent of VS Code's Problems panel. Run the project's type checker and linter programmatically and return structured diagnostics:

```typescript
interface Diagnostic {
  file: string;      // relative path
  line: number;
  column: number;
  severity: 'error' | 'warning' | 'info';
  message: string;
  source: string;    // 'tsc' | 'eslint' | 'ruff' | 'clippy' | etc.
  code?: string;
}
```

Detection logic:
1. If `tsconfig.json` exists → run `npx tsc --noEmit --pretty false 2>&1` and parse output
2. If `.eslintrc.*` or `eslint.config.*` exists → run `npx eslint . --format json 2>&1` and parse
3. If `pyproject.toml` or `setup.py` → run `ruff check --output-format json 2>&1`
4. If `Cargo.toml` → run `cargo check --message-format json 2>&1`

Parse each tool's output format into the `Diagnostic` array. Return as structured JSON.

### 1.4 Add `shell_long` tool

**File:** `packages/server/src/tools/shell.ts`

Like `shell` but:
- Timeout: up to 600 seconds (configurable via `timeoutMs` input param, default 120s)
- Max output buffer: 16MB
- Streams output chunks to the client via `{ type: 'tool.stream_chunk', toolCallId, chunk }` WebSocket messages
- Returns a `processId` immediately, client subscribes to stream events
- Has a companion `get_process_output(processId)` to fetch full buffered output at any time

### 1.5 Add `background_process` + `process_status` + `process_kill`

**File:** `packages/server/src/tools/shell.ts`

```typescript
// background_process: Start a long-running process, return a process ID
// Input: { command: string, cwd?: string, env?: Record<string, string>, label?: string }
// Output: { processId: string, pid: number, label: string }

// process_status: Check if a process is still running
// Input: { processId: string }
// Output: { processId, pid, label, running: boolean, exitCode?: number, recentOutput: string }

// process_kill: Kill a process
// Input: { processId: string, signal?: 'SIGTERM' | 'SIGKILL' }
// Output: { killed: boolean, message: string }
```

Store process metadata in a `Map<string, ProcessEntry>` in memory (not SQLite — processes don't persist across restarts). Label defaults to the first word of the command.

### 1.6 Add `git_checkpoint` tool

**File:** `packages/server/src/tools/git.ts`

Port the gsh `git checkpoint` behavior:
1. Run `git diff HEAD --stat` to get the change summary
2. Run `git diff HEAD` to get the full diff (truncated to 8KB for the AI prompt)
3. Call the AI model to generate a commit message from the diff (one-shot, no conversation history)
4. Run `git add -A && git commit -m "<generated_message>"`
5. Return the commit hash and message

The AI call for message generation should use a fast/cheap model (`claude-haiku-4-5` or equivalent). The commit message format: conventional commits style (`feat:`, `fix:`, `refactor:`, etc.).

### 1.7 Add Session Event Log (Engram learning loop)

**File:** `packages/server/src/tools/session.ts` + `packages/server/src/db/index.ts`

Add a new table to SQLite:
```sql
CREATE TABLE IF NOT EXISTS session_events (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  action TEXT NOT NULL,
  outcome TEXT NOT NULL DEFAULT '',
  surprise REAL NOT NULL DEFAULT 0.0,
  model TEXT NOT NULL DEFAULT '',
  tags TEXT NOT NULL DEFAULT '[]',  -- JSON array
  context TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_session ON session_events(session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_events_surprise ON session_events(surprise DESC);
```

Implement:
- `session_log_event({ action, outcome, surprise, model, tags, context })` — insert a row
- `session_search_log({ query, current_model?, limit? })` — TF-IDF search over `action + context` fields, boost same-model matches by 1.3x, boost high-surprise by `1 + surprise`, return top N results

TF-IDF implementation: build term vectors from `action + " " + context` text. Use cosine similarity for ranking. This can be a lightweight pure-TS implementation — does not need a vector database.

### 1.8 Add Project Direction Storage

**File:** `packages/server/src/tools/session.ts` + `packages/server/src/db/index.ts`

Add a `project_config` key-value table:
```sql
CREATE TABLE IF NOT EXISTS project_config (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
```

Implement:
- `session_set_direction({ direction: string })` — upsert `key = 'project_direction'`
- `session_get_direction()` — return the stored direction string, empty string if not set

The direction is included in the system prompt for every chat call.

### 1.9 Add `tool_approval` system

**File:** `packages/server/src/tools/approval.ts` + `packages/server/src/ws/handler.ts`

When a tool has `requiresApproval: true`, the execution pipeline:
1. Sends `{ type: 'tool.approval_required', approvalId, toolName, input, riskReason }` to the client
2. Stores a `Promise` resolver keyed by `approvalId` in a `Map`
3. When client sends `{ type: 'tool.approve', approvalId }` or `{ type: 'tool.deny', approvalId }`, resolves/rejects the promise
4. Timeout after 60 seconds (treat as deny)

The React client must show a modal/banner for approval requests with the tool name, inputs, and risk reason.

---

## Phase 2 Implementation — Feature Parity with Copilot

Implement after Phase 1 is fully working and tested.

### 2.1 Complete Git tooling

**File:** `packages/server/src/tools/git.ts`

Add all missing git tools from the manifest: `git_stage`, `git_unstage`, `git_checkout_file`, `git_branch_list`, `git_branch_create`, `git_branch_switch`, `git_branch_delete`, `git_push`, `git_pull`, `git_fetch`, `git_stash`, `git_stash_pop`, `git_show`, `git_blame`, `git_conflict_list`.

Use `simple-git` for all git operations (already installed). Add `git_remote_list` to expose remote URLs.

### 2.2 Test/Build/Lint runners

**File:** `packages/server/src/tools/testing.ts`

`detect_project_type`:
- Read `package.json`, `Cargo.toml`, `pyproject.toml`, `go.mod`, `*.csproj`
- Return: `{ language, framework, packageManager, testRunner, buildTool, devCommand, testCommand, buildCommand, lintCommand }`
- Cache result per project path in SQLite `project_config`

`run_tests`:
- Auto-detect test command from `detect_project_type`
- Run with `shell_long`, parse output for structured results
- Return: `{ passed: number, failed: number, skipped: number, total: number, failures: TestFailure[] }`
- Where `TestFailure = { name: string, file: string, line: number, message: string, stackTrace: string }`

`run_build`:
- Auto-detect build command
- Return structured build errors (reuse `Diagnostic` type from `get_diagnostics`)

`run_lint`:
- Run the project's configured linter
- Return structured `Diagnostic[]`

### 2.3 AI Subagent

**File:** `packages/server/src/tools/ai.ts`

`ai_subagent({ systemPrompt, tools, message, model? })`:
- Creates a new isolated `ChatContext` with a custom system prompt and tool subset
- Runs a single-turn or multi-turn conversation inside a sub-loop
- Returns the final assistant message text
- The parent agent can read results and continue its own loop

Tool subset for subagents is expressed as an array of tool names. The subagent only has access to the listed tools.

### 2.4 GitHub tools expansion

**File:** `packages/server/src/tools/github.ts`

Refactor the existing GitHub issues fetch into a proper tool. Add:
- `github_issues_get(number)` — fetch single issue with comments thread
- `github_issues_create(title, body, labels[])` — create issue (requires approval)
- `github_issues_comment(number, body)` — post comment (requires approval)
- `github_prs_list()` — list open PRs
- `github_prs_get(number)` — full PR details
- `github_actions_list()` — recent workflow runs

All GitHub API calls use `GITHUB_TOKEN` env var if set, otherwise unauthenticated (60 req/hr limit). Factor the HTTP client into a shared `githubFetch(path, options?)` helper that handles auth headers, rate limit headers, and error responses.

### 2.5 Web fetch + search

**File:** `packages/server/src/tools/web.ts`

`web_fetch({ url, selector? })`:
- Fetch URL using the built-in `fetch` API
- Strip HTML tags, boilerplate (nav, footer, ads), return clean readable text
- Optional CSS selector to extract a specific section
- Limit output to 50KB
- User-Agent: `MyEditor/1.0 (AI coding assistant)`

`web_search({ query, numResults? })`:
- Call SearXNG at `http://localhost:8888` if available (check with a HEAD request first)
- Fall back to DuckDuckGo Instant Answer API if SearXNG is down
- Return: `{ results: { title, url, snippet }[] }`

---

## Phase 3 Implementation — Beyond Copilot

Implement after Phase 2 is fully working.

### 3.1 Autonomous Application Testing: `ai_run_and_observe`

**File:** `packages/server/src/tools/ai.ts`

This is the signature feature that Copilot cannot do.

`ai_run_and_observe({ goal, timeout? })`:
1. Call `detect_project_type` to get `devCommand`
2. Start app via `background_process`
3. Wait for the app to be ready (detect "ready" signal in stdout, or wait for a port to open)
4. Make HTTP requests / interact with the running app
5. Capture screenshots if the app is a web app (via headless Chromium using `playwright` or `puppeteer`)
6. Feed observations to a sub-agent: "Given the goal: X, here is what the app does. Does it meet the goal? What is broken?"
7. Return `{ observations: string[], issues: string[], isGoalMet: boolean }`
8. Kill the background process when done

### 3.2 Realtime GitHub Awareness: `watch_github`

**File:** `packages/server/src/tools/github.ts`

Start a background polling loop (every 30s) that:
1. Checks for new/updated GitHub issues since the last check
2. Checks for failed GitHub Actions runs
3. Pushes `{ type: 'github.event', eventType, data }` WebSocket messages to all connected clients
4. The AI's system prompt includes a note: "You will receive github.event messages. When you see a new issue or CI failure, acknowledge it and offer to fix it."

The client AI chat panel must display `github.event` messages as proactive AI messages (not user messages).

### 3.3 Workspace Isolation: Branch Sessions (port from gsh)

**File:** `packages/server/src/tools/branches.ts`

Port the gsh branch session system directly:
- `branch_session_start({ branch })` — create a git worktree in a temp directory, check out the branch
- `branch_session_end({ branch, merge? })` — commit, remove worktree, optionally merge to baseline
- `branch_session_list()` — list all worktrees + their session associations
- `workspace_context()` — return current branch, worktree status, active sessions

Store session-to-worktree mapping in SQLite `project_config`.

### 3.4 Persistent Project Knowledge Base

**File:** `packages/server/src/tools/knowledge.ts`

Port gsh `write_knowledge_note` / `read_knowledge_note` / `search_knowledge_index`:
- Store knowledge notes as Markdown files in `.myeditor/knowledge/`
- Build a TF-IDF index file at `.myeditor/knowledge/_index.json` on every write
- `project_knowledge_search` searches the index; returns top N with snippets
- `project_knowledge_write(path, content)` — write note, rebuild index
- `project_knowledge_read(path)` — read note
- `project_knowledge_update(path, heading, content)` — replace a section by heading anchor

The AI uses this to remember cross-session facts: "I discovered that the auth module uses a custom JWT format — see knowledge/auth-quirks.md."

### 3.5 Context Window Management: `conversation_compact`

**File:** `packages/server/src/tools/session.ts`

When the conversation exceeds 60,000 tokens (estimate: ~200 chars per token, so ~300KB for messages):
1. Take the oldest 50% of messages
2. Ask the AI to summarize them into a condensed session summary
3. Store the summary in the session row
4. Delete the old messages from the database
5. The next chat call starts with the summary injected into the system prompt instead of the full history

The `get_context_window_usage` tool estimates current token count from message character lengths.

---

## Dynamic System Prompt

The system prompt for every chat call is built dynamically from live data. Update `packages/server/src/ai/context.ts` to build it like this:

```typescript
async function buildSystemPrompt(projectPath: string, sessionId: string): Promise<string> {
  const projectType = await detectProjectType(projectPath); // from testing.ts
  const direction = await getProjectDirection(projectPath);  // from session.ts
  const session = db.getSession(sessionId);
  const toolList = TOOL_REGISTRY.map(t => `- ${t.name}: ${t.description}`).join('\n');

  return `You are the AI core of MyEditor — an AI-native code editor. You ARE the editor. There is no separation between chat and IDE.

## Your Workspace
Project: ${projectPath}
Language: ${projectType.language}
Framework: ${projectType.framework ?? 'unknown'}
Branch: ${projectType.currentBranch}
Test command: ${projectType.testCommand ?? 'unknown — use detect_project_type to find it'}
Build command: ${projectType.buildCommand ?? 'unknown'}

## Project Direction
${direction || 'No direction set yet. Ask the user what they are building.'}

## Session Context
Session ID: ${sessionId}
Messages so far: ${session?.messageCount ?? 0}
Session summary: ${session?.summary || 'None yet'}

## Your Capabilities
You have access to the following tools. Use them proactively without asking permission for read operations. For destructive operations, the system will automatically request user approval.

${toolList}

## Behavior Rules
1. ALWAYS run get_diagnostics before claiming code is correct.
2. ALWAYS run run_build or run_tests after making code changes.
3. NEVER claim success without seeing actual tool output confirming it.
4. Log significant actions and failures using session_log_event with an accurate surprise score.
5. If you are unsure about something you did before, use session_search_log to check.
6. Update the project direction using session_set_direction after significant milestones.
7. Write knowledge notes for non-obvious discoveries using project_knowledge_write.
8. When you see a github.event message, proactively respond to it — don't wait to be asked.
9. Workspace confinement: all operations must stay within ${projectPath}. Never access files outside this directory.
10. You can start and stop background processes. Always kill dev servers you started when the task is done.
`;
}
```

---

## Client UI Requirements

The existing React client needs these additions:

### New UI Components

**`packages/client/src/components/AI/ToolApproval.tsx`**
- Modal that appears when `tool.approval_required` is received
- Shows tool name, a summary of inputs, and the risk reason
- Two buttons: **Approve** and **Deny**
- Has a 60-second countdown

**`packages/client/src/components/GitHub/IssueFeed.tsx`**
- Sidebar panel that shows real-time github.event messages
- "New Issue #42: Login broken on mobile" → click to load the issue
- "CI Failed: build.yml on main" → click to open the run

**`packages/client/src/components/AI/ProjectDirection.tsx`**
- Small persistent header above the chat showing the current project direction
- Click to edit it (fires `session_set_direction`)

**`packages/client/src/components/Session/SessionMemory.tsx`**
- Shows the session summary + recent log events
- Used for the user to see what the AI "remembers" about the current session

### Existing Component Upgrades

**`AIChat.tsx`** — Add:
- Auto-scroll that respects if the user has manually scrolled up (don't force-scroll if reading history)
- Markdown rendering for assistant messages (use `react-markdown` + `react-syntax-highlighter`)
- Tool call visualization shows duration, structured input/output, not just raw JSON
- An indicator when the AI is thinking vs. executing a tool

**`StatusBar.tsx`** — Add:
- Current model name
- Context window usage (tokens / max)
- "Issues" badge showing open GitHub issue count
- "CI" badge showing last build status

**`Editor.tsx`** — Add:
- Line range highlighting (when AI uses `highlight_range`)
- Diff viewer mode (when AI uses `open_diff`)
- "Jump to diagnostic" when AI uses `get_diagnostics`

---

## Database Schema Additions

Add to `packages/server/src/db/index.ts`:

```sql
-- Already exists: sessions, messages, tool_log

-- NEW: session events (Engram learning loop)
CREATE TABLE IF NOT EXISTS session_events (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  action TEXT NOT NULL,
  outcome TEXT NOT NULL DEFAULT '',
  surprise REAL NOT NULL DEFAULT 0.0,
  model TEXT NOT NULL DEFAULT '',
  tags TEXT NOT NULL DEFAULT '[]',
  context TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);

-- NEW: project config key-value store
CREATE TABLE IF NOT EXISTS project_config (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

-- NEW: background process registry (in-memory Map, not SQLite)
-- But add process_log for audit trail:
CREATE TABLE IF NOT EXISTS process_log (
  id TEXT PRIMARY KEY,
  label TEXT NOT NULL,
  command TEXT NOT NULL,
  cwd TEXT NOT NULL,
  pid INTEGER,
  exit_code INTEGER,
  started_at TEXT NOT NULL,
  ended_at TEXT
);
```

---

## Testing Requirements

After implementing each phase, validate with these exact commands:

```bash
# Phase 1 validation
cd packages/server && pnpm test  # or: pnpm build && node -e "require('./dist/index.js')"

# Check all new tools are registered
curl http://localhost:3000/tools | jq '.[] | .name'

# Validate workspace confinement
# (try to read /etc/passwd via the shell tool — must get WorkspaceEscapeError)

# Validate approval flow
# (use git_push tool — must trigger approval modal in UI)

# Phase 2 validation  
# Run the test suite for the project being edited through the AI
# AI should: detect test command → run tests → report structured results

# Phase 3 validation
# Start a dev server via background_process
# Call ai_run_and_observe with goal: "homepage loads without errors"
# AI should: start server → wait for ready → check output → return observations → kill server
```

---

## Constraints & Non-Negotiables

1. **TypeScript strict mode everywhere.** `tsconfig.base.json` must have `"strict": true`. No `any` types in new code. Use proper types or generics.
2. **No hardcoded paths.** All paths computed relative to `projectPath` argument. Never reference `/Users/...` in server code.
3. **pnpm workspace.** Do not add dependencies to the root `package.json`. Add to the specific package that needs them.
4. **Shared types are the contract.** Never add a new WebSocket message type without adding it to `packages/shared/src/index.ts` first.
5. **Tool names are snake_case.** The AI sees them and they must be consistent with the manifest.
6. **No console.log in production paths.** Use structured logging (the existing Fastify logger for HTTP, a simple `log()` wrapper for tools).
7. **All async errors are caught** at the tool execution layer. Uncaught promise rejections kill the server.
8. **The project goal is the north star.** Every feature is evaluated against: "Does this make the AI more capable of controlling the workspace autonomously?" If a feature is UI polish that doesn't increase AI capability, defer it.

---

## Reference Files

Before starting, read these files in the project:

1. `.github/knowledge/tool-manifest.md` — master tool list (this document's companion)
2. `PROJECT_GOAL.md` — the vision in the founder's own words
3. `packages/shared/src/index.ts` — all current types
4. `packages/server/src/ai/context.ts` — current AI loop implementation
5. `packages/server/src/ws/handler.ts` — current WebSocket routing
6. `packages/server/src/db/index.ts` — current database schema
7. `packages/client/src/store/index.ts` — current client state management

Then start with Phase 1.1 (`read_file` line range upgrade) and work through the list in order.
