# Engine — Implementation Roadmap

Source of truth for what Engine is building and in what order. Every phase lists the goal, concrete deliverables, and the decisions / risks that must not be skipped. See [PROJECT_GOAL.md](../PROJECT_GOAL.md) for the vision.

**Core rule:** AI-native, not AI-attached. If a phase tempts us into "bolt it on like VS Code does," stop and redesign.

---

## Original 5-Point Intent (preserved)

1. Full VS Code + Copilot-style editor. Prefer our own orchestration layer / harness over wrapping OpenClaude so we keep full control.
2. GitHub login inside the editor with persistent tokens.
3. With GitHub linked, monitor the user's repos. Any new repo whose README tags `@configuredAgentName` is picked up automatically by the background agent and implemented from the README idea.
4. Output is production-grade: great UI, tested, clean code, proper README, proper version control, zero data leakage. After initial build, keep iterating as new issues flow in.
5. Discord as a remote control surface. One server, a channel per project, clarifying questions over DM/threads. Token discipline throughout — short prompts, caveman-fine, because tokens cost money and time.

Everything below is the step-by-step plan for realizing those five points plus the things they implied but didn't spell out.

---

## Phase 0 — Foundation (current state)

Already standing:
- Tauri desktop shell + React client + Go server + WebSocket bridge
- File tree, editor surface, terminal, AI chat panel, connection profiles
- Session smoke tests, typecheck, coverage tooling

**Exit criteria (must be true before Phase 1 lands):**
- `pnpm dev:desktop` boots to a usable editor with zero console errors
- Server ↔ client WS handshake is authenticated (not just trusted-local)
- One end-to-end behavioral test opens a file, edits it, and saves via the AI path
- Session log + project direction are persisted across restarts (not just in-memory)

---

## Phase 1 — AI-Native Editor Core

**Goal:** The AI is the primary interface. No second-class "chat panel." The same surface that renders text also renders agent actions, diffs, tool calls, and validations inline.

### 1.1 Agent Harness (own it, don't rent it)
- Build our own orchestration layer. Do **not** depend on OpenClaude, Aider, or similar as the primary driver — wrap them only as swappable model backends.
- Harness responsibilities: tool registry, tool-call validation, retry/backoff, cost accounting, streaming, cancellation, structured event log.
- Every tool has a JSON schema, a timeout, a dry-run mode, and a permission scope. No free-form shell without an explicit scope grant.

### 1.2 Context & Memory Architecture
- **Three tiers**, written explicitly, not inferred:
  1. *Project direction* — living summary of goal, constraints, architecture decisions. Updated on meaningful milestones.
  2. *Session log* — append-only JSONL of actions, outcomes, surprise scores (Engram-style). Survives restarts.
  3. *Working context* — what the model sees right now. Small by design.
- **Token discipline:** short, dense prompts. Compression step before every model call. Budget per call is configured, enforced, and logged.
- Retrieval is deterministic (TF-IDF / embeddings with stable tie-break), not vibe-based.

### 1.3 Tool Reliability
- Tools return structured results with `ok | error | needs_input`, never opaque strings.
- Every tool has a behavioral test (`tester` agent runs it, not just unit tests).
- A tool that fails twice in a row is quarantined and surfaced to the user; the agent must not silently retry forever.

### 1.4 Workspace & Branch Isolation
- Git worktree per agent session. One chat = one branch = one worktree. Non-negotiable.
- Agent cannot write outside the workspace root. Enforced at the Go server boundary, not just in prompt text.
- Baseline branch restore on session end. Never leave the user on a feature branch.

### 1.5 Behavioral Validation (not just unit tests)
- `tester` agent can: boot the app, drive UI, read logs, form hypothesis, re-run, compare. Core, not optional.
- Every fix claim must cite: what was reproduced, what was changed, what was re-run, what output proves it.
- "The tests pass" is insufficient if no test covers the actual reported behavior — agent must author the missing test.

### 1.6 Universal Access
- Server runs on the user's machine; client connects from anywhere (desktop, web, mobile).
- Mobile is a first-class client surface, not a responsive afterthought. Design the WS protocol to survive flaky networks (resume, idempotent commands, offline queue).

**Exit criteria for Phase 1:**
- A fresh user can open a repo, ask the AI to implement a feature, and the AI edits, runs, validates, and commits — without switching out of Engine.
- The agent recovers cleanly from a killed server, a dropped WS, and a failed tool call.

---

## Phase 2 — GitHub Identity & Persistence

**Goal:** Engine is tied to a GitHub identity and can act on that identity's repos safely.

