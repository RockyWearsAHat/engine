# Engine Orchestration — Visual System Architecture

## The System at a Glance

```
┌─────────────────────────────────────────────────────────────────────┐
│                         ENGINE ORCHESTRATION                         │
│                                                                      │
│  User Request: "Fix issue #42"                                      │
│        ↓                                                            │
│  Configuration Resolution                                           │
│  ├─ Request override? (e.g., "use fast team")                      │
│  ├─ Project config? (.engine/config.yaml default_team)            │
│  └─ Global defaults? (built-in fallback)                           │
│        ↓                                                            │
│  ┌────────────────────────────────────────────┐                    │
│  │    ORCHESTRATOR (Chosen Team)              │                    │
│  │  (e.g., claude-opus-4.6)                   │                    │
│  └───┬────────┬────────┬────────┬────────────┘                    │
│      ↓        ↓        ↓        ↓                                   │
│  ┌────────┐┌────────┐┌────────┐┌────────────┐                     │
│  │ARCHITECT││IMPLEM. ││TESTER  ││DOCUMENTER │                     │
│  │(Sonnet)││(Gemma) ││(Haiku) ││(Gemma)    │                     │
│  └────────┘└────────┘└────────┘└────────────┘                     │
│      ↓         ↓       ↓           ↓                                │
│  Reviews   Writes  Tests    Updates                                │
│  Design    Code    Run      Docs                                   │
│      ↓         ↓       ↓           ↓                                │
│  ┌──────────────────────────────────────┐                         │
│  │      RESULT: Pull Request Created     │                         │
│  │      Tests Passing, Docs Updated      │                         │
│  │      Ready for Merge                  │                         │
│  └──────────────────────────────────────┘                         │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

---

## Team Selection Flow

```
User Request
    ↓
┌─────────────────────────────────────────┐
│   Team Resolution (Priority Order)       │
├─────────────────────────────────────────┤
│  1. Runtime Override?                    │
│     "Use fast team"                      │
│     ↓ (if yes → use fast team)           │
│  2. Request Parameter?                   │
│     api call: { team: "premium" }        │
│     ↓ (if yes → use premium team)        │
│  3. Project Default?                     │
│     .engine/config.yaml                  │
│     default_team: "default"              │
│     ↓ (if yes → use default team)        │
│  4. Global Fallback                      │
│     Built-in defaults                    │
│     ↓ (always works)                     │
└──────────┬──────────────────────────────┘
           ↓
    ┌──────────────────┐
    │ RESOLVED TEAM    │
    │ ┌──────────────┐ │
    │ │Orchestrator: │ │
    │ │  claude-opus │ │
    │ ├──────────────┤ │
    │ │Architect:    │ │
    │ │  sonnet      │ │
    │ ├──────────────┤ │
    │ │Implementer:  │ │
    │ │  gemma       │ │
    │ └──────────────┘ │
    │ Cost: $0.30      │
    └──────────────────┘
```

---

## Configuration Inheritance

```
.engine/config.yaml
┌─────────────────────────────────────────┐
│  Project-Level Configuration             │
├─────────────────────────────────────────┤
│  teams:                                   │
│    default:                               │
│      orchestrator: claude-opus            │
│      architect: sonnet                    │
│      implementer: gemma                   │
│                                           │
│  apis:                                    │
│    anthropic:                             │
│      enabled: true                        │
│      max_cost: 5.00                       │
│                                           │
│  autonomy:                                │
│    default: auto                          │
│    tests: auto                            │
│    bug_fixes: observe_after               │
└─────────────────────────────────────────┘
        ↓
   (inherited by)
        ↓
.engine/config.example.yaml
┌─────────────────────────────────────────┐
│  Global Default Configuration             │
├─────────────────────────────────────────┤
│  (same structure, fallback values)       │
│  (used if project config missing)        │
└─────────────────────────────────────────┘
```

---

## Pre-Built Teams Comparison

```
                SPEED    COST      QUALITY   BEST FOR
                ─────    ────      ───────   ────────

DEFAULT:        ▓▓▓ 15m  $ $0.30   ▓▓▓▓      Development
                Gemma(orch) + Sonnet(arch) + Haiku(tester)

FAST:           ▓ 5m    FREE      ▓▓        Quick Testing
                All Gemma (local)

