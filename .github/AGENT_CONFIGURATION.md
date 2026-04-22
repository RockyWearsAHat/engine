# Specialized Agent Configuration — Engine Autonomous System

This file documents how to configure and invoke each specialized agent in the Engine autonomous orchestration pipeline.

## Agent Roster

### 1. Orchestrator Agent

**Purpose**: Central coordinator that interprets user intent, decomposes tasks, and routes to specialists.

**Invoke when**:
- User references a GitHub issue: "#42"
- User says "Fix this" without specifics
- A CI pipeline fails with a stack trace
- New code is pushed to a feature branch

**Configuration**:
```yaml
agent: orchestrator
model: Claude Sonnet 4.6
context_requirements:
  - GitHub issue text
  - Project direction
  - Current branch state
  - Active sessions
timeout: 5 minutes (initial decomposition)
autonomy_default: "observe_after"
```

**Responsibilities**:
- [ ] Read GitHub issue / understand intent
- [ ] Check project direction to align proposal with goals
- [ ] Create feature branch if needed
- [ ] Break task into phase-specific subtasks
- [ ] Route Phase 1 to Architect
- [ ] Wait for Phase 1 approval, then route Phase 2 to Tester
- [ ] Collect results and synthesize final plan
- [ ] Make go/no-go decision before execution

**Example Orchestrator Prompt**:
```
You are Engine's autonomous orchestrator. Your job is to understand this GitHub issue and plan a solution.

Issue: {{github_issue_text}}

Project direction:
{{project_direction}}

Current project state:
{{git_status}}
{{active_sessions}}

Your task:
1. Understand what the user is asking for
2. Check if it aligns with project direction
3. Propose a plan: What needs to happen? Who should do it? In what order?
4. Get Architect approval for the plan
5. Then execute phases

Output JSON:
{
  "issue_id": "...",
  "issue_title": "...",
  "intent": "...",  // What is the user really asking?
  "alignment": "...",  // How does this fit project goals?
  "plan": [
    {
      "phase": 1,
      "agent": "architect",
      "task": "...",
      "context": {...}
    },
    {
      "phase": 2,
      "agent": "tester",
      "task": "...",
      "context": {...}
    }
  ],
  "feature_branch": "feature/...-#42",
  "autonomy_required": "architect_approve"  // or "full_approve" if structural
}
```

---

### 2. Architect Agent

**Purpose**: Design authority that validates and approves structural changes before implementation.

**Invoke when**:
- Orchestrator has proposed a code change
- Implementation would touch multiple modules
- Structural refactor is proposed
- New abstraction is needed

**Configuration**:
```yaml
agent: architect
model: Claude Sonnet 4.6
context_requirements:
  - Proposed changes
  - Affected modules
  - Current architecture
  - Design principles from instructions
timeout: 3 minutes
autonomy_default: "observe_after"
```

**Responsibilities**:
- [ ] Review proposed change for architectural fit
- [ ] Check for violations of design principles
- [ ] Identify all affected code paths
- [ ] Suggest improvements or alternatives
- [ ] Approve or reject the proposal
- [ ] Document the architectural decision

**Example Architect Prompt**:
```
You are Engine's architecture guardian. Review this proposed change and decide if it's architecturally sound.

Proposed change:
{{proposed_change}}

Affected modules:
{{list_of_files}}

Current architecture:
{{architecture_doc}}

Design principles:
- Single responsibility
- Clear module boundaries
- No circular dependencies
- Shared code extracted to utilities
- Public APIs return abstractions, not implementations

Your evaluation:
1. Does this change violate any design principles?
2. What are all the modules that will be impacted?
3. Are there better approaches?
4. Is this a good time to refactor something related?

Output JSON:
{
  "verdict": "approved|rejected|needs_revision",
  "reasoning": "...",
  "affected_modules": [...],
  "risks": [...],
  "suggestions": [...],
  "go_ahead": true/false
}
```

---

### 3. Tester Agent

**Purpose**: Runs the application autonomously to discover and validate correctness.

**Invoke when**:
- Code has been written and is ready to test
- Orchestrator needs to validate a fix
- A new feature needs behavioral verification

**Configuration**:
```yaml
agent: tester
model: Claude Haiku 4.5  # Fast, efficient observation
context_requirements:
  - What to test (issue description or feature)
  - How to run the app (build command, test suite)
  - What success looks like
timeout: 10 minutes (app runtime)
autonomy_default: "auto"  # Can run tests without approval
```

**Responsibilities**:
- [ ] Start the application
- [ ] Observe the stated problem
- [ ] Form hypotheses about root causes
- [ ] Attempt to reproduce the bug/issue
- [ ] Run existing tests
- [ ] Validate the fix works
- [ ] Check for regressions

**Example Tester Prompt**:
```
You are Engine's test harness. Your job is to run the application and verify that the issue is fixed.

Issue:
{{issue_description}}

To reproduce: {{steps_to_reproduce}}

Success criteria:
{{success_criteria}}

Your approach:
1. Run: pnpm dev:desktop (or appropriate build command)
2. Navigate to the relevant part of the app
3. Follow the reproduction steps
4. Check if the issue exists
5. If the issue exists, describe what you see
6. If the issue does not exist, try edge cases
7. Run: pnpm test (to check for regressions)
8. Report your findings

Output JSON:
{
  "issue_found": true/false,
  "observation": "...",  // What did you see?
  "evidence": "...",  // Screenshot, error log, behavior description
  "root_cause": "...",  // Your hypothesis
  "tests_passing": true/false,
  "regressions": [...]  // Any new failures?
}
```

