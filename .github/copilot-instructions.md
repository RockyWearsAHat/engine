# Engine — Project Instructions

# TALK LIKE A CAVEMAN, ALWAYS, THIS HELPS SAVE CONTEXT, LESS CONTEXT = MORE WORK FOR LESS MONEY!!! Don't use subagents the rate limiting is kinda gnarly right now.

# THE BUILD GATE WILL ALWAYS FAIL IF YOUR CODE IS ANY PERCENT UNTESTED, TO SAVE YOURSELF SOME TURNS ALWAYS WRITE THE TESTS FOR THE INTENDED BEHAVIOR AFTER IMPLEMENTING A BEHAVIOR AND AFTER THE END OF THE TURN AND ALL IMPLEMENTATION IS DONE RUN THE VERIFY GATE TO IDEALLY HAVE IT PASS FIRST TRY.

# ALWAYS CHECK THE WORKING BEHAVIORS IN .github/WORKING_BEHAVIORS.md BEFORE DOING ANYTHING. This file is the user-facing feature contract — it lists every feature the app has, written from the user's perspective (what they can DO), not implementation details. Every feature in that file is tested and enforced. If a feature is missing, it is not guaranteed to work. If the code has a feature not in the file, add it. If the file lists a feature that is broken, fix it. Always report any update to this file to the user. Do NOT write implementation internals, null-safety details, or edge case wiring into this file — only user-visible features and behaviors.

# DOCS MUST MATCH CODE. If a doc describes something not implemented in code, delete or update the doc immediately. Aspirational/roadmap docs are FORBIDDEN in the repo — use session memory or Obsidian Knowledge for future planning only. Any doc that diverges from the actual code is incorrect and must be deleted or corrected on sight.

# Anything out of date, old, unnecessary, or causing bloat in the code should immediately be removed as per CS 3500 principles. Consider asking me (the user) about questionable things that you are unsure if they should be removed, but for the most part, if you stumble across something like an old markdown document that claims something don't take that as proof our code might have changed, and the markdown documents are not generated from code. Know that code could have been created and not linked (shouldn't ever happen) or could be outdated with something newer replacing it, figure out which and remove the old one so we don't suffer from code bloat and an unworkable project due to confusion.

# IF YOU EVER NEED TO WAIT FOR A COMMAND TO FINISH, JUST RUN THE TERMINAL IN NON-ASYNC MODE TO WAIT FOR IT TO FINISH, ANY COMMANDS YOU CAN RUN ASYNC DO SO, BUT FOR A COMPILER, SOMETHING THAT IS SUPPOSED TO BE STEP AFTER STEP, IT'S BETTER TO WAIT FOR THE COMPILE TO FINISH AND THEN CHECK THE OUTPUT, THIS IS ALREADY BEING DONE JUST YOU CAN SAVE MANY TURNS AND THEREFORE TOKENS (WHICH OUR NEXT HEADING POINT IS ALL ABOUT) BY JUST RUNNING COMMANDS YOU NEED TO WAIT ON NON-ASYNC.

# ALL CODE YOU WRITE SHOULD CONSIDER THE UTMOST EFFICIENT WAY TO DO SOMETHING, DO NOT TRY TO NEEDLESSLY OPTIMIZE, BUT O(1) IS SIGNIFICANTLY BETTER THAN SOMETHING THAT IS O(n*n) WHEN RAN ACROSS A DATASET OF MILLIONS. CONSIDER TIME COMPLEXITY AND IF IT CAN BE DROPPED **WITHOUT** FUNCTIONAL DEGREDATION. IF SOMETHING IS PHYSICALLY SLOW (real world seconds) THAT IS BAD AND WE SHOULD CONSIDER REAPPROACHING THE CODE TO MAKE IT LESS SLOW.

THE HALLMARK OF BAD SOFTWARE IS SOFTWARE WRITTEN TO HAVE EDGE CASES THAT **MAY** BUT ARE NOT NECESSARILY ALWAYS HIT THAT CAUSE INCREDIBLY OBTUSE AND INCORRECT BEHAVIOR, OR CODE THAT SHOULD RUN INCREDIBLY QUICKLY AND INSTEAD IT TAKES AGES. BOTH OF THESE SIGNIFY BAD CODE, IF YOU EVER NOTICE THEM ANYWHERE, PAUSE, ANALYZE THAT AREA, CONSIDER WHAT MIGHT BE GOING WRONG, IF IT'S A SIMPLE FIX TO NOT TOUCH LOGIC BUT TOUCH TIME, DO IT AND CONTINUE, OTHERWISE REPORT YOUR FINDINGS TO THE USER FOR THE NEXT REQUEST ALONG WITH WHATEVER YOU WERE GOING TO SAY ANYWAYS IN YOUR FOLLOWUP.

## Project Identity
Engine is an AI-native code editor. AI is not bolted onto a text editor — it is the foundational architecture. Every feature is designed around AI-driven workflows.

**READ #file:../PROJECT_GOAL.md for the full vision and motivation behind Engine.**

## ALWAYS USE message: PARAM ON CHECKPOINT INSTEAD OF context:, THIS SAVES A SUBAGENT MODEL CALL!

## LINT POLICY: DO NOT INSTALL OR USE ESLINT/BIOME/OXC IN THIS REPO. USE gsh strict lint ONLY.

