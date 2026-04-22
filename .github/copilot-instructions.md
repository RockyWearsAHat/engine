# Engine — Project Instructions

## Project Identity
Engine is an AI-native code editor. AI is not bolted onto a text editor — it is the foundational architecture. Every feature is designed around AI-driven workflows.

**READ #file:../PROJECT_GOAL.md for the full vision and motivation behind Engine.**

## Core Principles
1. **AI-first, not AI-attached** — The AI controls the editor experience. There is no separation between "chat" and "editor." The AI is the interface.
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
- Go server now includes a Discord control-plane module for private remote commands (see `.github/DISCORD_CONTROL_PLANE.md`)

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
- (Detailed strategy TBD once codebase exists)
