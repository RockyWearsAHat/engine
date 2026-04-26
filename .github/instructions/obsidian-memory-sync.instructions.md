---
applyTo: "**"
description: "Keeps WORKING_BEHAVIORS mirrored into the workspace Obsidian vault and requires behavior/test-map updates for shipped feature changes."
---

# Obsidian Memory Sync

The vault at `obsidian-vault/` is the project's living knowledge base. It has five notes to maintain:

| Note | Purpose | Updated by |
| --- | --- | --- |
| `Engine/Working Behaviors` | User-facing feature contract | `pnpm sync:obsidian` (after WORKING_BEHAVIORS.md changes) |
| `Engine/Session Memory` | Auto-exported agent event log | `pnpm sync:obsidian` |
| `Engine/Architecture` | Tech stack, module map, key patterns | Agent — when structural changes land |
| `Engine/Knowledge` | Design decisions, discovered constraints, thought processes | Agent — when making non-obvious choices |
| `Engine/Progress Log` | Human and AI progress entries | Agent — at meaningful milestones |

## When to update each note

**Working Behaviors + sync:**
When a shipped behavior changes, update all three artifacts in the same session:
1. `.github/WORKING_BEHAVIORS.md`
2. `.github/working-behaviors-test-map.json`
3. Run `pnpm sync:obsidian`

Every non-`(IN PROGRESS)` section in WORKING_BEHAVIORS.md must have at least one linked test file in the test map.

**Architecture:**
Update `obsidian-vault/Engine/Architecture.md` directly (it is not auto-generated) when:
- A new module or package is added
- A dependency or tech choice changes
- The module map no longer reflects the code

**Knowledge:**
Add a `## Decision: <topic>` block to `obsidian-vault/Engine/Knowledge.md` when:
- Making a non-obvious architectural or design choice
- Discovering a constraint that is not obvious from reading the code
- Rejecting an approach so future sessions don't retry it
- Resolving a tradeoff that required real thought

Format: **Decided**, **Why**, **Rejected alternatives**, **Tradeoffs**.

**Progress Log:**
Add an entry using the `Templates/Progress Entry Template` when:
- A significant milestone completes
- A complex debugging session concludes
- A feature ships

Entries should capture *why* — reasoning, hypotheses, and what was learned — not just *what* changed.

## Rule: sync is cheap, stale vault is expensive

Always run `pnpm sync:obsidian` after behavior contract or session-memory changes. The command is fast and keeps Obsidian accurate for human review and AI context loading.
