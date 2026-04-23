# Engine Orchestration System — Complete Guide

This document indexes and explains all the pieces of Engine's autonomous orchestration system.

## What You're Building

Engine is an **autonomous development agent orchestration system**. Instead of running a single AI model, you define a **team of specialized agents** that work together:

- **Orchestrator** — Plans work, delegates tasks, coordinates
- **Architect** — Reviews designs, ensures consistency, identifies technical debt
- **Implementer** — Writes code changes
- **Tester** — Validates correctness, runs tests, observes behavior
- **Documenter** — Updates docs, comments, maintains knowledge

These agents work **fully autonomously by default**, but you control:
- Which models run each role (Claude, GPT, local Ollama, custom)
- How autonomous each agent is (auto, observe_after, approve, full_approve)
- Per-project configuration inheritance
- Per-request permission and team overrides
- Cost budgets and execution timeouts

## Documentation Map

### 🚀 Start Here

**New to Engine?** Start with these in order:

1. **[.engine/QUICKSTART.md](.engine/QUICKSTART.md)** (5 minutes)
   - Copy config, set API keys, validate, pick a team, give it work
   - Pre-built teams for different speed/cost profiles
   - Common problems and solutions

2. **[.engine/README.md](.engine/README.md)** (cheat sheet)
   - Quick reference for all providers and models
   - Pre-built team definitions with costs
   - User commands (switch teams, override models, etc.)
   - Common configurations for different scenarios

3. **[.github/CONFIGURABLE_TEAMS.md](.github/CONFIGURABLE_TEAMS.md)** (user guide)
   - How to create custom teams
   - How to select teams at runtime
   - How to override permissions per-request
   - Cost and speed tradeoff matrix
   - Real-world examples

### 🏗️ Architecture & Design

For **developers** building the orchestration system:

4. **[.github/TEAM_ARCHITECTURE.md](.github/TEAM_ARCHITECTURE.md)**
   - Configuration loading and validation flow
   - Team resolution algorithm (request override → project default → global default)
   - TypeScript interfaces and type definitions
   - WebSocket message formats for runtime team changes
   - Cost calculation per team configuration
   - Agent routing implementation patterns

5. **[.github/AUTONOMOUS_ORCHESTRATION.md](.github/AUTONOMOUS_ORCHESTRATION.md)**
   - High-level system vision and principles
   - 5-agent framework description
   - Orchestrator-worker pipeline architecture
   - Workflow examples (bug fixes, new features, refactoring)
   - Autonomy levels and governance model
   - Future extensions (GitHub integration, CI/CD, Discord control plane)

6. **[.github/AGENT_CONFIGURATION.md](.github/AGENT_CONFIGURATION.md)**
   - Specification for each agent type
   - When to invoke each agent
   - Example prompts and contexts
   - Cost optimization strategies
   - Agent communication patterns

7. **[.github/IMPLEMENTATION_GUIDE.md](.github/IMPLEMENTATION_GUIDE.md)**
   - Practical code patterns for orchestration
   - GitHub integration for issue-driven workflows
   - Agent execution with autonomy gates
   - WebSocket message handling
   - Example TypeScript implementations

### 📋 Configuration Reference

For **editing** `.engine/config.yaml`:

- **[.engine/config.example.yaml](.engine/config.example.yaml)** — Full example with all options
- **[.engine/README.md](.engine/README.md)** — Quick reference for models, teams, and settings

## Quick Navigation by Task

### "I want to get started"
→ [.engine/QUICKSTART.md](.engine/QUICKSTART.md)

### "I want to create a custom team"
→ [.github/CONFIGURABLE_TEAMS.md](.github/CONFIGURABLE_TEAMS.md) → "Create a Custom Team" section

### "I want to understand how costs work"
→ [.engine/README.md](.engine/README.md) → "Cost Limits" section

### "I want to switch teams at runtime"
→ [.github/CONFIGURABLE_TEAMS.md](.github/CONFIGURABLE_TEAMS.md) → "Runtime Team Selection" section

### "I want to understand the architecture"
→ [.github/TEAM_ARCHITECTURE.md](.github/TEAM_ARCHITECTURE.md)

### "I want to implement orchestration logic"
→ [.github/IMPLEMENTATION_GUIDE.md](.github/IMPLEMENTATION_GUIDE.md)

### "I want to see an end-to-end workflow"
→ [.github/AUTONOMOUS_ORCHESTRATION.md](.github/AUTONOMOUS_ORCHESTRATION.md) → "Workflows" section

### "I want to know which model to use"
→ [.engine/README.md](.engine/README.md) → "Models" and "Pre-Built Teams" sections

