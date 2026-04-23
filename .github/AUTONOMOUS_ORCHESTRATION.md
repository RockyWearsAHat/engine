# Autonomous Orchestration System — Engine Architecture

## Vision

Engine adopts the orchestrator-worker pipeline pattern from OpenClaw's Kelly agent architecture. The system should be **autonomous by default, human-in-the-loop by choice** — enabling developers to step away and let specialized agents work continuously, while maintaining the ability to review, approve, and redirect at critical checkpoints.

## Core Pattern: Orchestrator-Worker Pipeline

Inspired by Kelly's $2,000-per-app production pipeline and Anthropic's research on effective agent architectures, Engine uses a routing orchestrator that:

1. **Detects triggers** — GitHub issues, CI failures, code changes, PRs
2. **Decomposes tasks** — Breaks requests into phase-specific subtasks  
3. **Routes to specialists** — Sends each subtask to the most capable agent
4. **Synthesizes results** — Merges outputs into a coherent execution plan
5. **Executes with human oversight** — Requires approval at critical gates (configurable)

## The Five Specialized Agents

### 1. **Orchestrator** (Central Coordinator)
- Watches GitHub Issues, PR comments, and CI status
- Interprets human intent from issue descriptions
- Breaks work into phase-specific subtasks
- Routes to appropriate worker agents
- Synthesizes results and makes go/no-go decisions
- **Model**: Claude Sonnet 4.6 (planning, synthesis, multi-step reasoning)
- **Autonomy**: Medium — can approve non-breaking changes, escalates structural decisions
- **Checkpoint gates**: 
  - Before creating feature branches
  - Before running destructive operations (deletions, major refactors)
  - Before merging to main

### 2. **Architect** (Design Authority)
- Validates proposed changes against project direction and design principles
- Suggests improvements to code structure, module boundaries, and abstractions
- Approves architectural changes before implementation
- Reviews cross-file impact of changes
- **Model**: Claude Sonnet 4.6 (deep reasoning, design tradeoffs)
- **Autonomy**: High — can suggest, Medium — to approve
- **Checkpoint gates**: Before structural changes, new modules, or breaking API changes

### 3. **Tester** (Behavioral Authority)
- Runs the application autonomously to discover correctness
- Observes behavior and forms hypotheses about failures
- Validates that fixes solve the stated problem
- **Model**: Claude Haiku 4.5 + visual observation (efficient execution, observation)
- **Autonomy**: High — can run apps and test without approval
- **Checkpoint gates**: Reports test results before declaring "done"

### 4. **Implementer** (Code Generation)
- Writes actual code changes based on verified requirements
- Handles multi-file refactors with consistency checks
- Applies fixes across the codebase systematically
- **Model**: Claude Haiku 4.5 (fast, focused generation)
- **Autonomy**: Medium — writes code only after Architect approval of plan
- **Checkpoint gates**: Before pushing to feature branch

### 5. **Documenter** (Knowledge Keeper)
- Keeps README, API docs, and architecture notes in sync with reality
- Detects when code changes require documentation updates
- Maintains accurate project state records
- **Model**: Claude Haiku 4.5 (summarization, consistency)
- **Autonomy**: High — can update docs to match verified code state
- **Checkpoint gates**: Documents changes alongside code commits

## Autonomy Levels

Users configure their autonomy preference per task type:

```yaml
autonomy:
  bug_fixes: "approve"           # Require human approval before applying
  tests: "observe_after"         # Run tests, report results, human reviews
  documentation: "auto"          # Automatically update docs
  refactors: "architect_approve" # Requires Architect approval
  new_features: "full_approve"   # Requires human approval at all gates
  experimental: "auto"           # Full autonomy for experimental branches
```

### Autonomy Options
- **`auto`** — Agent acts fully autonomously, posts results in chat
- **`observe_after`** — Agent acts, then waits for human confirmation before next step
- **`approve`** — Agent prepares work, human must approve before execution
- **`architect_approve`** — Architect must review and sign off
- **`full_approve`** — All gates require human approval

## Example Workflow: GitHub Issue → Fixed

```
Human: "Fix #42 — sidebar not scrolling"
    ↓
[Orchestrator] Receives GitHub issue #42
    • Reads issue description and context
    • Creates feature branch: fix/sidebar-scroll-#42
    • Decomposes into phases:
      Phase 1: Understand the problem (read code, find root cause)
      Phase 2: Design fix (Architect approves)
      Phase 3: Implement (write code)
      Phase 4: Test (Tester validates)
      Phase 5: Document (Documenter updates relevant notes)
    ↓
[Tester] Runs Engine, observes sidebar behavior
    • Scrollbar appears but doesn't work
    • Hypothesis: CSS overflow property broken
    ↓
[Architect] Reviews proposed fix location
    • Approves: "Modify .sidebar CSS in index.css"
    ↓
[Implementer] Writes and applies fix
    • Changes in /packages/client/src/index.css
    • Adds proper browser-specific CSS
    ↓
[Tester] Re-runs Engine to verify fix
    • Scrollbar now works ✓
    • Tests pass ✓
    ↓
[Documenter] Checks if docs need updates
    • Updates TESTED_BEHAVIORS.md with new behavior
    ↓
[Orchestrator] Synthesizes results
    • Creates PR description
    • Runs autonomy gate check
    • If autonomy="approve": waits for human ✓
    • Merges fix into dev branch
    ↓
Human sees: Fixed sidebar scrolling + PR + updated docs
```