## Core Principles
1. **AI-first, not AI-attached** — The AI autonomously controls the editor (files, terminals, branches, agents) without requiring the human in the loop. Chat and editor are intentionally separate surfaces: the chat panel is the human's communication window with the AI dev lead; the editor is the code surface the AI operates on.
2. **Persistent context** — Full conversation history, project direction summaries, and session state are maintained and referenced automatically.
3. **Autonomous validation** — The AI runs the application, observes behavior, forms hypotheses, and validates fixes. Testing goes beyond unit tests to behavioral discovery.
4. **Reliable tooling** — Every tool works 100% of the time. No flaky tool calls, no ambiguous outputs.
5. **Workspace isolation** — Proper worktree management, branch isolation, and session tracking are built into the core.
6. **External event awareness** — GitHub Issues, CI failures, and other external signals are live inputs that trigger AI action.
7. **Universal access** — The editor runs remotely and works from any device including mobile.

## Architecture Direction
- **Client:** React + TypeScript, Vite, Tailwind CSS (`packages/client`)
- **Server:** Go WebSocket server (`packages/server-go`) — AI routing, git, file system, terminal, Discord, GitHub, DB
- **Desktop:** Tauri (Rust) shell wrapping the client build (`packages/desktop-tauri`)
- **Shared types:** TypeScript (`packages/shared`)
- **Database:** SQLite embedded in Go server — sessions, usage, project direction
- The editor wraps AI capabilities as first-class primitives, not extensions
- Session history and project direction are stored persistently, not just in-memory
- Agent orchestration is a core subsystem, not a plugin
- Go server includes a Discord control-plane module for private remote commands with project-local config in `.engine/discord.json` (see `.github/DISCORD_CONTROL_PLANE.md`)
- Full architecture reference: `obsidian-vault/Engine/Architecture.md`

## Obsidian Memory Usage
The vault at `obsidian-vault/` is the project's living knowledge base. Use it actively — not just as a sync target.

- **Before structural changes:** Read `obsidian-vault/Engine/Architecture.md` and `obsidian-vault/Engine/Knowledge.md` for existing decisions and constraints.
- **After significant decisions:** Add a `## Decision: <topic>` section to `obsidian-vault/Engine/Knowledge.md` with: what was decided, why, alternatives rejected, tradeoffs.
- **After behavior changes:** Update `.github/WORKING_BEHAVIORS.md` + `.github/working-behaviors-test-map.json`, then run `pnpm sync:obsidian`.
- **After any session-memory or behavior changes:** Run `pnpm sync:obsidian` to keep Obsidian current.
- Session events auto-export to `obsidian-vault/Engine/Session Memory.md` via the sync script.
- The Progress Log at `obsidian-vault/Engine/Progress Log.md` is for capturing *why* — use the template for each milestone.

## What AI Should Do
- Always reference project direction and prior conversation context before acting
- Validate changes by running the application, not just checking syntax
- Maintain awareness of the full project state across sessions
- Treat GitHub Issues as actionable tasks, not just references
- Commit at meaningful milestones with descriptive context

## What AI Should NOT Do
- Bolt features onto the codebase without considering the AI-first principle
- Treat this as a traditional text editor with AI features added on top
- Ignore session history or project direction when making decisions
- Skip behavioral validation in favor of only static analysis
- Create abstractions that separate the AI from the editor experience

## Coding Conventions
- CS3500 & 2420 best practices ALWAYS.

## Testing Strategy
- Unit tests for individual modules
- Integration tests for AI-editor interaction
- Behavioral tests: AI runs the app, observes, validates
- For UI tests, prefer one lightweight mount/smoke assertion per surface, then spend the rest of the test budget on real interactions, state changes, websocket/runtime wiring, and persisted side effects.
- Avoid brittle assertions that bind tests to exact copy, incidental layout, or a specific fully rendered static state unless that text/state is the contract being tested.
- Read `.github/WORKING_BEHAVIORS.md` before significant test work. It is the user-facing feature spec — write tests that enforce the features listed there.
  - If observed behavior differs from `.github/WORKING_BEHAVIORS.md`, update that file in the same session.
  - Every write/update to `.github/WORKING_BEHAVIORS.md` must be explicitly reported to the user in the response.
  - WORKING_BEHAVIORS.md is a product feature list, not a test log. Write it as a user would describe what the app does — no internal wiring, no null-safety caveats, no websocket message names.
	- Keep `.github/working-behaviors-test-map.json` in sync with all non-`(IN PROGRESS)` section headings.
	- Run `pnpm sync:obsidian` after behavior contract or session-memory changes so `obsidian-vault/Engine/Working Behaviors.md` and `obsidian-vault/Engine/Session Memory.md` stay current.
- (Detailed strategy TBD once codebase exists)

## Mandatory Completion Gate (Hard Stop)
- Agent must not finish a request until completion gate passes.
- Completion gate requirements:
	- 100% client coverage (statements, branches, functions, lines)
	- 100% Go coverage total
	- lint clean
	- typecheck clean
	- explicit CS 3500 verification attestation
	- explicit request and chat-history completion attestation
	- behavioral gate passed (or skipped if Playwright not installed) — `behavioralGatePassed: true` in report
- Enforced by Stop hook: `.github/hooks/mandatory-completion-gate.json`
- Gate implementation: `scripts/agent-completion-gate.mjs`
- Behavioral check: `scripts/behavioral-completion-check.mjs` (Playwright; skips gracefully if not installed)
	- Internal-only completion report artifact: `.github/session-memory/agent-completion-report.json` (agent/hook telemetry, not user-facing output)
