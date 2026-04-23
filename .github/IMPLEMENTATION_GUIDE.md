# Implementing the Autonomous Agent System — Practical Guide

This guide explains how to actually implement and invoke the orchestrator-worker pipeline in Engine.

## Part 1: The Orchestrator Entry Point

When a user says "Fix #42" or references a GitHub issue, we need an orchestrator to take over.

### Current System (Manual)
```
User: "Fix #42"
Human (Copilot): "I'll look at the issue..."
Human: [reads issue, understands intent, decides what to do, writes code, tests]
```

### New System (Orchestrator-Driven)
```
User: "Fix #42"
Agent: "Preparing orchestration..."
Orchestrator: [reads issue, understands intent, decomposes task, delegates]
├─ Routes phase 1 to Architect: "Is this design sound?"
├─ Architect: "Yes, proceed with these changes"
├─ Routes phase 2 to Implementer: "Write the code"
├─ Routes phase 3 to Tester: "Does the fix work?"
└─ Routes phase 4 to Documenter: "Update docs"
Orchestrator: "Done. Results ↓"
User sees: Fixed code + PR + passing tests + updated docs
```

## Part 2: Orchestrator Implementation Pattern

### Step 1: Detect the Trigger

Add this to Engine's chat handler:

```typescript
// In AIChat.tsx or similar
const detectOrchestrationTrigger = (message: string) => {
  // Pattern 1: "Fix #<number>"
  const issueMatch = message.match(/#(\d+)/);
  if (issueMatch) {
    return {
      type: 'github_issue',
      issueId: parseInt(issueMatch[1]),
      intent: message,
    };
  }

  // Pattern 2: "Fix this" when code is selected
  if (message.toLowerCase().includes('fix') && selectedCode) {
    return {
      type: 'code_fix',
      selectedCode,
      intent: message,
    };
  }

  // Pattern 3: CI failure context
  if (message.match(/test.*fail|error:/i)) {
    return {
      type: 'ci_failure',
      context: lastCIFailure,
      intent: message,
    };
  }

  return null;
};
```

### Step 2: Invoke the Orchestrator

```typescript
const invokeOrchestrator = async (trigger: OrchestrationTrigger) => {
  // Get context
  const projectDirection = await getProjectDirection();
  const gitStatus = await getGitStatus();
  const issue = await fetchGitHubIssue(trigger.issueId);

  // Call orchestrator with specific prompt
  const orchestratorResult = await runSubagent({
    agentName: 'Explore',  // Reuse existing orchestrator logic
    model: 'Claude Sonnet 4.6',
    prompt: `
You are Engine's autonomous orchestrator. Your job is to understand this GitHub issue and plan a solution.

GitHub Issue:
${issue.title}

${issue.body}

Project direction:
${projectDirection}

Current git state:
${gitStatus}

Your task:
1. Understand what the user is asking
2. Check if it aligns with project direction
3. Decompose into phases: Understand → Design → Implement → Test → Document
4. For each phase, specify which agent should handle it
5. Create a feature branch name
6. Identify critical decision points

Return JSON:
{
  "issue_id": ${trigger.issueId},
  "understanding": "...",
  "alignment": "ALIGNED|DIVERGENT|BLOCKED",
  "phases": [
    {
      "phase": 1,
      "name": "Understanding",
      "agent": "tester",
      "task": "Reproduce the issue and understand the root cause",
      "needs_approval": false
    },
    {
      "phase": 2,
      "name": "Design",
      "agent": "architect",
      "task": "Propose a fix that aligns with architecture",
      "needs_approval": true
    },
    {
      "phase": 3,
      "name": "Implementation",
      "agent": "implementer",
      "task": "Write the code changes",
      "needs_approval": false
    },
    {
      "phase": 4,
      "name": "Validation",
      "agent": "tester",
      "task": "Verify the fix works",
      "needs_approval": false
    },
    {
      "phase": 5,
      "name": "Documentation",
      "agent": "documenter",
      "task": "Update relevant docs",
      "needs_approval": false
    }
  ],
  "feature_branch": "fix/...-#${trigger.issueId}",
  "estimated_time_minutes": 15,
  "critical_gates": ["before_implementation", "before_test"]
}
    `,
    description: 'Orchestrate fix for GitHub issue',
  });

  return orchestratorResult;
};
```

### Step 3: Execute Phases Sequentially