## Trigger Detection

The orchestrator continuously monitors:

### GitHub Triggers
- New issues assigned to the system
- Issues with label `ai:auto` (operator opts-in to autonomy)
- PR comments asking for specific actions
- CI failures with stack traces

### Local Triggers
- Code changes detected in git
- Test failures from local runs
- Workspace status changes (branch switches, new sessions)

### Manual Triggers
- User asks in chat: "Fix this"
- User references an issue: "#42"
- User asks for a specific action: "Run tests on this module"

## The Human-Out-Of-Loop Contract

When configured for autonomy, the system promises:

1. **I will tell you what I'm doing** — Decisions are logged and explainable
2. **I will ask before breaking things** — Structural changes get escalated
3. **I will validate my own work** — Tests run automatically, results are visible
4. **I will stop if you say stop** — A single "halt" command pauses everything
5. **I will stay within bounds** — Autonomy is scoped (e.g., feature branches only, no main)

## Implementation Roadmap

### Phase 1: Foundation (Weeks 1-2)
- [ ] Orchestrator agent that can read GitHub issues and decompose tasks
- [ ] Tester agent that can run Engine and observe behavior
- [ ] Manual trigger system ("Fix #42")
- [ ] Simple approval gates (human must type "approve")

### Phase 2: Specialization (Weeks 3-4)
- [ ] Architect agent reviews proposed changes
- [ ] Implementer agent handles multi-file edits consistently
- [ ] Documenter agent detects what docs need updating
- [ ] Parallel execution of independent subtasks

### Phase 3: Autonomy (Weeks 5-6)
- [ ] Configurable autonomy levels per task
- [ ] GitHub trigger detection (auto-respond to issues)
- [ ] Autonomous branch management (create/merge feature branches)
- [ ] Comprehensive logging for human review

### Phase 4: Intelligence (Weeks 7+)
- [ ] Learn from project direction and history
- [ ] Detect patterns of common failures and fix them proactively
- [ ] Optimize which agent gets routed to which task (cost + speed)
- [ ] Build internal project knowledge base

## Key Architectural Decisions

### Why Orchestrator-Worker and Not Single-Agent?
Kelly's approach with Anthropic's validation: **specialized models for specific phases produce better results than one model trying to do everything**. GPT for ideation, Opus for architecture, Codex for implementation — each is optimal for its task.

For Engine:
- Fast testing tasks → Haiku
- Complex design decisions → Sonnet  
- Orchestration + synthesis → Sonnet
- Documentation → Haiku

### Why Human Oversight Matters
Even fully autonomous agents need human integration for:
- **Accountability**: Humans remain responsible for deployed code
- **Trust**: Humans can halt if behavior becomes unexpected
- **Learning**: Humans guide agent behavior through feedback
- **Identity**: Only humans can interact with external systems (GitHub, app stores)

### Why "Out of the Loop" is the Goal
- Developers can sleep, work on other things, or context-switch
- Time-to-fix becomes independent of human availability
- 24/7 automated testing means issues are caught and fixed before sleep
- The system becomes a "second developer" that never gets tired

## Interaction Model

### For Humans Who Want Full Autonomy
```
Human: "Fix #42"
Agent: [Works autonomously for 10-15 min]
Agent: "Done. PR #999 merged to dev. Tests passing."
Human: (comes back later to review what happened)
```

### For Humans Who Want To Watch
```
Human: "Fix #42"
Agent: "Starting work. Feature branch: fix/sidebar-#42"
Agent: "Root cause identified. Proposing fix."
Agent: [Awaits human] "Approved?"
Human: "Looks good, go."
Agent: "Testing..."
Agent: "Done."
```

### For Humans Who Want To Control Each Step
```
Human: "Fix #42 with full approval"
Config: autonomy.bug_fixes = "full_approve"
Agent: [After each phase] "Next step: ___. OK?"
Human: [Approves each step]
```

## Success Metrics

An autonomous agent system succeeds when:

1. **Time to fix** — From issue report to merged PR ≤ 20 minutes (Kelly's baseline)
2. **Human involvement** — ≤ 5 minutes of human time per issue
3. **Correctness** — All fixes are verifiable and tested
4. **Transparency** — Humans can understand what the agent did and why
5. **Safety** — No data loss, no unauthorized changes, always recoverable

## References

- Anthropic. "Building Effective Agents." Dec. 19, 2024. https://www.anthropic.com/research/building-effective-agents
- "These Openclaw Agents Are Making More Money Than You" — Kelly's $2,000-per-app orchestration pattern
- Maes, P. "Designing Autonomous Agents." MIT Press, 1995

---

**Status**: Design phase  
**Next**: Implement orchestrator agent + GitHub issue trigger detection  
**Owner**: Engine architectural team