PREMIUM:        ▓▓▓▓ 20m $$$1.05  ▓▓▓▓▓     Critical Features
                All Claude (Opus)

OPENAI:         ▓▓▓ 15m  $ $0.50   ▓▓▓▓      GPT Preference
                GPT-5.4 (orch) + GPT-4o (others)
```

---

## Cost Breakdown (Default Team)

```
┌─────────────────────────────────────────┐
│  Per-Issue Cost Analysis (Default Team)  │
├─────────────────────────────────────────┤
│                                           │
│  Orchestrator (Gemma)       LOCAL  $0.00 │
│  ├─ Plan task: 2 min                    │
│  ├─ Delegate work                       │
│  └─ Review results                      │
│                                           │
│  Architect (Sonnet)        CLAUDE $0.10 │
│  ├─ Review design                       │
│  └─ Suggest improvements                │
│                                           │
│  Implementer (Gemma)        LOCAL  $0.00 │
│  ├─ Write code                          │
│  ├─ Apply changes                       │
│  └─ Commit                              │
│                                           │
│  Tester (Haiku)            CLAUDE $0.15 │
│  ├─ Run tests                           │
│  ├─ Validate behavior                   │
│  └─ Report issues                       │
│                                           │
│  Documenter (Gemma)         LOCAL  $0.00 │
│  ├─ Update comments                     │
│  └─ Update README                       │
│                                           │
├─────────────────────────────────────────┤
│  TOTAL PER ISSUE:                 $0.30 │
│  Monthly (100 issues):           $30.00 │
└─────────────────────────────────────────┘
```

---

## User Command Flow

```
User Input: "Fix issue #42 with premium team"
    ↓
┌──────────────────────────────────────────┐
│  Parse Command                            │
├──────────────────────────────────────────┤
│  action: "fix_issue"                      │
│  issue: "#42"                             │
│  team_override: "premium"                 │
└───────┬────────────────────────────────────┘
        ↓
┌──────────────────────────────────────────┐
│  Resolve Team                             │
├──────────────────────────────────────────┤
│  Check: premium team in config?           │
│  ✓ Yes → Use premium team                │
└───────┬────────────────────────────────────┘
        ↓
┌──────────────────────────────────────────┐
│  Load Configuration                       │
├──────────────────────────────────────────┤
│  premium:                                 │
│    orchestrator: claude-opus              │
│    architect: claude-opus                 │
│    implementer: claude-sonnet             │
│    tester: claude-haiku                   │
│    documenter: claude-haiku               │
└───────┬────────────────────────────────────┘
        ↓
┌──────────────────────────────────────────┐
│  Check Cost Budget                        │
├──────────────────────────────────────────┤
│  max_cost_per_issue: $5.00                │
│  estimated_cost: $1.05                    │
│  ✓ Within budget → Proceed                │
└───────┬────────────────────────────────────┘
        ↓
┌──────────────────────────────────────────┐
│  Spawn Agent Team                         │
├──────────────────────────────────────────┤
│  ✓ Orchestrator (Opus) started            │
│  ✓ Architect (Opus) ready                 │
│  ✓ Implementer (Sonnet) ready             │
│  ✓ Tester (Haiku) ready                   │
│  ✓ Documenter (Haiku) ready               │
└───────┬────────────────────────────────────┘
        ↓
    [Agents work...]
        ↓
┌──────────────────────────────────────────┐
│  Result                                   │
├──────────────────────────────────────────┤
│  ✓ Issue fixed                            │
│  ✓ Tests passing                          │
│  ✓ PR created                             │
│  ✓ Docs updated                           │
│  ✓ Cost: $1.03 (under budget)             │
└──────────────────────────────────────────┘
```

---

## Autonomy Level Workflow

```
Default: "auto" (autonomous)
    ↓
[Agents execute]
    ↓
[Commit and continue]

┌─────────────────────────────────────────┐
      vs.
┌─────────────────────────────────────────┐

observe_after (execute then notify)
    ↓
[Agents execute]
    ↓
[Report results]
    ↓
⏸ Wait for user approval
    ↓
[User: "OK, continue"]
    ↓
[Proceed to next step]

┌─────────────────────────────────────────┐
      vs.
┌─────────────────────────────────────────┐

full_approve (ask before each action)
    ↓
[Propose action]
    ↓
