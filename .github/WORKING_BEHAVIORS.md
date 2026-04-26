# Working Behaviors

Features in the main sections below are tested and enforced — if listed, it works; if broken, fix it; if a new feature lands, add it. Features marked **(IN PROGRESS)** are partially implemented and not yet fully enforced.

---

## AI Chat Panel

Send messages with Enter or Cmd/Ctrl+Enter. Cannot send while empty or when no session is active. Stop a streaming response mid-flight with the stop button. Retry a failed response without re-typing. Messages pulse while streaming. The panel auto-scrolls to the bottom; scrolling up pauses auto-scroll and shows a jump-to-bottom button. Tool calls appear inline with expandable input/output detail. Markdown renders with full formatting: headings, lists, code blocks with syntax highlighting, bold, italic, strikethrough, inline code, blockquotes, and horizontal rules.

---

## Command Palette

Cmd/Ctrl+P opens file search across the active workspace. Cmd/Ctrl+Shift+P opens command mode to filter and run available commands. Escape closes either mode.

---

## Agent Panel

Create new AI sessions for the active project. Load and switch between previous sessions. See live agent activity and recent tool calls as the agent works. Each agent session shows its active branch and worktree, so multiple agents working on the same project remain isolated and do not interfere with each other. The selected Engine team persists and is automatically resolved from `.engine/config.yaml` for autonomous runs (including issue/Discord-triggered work), so orchestrator model routing stays consistent without needing a manual reselection each session.

---

## AI Tools (what the AI can do autonomously)

The AI agent can use these tools when working on your code:

**File System:** Read files, write files, list directories, search for files by pattern.

**Shell:** Run arbitrary shell commands in the project workspace.

**Editor Integration:** Open a file in the editor, list currently open tabs, close a tab, focus a specific tab.

**Git:** Check git status, view diffs, commit staged changes, push to remote, pull from remote, create or switch branches.

**GitHub Issues:** List open issues, view issue details, close an issue, create a new issue, post a comment on an issue.

**Process Management:** List running processes with CPU and memory info. Kill or terminate a process by PID (requires user approval).

**System Info:** Query operating system, architecture, CPU, memory, and disk usage.

**Search History:** Search past AI session history for relevant context.

**Browser Automation:** Open a URL in the default browser (macOS and Linux).

**Screenshot:** Capture a screenshot of the screen and save it to disk (macOS and Linux).

**Repository Cloning:** Clone a remote git repository into the local workspace by URL. If no destination path is given, Engine derives one automatically under the workspace directory.

**Test Runner:** Run the project's test suite from within an AI session.

**Behavioral Debugging:** Run the application, observe its live behavior, form hypotheses about what is wrong, and validate fixes by running the app again — not only by running unit tests. Behavioral completion checks are project-aware: web apps use browser checks, APIs/services use endpoint/health checks, and CLI/library projects use command-based verification.

**Project Tools:** Define custom tools for any project by placing JSON files in `.engine/tools/<name>.json`. Each file specifies a description and a shell command to run. The AI discovers these tools automatically, can find them via `search_tools`, and can invoke them just like built-in tools. Inputs are passed as environment variables (e.g., `INPUT_<NAME>=value`) to prevent injection.

---

## AI Provider Support

Connect to Anthropic (Claude), OpenAI-compatible endpoints, or Ollama for local models. The active provider is selected per session. Streaming responses are displayed token-by-token as they arrive.

---

## AI Safety

The AI scans outgoing messages for secrets (API keys, tokens, private keys) and blocks sending if a secret is detected. A message containing a detected secret is never sent.

---

## AI Session History

Past AI sessions are stored and searchable. The AI automatically incorporates recent session history as context when starting a new session. Sessions can be summarized and retrieved. Engine also maintains a living project direction summary — tracking where the project started, key decisions that were made, and where it is heading — which persists across sessions and is automatically referenced when starting new work. On the first request of a session, Engine also captures a structured project intake profile (project type, success criteria, deploy target, verification strategy, and live-check command) and reuses it for autonomous verification.

---

## Autonomous Development Loop

Each AI session starts with an explicit autonomous working baseline in the session summary. As work progresses, Engine continuously cycles through planning, execution, validation, and revision until the request is complete. Session summaries are kept current with the active focus, validation status, weak points, and the next autonomous step so users can understand what Engine is doing and what it will do next.

When direction is sufficient, Engine continues forward autonomously. Before stopping to ask the user anything, Engine classifies the blocker: human-required (missing credentials/secrets, irreversible destructive actions, or product decisions where user preference materially changes the outcome) vs. AI-resolvable (everything else — design choices, naming, ambiguity, missing context, tool errors). For AI-resolvable blockers, Engine picks the safest reasonable option, prefixes the message with "Assumption:", and continues without stopping. Only human-required blockers cause Engine to pause and ask. If style direction is not specified on the first request, Engine explicitly states the style assumption it selected (and invites reshaping) in chat and via Discord DM when configured.

---

## File Tree

Six tabs: Explorer, Git, Search, Issues, Open Editors, Usage Dashboard.

**Explorer:** Browse the workspace file tree. Files show live git status badges: modified, staged, untracked, ignored. Toggle hidden files on or off. Expand or collapse folders individually. Right-click in the tree to create a new file or folder in the selected location. Context menus support scoped Expand All and Collapse All (for a selected folder or sibling level) and global Expand All/Collapse All from empty tree space. Folder grouping can be toggled from the context menu and the preference is remembered across sessions.

**Git:** See the current branch, staged files, unstaged changes, and untracked files. No repository shows a clear empty state. Type a commit message and commit staged changes. Click any file in the change lists to view its diff.

**Search:** Search across the workspace. Results show file path, line number, and preview. Loading, error, and empty states are each clearly communicated.