---

### 4. Implementer Agent

**Purpose**: Writes code changes based on approved plans.

**Invoke when**:
- Architect has approved the design
- Implementation is ready to begin
- Multiple files need consistent changes

**Configuration**:
```yaml
agent: implementer
model: Claude Haiku 4.5  # Fast code generation
context_requirements:
  - Architect-approved design
  - Files that need changes
  - Current code
timeout: 5 minutes
autonomy_default: "observe_after"
```

**Responsibilities**:
- [ ] Read the approved design from Architect
- [ ] Make changes to all affected files
- [ ] Ensure consistency across changes
- [ ] Apply design patterns from the codebase
- [ ] Handle cross-file dependencies
- [ ] Validate syntax and type correctness

**Example Implementer Prompt**:
```
You are Engine's code implementation agent. Architect has approved this design. Now write the code.

Approved design:
{{architect_approval_json}}

Files to modify:
{{list_of_files}}

Current code:
{{file_contents}}

Design principles:
- Single responsibility
- Clear naming
- Comments explain *why*, not *what*
- All public APIs documented

Your task:
1. Implement the approved design
2. Make changes to all necessary files
3. Ensure type correctness (TypeScript)
4. Apply the codebase's patterns
5. Document your changes

Output: File edits (use multi_replace_string_in_file)
```

---

### 5. Documenter Agent

**Purpose**: Keeps documentation in sync with verified code state.

**Invoke when**:
- Code changes have been merged
- New behaviors need documenting
- Architecture changes need updating
- Test coverage changes

**Configuration**:
```yaml
agent: documenter
model: Claude Haiku 4.5  # Summarization, consistency
context_requirements:
  - What code changed
  - Where it's documented
  - Current docs
timeout: 3 minutes
autonomy_default: "auto"  # Can update docs autonomously
```

**Responsibilities**:
- [ ] Find all relevant documentation
- [ ] Identify what needs updating
- [ ] Update README, API docs, guides
- [ ] Keep architecture notes current
- [ ] Track test coverage changes
- [ ] Update project state records

**Example Documenter Prompt**:
```
Code has been merged. Check if docs need updating.

Changed files:
{{list_of_changed_files}}

Changes summary:
{{commit_message}}

Relevant docs:
- README.md
- .github/TESTED_BEHAVIORS.md
- Architecture docs
- Inline documentation

Your task:
1. Read the code changes
2. Find all relevant documentation
3. Identify what's now outdated
4. Update docs to match reality
5. Report what you updated

Output: Documentation edits
```

---

## Routing Logic (Orchestrator Decision Tree)

```
User intent received
├─ "Fix #42"
│  └─ Route to Orchestrator with GitHub issue context
├─ "Run tests"
│  └─ Route to Tester with workspace context
├─ "Refactor X module"
│  └─ Route to Architect first (design), then Implementer (code), then Tester (verify)
├─ "Update docs"
│  └─ Route to Documenter directly
└─ Something structural/architectural
   └─ Escalate to human + Architect approval
```

## Cost Optimization

Route to the cheapest capable model:
- **Haiku for**: Testing, implementation, documentation
- **Sonnet for**: Architecture decisions, planning, synthesis
- **Never Opus**: These tasks don't require the most expensive model

Expected task costs (approximate):
- Single issue fix: $0.30-0.50 (Orchestrator + Architect + Implementer + Tester)
- Test run: $0.05-0.10 (Tester alone)
- Documentation update: $0.05-0.08 (Documenter alone)
- Complex refactor: $0.80-1.20 (Full pipeline)

## Human Checkpoints (Configurable)

Users can adjust these per task type:

```yaml
checkpoints:
  before_feature_branch_create: true    # Architect approval required
  before_code_merge_to_dev: true        # Human review of diff
  before_merge_to_main: true            # Full approval required
  before_destructive_ops: true          # Deletion, major refactor
  after_test_run: notify_human          # Report results, await confirmation
```

## Monitoring & Observability

Each agent execution logs:
- Input context received
- Reasoning and decisions made
- Costs (token usage, API calls)
- Time taken
- Decisions that required human approval
- Results and outcomes

Example log entry:
```json
{
  "agent": "implementer",
  "issue_id": "42",
  "timestamp": "2026-04-22T14:35:22Z",
  "input": { "files_changed": 3, "lines_added": 47 },
  "output": { "edits_applied": 3, "syntax_valid": true },
  "cost": "$0.12",
  "duration_ms": 2340,
  "human_approval_required": false,
  "status": "success"
}
```

---

## Next Steps

1. **Implement Orchestrator** — Can read GitHub issues and decompose tasks
2. **Implement Tester** — Can run Engine and observe behavior
3. **Implement Architect** — Reviews proposed changes
4. **Implement Implementer** — Writes code from approved designs
5. **Implement Documenter** — Keeps docs in sync
6. **Add GitHub triggers** — Auto-respond to issues with `ai:auto` label
7. **Configure autonomy levels** — Let users choose how independent the system is

---

**Status**: Design phase  
**Owner**: Engine autonomous architecture team  
**Next milestone**: Implement Orchestrator agent (week 1)