⏸ Wait for user approval
    ↓
[User: "Yes, do it"]
    ↓
[Execute single action]
    ↓
[Repeat for each action]
```

---

## Documentation Map (Visual)

```
                    .engine/ (User Entry Point)
                          │
        ┌─────────────────┼─────────────────┐
        ↓                 ↓                 ↓
    QUICKSTART.md    README.md        INDEX.md
    (5 min setup)   (Quick ref)    (Navigation)
        │                 │              │
        └─────────────────┼──────────────┘
                          ↓
                  config.example.yaml
                   (Copy to .yaml)
                          │
        ┌─────────────────┼─────────────────┐
        ↓                 ↓                 ↓
   SUMMARY.md    IMPLEMENTATION_    SYSTEM_VISION.md
   (Overview)     STATUS.md          (Big picture)
                  (Roadmap)
                          │
                          ↓
                  .github/ (Developer Docs)
                          │
        ┌─────────────────┼──────────────┬──────────────┐
        ↓                 ↓              ↓              ↓
  TEAM_ARCH.md  AUTONOMOUS_   AGENT_CONFIG.md   IMPLEMENT.md
  (Technical)   ORCH.md         (Specs)          (Code)
               (Vision)
```

---

## Agent Interaction Diagram

```
        Orchestrator
            │
    ┌───────┼───────┬────────┬──────────┐
    │       │       │        │          │
    ↓       ↓       ↓        ↓          ↓
  [Ask]  [Ask]   [Ask]    [Ask]     [Ask]
    │       │       │        │          │
    ↓       ↓       ↓        ↓          ↓
Architect Implem  Tester  Documen   (Others)
 │Review │Write │Run    │Update
 │ Design│Code  │Tests  │Docs
 │       │      │       │
 ↓       ↓      ↓       ↓
[Results][Code][Pass/Fail][Updated]
    │       │       │        │
    └───────┼───────┼────────┘
            ↓
      Orchestrator
      (Aggregate)
            │
            ↓
         Result
```

---

## Configuration Edit Example

```
# User edits .engine/config.yaml

teams:
  my_custom_team:
    orchestrator:
      model: "anthropic:claude-opus-4.6"
      timeout_seconds: 180
    architect:
      model: "anthropic:claude-sonnet-4.6"
      timeout_seconds: 120
    implementer:
      model: "openai:gpt-5.4"
      timeout_seconds: 300
    tester:
      model: "anthropic:claude-haiku-4.5"
      timeout_seconds: 600
    documenter:
      model: "ollama:gemma4:31b"
      timeout_seconds: 90

# Then user can say:
# "Fix this with my_custom_team"
```

---

## Permission Scoping Example

```
User Request: "Fix issue #42"
    ↓
Resolve Team Config
    ↓
Get Permissions:
    ├─ read_files: true         ✓ Can read project files
    ├─ write_files: true        ✓ Can edit files
    ├─ invoke_tools: true       ✓ Can use build tools
    ├─ create_files: true       ✓ Can create new files
    ├─ delete_files: false      ✗ Cannot delete
    ├─ push_to_remote: false    ✗ Cannot push to remote
    └─ merge_main: false        ✗ Cannot merge main branch
    ↓
Agents execute with these constraints
    ↓
Can: Read, write, create, build, test
Cannot: Delete, push remote, merge critical branches
```

---

## The Complete Picture

```
     User Request
           │
           ↓
    ┌──────────────┐
    │ Find Team    │
    │ (4 levels)   │
    └──────┬───────┘
           ↓
    ┌──────────────┐
    │Check Budget  │
    │Cost Limit OK?│
    └──────┬───────┘
           ↓
    ┌──────────────┐
    │Spawn Agents  │
    │Per Team Spec │
    └──────┬───────┘
           ↓
    ┌──────────────┐
    │ Orchestrator │
    │ coordinates  │
    └──────┬───────┘
           ↓
    [5 Agents Work]
           ↓
    ┌──────────────┐
    │Track Costs   │
    │vs. Budget    │
    └──────┬───────┘
           ↓
    ┌──────────────┐
    │ Result       │
    │ (with logs)  │
    └──────────────┘
```

---

**The system is modular, comprehensible, and ready to implement.**

See `.engine/INDEX.md` for the complete documentation navigation.