### "I want to use local models for free"
→ [.engine/QUICKSTART.md](.engine/QUICKSTART.md) → "Using Local Models (Free)" section

### "I want to set approval gates"
→ [.engine/README.md](.engine/README.md) → "Autonomy Levels" section

### "I want to restrict API costs"
→ [.engine/README.md](.engine/README.md) → "Cost Limits" section

## Configuration Hierarchy

Teams are resolved in this order (first match wins):

```
1. Runtime override
   User says: "Use fast team"

2. Request-specific override
   API call includes: { team: "premium" }

3. Project default team
   In .engine/config.yaml: default_team: "default"

4. Global default team
   In .engine/config.example.yaml: default_team: "default"
```

Permissions follow the same hierarchy:
```
1. Per-request overrides
   User: "approve this before running"

2. Project permission scope
   .engine/config.yaml: permissions: { write_files: true }

3. Global defaults
   .engine/config.example.yaml defaults
```

## Models at a Glance

### Anthropic (Claude)
- `claude-opus-4.6` — Most capable, best for complex reasoning
- `claude-sonnet-4.6` — Balanced, good for design and planning
- `claude-haiku-4.5` — Fast, cheap, good for simple tasks

### OpenAI (GPT)
- `gpt-5.4` — Most capable
- `gpt-4o` — Balanced
- `gpt-4o-mini` — Fast, cheap

### Ollama (Local)
- `gemma4:31b` — Recommended local model
- `llama2:13b` — Good alternative
- `qwen:32b` — Very capable local model
- Free, runs entirely locally, no API key needed

## Pre-Built Teams

### `default` — Recommended for Development
- Orchestrator: `ollama:gemma4:31b` (local, fast)
- Architect: `anthropic:claude-sonnet-4.6`
- Implementer: `ollama:gemma4:31b` (local, fast)
- Tester: `anthropic:claude-haiku-4.5`
- Documenter: `ollama:gemma4:31b` (local, fast)

**Cost**: ~$0.30 per issue | **Time**: 15-20 minutes

### `fast` — Speed Only
- All models: `ollama:gemma4:31b`
- **Cost**: Free | **Time**: 5-10 minutes

### `premium` — Quality First
- All models: `anthropic:claude-opus-4.6` or `claude-sonnet-4.6`
- **Cost**: ~$1.05 per issue | **Time**: 20 minutes

### `openai` — OpenAI Models
- Orchestrator: `openai:gpt-5.4`
- Architect: `openai:gpt-5.4`
- Implementer: `openai:gpt-4o`
- Tester: `openai:gpt-4o-mini`
- Documenter: `openai:gpt-4o-mini`

**Cost**: ~$0.50 per issue | **Time**: 15-20 minutes

See [.engine/README.md](.engine/README.md) for full list.

## Common User Commands

### Run work with default team
```
"Fix issue #42"
```

### Switch teams
```
"Use fast team"
"Use premium team"
"Use openai team"
"Use local team"
```

### Override specific model
```
"Use opus as orchestrator"
"Let sonnet design this"
"Implement with gpt-5.4"
```

### Request approval
```
"Fix this but get approval before running"
```

### Batch work
```
"Fix issues #10, #11, #12"
```

### Specify workflow
```
"Write tests first, then implement"
```

See [.github/CONFIGURABLE_TEAMS.md](.github/CONFIGURABLE_TEAMS.md) for more.

## Autonomy Levels

| Level | Behavior |
|-------|----------|
| `auto` | Execute fully without asking |
| `observe_after` | Execute, report, wait for OK before next step |
| `approve` | Get approval before executing |
| `full_approve` | Every gate requires approval |

Set per task type or globally:
```yaml
autonomy:
  tests: "auto"  # Always run tests
  bug_fixes: "observe_after"  # Run, report, wait
  new_features: "full_approve"  # Every gate needs approval
```

## Setup Checklist

- [ ] Copy `.engine/config.example.yaml` → `.engine/config.yaml`
- [ ] Set API keys in `.env.local`
- [ ] Run `engine config check`
- [ ] (Optional) Create custom teams
- [ ] (Optional) Set cost limits
- [ ] (Optional) Set autonomy levels per task
- [ ] Start giving agents work

Takes 5 minutes. See [.engine/QUICKSTART.md](.engine/QUICKSTART.md).

## File Locations

