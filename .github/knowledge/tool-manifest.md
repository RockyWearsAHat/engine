# MyEditor — Master Tool Manifest

This file is the authoritative reference for every tool that MyEditor's AI agent must have access to. It defines the full surface area of agent capability: what tools exist, what they do, their current implementation status, and which ones are ported from gsh or inspired by Copilot.

The goal: a terminal-native AI editor where the AI is the interface — not a chat widget bolted onto an editor. The agent must be 100% capable inside its workspace without ever needing the user to manually intervene.

---

## Implementation Status Legend

- ✅ **Implemented** — exists and works in the current codebase
- 🔧 **Partial** — scaffolded or partially working
- ❌ **Missing** — not yet implemented
- 🔄 **Needs upgrade** — implemented but needs significant improvement

---

## Category 1: Filesystem Tools

Core file system operations. The agent must be able to read, write, search, and navigate any file in the workspace without user assistance.

| Tool | Status | Description |
|------|--------|-------------|
| `read_file` | ✅ | Read file contents with language detection. Supports line ranges. |
| `write_file` | ✅ | Write/overwrite file; creates parent dirs automatically. |
| `create_file` | ❌ | Create a new file — fails safely if already exists (distinct from write_file). |
| `list_directory` | ✅ | Recursive tree listing up to configurable depth. Respects IGNORED list. |
| `delete_file` | ❌ | Delete a file or empty directory. |
| `move_file` | ❌ | Move/rename a file or directory atomically. |
| `copy_file` | ❌ | Copy a file to a new path. |
| `file_exists` | ❌ | Check if a path exists and return metadata (type, size, mtime). |
| `watch_file` | ❌ | Watch a path for changes and push events to the client in real-time. |
| `read_file_range` | 🔧 | Read a specific line range from a file (currently full file only). |
| `get_file_info` | ❌ | Get stats: size, mtime, permissions, encoding, line count. |

**Upgrade needed on `read_file`:** Must support `startLine`/`endLine` parameters to avoid sending 10MB files to the model. Should hard-reject binary files with a clear error, not return garbage.

---

## Category 2: Search & Code Intelligence Tools

The agent must be able to find anything in the codebase as if it had a language server.

