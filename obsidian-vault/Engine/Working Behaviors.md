---
source: ../.github/WORKING_BEHAVIORS.md
generatedAt: 2026-06-06T01:48:28.980Z
sectionCount: 28
---

# Working Behaviors

This note mirrors the repository contract and is regenerated automatically.

## Section Index

- AI Chat Panel
- Command Palette
- Agent Panel
- AI Tools (what the AI can do autonomously)
- AI Provider Support
- AI Safety
- AI Session History
- Autonomous Development Loop
- File Tree
- Preferences Panel
- GitHub Event Detection
- Status Bar
- Terminal Panel
- Markdown Preview
- Discord Control Plane
- Repository Registry
- Autonomous Work Trigger
- Configurable Autonomous Commit and Push
- End-to-End Autonomous Build
- Per-Project Memory
- Persistent Background Service
- Machine Connections (IN PROGRESS)
- LAN Compute Mesh
- Local-LLM-First Cost Router
- Remote / Mobile Access (IN PROGRESS)
- App Shell
- Discord as Primary Progress Channel
- Autonomous Multi-Agent Team (IN PROGRESS)

## Source Contract

# Working Behaviors

This is the project contract. If a behavior is listed here, Engine is expected to do it end to end. Keep it short, user-facing, and in sync with the code. Anything marked **(IN PROGRESS)** is visible but not fully enforced yet.

---

## AI Chat Panel

Send messages with Enter or Cmd/Ctrl+Enter. Cannot send while empty or when no session is active. Stop a streaming response mid-flight with the stop button. Retry a failed response without re-typing. Messages pulse while streaming. The panel auto-scrolls to the bottom; scrolling up pauses auto-scroll and shows a jump-to-bottom button. Tool calls appear inline with expandable input/output detail. Markdown renders with full formatting: headings, lists, code blocks with syntax highlighting, bold, italic, strikethrough, inline code, blockquotes, and horizontal rules.

---

## Command Palette

Cmd/Ctrl+P opens file search across the active workspace. Cmd/Ctrl+Shift+P opens command mode to filter and run available commands. Escape closes either mode.

---

## Agent Panel

Create new AI sessions for the active project, switch between previous sessions, and watch live agent activity while work is running. Each session stays isolated by branch and worktree. Autonomous runs use the team and model policy selected for the project; cloud models are only used when the needed credentials exist.

---

## AI Tools (what the AI can do autonomously)

The AI agent can use these tools when working on your code:

**File System:** Read files, write files, list directories, search for files by pattern.

**Shell:** Run arbitrary shell commands from the project workspace by default. Autonomous commands stay rooted in the project for normal build/test work, automatically avoid parent `go.work` leakage for Go projects, and receive clear awareness notes when they intentionally use paths or working directories outside the project root.

**Editor Integration:** Open a file in the editor, list currently open tabs, close a tab, focus a specific tab.

**Git:** Check git status, view diffs, commit staged changes, push to remote, pull from remote, create or switch branches.

**GitHub Issues:** List open issues, view issue details, close an issue, create a new issue, post a comment on an issue.

**Process Management:** List running processes with CPU and memory info. Kill or terminate a process by PID (requires user approval).

**System Info:** Query operating system, architecture, CPU, memory, and disk usage.

**Search History:** Search past AI session history for relevant context.

**Agent Team Communication:** The lead agent and autonomous worker teams can discover live project agents, send focused delegation messages, read their inbox, and await replies. The user continues talking to one lead agent while the team exchanges concise context packets behind the scenes.

**Browser Automation:** Open a URL in the default browser (macOS and Linux).

**Screenshot:** Capture a screenshot of the screen and save it to disk (macOS and Linux).

**Repository Cloning:** Clone a remote git repository into the local workspace by URL. If no destination path is given, Engine derives one automatically under the workspace directory.

**Test Runner:** Run the project's test suite from within an AI session.

**Behavioral Debugging:** Run the application, observe its live behavior, form hypotheses about what is wrong, and validate fixes by running the app again — not only by running unit tests. Behavioral completion checks are project-aware: web apps use browser checks, APIs/services use endpoint/health checks, and CLI/library projects use command-based verification.

**Project Tools:** Define custom tools for any project by placing JSON files in `.engine/tools/<name>.json`. Each file specifies a description and a shell command to run. The AI discovers these tools automatically, can find them via `search_tools`, and can invoke them just like built-in tools. Inputs are passed as environment variables (e.g., `INPUT_<NAME>=value`) to prevent injection.