```typescript
const executePlan = async (plan: OrchestratorPlan) => {
  const results = [];

  for (const phase of plan.phases) {
    console.log(`[${phase.name}] Starting phase...`);
    
    // Check if this phase needs approval
    if (phase.needs_approval && plan.autonomy !== 'auto') {
      const approved = await askUserApproval(phase.task);
      if (!approved) {
        console.log(`[${phase.name}] Blocked by user`);
        return results;
      }
    }

    // Invoke the right agent for this phase
    const phaseResult = await invokeAgent(phase);
    results.push({
      phase: phase.name,
      result: phaseResult,
      status: 'complete',
    });

    // Check for critical issues
    if (phaseResult.status === 'failed') {
      console.error(`Phase failed: ${phase.name}`);
      return results;
    }
  }

  return results;
};
```

## Part 3: Specialized Agent Implementation

### Tester Agent Pattern

Testers need to actually run the application and observe behavior.

```typescript
const invokeTestAgent = async (task: TestTask) => {
  const result = await runSubagent({
    agentName: 'tester',
    model: 'Claude Haiku 4.5',
    prompt: `
You are Engine's test harness. Your job is to run the application and verify behavior.

Issue: ${task.issueDescription}

To reproduce: ${task.reproductionSteps}

Steps:
1. Run: pnpm dev:desktop
2. Navigate to: ${task.targetFeature}
3. Perform: ${task.reproductionSteps}
4. Check: ${task.successCriteria}
5. Run tests: pnpm test
6. Report findings

What you observe:
- Does the issue exist? (Yes/No/Partial)
- Root cause hypothesis: (...)
- Tests passing: (Yes/No)
- Any regressions: (List)

Output JSON with findings.
    `,
    description: 'Test and validate application behavior',
  });

  return result;
};
```

### Architect Agent Pattern

Architects review changes *before* they're implemented.

```typescript
const invokeArchitectAgent = async (proposal: CodeProposal) => {
  const result = await runSubagent({
    agentName: 'Explore',
    model: 'Claude Sonnet 4.6',
    prompt: `
You are Engine's architecture guardian. Review this proposed change.

Proposed change:
${proposal.description}

Files affected:
${proposal.affectedFiles.join(', ')}

Current architecture:
${await readArchitectureDoc()}

Design principles:
${readDesignPrinciples()}

Questions:
1. Does this violate any design principles?
2. What modules are impacted?
3. Are there better approaches?
4. Should we refactor something related?

Verdict: APPROVED | NEEDS_REVISION | REJECTED
Reasoning: (...)
Suggestions: (...)
    `,
    description: 'Architect review of proposed changes',
  });

  return result;
};
```

### Implementer Agent Pattern

Implementers write the actual code.

```typescript
const invokeImplementerAgent = async (design: ArchitectApproval) => {
  const result = await runSubagent({
    agentName: 'Explore',
    model: 'Claude Haiku 4.5',
    prompt: `
You are Engine's implementation agent. Architect approved this design. Write the code.

Approved design:
${design.approval}

Files to modify:
${design.affectedFiles.map(f => readFile(f)).join('\n---\n')}

Your task:
1. Implement the approved design
2. Follow these patterns from the codebase: [insert patterns]
3. Ensure type correctness (TypeScript)
4. All public methods documented
5. Use existing utilities, don't duplicate

Output: Edits in multi_replace_string_in_file format
    `,
    description: 'Implement approved design',
  });

  return result;
};
```

### Documenter Agent Pattern

Documenters keep docs in sync.

```typescript
const invokeDocumenterAgent = async (
  changedFiles: string[],
  commitMessage: string
) => {
  const result = await runSubagent({
    agentName: 'Explore',
    model: 'Claude Haiku 4.5',
    prompt: `
Code has been merged. Check if docs need updating.

Changed files:
${changedFiles.join(', ')}

Changes:
${commitMessage}

Docs to check:
- README.md
- .github/TESTED_BEHAVIORS.md
- API documentation
- Architecture notes

Your task:
1. Read the code changes
2. Find all docs that mention changed code
3. Update docs to match reality
4. Report what you updated

Output: Documentation edits
    `,
    description: 'Update documentation after code changes',
  });

  return result;
};
```

## Part 4: Autonomy Configuration

Users configure how much autonomy they want:

```typescript
type AutonomyLevel = 'auto' | 'observe_after' | 'approve' | 'architect_approve';

const autonomyConfig: Record<TaskType, AutonomyLevel> = {
  bug_fixes: 'observe_after',        // Run, then wait for OK
  tests: 'auto',                      // Run without asking
  documentation: 'auto',              // Update docs automatically
  refactors: 'architect_approve',    // Need architect sign-off
  new_features: 'approve',            // Need human approval
  experimental: 'auto',               // Full autonomy on exp branches
};

// Before executing a phase
if (autonomyConfig[phase.taskType] === 'auto') {
  // Execute immediately
  await executePhase(phase);
} else if (autonomyConfig[phase.taskType] === 'observe_after') {
  // Execute, then report and wait for confirmation
  const result = await executePhase(phase);
  const confirmed = await askUserApproval(`Results: ${result.summary}`);
  if (!confirmed) throw new Error('User rejected results');
} else if (autonomyConfig[phase.taskType] === 'approve') {
  // Get approval first
  const confirmed = await askUserApproval(`Can I ${phase.description}?`);
  if (!confirmed) throw new Error('User rejected proposal');
  await executePhase(phase);
}
```

## Part 5: Monitoring & Logging

Track everything for transparency:

```typescript
interface AgentExecution {
  agentName: string;
  taskType: string;
  timestamp: Date;
  input: Record<string, unknown>;
  output: Record<string, unknown>;
  duration_ms: number;
  cost_dollars: number;
  status: 'success' | 'failed' | 'blocked_by_human';
  humanApprovalRequired: boolean;
  result: unknown;
}

const logExecution = async (exec: AgentExecution) => {
  // Store in session memory for review
  await mcp_gsh_log_session_event({
    action: `agent: ${exec.agentName}`,
    outcome: `${exec.status}`,
    surprise: exec.status === 'failed' ? 0.8 : 0.1,
    tags: ['autonomy', exec.taskType, exec.agentName],
    context: JSON.stringify({
      duration_ms: exec.duration_ms,
      cost: `$${exec.cost_dollars}`,
      approved: !exec.humanApprovalRequired,
    }),
  });
};
```

## Part 6: Human Override Commands

Always allow users to stop/redirect:

```typescript
const handleHumanCommand = (message: string) => {
  if (message.toLowerCase() === 'halt' || message.toLowerCase() === 'stop') {
    orchestration.halt();
    return 'Halted orchestration. All current operations paused.';
  }

  if (message.toLowerCase().startsWith('skip phase')) {
    orchestration.skipCurrentPhase();
    return 'Skipped to next phase.';
  }

  if (message.toLowerCase().startsWith('reject')) {
    orchestration.rejectCurrentProposal();
    return 'Proposal rejected. Waiting for new direction.';
  }

  if (message.toLowerCase().startsWith('retry')) {
    orchestration.retryCurrentPhase();
    return 'Retrying current phase...';
  }
};
```

## Part 7: GitHub Integration

Auto-respond to issues marked with `ai:auto`:

```typescript
const monitorGitHubIssues = async () => {
  const issues = await octokit.issues.listForRepo({
    owner: config.owner,
    repo: config.repo,
    labels: ['ai:auto'],
    state: 'open',
  });

  for (const issue of issues.data) {
    if (!issue.assignee || issue.assignee.login !== 'engine-bot') {
      // Auto-assign to engine and start orchestration
      await octokit.issues.addAssignees({
        owner: config.owner,
        repo: config.repo,
        issue_number: issue.number,
        assignees: ['engine-bot'],
      });

      // Start orchestration
      await invokeOrchestrator({
        type: 'github_issue',
        issueId: issue.number,
        intent: `Fix: ${issue.title}`,
      });
    }
  }
};
```

## Summary: End-to-End Flow

```
1. User says "Fix #42"
2. Chat detects orchestration trigger
3. Invoke Orchestrator with issue context
4. Orchestrator reads issue, creates decomposition
5. For each phase:
   a. Route to appropriate agent (Architect, Implementer, Tester, etc.)
   b. Check autonomy level (auto/observe/approve)
   c. If needs approval, wait for human
   d. Execute the agent
   e. Log results
6. Synthesize final results
7. Present to user: "Fixed. PR #999. Tests passing. Docs updated."
```

**Next**: Implement the Orchestrator agent first, then add agents one by one.