| File | Purpose |
|------|---------|
| `.engine/config.yaml` | Your project configuration |
| `.engine/config.example.yaml` | Full example with all options |
| `.engine/QUICKSTART.md` | 5-minute setup guide |
| `.engine/README.md` | Quick reference (models, teams, commands) |
| `.github/CONFIGURABLE_TEAMS.md` | User guide for team configuration |
| `.github/TEAM_ARCHITECTURE.md` | Technical architecture documentation |
| `.github/AUTONOMOUS_ORCHESTRATION.md` | System vision and design |
| `.github/AGENT_CONFIGURATION.md` | Agent specifications |
| `.github/IMPLEMENTATION_GUIDE.md` | Code patterns for implementation |
| `.engine/logs/` | Execution logs (auto-created) |

## Cost Examples

### Development Loop (All Features)
- **Team**: `default` (local Gemma orchestrator + Sonnet architect)
- **Per Issue**: $0.30
- **Per Month (100 issues)**: $30
- **Speed**: 15-20 minutes per issue

### Startup (Minimum Budget)
- **Team**: `fast` (all local Ollama)
- **Per Issue**: Free
- **Per Month**: Free
- **Speed**: 5-10 minutes per issue
- **Trade-off**: Less sophisticated planning

### Premium (Quality Focus)
- **Team**: `premium` (Opus orchestrator)
- **Per Issue**: $1.05
- **Per Month (50 issues)**: $52.50
- **Speed**: 20 minutes per issue
- **Trade-off**: Most capable

Set cost limits:
```yaml
dev_loop:
  max_cost_per_issue: 1.00
```

Agents will refuse tasks exceeding the budget.

## Governance

**Default Behavior**:
- Agents execute autonomously without human approval
- They respect cost budgets
- They validate changes before committing
- They report results and next steps

**Approval Modes**:
- `auto` — No approval needed (default)
- `observe_after` — Propose, execute, wait for OK
- `approve` — Wait for approval before executing
- `full_approve` — Every gate needs approval

Set per issue type:
```yaml
autonomy:
  main_branch_merge: "full_approve"  # Never auto-merge main
  documentation: "auto"              # Auto-update docs
  tests: "auto"                       # Always run tests
  bug_fixes: "observe_after"         # Run, then wait
```

## Next Steps

1. **Read [.engine/QUICKSTART.md](.engine/QUICKSTART.md)** (5 minutes)
   - Copy config, set keys, validate

2. **Give agents some work**
   ```
   "Fix issue #42"
   ```

3. **Monitor execution**
   ```bash
   engine logs --recent
   ```

4. **Customize if needed**
   - Create custom teams
   - Set cost limits
   - Configure approval gates
   - Mix and match models

5. **Learn the architecture** (for developers)
   - Read [.github/TEAM_ARCHITECTURE.md](.github/TEAM_ARCHITECTURE.md)
   - Understand agent routing
   - Implement custom workflows

## Philosophy

**Default to Autonomy, Allow Transparency**

The system is designed to:
- Work **fully autonomously** by default (no human overhead)
- Provide **visibility** into what agents are doing (logs, reports)
- Allow **control** when needed (approval gates, cost limits, team overrides)
- Keep **humans in charge** of the big decisions (which team, which branch, when to merge)

Users can set any autonomy level they want — from **fully autonomous** ("just fix it") to **full approval** ("ask me about everything").

The system respects this choice and executes accordingly.

## Architecture Philosophy

From the Kelly/OpenClaw essay: **Orchestrator-Worker Pipeline**

```
User Request
    ↓
Orchestrator
    ├─→ Architect (review design)
    ├─→ Implementer (write code)
    ├─→ Tester (validate)
    └─→ Documenter (update docs)
    ↓
Result
```

Each worker is specialized. The orchestrator coordinates. Users pick which models run each role.

This is different from a single monolithic AI because:
- **Parallelization**: Architect and Implementer can work simultaneously
- **Specialization**: Each role optimized for its task (Opus for planning, Haiku for testing)
- **Cost efficiency**: Expensive models only where needed
- **Observability**: Each agent's work is visible and reviewable
- **Composability**: Mix and match models for different projects

## Support

- **Configuration questions**: [.engine/README.md](.engine/README.md)
- **Setup help**: [.engine/QUICKSTART.md](.engine/QUICKSTART.md)
- **How to customize**: [.github/CONFIGURABLE_TEAMS.md](.github/CONFIGURABLE_TEAMS.md)
- **Technical details**: [.github/TEAM_ARCHITECTURE.md](.github/TEAM_ARCHITECTURE.md)
- **System design**: [.github/AUTONOMOUS_ORCHESTRATION.md](.github/AUTONOMOUS_ORCHESTRATION.md)

---

**Engine is ready. Give it work.**
