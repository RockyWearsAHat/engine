# Configurable Agent Teams — User Guide

This guide explains how to customize which agents (models) work on your project and how to select teams at runtime.

## Quick Start

1. **Copy the example config**
   ```bash
   cp .engine/config.example.yaml .engine/config.yaml
   ```

2. **Use the default team** (no configuration needed)
   - Default is optimized for speed + cost
   - Orchestrator: Local Gemma 4:31B
   - Architect: Claude Sonnet 4.6
   - Implementer: Local Gemma 4:31B
   - Tester: Claude Haiku 4.5
   - Documenter: Local Gemma 4:31B

3. **That's it!** Your system is ready to go.

---

## Selecting Teams at Runtime

Once configured, users can switch teams with simple commands:

### Using Default Team (No Command Needed)
```
User: "Fix #42"
System uses: default team (fast, balanced)
```

### Switching to Premium Team
```
User: "Fix #42 with premium team"
System: Uses Opus for orchestrator, Sonnet for architect
```

### Switching to Fast Team (Experimental Branch)
```
User: "Fix this with fast team"
System: Uses all local Gemma models (instant, no API calls)
```

### Switching to OpenAI Team
```
User: "Refactor this with openai team"
System: Uses GPT-5.4 for orchestration, GPT-4o for implementation
```

### Using a Specific Model for One Task
```
User: "Use opus as orchestrator for this fix"
System: Swaps just the orchestrator to Opus, keeps other agents
```

### Using a Local Model
```
User: "Orchestrate this with gemma4:31b"
System: Switches orchestrator to local Gemma, keeps rest of default team
```

---

## Understanding the Default Team

The default team is optimized for **dev loop speed** with **reasonable cost**:

```yaml
default:
  orchestrator: ollama:gemma4:31b  # Local, instant, $0.00
  architect:   anthropic:sonnet    # Cloud, deep reasoning, $0.12
  implementer: ollama:gemma4:31b   # Local, code generation, $0.00
  tester:      anthropic:haiku     # Cloud, observation, $0.05
  documenter:  ollama:gemma4:31b   # Local, summarization, $0.00

Cost per issue:   ~$0.30
Time to complete: 15-20 minutes
Human time:       5 minutes (review results)
```

**Why this mix?**
- **Local models (Gemma)** for fast iteration, no API cost
- **Sonnet for architect** because design decisions need deep reasoning
- **Haiku for tester** because it's cheap and fast at observation
- **Result**: 10x faster than all-cloud, 1/3 the cost

---

## Creating Custom Teams

Add your own team to `.engine/config.yaml`:

```yaml
teams:
  my_custom_team:
    description: "My specific workflow"
    orchestrator:
      model: "openai:gpt-5.4"
      timeout_seconds: 180
    architect:
      model: "anthropic:claude-sonnet-4.6"
      timeout_seconds: 120
    implementer:
      model: "ollama:llama2:13b"
      timeout_seconds: 120
    tester:
      model: "anthropic:claude-haiku-4.5"
      timeout_seconds: 600
    documenter:
      model: "ollama:llama2:13b"
      timeout_seconds: 90
```

Then use it:
```
User: "Fix this with my_custom_team"
```

---

## API Configuration — Credentials & Permissions

### Setting Up Providers

Edit `.engine/config.yaml`:

```yaml
apis:
  anthropic:
    enabled: true
    rate_limit: 10_000_tokens_per_minute
    max_cost_per_execution: 5.00  # Max $5 per task
    timeout_seconds: 300

  openai:
    enabled: true
    rate_limit: 10_000_tokens_per_minute
    max_cost_per_execution: 5.00
    timeout_seconds: 300

  ollama:
    enabled: true
    base_url: "http://127.0.0.1:11434"  # Local model server
    max_cost_per_execution: 0.00  # Free (local)
    timeout_seconds: 300
```

### API Keys

**Never commit API keys to git!** Use environment variables instead:

```bash
# Set before running Engine
export ANTHROPIC_API_KEY="sk-ant-..."
export OPENAI_API_KEY="sk-..."
export OLLAMA_BASE_URL="http://localhost:11434"
```

Or in `.env.local` (git-ignored):
```
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...
OLLAMA_BASE_URL=http://127.0.0.1:11434
```

### Cost Controls

Set maximum spend per task:

```yaml
apis:
  anthropic:
    max_cost_per_execution: 2.00  # Stop if any task costs >$2

dev_loop:
  max_cost_per_issue: 1.00  # Stop if entire issue costs >$1
```

If a task would exceed these limits, it's escalated or rejected.

---

## Task-Type Autonomy Configuration

Control how much autonomy each type of work has:

```yaml
autonomy:
  bug_fixes:
    level: "observe_after"   # Agent acts, reports, waits for OK
    team: "default"
    approval_required: true

  tests:
    level: "auto"            # Run without asking
    team: "default"
    approval_required: false

  documentation:
    level: "auto"            # Update docs automatically
    team: "fast"
    approval_required: false

  new_features:
    level: "full_approve"    # Every gate needs approval
    team: "premium"
    approval_required: true

  experimental:
    level: "auto"            # Full autonomy on exp branches
    team: "fast"
    approval_required: false
```

### Autonomy Levels Explained

| Level | Behavior | Example |
|-------|----------|---------|
| `auto` | Agent acts alone, reports results | "Fixed bug. PR #123." |
| `observe_after` | Agent acts, reports, waits for OK | "Results ready. Approve?" |
| `approve` | Get approval first, then act | "Proposed fix. OK?" |
| `architect_approve` | Architect reviews design first | "Design approved by Architect." |
| `full_approve` | Every gate needs approval | Step-by-step human oversight |