**Issues:** Browse open GitHub issues for the project. Click an issue to open it in the browser. Loading, error, and empty states are each clearly communicated.

**Open Editors:** See all open files. Click to switch between them. Collapse or expand the list.

**Usage Dashboard:** View API usage analytics in a dedicated sidebar tab with two scopes: project-wide and user-wide. See total spend, input/output tokens, total tokens, average price per token, active development time, and AI compute time. Filter metrics to a specific model and inspect detailed breakdown tables per project and per model.

---

## Preferences Panel

Tabs for Editor, Discord, and GitHub preferences. Control editor font and theme. Configure Discord integration. Configure GitHub token and repo. Form validation blocks saving an incomplete connection.

---

## Status Bar

Shows language, line count, and cursor position, updated live as the active file changes. Toggle markdown preview mode on or off directly from the status bar.

---

## Terminal Panel

Create new terminals in the active workspace directory. Close any terminal. Send commands. Terminal output streams live.

---

## Markdown Preview

Renders headings h1–h6, ordered and unordered lists, fenced code blocks with syntax highlighting, links, bold, italic, strikethrough, inline code, and blockquotes. External links open in the browser.

---

## Discord Control Plane

Connect Engine to a Discord bot for remote control. Once configured, send commands to your running Engine instance from any Discord channel your bot is in.

From Preferences → Discord, testing or saving a Discord config can return a one-click bot invite link with the required scopes/permissions so the bot can be added to the target server quickly. When Discord is enabled but not yet connected to a server, the action row surfaces the invite button directly so the user can link the bot immediately.

Available commands: `help`, `status` (server health), `sessions` (list AI sessions), `lastcommit` (most recent git commit), `pause`/`resume` (halt or resume AI activity), `ask` (send a message to the AI), `search` (search session history), `history` (view recent session history), `project add/list/remove` (manage which projects the bot monitors — accepts a local path or a GitHub/git URL which Engine clones automatically), `projects` (list all monitored projects).

Configuration lives in `.engine/discord.json` in the project root. Environment variables override file config. The bot only responds to authorized users and channels as configured. When Engine is genuinely blocked and cannot proceed autonomously, it posts a help request in the relevant Discord project thread describing what it tried, what failed, and what information it needs — rather than silently stopping.

---

## Repository Registry

Engine maintains a list of repositories it is responsible for developing. Add or remove repos from the registry via Discord (`project add/remove`) or the Preferences panel. For each tracked repo, Engine can clone it locally if not already present, pull the latest changes, and start work autonomously.

Engine communicates its current state clearly: "I have been linked to `<repo-a>` and `<repo-b>`. I am continuously monitoring and developing these projects — working off open issues, tracking progress by building and running the application directly, and validating changes end-to-end. Any other project I get tagged in, I can add to my automatic workflow or implement a specific change up to a defined state."

When tagged in a new repo (via GitHub issue, README mention, or Discord command), Engine either adds it to the continuous workflow or executes the requested work as a one-off up to the point described, then reports completion.

When a README or issue in a tracked repo contains instructions like "clone `<url>` and implement `<feature>`", Engine reads and executes those instructions directly — cloning the target repo into the workspace, making the requested changes, running tests, and committing — without requiring the user to set anything up manually.

---

## Autonomous Work Trigger

Opening a GitHub Issue, pushing a README update, or a CI workflow failure causes Engine to automatically pick up the task and begin working — no manual prompt needed. Engine posts progress updates to the relevant Discord project thread as it works.

---

## Configurable Autonomous Commit and Push

Headless Engine sessions (scaffold, CI fix, issue resolution) can commit and push without blocking for human approval. Configure this per-project in `.engine/config.yaml`:

```yaml
autonomous:
	auto_commit: true   # commit without user approval
	auto_push: true     # push after commit (requires auto_commit: true)
	branch: "engine/work"  # branch Engine works on; omit to use current branch
```

Secret scanning still runs on every commit regardless of `auto_commit` — commits containing secrets are blocked unconditionally.

---

## End-to-End Autonomous Build

Given only a GitHub repository with a README describing a project idea, Engine scaffolds, implements, tests, and delivers the project entirely on its own. It plans and writes out what the idea means before writing any code, asks clarifying questions only if genuinely blocked, then drives the work to completion without requiring further human prompting.

---

## Machine Connections (IN PROGRESS)

Connect to remote machines by host and workspace path. Pair a new machine with a code. Save and manage connection profiles. Forget all saved profiles at once.

---

## Remote / Mobile Access (IN PROGRESS)

Access Engine from any device including a phone. Engine runs on your local machine; you connect to it remotely via a browser. All features — chat, file tree, terminal, agent sessions — are available remotely without installing anything on the remote device.

---

## App Shell

Cmd/Ctrl+P opens file search. Cmd/Ctrl+Shift+P opens the command palette. Cmd/Ctrl+, opens preferences. AI approval requests (e.g. killing a process) surface as a modal with allow and deny.
**Browser Automation:** Navigate Chrome to a URL, read the visible page text, click at screen coordinates, and type text — all from within an AI session (macOS via AppleScript; Linux via xdotool). The AI can research the web, fill in login forms, and interact with browser-based services autonomously.

**Credential Storage:** Store, retrieve, and delete credentials by named key in the OS keychain (macOS Keychain; Linux secret-service). Credentials are scoped to this machine, not per project, so they persist across Engine sessions and are reusable whenever the agent needs them again.

**Discord DM to Owner:** When the AI is blocked and needs credentials, approval, or other input it cannot obtain autonomously, it can DM the configured Discord owner directly to request that information. The owner's Discord user ID is resolved from the bot's `AllowedUsers` config.