---

## AI Provider Support

Connect to Anthropic (Claude), OpenAI-compatible endpoints, or local models such as Ollama and llama.cpp. The active provider is chosen per session. Streaming responses appear token by token. Role-specific overrides can still direct planner/reviewer behavior when needed.

---

## AI Safety

The AI scans outgoing messages for secrets (API keys, tokens, private keys) and blocks sending if a secret is detected. A message containing a detected secret is never sent.

---

## AI Session History

Past AI sessions are stored and searchable. Engine automatically reuses recent session history, keeps a living project direction summary, and records a structured intake profile on the first request so future autonomous work starts from the right context.

---

## Autonomous Development Loop

Engine works in a simple loop: intake, plan, build, review, validate, repeat until done. If validation fails, it reopens the relevant work instead of stopping at the first pass. Issue-triggered autonomous runs automatically retry AI-resolvable stalls, including exhausted turn budgets, before surfacing a real blocker. If the run starts without a project path, it fails fast with a clear error. Project memory is persisted so a restart can pick up where it left off.

---

## File Tree

Seven tabs: Explorer, Quality Index, Git, Search, Issues, Open Editors, Usage Dashboard.

**Explorer:** Browse the workspace file tree. Files show live git status badges: modified, staged, untracked, ignored. Toggle hidden files on or off. Expand or collapse folders individually. Right-click in the tree to create a new file or folder in the selected location. Context menus support scoped Expand All and Collapse All (for a selected folder or sibling level) and global Expand All/Collapse All from empty tree space. Folder grouping can be toggled from the context menu and the preference is remembered across sessions. Open editors is a sub-tab that lives at the top of the explorer tab and shows currently open tabs in the editor. Click any file to focus it immediately.

**Quality Index:** View deterministic AI-linter findings in an explorer-style hierarchy (folders, then files, then issue rows), including duplicate logic, dead-code candidates, documentation gaps, large uncommented blocks, React pitfalls (such as unstable list keys and inline JSX handlers), and CSS selector usage drift (selectors with no matching class/className usage). Generated files are filtered out before scan results are produced.

**Git:** See the current branch, staged files, unstaged changes, and untracked files. No repository shows a clear empty state. Type a commit message and commit staged changes. Click any file in the change lists to view its diff.

**Search:** Search across the workspace. Results show file path, line number, and preview. Loading, error, and empty states are each clearly communicated.

**Issues:** Browse open GitHub issues for the project. Click an issue to open it in the browser. Loading, error, and empty states are each clearly communicated.

**Usage Dashboard:** View API usage analytics in a dedicated sidebar tab with two scopes: project-wide and user-wide. See total spend, input/output tokens, total tokens, average price per token, active development time, and AI compute time. Filter metrics to a specific model and inspect detailed breakdown tables per project and per model.

---

## Preferences Panel

Tabs for Editor, Discord, and GitHub preferences. Control editor font and theme. Configure Discord integration. Configure GitHub token and repo. Form validation blocks saving an incomplete connection.

Log in to GitHub directly from the Preferences panel using the **Login with GitHub** button — no copy-pasting tokens needed. The button starts the GitHub Device Authorization Flow: Engine displays a short user code and a link to `github.com/login/device`. After the user enters the code on GitHub the token is saved automatically and the login button disappears. While authorization is pending the panel shows a Cancel button to abort the flow. Requires `GITHUB_CLIENT_ID` to be set in the server environment (register an OAuth App on GitHub to get one). On successful login, Engine also auto-provisions a `GITHUB_WEBHOOK_SECRET` when missing so webhook validation is ready without manual secret setup.

Configure where Engine stores autonomously-cloned repositories using the **Autonomous project save path** field in the GitHub section of Preferences. By default, Engine stores clones in `~/.engine/projects` to avoid nested-repository workspace conflicts. A folder-browse button opens a directory picker for quick selection. The selected path is persisted and synced to the server as the `ENGINE_CLONES_DIR` environment variable.

From Preferences -> Model, users can run a **local fleet quick scanner** that evaluates machine CPU/RAM budgets and recommends efficient llama.cpp fleet settings (backend count, ports, parallelism, threads, batching, context size). The scanner can also write the recommendation directly to `.engine/llama-fleet.env` for easy startup.