### 2.1 Auth
- GitHub OAuth device flow (works on mobile, works headless).
- Tokens stored in OS keychain (macOS Keychain, Windows Credential Manager, libsecret). Never plaintext on disk.
- Scope minimization: start with `repo` + `read:user`; add scopes only when a feature needs them and the user consents per-scope.
- Token refresh + revocation handling. Revoked token = immediate agent halt, not silent failure.

### 2.2 Identity Model
- One Engine instance = one primary GitHub identity. Multi-account later, not now.
- Every git commit the agent makes is signed (SSH or GPG) with a key tied to the instance. No anonymous commits.
- Clear audit log per repo: which agent, which session, which commit, which prompt triggered it.

### 2.3 Secret Hygiene
- Scan staged diffs for secrets before any push. Block on match. Configurable allowlist.
- `.env` and equivalent patterns never read into model context without explicit user approval.

---

## Phase 3 — Autonomous Repo Monitoring

**Goal:** New repo tagged `@configuredAgentName` in the README → Engine starts working. Existing repo gets an issue → Engine triages and (if configured) fixes.

### 3.1 Event Ingestion (webhooks > polling)
- Prefer GitHub App + webhooks over polling. Polling is fallback only when the user can't host a webhook endpoint.
- Webhook endpoint runs inside Engine server; Tailscale / cloudflared tunnel exposes it if needed.
- Every event has an idempotency key. Replayed webhooks must not double-trigger.

### 3.2 Trigger Semantics
- `@configuredAgentName` in README → project scaffold trigger.
- Issue with configured label (default `engine:fix`) → fix trigger.
- PR review comment mentioning the agent → iteration trigger.
- All triggers are **opt-in per repo**. Never act on a repo the user did not explicitly enroll.

### 3.3 Project Scaffolding Pipeline
- Stages: *understand README → ask clarifying questions → choose stack → scaffold → implement → test → document → open PR*.
- Each stage is a checkpoint. User can pause, inspect, redirect.
- Scaffold templates are versioned and auditable; the agent does not invent project structure from scratch every time.

### 3.4 Iteration Loop
- After initial build, Engine subscribes to: new issues, failed CI, dependabot alerts, user comments.
- Each triggers a bounded work session. Work sessions have hard cost caps (tokens + wall clock) enforced by the harness.

### 3.5 Safety Rails
- Dry-run mode per repo: agent proposes PRs but never merges.
- Max concurrent sessions per repo (default 1). Prevents two agents fighting over a branch.
- Blast-radius check: deleting files, rewriting history, force-pushing — all require user confirmation through the editor or Discord.

---

## Phase 4 — Production-Quality Output

**Goal:** What the agent ships is actually good. Not a demo. Not a toy.

### 4.1 Quality Gates (all must pass before PR is opened)
- Typecheck clean, lint clean, tests pass, coverage meets configured threshold
- README reflects the actual code, not the prompt
- License, `.gitignore`, CI config appropriate for the stack
- No committed secrets, no committed build artifacts
- UI work: screenshots captured and attached to the PR, a11y pass run

### 4.2 Data Isolation Between Projects
- Each monitored repo has its own session log, project direction, and knowledge notes.
- Zero cross-contamination: Repo A's context never enters Repo B's prompts.
- Enforced at the harness layer, tested explicitly.

### 4.3 Observability
- Per-session trace: model calls, tool calls, file diffs, cost, duration.
- Traces are queryable from inside the editor (`show me what happened in session X`).
- Error budget per repo: too many failed sessions → auto-pause + user alert.

### 4.4 Version Control Discipline
- Branch per task. Descriptive names (`feat/`, `fix/`, `chore/`).
- Commits are small, messages are written from the diff (not the prompt).
- `main` / `dev` is never committed to directly by the agent — always via PR.

---

## Phase 5 — Discord Control Plane

**Goal:** Chat with Engine from anywhere. Discord is the remote UI for the agent.

