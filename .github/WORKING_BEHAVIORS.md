# Working Behaviors

Every feature listed here is tested and enforced. If it is listed here, it works. If it is broken, fix it. If a new feature lands, add it here. Mark unfinished features (IN PROGRESS).

---

## AI Chat Panel

Send messages with Enter or Cmd/Ctrl+Enter. Cannot send while empty or when no session is active. Stop a streaming response mid-flight with the stop button. Retry a failed response without re-typing. Messages pulse while streaming. The panel auto-scrolls to the bottom; scrolling up pauses auto-scroll and shows a jump-to-bottom button. Tool calls appear inline with expandable input/output detail. Markdown renders with full formatting: headings, lists, code blocks with syntax highlighting, bold, italic, strikethrough, inline code, blockquotes, and horizontal rules.

---

## Command Palette

Cmd/Ctrl+P opens file search across the active workspace. Cmd/Ctrl+Shift+P opens command mode to filter and run available commands. Escape closes either mode.

---

## Agent Panel

Create new AI sessions for the active project. Load and switch between previous sessions. See live agent activity and recent tool calls as the agent works.

---

## File Tree

Five tabs: Explorer, Git, Search, Issues, Open Editors.

**Explorer:** Browse the workspace file tree. Files show live git status badges: modified, staged, untracked, ignored. Toggle hidden files on or off. Expand or collapse folders individually. Right-click a folder to expand or collapse its entire subtree. Folder grouping preference is remembered across sessions.

**Git:** See the current branch, staged files, unstaged changes, and untracked files. No repository shows a clear empty state. Type a commit message and commit staged changes. Click any file in the change lists to view its diff.

**Search:** Search across the workspace. Results show file path, line number, and preview. Loading, error, and empty states are each clearly communicated.

**Issues:** Browse open GitHub issues for the project. Click an issue to open it in the browser. Loading, error, and empty states are each clearly communicated.

**Open Editors:** See all open files. Click to switch between them. Collapse or expand the list.

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

## Machine Connections (IN PROGRESS)

Connect to remote machines by host and workspace path. Pair a new machine with a code. Save and manage connection profiles. Forget all saved profiles at once.

---

## App Shell

Cmd/Ctrl+P opens file search. Cmd/Ctrl+Shift+P opens the command palette. Cmd/Ctrl+, opens preferences. AI approval requests surface as a modal with allow and deny.


