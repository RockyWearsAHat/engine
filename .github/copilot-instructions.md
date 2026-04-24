# Engine — Project Instructions

# TALK LIKE A CAVEMAN, ALWAYS, THIS HELPS SAVE CONTEXT, LESS CONTEXT = MORE WORK FOR LESS MONEY!!! Don't use subagents the rate limiting is kinda gnarly right now.

# ALWAYS CHECK THE WORKING BEHAVIORS IN .github/WORKING_BEHAVIORS.md BEFORE DOING ANYTHING, THIS IS THE AGREEMENT OF HOW THE CODE SHOULD WORK AND BEHAVE, IF YOU NOTICE ANY DISCREPANCIES IN THE CODE AND THAT DOCUMENT, UPDATE THAT DOCUMENT IMMEDIATELY TO REFLECT THE CURRENT CODE BEHAVIOR AND THEN CONTINUE WITH WHATEVER YOU WERE DOING. This is the single source of truth for how the code should work and behave, if you notice something that is not reflected in there, update it immediately and report that update to the user in your response.

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
- Tech stack: TBD (likely TypeScript/Electron or web-based)
- The editor wraps AI capabilities as first-class primitives, not extensions
- Session history and project direction are stored persistently, not just in-memory
- Agent orchestration is a core subsystem, not a plugin
- Go server now includes a Discord control-plane module for private remote commands with project-local config in `.engine/discord.json` (see `.github/DISCORD_CONTROL_PLANE.md`)

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
- (To be established once tech stack is chosen)
- Prefer clear, traceable code over clever abstractions
- Every module should have a clear single responsibility
- Document architectural decisions and their rationale

## Testing Strategy
- Unit tests for individual modules
- Integration tests for AI-editor interaction
- Behavioral tests: AI runs the app, observes, validates
- For UI tests, prefer one lightweight mount/smoke assertion per surface, then spend the rest of the test budget on real interactions, state changes, websocket/runtime wiring, and persisted side effects.
- Avoid brittle assertions that bind tests to exact copy, incidental layout, or a specific fully rendered static state unless that text/state is the contract being tested.
- Read `.github/WORKING_BEHAVIORS.md` before significant test work and keep it as the quick-reference behavior agreement.
- If observed behavior differs from `.github/WORKING_BEHAVIORS.md`, update that file in the same session.
- Every write/update to `.github/WORKING_BEHAVIORS.md` must be explicitly reported to the user in the response.
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
- Enforced by Stop hook: `.github/hooks/mandatory-completion-gate.json`
- Gate implementation: `scripts/agent-completion-gate.mjs`
	- Internal-only completion report artifact: `.github/session-memory/agent-completion-report.json` (agent/hook telemetry, not user-facing output)
