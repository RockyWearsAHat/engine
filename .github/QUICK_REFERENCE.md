# Quick Reference — Autonomous Orchestration System

A one-page visual guide to Engine's new autonomous agent system.

## The Five Specialized Agents

```
┌─────────────────────────────────────────────────────────────────────┐
│                         ORCHESTRATOR (Sonnet)                       │
│  Detects intent • Decomposes tasks • Routes to specialists           │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐               │
│  │ ARCHITECT    │  │ IMPLEMENTER  │  │ TESTER       │               │
│  │ (Sonnet)     │  │ (Haiku)      │  │ (Haiku)      │               │
│  │              │  │              │  │              │               │
│  │ Validates    │  │ Writes code  │  │ Runs app     │               │
│  │ design       │  │ from plan    │  │ Observes     │               │
│  │              │  │              │  │ behavior     │               │
│  └──────────────┘  └──────────────┘  └──────────────┘               │
│                                                                      │
│  ┌──────────────┐                                                    │
│  │ DOCUMENTER   │                                                    │
│  │ (Haiku)      │                                                    │
│  │              │                                                    │
│  │ Updates docs │                                                    │
│  │ Keeps sync   │                                                    │
│  └──────────────┘                                                    │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

## Example Workflow: GitHub Issue → Fixed

```
USER: "Fix #42"
  ↓
ORCHESTRATOR reads issue & decomposes:
  ├─ Phase 1: Understand (TESTER runs app, observes issue)
  ├─ Phase 2: Design (ARCHITECT validates approach)
  ├─ Phase 3: Code (IMPLEMENTER writes changes)
  ├─ Phase 4: Test (TESTER verifies fix works)
  └─ Phase 5: Docs (DOCUMENTER updates README, etc)
  ↓
RESULT: Fixed code + PR + passing tests + updated docs
```

## Autonomy Levels (User Configurable)

| Level | What Happens | When to Use |
|-------|---|---|
| **`auto`** | Agent acts fully alone | Documentation, safe changes |
| **`observe_after`** | Agent acts, reports, waits for OK | Testing, observing behavior |
| **`approve`** | Get approval first, then act | Bug fixes, important changes |
| **`architect_approve`** | Architect must review design | Refactors, structural changes |
| **`full_approve`** | All gates need human approval | Main branch, breaking changes |

## Configuration Example

```yaml
# engine.yml - Set your autonomy level
autonomy:
  bug_fixes: "approve"             # Require approval before fixing
  tests: "observe_after"           # Run, report, wait for OK
  documentation: "auto"            # Update docs automatically
  refactors: "architect_approve"   # Architect must sign off
  new_features: "full_approve"     # Full approval needed
  experimental: "auto"             # Full autonomy on exp branches
```

## Cost-Per-Issue Estimate

**Simple bug fix:**
- Orchestrator (1 min): $0.05
- Architect review (2 min): $0.12
- Implementation (3 min): $0.08
- Testing (2 min): $0.05
- Documentation (1 min): $0.03
- **Total: ~$0.33 per issue**

**Complex refactor:**
- Orchestrator (3 min): $0.15
- Architect (5 min): $0.30
- Implementation (8 min): $0.20
- Testing (5 min): $0.12
- Documentation (2 min): $0.05
- **Total: ~$0.82 per issue**

## Human Oversight — The Control Panel

Always available:

```
User commands:
  "halt"              → Stop all work immediately
  "skip phase"        → Jump to next phase
  "reject"            → Reject current proposal
  "retry"             → Try again
  "approve"           → Greenlight proposed changes
```

## Key Principles

✅ **Autonomous by default** — Agents work 24/7 without human input  
✅ **Human in the loop by choice** — Users set autonomy level  
✅ **Transparent** — All decisions logged and explainable  
✅ **Safe** — Approval gates at critical points  
✅ **Efficient** — Specialized models for specific tasks  
✅ **Recoverable** — Always can halt, revert, or redirect  

## Implementation Status

| Phase | Task | Status |
|-------|------|--------|
| 1 | Design orchestration system | ✅ Complete |
| 1 | Spec 5 specialized agents | ✅ Complete |
| 1 | Document human-in-the-loop patterns | ✅ Complete |
| 2 | Implement Orchestrator agent | ⏳ Pending |
| 2 | Implement Tester agent | ⏳ Pending |
| 3 | Add autonomy configuration | ⏳ Pending |
| 3 | GitHub issue trigger detection | ⏳ Pending |
| 4 | Optimize agent routing | ⏳ Future |

## How It Differs from V1

### Before (Today)
```
User: "I need to fix this"
Copilot: [reads code, makes changes, tells user to test]
User: [manually tests, finds issues, asks Copilot to fix more]
User: [writes docs, updates tracking]
Total human time: 30-60 minutes per issue
```

### After (Autonomous System)
```
User: "Fix #42" or "Fix this"
Orchestrator: [detects intent, decompose, routes]
├─ Tester: [runs app, observes bug]
├─ Architect: [validates fix design]
├─ Implementer: [writes code]
├─ Tester: [verifies fix works]
└─ Documenter: [updates docs]
System: [creates PR, merges if approved]
Total human time: 5 minutes (review results)
Result ready in: 15-20 minutes
```

## Reading Material

**Must read:**
1. `.github/AUTONOMOUS_ORCHESTRATION.md` — Full vision & design
2. `.github/AGENT_CONFIGURATION.md` — Agent specs & prompts
3. `.github/IMPLEMENTATION_GUIDE.md` — Code patterns

**References:**
- OpenClaw's Kelly: Builds $2,000-per-app in 15 minutes
- Anthropic: "Building Effective Agents" (Dec 2024)
- Human-AI partnership theory

---

**Next Step**: Begin Orchestrator agent implementation this week.