---

## User Request Syntax

Users can customize behavior with natural language:

### Team Selection
```
"Fix this with premium team"
"Orchestrate this with fast team"
"Use openai team for this feature"
```

### Model Selection
```
"Use opus as orchestrator"
"Let sonnet design this refactor"
"Use gpt-5.4 for implementation"
"Orchestrate with local gemma"
```

### Autonomy Control
```
"Fix this with full_approve"
"Do this autonomously"
"Architect must approve this change"
"Run tests automatically"
```

### Combination Requests
```
"Fix #42 with premium team and architect approval"
"Refactor this module with opus orchestrator using fast team"
"Use gpt-5.4 for orchestration, keep rest default"
```

---

## Cost & Speed Estimates

**Estimating task costs:**

```
Simple bug fix (observe_after):
  - Orchestrator (Sonnet): $0.05
  - Architect review (Sonnet): $0.12
  - Implementer (Haiku): $0.08
  - Tester (Haiku): $0.05
  Total: ~$0.30, 15 minutes

Complex refactor (architect_approve):
  - Orchestrator (Sonnet): $0.15
  - Architect (Sonnet): $0.30
  - Implementer (Sonnet): $0.20
  - Tester (Haiku): $0.12
  - Documenter (Haiku): $0.05
  Total: ~$0.82, 20 minutes

Premium team (Opus orchestrator):
  - Orchestrator (Opus): $0.40
  - Architect (Opus): $0.40
  - Implementer (Sonnet): $0.20
  - Tester (Haiku): $0.05
  Total: ~$1.05, 20 minutes
```

**Speed benefits:**
- Local Gemma: 2-5 second responses (no API roundtrip)
- Claude Haiku: 5-10 seconds (very fast)
- Claude Sonnet: 10-15 seconds (balanced)
- Claude Opus: 15-30 seconds (deep thinking)

---

## Advanced Configuration

### Per-Project Overrides

Some teams prefer different defaults:

```yaml
# Frontend team
dev_loop:
  default_team: "fast"  # Use fast team by default
  branch_scope: "feature/*,ui/*"

# Data science team
dev_loop:
  default_team: "premium"  # Use premium for data work
  max_cost_per_issue: 5.00  # Higher budget for complex analysis
```

### Temperature (Model Creativity)

Tune how "creative" each agent is:

```yaml
advanced:
  orchestrator_temperature: 0.7  # Creative planning
  architect_temperature: 0.5     # Balanced design
  implementer_temperature: 0.3   # Deterministic code
  tester_temperature: 0.5        # Observant
  documenter_temperature: 0.3    # Consistent docs
```

Lower temperature = more predictable  
Higher temperature = more creative

### Auto-Escalation

If an agent times out, automatically use a better model:

```yaml
advanced:
  auto_escalate_on_timeout: true
  escalation_model_map:
    "ollama:*": "anthropic:claude-sonnet-4.6"
    "openai:gpt-4o-mini": "openai:gpt-5.4"
```

---

## Real-World Examples

### Startup (Minimal Budget)
```yaml
teams:
  default:
    orchestrator: "ollama:gemma4:31b"
    architect: "anthropic:claude-sonnet-4.6"  # Only cloud model
    implementer: "ollama:gemma4:31b"
    tester: "ollama:gemma4:31b"
    documenter: "ollama:gemma4:31b"

dev_loop:
  max_cost_per_issue: 0.15  # Under $0.20 per issue
```

**Result**: ~$5/month for unlimited issues

### Serious Business (Quality First)
```yaml
teams:
  default:
    orchestrator: "anthropic:claude-opus-4.6"
    architect: "anthropic:claude-opus-4.6"
    implementer: "anthropic:claude-sonnet-4.6"
    tester: "anthropic:claude-haiku-4.5"
    documenter: "anthropic:claude-haiku-4.5"

dev_loop:
  max_cost_per_issue: 5.00  # Budget for quality
```

**Result**: Best quality, ~$20/month for 50 issues

### Balanced (Recommended)
```yaml
# Use the default team (already in config.example.yaml)
# Mix of local and cloud, optimized for dev loop speed

dev_loop:
  default_team: "default"
  max_cost_per_issue: 1.00
```

**Result**: Fast iteration, reasonable cost, ~$10-15/month for 50 issues

---

## Monitoring Costs

View what you're spending:

```bash
# See recent execution costs
engine logs --costs recent

# See cost breakdown by agent
engine logs --costs by-agent

# See cost per issue
engine logs --costs by-issue

# Set budget alerts
engine config set --max-monthly-cost 100
```

---

## Troubleshooting

### Models unavailable?
Check APIs are enabled and keys are set:
```bash
engine config check  # Validates all configured models
```

### Costs too high?
Switch to a cheaper team:
```
User: "Use fast team for this"
```

Or lower cost limits:
```yaml
dev_loop:
  max_cost_per_issue: 0.50  # Stricter budget
```

### Performance slow?
Use local models or escalate to faster models:
```yaml
advanced:
  auto_escalate_on_timeout: true  # Automatically use faster models
```

---

## Defaults Philosophy

These defaults exist because:

✅ **No configuration needed** — Works out of the box  
✅ **Speed optimized** — Dev loop stays responsive  
✅ **Cost conscious** — Stays within reasonable budgets  
✅ **Balanced quality** — Uses cloud models for important decisions  
✅ **User-friendly** — Natural language overrides, no syntax required  

Users can customize everything, but they don't have to.