### 5.1 Bot Architecture
- One Discord server per Engine instance (user's own server, bot invited with minimal perms).
- One channel per active project, auto-created on enrollment. Channel name mirrors repo name.
- Threaded conversations per session so multiple concurrent sessions don't collide.

### 5.2 Capabilities
- Status: `what are you working on`, `show last commit`, `show failing test`
- Control: `pause repo X`, `resume`, `approve PR #N`, `abort session`
- Q&A: the agent asks the user clarifying questions mid-task and waits
- File / screenshot delivery for UI work

### 5.3 Security
- Commands are scoped to the Discord user's linked GitHub identity.
- No destructive action from Discord without explicit confirmation reaction.
- Rate-limited per user; bot cannot be turned into a DoS vector against the editor.

### 5.4 Token Discipline in Chat
- Discord replies obey the same compression rules as internal prompts. Short, dense, caveman-fine.
- Long outputs posted as attached files, not 40-message walls.

---

## Cross-Cutting Concerns (apply to every phase)

Easy to forget, expensive to add later. Treat as acceptance criteria, not wishlist items.

### Security & Sandboxing
- Agent filesystem access jailed to the workspace root at the Go layer.
- Network access allowlisted per tool (e.g., `fetch` hits public HTTPS; `git` only GitHub).
- Shell commands go through a reviewed allowlist first; arbitrary exec is a privileged scope.
- All tokens / keys live in OS keychain.

### Cost Controls
- Per-session, per-repo, per-day token and dollar caps. Hard-stop at cap, not soft warning.
- Local models (Ollama) preferred for high-volume low-stakes steps; paid providers for the closer.
- Every paid call logs `model, input_tokens, output_tokens, cost_estimate`.

### Failure Recovery
- Crash-safe session state: if the server dies mid-session, the next boot resumes or cleanly aborts. No zombie branches.
- Retry policy is explicit and capped. No infinite loops.
- High-surprise failures are logged to session memory so the next agent doesn't repeat them.

### Privacy & Data Leakage
- Default: code never leaves the user's machine except via the model API the user explicitly chose.
- Telemetry opt-in, off by default, never includes source code.
- "What left the machine in this session" report per session.

### Testing Beyond Unit Tests
- Unit tests for modules.
- Integration tests for editor ↔ server.
- Behavioral tests: `tester` agent runs the app, discovers correctness criteria from PROJECT_GOAL, validates.
- Smoke tests run on every commit to Engine itself.

### Performance
- Editor input latency target: <16ms for keystrokes, <150ms for AI-streamed tokens to render.
- WS protocol supports backpressure; client never freezes because server is streaming too fast.
- File tree and search scale to >100k files without blocking.

### UX Principles
- One surface. AI actions, diffs, tool results render inline in the same view as code.
- Every agent action is inspectable and reversible (diff view + one-click revert).
- Status always visible: which session is running, on which branch, how much it has cost.

### Extensibility
- Tools are plugins with schemas — adding one does not require a fork.
- Model backends are swappable (OpenAI, Anthropic, local Ollama, Azure) behind one interface.
- Project direction and session log formats are versioned; migrations are written, not assumed.

---

## Milestone Order (do not reorder without a reason)

1. **M1 — Solid Phase 0 exit** (foundation hardened, auth'd WS, persisted session)
2. **M2 — Phase 1.1 + 1.2 + 1.3** (harness, memory, tools) — the *brain*
3. **M3 — Phase 1.4 + 1.5** (isolation, behavioral validation) — the *safety*
4. **M4 — Phase 2** (GitHub identity) — the *hands on the outside world*
5. **M5 — Phase 3** (autonomous monitoring) with dry-run default
6. **M6 — Phase 4** (quality gates) before removing dry-run default
7. **M7 — Phase 5** (Discord) once M5/M6 are stable
8. **M8 — Mobile client polish + multi-repo concurrency**

Each milestone ships with: working demo, test coverage, updated `copilot-instructions.md`, and a retrospective entry in session memory.

---

## What the Original Roadmap Implied but Didn't Spell Out

Captured so we don't lose them:

- **Own orchestration means own observability.** If we control the harness, we must give ourselves traces — otherwise debugging is worse than Copilot.
- **"Zero data leakage" needs a definition.** Source never transmitted except to the explicitly selected model provider; no telemetry of code; no cross-repo context bleed.
- **"Complete functionality" needs gates.** Without automated quality gates, "complete" drifts to "compiled." Gate on tests, docs, CI, and behavioral validation.
- **Discord organization implies project lifecycle.** Creating a channel per project also means archiving on repo archive, renaming on rename, handling deleted repos gracefully.
- **Mobile access implies resume semantics.** A phone on cellular will drop the WS. The protocol must treat disconnect as normal, not exceptional.
- **"Less tokens is better" implies a compressor.** Need an actual summarization pass, not just shorter system prompts. Budget it, measure it, test it.
- **Monitoring other people's repos is dangerous.** Decide: only repos the user owns, or also collaborator repos? Default owner-only until we have proper scope UI.
- **The agent will sometimes be wrong.** A clean undo surface is as important as the forward path. Every action reversible, every session abortable.