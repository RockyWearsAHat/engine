# Working Behaviors

This is the project contract. If a behavior is listed here, Engine is expected to do it end to end. Keep it user-facing, short, and in sync with the code. Anything marked **(IN PROGRESS)** is visible but not fully enforced yet.

---

## AI Chat Panel

Send messages with Enter or Cmd/Ctrl+Enter. You cannot send while the box is empty or no session is active. You can stop a streaming response, retry a failed response, and jump back to the bottom after scrolling away. Tool calls appear inline, and markdown renders with full formatting.

---

## Command Palette

Cmd/Ctrl+P opens file search across the active workspace. Cmd/Ctrl+Shift+P opens command mode to run available commands. Escape closes either mode.

---

## Agent Panel

Create new AI sessions for the active project, switch between previous sessions, and watch live agent activity while work is running. Each session stays isolated by branch and worktree.

---

## AI Tools (what the AI can do autonomously)

The AI agent can use these tools when working on your code:

**File System:** Read files, write files, list directories, and search for files by pattern.

**Shell:** Run shell commands from the project workspace by default.

**Editor Integration:** Open a file, list current tabs, close a tab, and focus a specific tab.

**Git:** Check status, view diffs, commit staged changes, push, pull, and switch branches.

**GitHub Issues:** List open issues, view issue details, close issues, create issues, and post comments.

**Process Management:** List running processes with CPU and memory info. Kill or terminate a process by PID with approval.

**System Info:** Query operating system, architecture, CPU, memory, and disk usage.

**Search History:** Search past AI session history for relevant context.

**Agent Team Communication:** Discover live project agents, send delegation messages, read inboxes, and await replies.

**Browser Automation:** Open a URL in the default browser.

**Screenshot:** Capture a screenshot of the screen and save it to disk.

**Repository Cloning:** Clone a remote git repository into the local workspace by URL.

**Test Runner:** Run the project's test suite from within an AI session.

**Behavioral Debugging:** Run the application, observe live behavior, form hypotheses, and validate fixes by running the app again.

**Project Tools:** Define custom tools by placing JSON files in `.engine/tools/<name>.json`.

---

## AI Provider Support

Connect to Anthropic (Claude), OpenAI-compatible endpoints, or local models such as Ollama and llama.cpp. The active provider is chosen per session.

---

## AI Safety

The AI scans outgoing messages for secrets and blocks sending if a secret is detected. Autonomous sessions never stop to ask for shell, commit, push, or process-kill approval — they proceed automatically and log the action. If a credential or external access is required and cannot be obtained autonomously (browser login, keychain, environment), Engine reports what is needed as a notification rather than a blocking gate.

---

## AI Session History

Past AI sessions are stored and searchable. Engine reuses recent session history and keeps a living project direction summary.

---

## Autonomous Development Loop

Engine works in a loop: intake, plan, build, review, validate, repeat until done. If validation fails, it reopens the relevant work instead of stopping at the first pass. When steps are exhausted, Engine automatically resets and retries up to three times before surfacing a blocked state. The loop never stops to request human approval mid-execution — it proceeds, acts, and only reports when truly blocked by something it cannot resolve autonomously.

---

## File Tree

Seven tabs: Explorer, Quality Index, Git, Search, Issues, Open Editors, Usage Dashboard.

**Explorer:** Browse the workspace file tree, toggle hidden files, expand or collapse folders, create files or folders, and open files directly. Open editors appears at the top of the explorer tab.

**Quality Index:** View deterministic AI-linter findings in a folder-and-file hierarchy.

**Git:** See the current branch, staged files, unstaged changes, and untracked files. Type a commit message and commit staged changes.

**Search:** Search across the workspace and see file path, line number, and preview results.

**Issues:** Browse open GitHub issues for the project and open an issue in the browser.

**Usage Dashboard:** View API usage analytics for the project and the user, including spend, tokens, and model breakdowns.

---

## Preferences Panel

Tabs for Editor, Discord, and GitHub preferences. Control editor font and theme, configure Discord integration, and configure GitHub token and repo.

Log in to GitHub directly from Preferences with the **Login with GitHub** button.

Configure where Engine stores autonomously-cloned repositories using the **Autonomous project save path** field.