---

## GitHub Event Detection

When a `GITHUB_TOKEN` is set, Engine watches GitHub activity in near real time and starts work automatically when a repo needs attention. README tagging with `@engine` kicks off the autonomous scaffold flow, and the repo is announced in Discord so progress is visible.

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

Available commands: `help`, `status` (server health), `sessions` (list AI sessions), `lastcommit` (most recent git commit), `pause`/`resume` (halt or resume AI activity), `stop` (terminate the active orchestrator for a project), `redirect` (inject a new instruction the orchestrator picks up at the next step), `plan` (print the live `.engine/plan.md` for a project), `orchestrators` (list projects with an active orchestrator), `issues` (list GitHub issues currently assigned to Engine across enrolled projects), `identity` (show Engine's current GitHub login/token/project-board identity state), `ask` (send a message to the AI), `search` (search session history), `history` (view recent session history), `project add/list/remove` (manage which projects the bot monitors — accepts a local path or a GitHub/git URL which Engine clones automatically), `projects` (list all monitored projects).

Configuration lives in `.engine/discord.json` in the project root. Environment variables override file config. Project channels are the main surface: users can ask questions there, issue commands, see status, and get help when Engine is blocked.

---

## Repository Registry

Engine maintains a list of repositories it is responsible for developing. Add or remove repos from the registry via Discord (`project add/remove`) or the Preferences panel. For each tracked repo, Engine can clone it locally if not already present, pull the latest changes, and start work autonomously.

Engine communicates its current state clearly: "I have been linked to `<repo-a>` and `<repo-b>`. I am continuously monitoring and developing these projects — working off open issues, tracking progress by building and running the application directly, and validating changes end-to-end. Any other project I get tagged in, I can add to my automatic workflow or implement a specific change up to a defined state."

When tagged in a new repo (via GitHub issue, README mention, or Discord command), Engine either adds it to the continuous workflow or executes the requested work as a one-off up to the point described, then reports completion.

When a README or issue in a tracked repo contains instructions like "clone `<url>` and implement `<feature>`", Engine reads and executes those instructions directly — cloning the target repo into the workspace, making the requested changes, running tests, and committing — without requiring the user to set anything up manually.

---

## Autonomous Work Trigger

Opening a GitHub issue, updating a README with `@engine`, or hitting a CI failure can start work automatically. Engine posts kickoff and progress updates to Discord, deduplicates repeated triggers, and keeps the user-facing loop focused on the current job instead of reopening the same task again and again.

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

When local-first routing is enabled, Engine uses local models for simple work first so generation stays fast and cheap. Heavier roles stay on the configured stronger provider when one is available.

---

## End-to-End Autonomous Build

Given only a GitHub repository with a README describing a project idea, Engine can scaffold, implement, test, and deliver the project on its own. It plans first, asks only when truly blocked, and keeps moving until the work is complete.

---

## Per-Project Memory

Every project Engine works on gets its own local memory directory inside the project itself, like a git repo carries its own `.git`. Sessions, messages, and project state for an autonomous build live in `<project>/.engine/state.db`, not in a shared global database. Switching between projects keeps each project's history separate; deleting a project's directory removes only that project's memory.

---

## Persistent Background Service

Run Engine's Go server as a macOS launchd agent so it stays alive across reboots and monitors GitHub and Discord without the desktop app open. Install with `./scripts/engine-service.sh install` (requires `ANTHROPIC_API_KEY` and optionally `GITHUB_TOKEN` in the environment). Uninstall with `uninstall`, check with `status`, tail logs with `logs`. Once installed the service restarts automatically if it crashes.

---

## Machine Connections (IN PROGRESS)

Connect to remote machines by host and workspace path. Pair a new machine with a code. Save and manage connection profiles. Forget all saved profiles at once.

---

## LAN Compute Mesh

Pair a Mac and a PC on the same network so they can share test-execution and local-model inference load. Start the mesh listener with `ENGINE_MESH=1`; the listener binds to `:24445` by default and exposes three HMAC-signed endpoints: `/mesh/health` (peer capability summary, including the peer's local Ollama URL when configured), `/mesh/exec` (run a shell command on the peer and stream stdout/stderr back), and `/mesh/inference` (proxy an Ollama-compatible request to the peer's local model and return the response). Peers are listed in `~/.engine/mesh.json` with a per-peer shared secret and optional `ollamaURL` / role tags (`tests`, `inference`, `build`). The autonomous builder role has a `mesh_exec` tool that dispatches commands to a named peer or a peer matching a role tag. Requests outside the configured peer set are rejected; the HMAC + timestamp check enforces a 5-minute clock-skew window.

## Local-LLM-First Cost Router

When `ENGINE_LOCAL_FIRST=1` is set, light roles route to a local Ollama model by default while heavy roles stay on the configured cloud provider. Light roles (mechanical work where a 7B–13B model is sufficient): grill interviewer, planner, documenter, intaker. Heavy roles (frontier-model reasoning is justified): architect, implementer, autonomous builder. Per-role environment overrides (`ENGINE_PLANNER_MODEL`, `ENGINE_REVIEWER_MODEL`, etc.) and explicit `ctx.ModelOverride` calls still win over the router, so the routing decision only applies when nothing stronger has been requested. Combined with the mesh inference proxy, this lets a Mac running the orchestrator delegate cheap inference to a PC's beefier local model for zero marginal token cost.

---

## Remote / Mobile Access (IN PROGRESS)

Access Engine from any device including a phone. Engine runs on your local machine; you connect to it remotely via a browser. All features — chat, file tree, terminal, agent sessions — are available remotely without installing anything on the remote device.

---

## App Shell

Cmd/Ctrl+P opens file search. Cmd/Ctrl+Shift+P opens the command palette. Cmd/Ctrl+, opens preferences. AI approval requests (e.g. killing a process) surface as a modal with allow and deny.
**Browser Automation:** Navigate Chrome to a URL, read the visible page text, click at screen coordinates, and type text — all from within an AI session (macOS via AppleScript; Linux via xdotool). The AI can research the web, fill in login forms, and interact with browser-based services autonomously.

**Credential Storage:** Store, retrieve, and delete credentials by named key in the OS keychain (macOS Keychain; Linux secret-service). Credentials are scoped to this machine, not per project, so they persist across Engine sessions and are reusable whenever the agent needs them again.

**Discord DM to Owner:** When the AI is blocked and needs credentials, approval, or other input it cannot obtain autonomously, it can DM the configured Discord owner directly to request that information. The owner's Discord user ID is resolved from the bot's `AllowedUsers` config.

## Discord as Primary Progress Channel

When Discord is configured, the AI posts milestone completions, task summaries, and session updates to the project's Discord channel — not to the in-editor chat. In-editor AI responses are terse (1–3 sentences): acknowledge the task, state what's happening, done. Discord is where the user sees what Engine actually accomplished.


## Autonomous Multi-Agent Team (IN PROGRESS)

The lead agent analyzes project objectives and automatically determines which specialist agents are needed. Team composition is task-driven:

**Build Tasks** (implement, scaffold, create): Planner → Scaffolder → Implementer → Tester → Reviewer pipeline. Each specialist has a focused responsibility and communicates results back to the lead.

**Test & Debug Tasks** (fix, debug, test failures): Tester and Reviewer specialization. Tester iterates on fixes; Reviewer validates no regressions.

**Design & Architecture Tasks** (refactor, review architecture): Architect and Reviewer specialization. Architect proposes structural changes; Reviewer validates design principles.

**Documentation Tasks** (update docs, write README): Documenter specialization. Produces and updates project documentation.

**Play-Test Tasks** (for WebApps and Services): AutonomousBuilder with play-tester context. Exercises features and discovers UX findings.

Each specialist receives a concise task brief and context hints (e.g., "coverage": "100%"). Specialists use project-scoped async message routing to exchange handoff packets; the lead orchestrates and synthesizes final results. No specialist blocks on another; all work is async-first.

Team configuration is set via `engine.team.set` WebSocket message (team name, model provider, model). Configuration is resolved from `.engine/config.yaml` if not explicitly provided. Once set, `ENGINE_MODEL_PROVIDER`, `ENGINE_MODEL`, and `ENGINE_ACTIVE_TEAM` environment variables are updated so all agents use the same model policy.

Agent Discovery, Message Routing, and Inbox Management are available to specialists via peer tools: `agent_list` (see teammates), `agent_send` (send focused packet), `agent_inbox` (receive pending messages), `agent_await` (block until a specific reply arrives with timeout).


