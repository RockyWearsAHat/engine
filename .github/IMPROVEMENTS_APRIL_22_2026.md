# Engine Improvements — April 22, 2026

## Summary

Two major improvements to Engine were implemented today:

### 1. **Settings Menu Visual Refinement**

The preferences panel was redesigned to feel cleaner, lighter, and more refined by:

- **Removed visual noise**: Replaced box-based section navigation with a clean underline tab style
- **Better spacing hierarchy**: Increased consistent spacing between sections (28px → 18px gaps)
- **Simplified borders**: Reduced borders throughout, using subtle lines only where necessary
- **Refined inputs**: Lightened input backgrounds, improved border colors, added rounded corners
- **Updated typography**: Made labels uppercase and letter-spaced for clarity
- **Better toggle switches**: Improved size, color, and animation of toggle controls
- **Message styling**: Made messages cleaner with subtle backgrounds instead of stark borders
- **Chip buttons**: Changed from underline style to proper button styling with refined hover states

**Visual impact**: The preferences panel now feels like a modern settings interface instead of a technical config tool. It's lighter, less cluttered, and more approachable.

**Files modified**:
- `packages/client/src/index.css` — All styling updates
- `packages/client/src/components/Preferences/PreferencesPanel.tsx` — Input styling refinement

### 2. **Autonomous Orchestration Architecture — Kelly/OpenClaw Pattern**

Engine now has a documented roadmap for becoming a truly autonomous system inspired by Kelly's $2,000-per-app orchestration pipeline. This includes:

#### Core Innovation: Orchestrator-Worker Pipeline

Instead of one monolithic AI making all decisions, Engine uses **specialized agents** routed by a central orchestrator:

- **Orchestrator**: Understands intent, decomposes tasks, coordinates workers
- **Architect**: Validates designs, approves structural changes
- **Tester**: Runs the application, observes behavior, validates fixes
- **Implementer**: Writes code from approved designs
- **Documenter**: Keeps docs in sync with reality

Each agent is optimized for its specific task:
- Haiku for testing, implementation, documentation (fast, cost-effective)
- Sonnet for planning, architecture decisions, synthesis (deep reasoning)

#### Human-Out-Of-Loop Philosophy

"Human out of the loop as much as they want to be" — Users configure autonomy level:

```yaml
autonomy:
  bug_fixes: "approve"           # Require approval before applying
  tests: "observe_after"         # Run tests, report, wait for confirmation
  documentation: "auto"          # Automatically update docs
  refactors: "architect_approve" # Requires Architect sign-off
  new_features: "full_approve"   # All gates require approval
  experimental: "auto"           # Full autonomy on experimental branches
```

#### Example Workflow

```
User: "Fix #42"
    ↓
[Orchestrator] Decomposes task into phases
    ├─ Phase 1: Understand (route to Tester)
    ├─ Phase 2: Design (route to Architect, get approval)
    ├─ Phase 3: Implement (route to Implementer)
    ├─ Phase 4: Validate (route to Tester)
    └─ Phase 5: Document (route to Documenter)
    ↓
Result: Fixed code + PR + passing tests + updated docs
(15 minutes, ~5 minutes of human time, fully transparent)
```

#### Key Metrics (Target)