From Preferences -> Model, users can run a **local fleet quick scanner** that recommends llama.cpp fleet settings.

---

## GitHub Event Detection

When a `GITHUB_TOKEN` is set, Engine watches GitHub activity in near real time and starts work automatically when a repo needs attention.

---

## Status Bar

Shows language, line count, and cursor position. Toggle markdown preview mode from the status bar.

---

## Terminal Panel

Create new terminals in the active workspace directory, close terminals, and send commands.

---

## Markdown Preview

Renders common markdown formatting and opens external links in the browser.

---

## Discord Control Plane

Connect Engine to a Discord bot for remote control. Once configured, send commands to your running Engine instance from any Discord channel your bot is in.

In an enrolled project channel, normal conversation is enough to direct work. Users do not need to prefix messages with `!ask`, `!auto`, or `@engine` for Engine to prepare or implement what they described.

From Preferences → Discord, testing or saving a Discord config can return a one-click bot invite link.

Available commands include help, status, sessions, lastcommit, pause/resume, stop, redirect, plan, orchestrators, issues, identity, ask, search, history, and project add/list/remove.

Configuration lives in `.engine/discord.json` in the project root. Environment variables override file config.

---

## Repository Registry

Engine maintains a list of repositories it is responsible for developing. Add or remove repos from the registry via Discord or the Preferences panel.

---

## Autonomous Work Trigger

Opening a GitHub issue, adding an issue comment on an assigned repository, updating a README with `@engine`, or hitting a CI failure can start work automatically. For enrolled project channels and assigned repositories, Engine listens by default; users do not need to tag `@engine` to get a response. For README-driven builds, the README idea plus the `@engine` tag are enough to begin; no separate prompt or step-by-step human steering is required.

---

## Configurable Autonomous Commit and Push

Headless Engine sessions can commit and push without blocking for human approval. Configure this per-project in `.engine/config.yaml`:

```yaml
autonomous:
	auto_commit: true   # commit without user approval
	auto_push: true     # push after commit (requires auto_commit: true)
	branch: "engine/work"  # branch Engine works on; omit to use current branch
```

Secret scanning still runs on every commit regardless of `auto_commit`.

---

## End-to-End Autonomous Build

Given only a GitHub repository with a README describing a project idea and an `@engine` tag, Engine treats that idea as the full request and can scaffold, implement, test, and deliver the project on its own. The user validates whether the finished result matches the idea rather than feeding follow-up prompts during execution.

---

## Per-Project Memory

Every project Engine works on gets its own local memory directory inside the project itself. Sessions, messages, and project state live with the project, not in a shared global database.

---

## Persistent Background Service

Run Engine's Go server as a macOS launchd agent so it stays alive across reboots and monitors GitHub and Discord without the desktop app open.

---

## Machine Connections (IN PROGRESS)

Connect to remote machines by host and workspace path. Pair a new machine with a code and manage connection profiles.

---

## LAN Compute Mesh

Pair a Mac and a PC on the same network so they can share test-execution and local-model inference load.

## Local-LLM-First Cost Router

When `ENGINE_LOCAL_FIRST=1` is set, light roles route to a local Ollama model by default while heavy roles stay on the configured cloud provider.

---

## Remote / Mobile Access (IN PROGRESS)

Access Engine from any device including a phone through a browser.

---

## App Shell

Cmd/Ctrl+P opens file search. Cmd/Ctrl+Shift+P opens the command palette. Cmd/Ctrl+, opens preferences. AI approval requests surface as a modal.
**Browser Automation:** Navigate to a URL, read the visible page text, click, and type from within an AI session.

**Credential Storage:** Store, retrieve, and delete credentials by named key in the OS keychain.

**Discord DM to Owner:** When the AI is blocked and needs credentials, approval, or other input it cannot obtain autonomously, it can DM the configured Discord owner directly.

## Discord as Primary Progress Channel

When Discord is configured, the AI posts milestone completions, task summaries, and session updates to the project's Discord channel.


## Autonomous Multi-Agent Team (IN PROGRESS)

The production autonomous path is a single orchestrator loop that owns planning, execution, review, and behavioral validation for the project end to end.