| Tool | Status | Description |
|------|--------|-------------|
| `search_files` | ✅ | Ripgrep regex/literal search across workspace with file pattern filter. |
| `find_files` | ❌ | Find files by name/glob pattern (like `find . -name "*.ts"`). |
| `semantic_search` | ❌ | TF-IDF or embedding-based semantic search across the codebase. |
| `find_symbol` | ❌ | Find all definitions of a function, class, or variable by name. |
| `find_references` | ❌ | Find all uses/references of a symbol across the codebase. |
| `get_diagnostics` | ❌ | Get all current compiler errors, lint warnings from language servers (replaces VS Code's Problems panel). |
| `get_document_symbols` | ❌ | List all symbols (functions, classes, exports) in a file. |
| `grep_ast` | ❌ | AST-aware search: find all arrow functions, all `async` methods, all React components, etc. |

---

## Category 3: Shell / Terminal Tools

The agent must be able to run anything — builds, tests, installs, servers, arbitrary scripts.

| Tool | Status | Description |
|------|--------|-------------|
| `shell` | ✅ | Execute a shell command in a bash subshell, return stdout+stderr. 30s timeout, 4MB buffer. |
| `shell_long` | ❌ | Long-running shell command with streaming output and configurable timeout (up to 10 min). |
| `terminal_create` | ✅ | Create a persistent PTY terminal session (via xterm/node-pty). |
| `terminal_write` | ✅ | Write input to a running terminal session. |
| `terminal_read` | ✅ | Read recent output from a terminal session. |
| `terminal_resize` | ✅ | Resize a terminal (cols/rows). |
| `terminal_close` | ✅ | Close and destroy a terminal session. |
| `terminal_list` | ❌ | List all active terminal sessions with CWD and PID. |
| `background_process` | ❌ | Start a process as a background daemon (dev server, test watcher), return a process ID. |
| `process_status` | ❌ | Check if a background process is still running, get its recent output. |
| `process_kill` | ❌ | Kill a background process by ID. |
| `get_environment` | ❌ | Read environment variables available in the shell (filtered, no secrets). |

**Security:** `shell` must confine execution to the project root. Commands that attempt `cd` outside the workspace or access `~/.ssh`, `~/.aws`, etc. should be blocked or require explicit user approval.

---

## Category 4: Git & Version Control Tools

Full git workflow — the agent must be able to manage branches, commits, diffs, conflicts, and remote operations without leaving the editor.

| Tool | Status | Description |
|------|--------|-------------|
| `git_status` | ✅ | Current branch, staged/unstaged/untracked files, ahead/behind counts. |
| `git_diff` | ✅ | Diff staged + unstaged changes, optionally for a specific file. |
| `git_log` | ✅ | Recent commit history with hash, message, author, date. |
| `git_commit` | ✅ | Stage all (`-A`) and commit with a message. |
| `git_stage` | ❌ | Stage specific files (not all). |
| `git_unstage` | ❌ | Unstage files. |
| `git_checkout_file` | ❌ | Discard changes to a specific file (restore from HEAD). |
| `git_branch_list` | ❌ | List all local and remote branches. |
| `git_branch_create` | ❌ | Create and optionally checkout a new branch. |
| `git_branch_switch` | ❌ | Switch to an existing branch. |
| `git_branch_delete` | ❌ | Delete a local branch. |
| `git_merge` | ❌ | Merge a branch into the current branch; report conflicts. |
| `git_rebase` | ❌ | Rebase current branch onto a target. |
| `git_push` | ❌ | Push current branch to remote (with force option). |
| `git_pull` | ❌ | Pull latest from remote. |
| `git_fetch` | ❌ | Fetch from remote without merging. |
| `git_stash` | ❌ | Stash current changes with an optional message. |
| `git_stash_pop` | ❌ | Pop the latest stash. |
| `git_reset` | ❌ | Hard/soft/mixed reset to a commit. |
| `git_show` | ❌ | Show full diff for a specific commit. |
| `git_blame` | ❌ | Show line-by-line authorship for a file. |
| `git_worktree_list` | ❌ | List all git worktrees. |
| `git_worktree_add` | ❌ | Create a new worktree for a branch. |
| `git_worktree_remove` | ❌ | Remove a worktree. |
| `git_checkpoint` | ❌ | **GSH-ported:** Stage all, AI-generate commit message from diff, commit. No message required — AI writes it from the diff. |
| `git_conflict_list` | ❌ | List all files with merge conflicts. |
| `git_conflict_resolve` | ❌ | Resolve a conflict in a file (accept ours/theirs/manual). |
| `git_remote_list` | ❌ | List configured remotes with URLs. |
| `git_tag` | ❌ | Create or list tags. |

---

## Category 5: Session & Conversation Memory Tools

The project goal explicitly calls for persistent conversation history, session context, project direction summaries, and long-running session state. This is where MyEditor beats Copilot.

| Tool | Status | Description |
|------|--------|-------------|
| `session_list` | ✅ | List all sessions for the current project. |
| `session_create` | ✅ | Create a new session, linked to the current branch. |
| `session_load` | ✅ | Load a session and its full message history. |
| `session_delete` | ❌ | Delete a session and all its messages. |
| `session_summarize` | ❌ | AI-generate a summary of the current session conversation and store it. Same pattern as gsh `get_session_summary`. |
| `session_get_summary` | ❌ | Retrieve the stored summary for a session. |
| `session_search_log` | ❌ | **GSH-ported:** TF-IDF search across all session events/messages. Surface high-surprise past failures. Same as gsh `search_session_log`. |
| `session_log_event` | ❌ | **GSH-ported:** Log an action + outcome + surprise score to the session event log. Used for the Engram learning loop. Same as gsh `log_session_event`. |
| `session_set_direction` | ❌ | Store/update the project direction summary — where the project is heading, key decisions made, current milestone. Survives across sessions. |
| `session_get_direction` | ❌ | **GSH-ported:** Read the project direction summary. Same as gsh `get_project_direction`. |
| `project_knowledge_write` | ❌ | **GSH-ported:** Write a knowledge note to `.github/knowledge/`. Equivalent to gsh `write_knowledge_note`. |
| `project_knowledge_read` | ❌ | **GSH-ported:** Read a knowledge note from `.github/knowledge/`. |
| `project_knowledge_search` | ❌ | **GSH-ported:** TF-IDF search across all knowledge notes in the project. Equivalent to gsh `search_knowledge_index`. |
| `project_knowledge_update` | ❌ | Update a section of a knowledge note by heading anchor. |
| `conversation_compact` | ❌ | Summarize and compress old messages into the session summary to manage context window size. |
| `get_context_window_usage` | ❌ | Report how many tokens are currently in-flight in the active conversation. |

---

## Category 6: GitHub & Issue Tracking Tools

The core PROJECT_GOAL: "I should be able to report an issue on GitHub and have the AI immediately know — oh shit, there's an issue, I gotta go fix this."

| Tool | Status | Description |
|------|--------|-------------|
| `github_issues_list` | ✅ | List open issues from the repo's GitHub Issues. |
| `github_issues_get` | ❌ | Get a specific issue by number with full body, comments, linked PRs. |
| `github_issues_create` | ❌ | Create a new GitHub issue with title, body, labels. |
| `github_issues_comment` | ❌ | Post a comment on an issue. |
| `github_issues_close` | ❌ | Close an issue with an optional comment. |
| `github_issues_assign` | ❌ | Assign an issue to a user. |
| `github_issues_label` | ❌ | Add/remove labels on an issue. |
| `github_issues_watch` | ❌ | **Realtime:** Subscribe to new/updated issues via GitHub webhook or polling. Push to agent when new issues arrive. |
| `github_prs_list` | ❌ | List open pull requests. |
| `github_prs_get` | ❌ | Get full PR details including diff summary, review status, comments. |
| `github_prs_create` | ❌ | Create a pull request from the current branch. |
| `github_prs_review` | ❌ | Post a review comment on a PR. |
| `github_prs_merge` | ❌ | Merge a PR. |
| `github_actions_list` | ❌ | List recent CI workflow runs and their status. |
| `github_actions_get` | ❌ | Get logs and status for a specific workflow run. |
| `github_actions_watch` | ❌ | **Realtime:** Watch a CI run and stream its status to the agent. |
| `github_repo_info` | ❌ | Get repo metadata: default branch, languages, topics, description. |
| `github_notifications` | ❌ | Fetch unread GitHub notifications — issues mentioned in, CI failures, PR reviews. |

---

## Category 7: AI & Model Orchestration Tools

The agent must be able to spawn sub-agents, route to different models, and manage its own reasoning — features that Copilot implements poorly.

| Tool | Status | Description |
|------|--------|-------------|
| `ai_chat` | ✅ | Core chat loop — send message, stream response, execute tool calls, loop. |
| `ai_stop` | 🔧 | Stop the current AI stream (implemented in shared types but not fully wired). |
| `ai_subagent` | ❌ | Spawn a sub-agent with a different system prompt, tool set, and/or model. Collect results. |
| `ai_model_list` | ❌ | List available models (Anthropic Claude versions, OpenAI GPT, etc.). |
| `ai_model_switch` | ❌ | Switch the active model for the current session. |
| `ai_summarize` | ❌ | Ask the model to summarize text/code/diff without starting a chat conversation. |
| `ai_generate_commit_message` | ❌ | **GSH-ported:** Generate an AI commit message from the current diff. Used by `git_checkpoint`. |
| `ai_review_code` | ❌ | Ask the model to review a file or diff for bugs, style issues, security problems. |
| `ai_explain_code` | ❌ | Ask the model to explain what a file or snippet does. |
| `ai_detect_tests` | ❌ | **GSH-ported:** Detect the test framework and test command for the project. Same as gsh `upload-test-detection`. |
| `ai_run_and_observe` | ❌ | Start the application, observe its output, form a hypothesis about behavior, report findings. |
| `ai_fix_errors` | ❌ | Read all current diagnostics, reason about root causes, and generate targeted fixes. |
| `tool_approval` | ❌ | Request explicit user approval before executing a dangerous tool (destructive shell commands, git push, etc.). Three-tier: auto-approve / ask / always-ask. |

---

## Category 8: Web & Research Tools

The agent must be able to research documentation, fetch URLs, and search the web — features the gsh MCP server provides.

| Tool | Status | Description |
|------|--------|-------------|
| `web_fetch` | ❌ | **GSH-ported:** Fetch and clean a URL (strip boilerplate, return readable text). Same as gsh `scrape_webpage`. |
| `web_search` | ❌ | **GSH-ported:** Search the web via SearXNG (local) or fallback. Same as gsh `search_web`. |
| `web_search_docs` | ❌ | Search official documentation for a library/framework (scoped web search). |
| `npm_info` | ❌ | Get package info from npm registry: versions, description, deps, weekly downloads. |
| `crate_info` | ❌ | Get crate info from crates.io. |
| `pypi_info` | ❌ | Get package info from PyPI. |

---

## Category 9: Code Execution & Testing Tools

The agent must be able to run the application, run tests, observe output, and validate its own fixes — not just generate code and hand it back.

| Tool | Status | Description |
|------|--------|-------------|
| `run_tests` | ❌ | Detect and run the test suite, return structured results (pass/fail/error per test). |
| `run_build` | ❌ | Run the build command for the project and return errors. |
| `run_lint` | ❌ | **GSH-ported:** Run strict linting (ESLint, tsc, ruff, clippy). Return categorized errors. Same as gsh `strict_lint`. |
| `run_app` | ❌ | Start the application in a background process using the detected dev command. |
| `run_script` | ❌ | Run a specific script from package.json / Makefile / etc. |
| `get_test_results` | ❌ | Get the results of the most recent test run. |
| `get_build_errors` | ❌ | Get compiler/build errors from the most recent build. |
| `coverage_report` | ❌ | Run tests with coverage and return the coverage summary. |
| `detect_project_type` | ❌ | Detect language, framework, package manager, test runner, build tool from the project structure. |

---

## Category 10: Editor State & UI Control Tools

The agent must be able to control what the user sees — open files, highlight code, show diffs, trigger the UI.

| Tool | Status | Description |
|------|--------|-------------|
| `open_file` | ✅ | Signal the client to open a file in the editor view. |
| `open_diff` | ❌ | Show a side-by-side or inline diff between two versions of a file. |
| `highlight_range` | ❌ | Highlight a range of lines in the editor with a color/label. |
| `show_notification` | ❌ | Show a toast/notification to the user in the UI. |
| `set_editor_theme` | ❌ | Change the syntax highlighting theme. |
| `get_cursor_position` | ❌ | Get the user's current cursor position and selection in the editor. |
| `focus_terminal` | ❌ | Bring a terminal session to the foreground in the UI. |
| `get_open_files` | ❌ | List files currently open in the editor tabs. |

---

## Category 11: Branch Session & Worktree Tools

Ported directly from gsh. This is what lets multiple parallel agent sessions work on different branches without stomping on each other.

| Tool | Status | Description |
|------|--------|-------------|
| `branch_session_start` | ❌ | **GSH-ported:** Create an isolated worktree for this session's branch. |
| `branch_session_end` | ❌ | **GSH-ported:** Commit remaining changes, remove worktree, restore baseline. |
| `branch_session_list` | ❌ | **GSH-ported:** List all active branch sessions and their status. |
| `branch_read_file` | ❌ | **GSH-ported:** Read a file from any branch without checkout. |
| `workspace_context` | ❌ | **GSH-ported:** Return workspace root, current branch, worktree status, active sessions. |

---

## Category 12: Process & System Monitoring Tools

The agent must know when builds break, tests fail, or CI fires without being asked.

| Tool | Status | Description |
|------|--------|-------------|
| `system_resource_usage` | ❌ | Current CPU, memory, disk usage of the machine. |
| `port_list` | ❌ | List which ports are currently in use and which process owns them. |
| `process_list` | ❌ | List running processes relevant to the project (node, python, cargo, etc.). |
| `watch_directory` | ❌ | Watch a directory for file changes and stream events (created/modified/deleted). |
| `watch_github` | ❌ | Poll GitHub for new issues, PR updates, CI failures and push to the agent as events. |

---

## Category 13: Configuration & Project Setup Tools

| Tool | Status | Description |
|------|--------|-------------|
| `get_project_config` | ❌ | Read and parse project config (package.json, tsconfig.json, Cargo.toml, pyproject.toml, etc.). |
| `set_project_config` | ❌ | Write changes back to project config files safely (merge, not overwrite). |
| `detect_dependencies` | ❌ | List all project dependencies with current and latest versions. |
| `install_dependencies` | ❌ | Run the package manager install/add command. |
| `manage_env` | ❌ | Read/write `.env` file (with secret masking — never log values). |
| `scaffold_file` | ❌ | Generate a new file from a template (new React component, new API route, new test, etc.). |

---

## Summary Stats

| Status | Count |
|--------|-------|
| ✅ Implemented | 16 |
| 🔧 Partial | 2 |
| ❌ Missing | ~95 |
| **Total** | **~113** |

---

## Tool Implementation Priority Order

### Phase 1 — Core Agent Capability (must have before Phase 2)
1. `read_file` with line ranges (upgrade)
2. `find_files` — glob/name search
3. `get_diagnostics` — compiler/lint errors piped to AI
4. `shell_long` — streaming, long-timeout shell
5. `background_process` + `process_status` + `process_kill`
6. `git_checkpoint` — AI commit message generation
7. `session_log_event` + `session_search_log` — Engram learning loop
8. `session_get_direction` + `session_set_direction` — project direction
9. `tool_approval` — user consent for dangerous ops

### Phase 2 — Feature Parity with Copilot
10. `git_branch_create` + `git_branch_switch` + `git_branch_list`
11. `git_push` + `git_pull`
12. `run_tests` + `run_build` + `run_lint`
13. `ai_subagent` — multi-agent orchestration
14. `github_issues_get` + `github_issues_watch` (realtime)
15. `web_fetch` + `web_search`

### Phase 3 — Beyond Copilot
16. `ai_run_and_observe` — autonomous application testing
17. `github_actions_watch` — realtime CI monitoring
18. `watch_github` — push-based issue/PR awareness
19. `branch_session_start/end` — worktree isolation (port from gsh)
20. `project_knowledge_write/read/search` — persistent KB (port from gsh)
21. `semantic_search` — embedding-based code search
22. `conversation_compact` — context window management

---

## Key Design Principles

1. **Every tool returns structured data**, not just strings. Use typed response objects so the AI can reason about results, not just print them.
2. **No tool silently fails.** All errors are surfaced with a clear `isError: true` flag, error message, and suggested recovery.
3. **Dangerous tools require approval.** `git_push`, `git_reset --hard`, `process_kill`, anything touching `~` outside the workspace — all go through `tool_approval`.
4. **Tools are composable.** The agent should be able to call `get_diagnostics` → `read_file` (at error line) → `ai_fix_errors` → `write_file` → `run_build` in one uninterrupted loop.
5. **Realtime beats polling.** Where possible, tools should push events (file watcher, GitHub webhook, CI stream) rather than requiring the AI to poll on a schedule.
6. **Workspace confinement.** All filesystem and shell tools operate within the registered project root. Attempts to escape are blocked and logged.