- **Time to fix**: ≤ 20 minutes (Kelly's baseline)
- **Human involvement**: ≤ 5 minutes per issue
- **Correctness**: All fixes verified by automated tests
- **Transparency**: Humans can understand every decision

#### What This Solves

The essay from OpenClaw/Kelly describes a fundamental insight: **autonomous agents are only viable when humans handle what agents cannot** (identity, accounts, legal presence) **and agents handle what humans struggle with** (24/7 operation, parallelization, systematic thinking).

For Engine, this means:
- Agents work 24/7 on GitHub issues without human intervention
- Issues are fixed and merged before the developer wakes up
- Humans maintain veto power at critical gates
- The system becomes a "second developer" that never sleeps

**Files created**:
- `.github/AUTONOMOUS_ORCHESTRATION.md` — Complete system design
- `.github/AGENT_CONFIGURATION.md` — Specialized agent specs and prompts
- `.github/IMPLEMENTATION_GUIDE.md` — Practical code patterns to implement

## Implementation Roadmap

### Phase 1: Foundation (Current)
- [x] Design complete autonomy architecture
- [x] Spec out 5 specialized agents
- [x] Document human-in-the-loop patterns
- [ ] Implement Orchestrator agent
- [ ] Implement Tester agent

### Phase 2: Specialization (Weeks 2-3)
- [ ] Implement Architect agent
- [ ] Implement Implementer agent
- [ ] Implement Documenter agent
- [ ] Connect agents with routing logic

### Phase 3: Autonomy (Weeks 4-5)
- [ ] GitHub issue trigger detection
- [ ] Autonomy level configuration
- [ ] Approval gates and checkpoints
- [ ] Comprehensive logging

### Phase 4: Intelligence (Weeks 6+)
- [ ] Learn from project direction
- [ ] Detect common failure patterns
- [ ] Optimize agent selection (cost + speed)
- [ ] Build internal knowledge base

## Technical Details

### Settings Menu Changes

**Before**:
- Box-like section buttons with pill shape
- Heavy borders between every field
- Inconsistent spacing (14px, 18px, 10px gaps)
- Basic input styling
- Underline chips without clear active state

**After**:
- Clean tab-style navigation with bottom border underline
- Subtle separators between major sections only
- Consistent spacing throughout (16-28px)
- Refined input styling with better contrast
- Proper button-style chips with hover states and clear active state
- Improved toggle switch styling
- Better visual hierarchy with updated typography

**CSS Changes**:
- Reduced borders: Many `1px solid var(--border)` removed
- Better spacing: Increased `gap` values, standardized padding
- Typography: Made labels uppercase with letter-spacing
- Colors: Lighter input backgrounds, more subtle borders
- Radius: Added rounded corners (4px) to chips and inputs

### Orchestration Architecture

**Key Design Decisions**:

1. **Orchestrator-Worker vs. Single Agent**
   - ✓ Specialized models perform better at specific tasks
   - ✓ Parallelize independent subtasks
   - ✓ Route to cheapest capable model
   - ✗ Single agent tries to do everything and fails at some

2. **Why Autonomy With Human Oversight?**
   - Accountability: Humans remain responsible for deployed code
   - Trust: Humans can halt if behavior unexpected
   - Learning: Humans guide agent behavior through feedback
   - Identity: Only humans interact with external systems

3. **Model Selection Strategy**
   - Haiku for: Testing (fast observation), Implementation (quick writing), Documentation (summarization)
   - Sonnet for: Architecture (deep design), Orchestration (complex planning), Synthesis (multi-step reasoning)
   - Never Opus for these tasks (cost not justified)

## How to Use These Improvements

### Settings Menu
1. Open Settings (⌘+, or Menu → Settings)
2. Notice cleaner tab navigation
3. Sections feel more spacious and refined
4. Inputs look lighter and more integrated

### Autonomous System
1. Read `.github/AUTONOMOUS_ORCHESTRATION.md` for vision
2. Review `.github/AGENT_CONFIGURATION.md` for agent specs
3. Study `.github/IMPLEMENTATION_GUIDE.md` for code patterns
4. Begin with Orchestrator agent implementation

## References

- **Settings Design**: Modern SaaS patterns (Stripe, Linear, GitHub settings)
- **Autonomous System**: 
  - OpenClaw's Kelly: $2,000-per-app orchestration
  - Anthropic's "Building Effective Agents" (Dec 2024)
  - Human-AI partnership models from research literature

## Next Immediate Actions

1. **Start Orchestrator Implementation** 
   - Add GitHub issue detection
   - Create decomposition prompt
   - Route to worker agents

2. **Implement Tester Agent**
   - Run the application
   - Observe and report behavior
   - Validate fixes

3. **Connect to Chat**
   - Detect "Fix #42" in user messages
   - Invoke orchestrator automatically
   - Show decomposition plan to user

4. **Add Approval Gates**
   - Implement `await askUserApproval()` pattern
   - Store autonomy configuration
   - Log all human decisions

## Status

✅ **Complete**: Design documentation, styling refinement  
⏳ **In Progress**: Documentation creation  
⏸️ **Pending**: Implementation of orchestrator system  

**Owner**: Engine architecture team  
**Next Review**: End of week (implementation progress)  
**Complexity**: High (requires re-architecting agent invocation patterns)  

---

*These improvements transform Engine from a smart editor into an autonomous development partner.*
