---
source: ../.github/WORKING_BEHAVIORS.md
generatedAt: 2026-05-19T07:23:57.120Z
sectionCount: 27
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

## Source Contract

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

Connect to Anthropic (Claude), OpenAI-compatible endpoints, or Ollama for local models. The active provider is selected per session. Streaming responses are displayed token-by-token as they arrive. Engine also supports role-specific model overrides for split execution by setting planner/reviewer model env vars (`ENGINE_PLANNER_MODEL`, `ENGINE_PLANNER_PROVIDER`, `ENGINE_REVIEWER_MODEL`, `ENGINE_REVIEWER_PROVIDER`) while keeping the main worker model on `ENGINE_MODEL`.

---

## AI Safety

The AI scans outgoing messages for secrets (API keys, tokens, private keys) and blocks sending if a secret is detected. A message containing a detected secret is never sent.

---

## AI Session History

Past AI sessions are stored and searchable. The AI automatically incorporates recent session history as context when starting a new session. Sessions can be summarized and retrieved. Engine also maintains a living project direction summary — tracking where the project started, key decisions that were made, and where it is heading — which persists across sessions and is automatically referenced when starting new work. On the first request of a session, Engine also captures a structured project intake profile (project type, success criteria, deploy target, verification strategy, and live-check command) and reuses it for autonomous verification.

---

## Autonomous Development Loop

Each AI session starts with an explicit autonomous working baseline in the session summary. As work progresses, Engine continuously cycles through planning, execution, validation, and revision until the request is complete. Session summaries are kept current with the active focus, validation status, weak points, and the next autonomous step so users can understand what Engine is doing and what it will do next.

Tagging `@engine` in a GitHub README starts the orchestrator: **intake → PRD → plan → execute → review → validate**, looping until completion or the safety cap of 200 outer iterations.

The documentation pipeline produces five layered files under `<project>/.engine/`, each owned by a specific role and read only by the roles that need it:

- **`design.md`** — written by `RoleGriller`, which walks the design tree branch-by-branch (Matt Pocock's "grill me", dependency-aware) and labels every decision DECIDED, ASSUMPTION, or OPEN. It splits critical from non-critical modules and lists open risks. No vocabulary table, no PRD — that's the next phase.
- **`vocabulary.md`** — the ubiquitous-language terms table distilled from the design concept. Used verbatim by planner, builder, and reviewer.
- **`prd.md`** — the module-aware product requirements document. Names each module the project will contain, its path, public interface, purpose, and whether it is critical (auth, payments, persistence — deeply reviewed) or non-critical (interface-only review).
- **`modules.md`** — auto-maintained module index, regenerated after every approved plan step. Path → purpose → public interface → critical/non-critical. The planner and reviewer of the next step always start from the current map.
- **`plan.md`** — TDD-shaped checkbox plan. Each step is a vertical slice: failing test → minimum implementation → refactor for module depth.

Runtime state lives in `orchestration.json` so a laptop restart resumes at the first unchecked step. The reviewer evaluates each step on six axes: acceptance criterion passes, a test was written first, the code uses the ubiquitous-language vocabulary, new modules are deep (small public surface relative to internal complexity, per Ousterhout), the change is *minimal* (no speculative abstractions, no defensive code for impossible cases, no unrelated refactors), and the change landed in the module from the PRD that owns it (not a parallel duplicate). REJECT findings flow back as the next builder turn. When every step is checked, the behavioral validator boots the application (web server, CLI, or API) and verifies the README's acceptance criteria end-to-end; failure reopens the most recent step.

Autonomous teams share a project-scoped communication pool. Worker teams can ask each other narrow questions, hand off findings, and wait for replies without dumping every role's full context into one model window; the lead agent remains the single user-facing reporter.

When direction is sufficient, Engine continues forward autonomously. Before stopping to ask the user anything, Engine classifies the blocker: human-required (missing credentials/secrets, irreversible destructive actions, or product decisions where user preference materially changes the outcome) vs. AI-resolvable (everything else — design choices, naming, ambiguity, missing context, tool errors). For AI-resolvable blockers, Engine picks the safest reasonable option, prefixes the message with "Assumption:", and continues without stopping. Only human-required blockers cause Engine to pause and ask. If style direction is not specified on the first request, Engine sends one short style-assumption notice (with an override invitation) in chat and via Discord DM when configured. Engine treats deploy/publish as explicit-only: deployment and publish actions are blocked by default unless the request contains explicit publish/deploy intent evidence.

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

Log in to GitHub directly from the Preferences panel using the **Login with GitHub** button — no copy-pasting tokens needed. The button starts the GitHub Device Authorization Flow: Engine displays a short user code and a link to `github.com/login/device`. After the user enters the code on GitHub the token is saved automatically and the login button disappears. While authorization is pending the panel shows a Cancel button to abort the flow. Requires `GITHUB_CLIENT_ID` to be set in the server environment (register an OAuth App on GitHub to get one). On successful login, Engine also auto-provisions a `GITHUB_WEBHOOK_SECRET` when missing so webhook validation is ready without manual secret setup.

Configure where Engine stores autonomously-cloned repositories using the **Autonomous project save path** field in the GitHub section of Preferences. By default, Engine stores clones in `~/.engine/projects` to avoid nested-repository workspace conflicts. A folder-browse button opens a directory picker for quick selection. The selected path is persisted and synced to the server as the `ENGINE_CLONES_DIR` environment variable.

---

## GitHub Event Detection

When a `GITHUB_TOKEN` is set, Engine monitors the authenticated user's GitHub activity in near-real-time using the GitHub Events API with ETag conditional requests (304 responses are instant and do not count against rate limits). If the server started before a token existed, completing GitHub login in Preferences automatically starts the watcher so monitoring begins immediately. Engine checks for `@engine` in README files when:

When `@engine` appears in a README for the first time, Engine triggers the autonomous scaffolding workflow for that repository. The detection latency is typically under one minute, far faster than the previous 5-minute polling approach. After detecting `@engine` and starting the scaffold session, Engine automatically enrolls the repository in Discord (creating a project channel) and announces it in the control channel so the team knows Engine has picked it up.

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

Configuration lives in `.engine/discord.json` in the project root. Environment variables override file config. The bot only responds to authorized users and channels as configured. Project channels are the primary communication surface: users can chat directly in the project channel (no `!ask` required) and Engine responds there. Status-like project-channel messages such as `status?` are answered as project status updates instead of starting a new AI task, and status replies include recent scaffold session context when available. Command mode is still available for administrative actions (`!status`, `!sessions`, `!pause`, etc.). When Engine is genuinely blocked and cannot proceed autonomously, it posts a help request in the relevant Discord project channel describing what it tried, what failed, and what information it needs — rather than silently stopping.

---

## Repository Registry

Engine maintains a list of repositories it is responsible for developing. Add or remove repos from the registry via Discord (`project add/remove`) or the Preferences panel. For each tracked repo, Engine can clone it locally if not already present, pull the latest changes, and start work autonomously.

Engine communicates its current state clearly: "I have been linked to `<repo-a>` and `<repo-b>`. I am continuously monitoring and developing these projects — working off open issues, tracking progress by building and running the application directly, and validating changes end-to-end. Any other project I get tagged in, I can add to my automatic workflow or implement a specific change up to a defined state."

When tagged in a new repo (via GitHub issue, README mention, or Discord command), Engine either adds it to the continuous workflow or executes the requested work as a one-off up to the point described, then reports completion.

When a README or issue in a tracked repo contains instructions like "clone `<url>` and implement `<feature>`", Engine reads and executes those instructions directly — cloning the target repo into the workspace, making the requested changes, running tests, and committing — without requiring the user to set anything up manually.

---

## Autonomous Work Trigger

Opening a GitHub Issue, pushing a README update, or a CI workflow failure causes Engine to automatically pick up the task and begin working — no manual prompt needed. Engine posts kickoff and major progress updates to the relevant Discord project channel as it works.

For README-triggered autonomous scaffold runs, Engine hands the brief to the orchestrator (`ai.RunAutonomousProject`), which produces a plan, executes each step, runs the reviewer + tests as a gate, and only declares completion after the behavioral validator confirms the application actually works. The plan is persisted to `<project>/.engine/plan.md`; users can inspect it from any project Discord channel with `!plan`. If a step is rejected, the reviewer's findings flow back as the next builder turn — no silent failures. The outer loop is bounded by a 200-iteration safety cap.

When GitHub Project-board settings are configured (`ENGINE_GITHUB_PROJECT_NUMBER` + owner/token identity), Engine links picked-up issues into that Project v2 board and updates issue status field values (for example, "In Progress", "Done", or "Blocked") as orchestration phases change.

Engine also deduplicates repeated README-triggered scaffold starts per repository so duplicate GitHub events do not repeatedly re-enroll the same project channel or spam repeated kickoff messages in Discord.

Autonomous mode is engaged by intent rather than by matching a fixed phrase list. When a user sends a chat message in the Engine app or a Discord project channel, Engine parses the request through its intent classifier — if the message reads as a structural-work directive (build, implement, fix, refactor, scaffold, create, add, write, test, deploy, etc.), that turn switches into autonomous mode. Question-form prompts ("what does build do?", "explain the build process", "why did the test fail") stay in interactive mode even when they contain workflow keywords. Users can also force autonomous mode explicitly with quick commands: prefix any chat message with `/auto`, `/autonomous`, `/build`, or `/ship`, or in Discord use `!auto <prompt>` (aliases: `!autonomous`, `!build`). The shell stops gating commands behind approval modals; instead it surfaces inline awareness notes whenever a command leaves the project root or touches risky paths. A short notice ("Autonomous mode engaged — approvals bypassed, awareness notes will be surfaced inline.") is posted to the channel/chat so the user always knows when they crossed into autonomous control. Conversational and informational prompts remain in interactive mode and continue to use approval modals.

Each scaffold retry inherits a "Prior scaffold attempts" summary at the top of its prompt, including the count of previous attempts and the last assistant message from each, plus an explicit directive to read PROJECT_GOAL.md and existing files before continuing — so retried autonomous sessions resume with awareness of what already happened instead of starting from zero.

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


