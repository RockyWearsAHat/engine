# Team Configuration Cheat Sheet

Quick reference for configuring and using agent teams.

## Config File Location
```
.engine/config.yaml
```

## Basic Structure
```yaml
teams:
  team_name:
    orchestrator:
      model: "provider:model-name"
      timeout_seconds: 180
    architect:
      model: "provider:model-name"
      timeout_seconds: 120
    # ... other agents

apis:
  provider_name:
    enabled: true
    max_cost_per_execution: 5.00

autonomy:
  default: "observe_after"
  bug_fixes:
    level: "observe_after"
    team: "default"
```

## Providers

| Provider | Format | Example | Cost | Speed |
|----------|--------|---------|------|-------|
| Anthropic | `anthropic:model-name` | `anthropic:claude-opus-4.6` | $$$$ | Medium |
| OpenAI | `openai:model-name` | `openai:gpt-5.4` | $$$$ | Medium |
| Ollama (Local) | `ollama:model-name` | `ollama:gemma4:31b` | Free | Fast |
| Custom | `custom:model-name` | `custom:my-model` | Varies | Varies |

## Models

### Anthropic
- `claude-opus-4.6` — Most capable, slow, expensive
- `claude-sonnet-4.6` — Balanced, good for design/planning
- `claude-haiku-4.5` — Fast, cheap, good for observation/tests

### OpenAI
- `gpt-5.4` — Most capable, expensive
- `gpt-4o` — Balanced capability, good price
- `gpt-4o-mini` — Fast, cheap

### Ollama (Local)
- `gemma4:31b` — Local, free, good for implementation
- `llama2:13b` — Local, free, good for text
- `qwen:32b` — Local, free, very capable

## Environment Variables
```bash
export ANTHROPIC_API_KEY="sk-ant-..."
export OPENAI_API_KEY="sk-..."
export OLLAMA_BASE_URL="http://127.0.0.1:11434"
```

## Pre-Built Teams

### `default` (Recommended)
```yaml
Orchestrator: ollama:gemma4:31b (local, fast)
Architect:   anthropic:claude-sonnet-4.6
Implementer: ollama:gemma4:31b (local, fast)
Tester:      anthropic:claude-haiku-4.5
Documenter:  ollama:gemma4:31b (local, fast)

Cost: ~$0.30 per issue
Time: 15-20 minutes
```

### `premium` (Quality First)
```yaml
Orchestrator: anthropic:claude-opus-4.6
Architect:   anthropic:claude-opus-4.6
Implementer: anthropic:claude-sonnet-4.6
Tester:      anthropic:claude-haiku-4.5
Documenter:  anthropic:claude-haiku-4.5

Cost: ~$1.05 per issue
Time: 20 minutes
```

### `fast` (Speed Only)
```yaml
Orchestrator: ollama:gemma4:31b
Architect:   ollama:gemma4:31b
Implementer: ollama:gemma4:31b
Tester:      ollama:gemma4:31b
Documenter:  ollama:gemma4:31b

Cost: Free
Time: 5-10 minutes
```

### `openai` (OpenAI Models)
```yaml
Orchestrator: openai:gpt-5.4
Architect:   openai:gpt-5.4
Implementer: openai:gpt-4o
Tester:      openai:gpt-4o-mini
Documenter:  openai:gpt-4o-mini

Cost: ~$0.50 per issue
Time: 15-20 minutes
```

## User Commands

### Use Default Team
```
"Fix #42"
```

### Switch Teams
```
"Fix #42 with premium team"
"Use fast team"
"Refactor with openai team"
```

### Override Specific Model
```
"Use opus as orchestrator"
"Let sonnet design this"
"Implement with gpt-5.4"
```

### Mix Custom
```
"Use opus for orchestrator, rest default"
"Fast team but with sonnet architect"
```

## Autonomy Levels

| Level | Behavior |
|-------|----------|
| `auto` | No approval needed |
| `observe_after` | Agent acts, reports, waits for OK |
| `approve` | Get approval first |
| `architect_approve` | Architect reviews design |
| `full_approve` | Every gate needs approval |

### Set Per Task Type
```yaml
autonomy:
  tests: "auto"  # Always run without asking
  bug_fixes: "observe_after"  # Run, then wait
  new_features: "full_approve"  # Every gate needs approval
```

## Cost Limits

```yaml
dev_loop:
  max_cost_per_issue: 1.00  # Stop if cost > $1

apis:
  anthropic:
    max_cost_per_execution: 5.00  # Stop if task > $5
```

## Temperature (Creativity)

```yaml
advanced:
  orchestrator_temperature: 0.7  # More creative
  architect_temperature: 0.5     # Balanced
  implementer_temperature: 0.3   # Less creative, more deterministic
  tester_temperature: 0.5
  documenter_temperature: 0.3
```

## Create Custom Team

```yaml
teams:
  my_team:
    orchestrator:
      model: "openai:gpt-5.4"
      timeout_seconds: 180
    architect:
      model: "anthropic:claude-sonnet-4.6"
      timeout_seconds: 120
    implementer:
      model: "ollama:gemma4:31b"
      timeout_seconds: 120
    tester:
      model: "anthropic:claude-haiku-4.5"
      timeout_seconds: 600
    documenter:
      model: "ollama:gemma4:31b"
      timeout_seconds: 90
```

Then use:
```
"Fix this with my_team"
```

## Validate Config
```bash
engine config check
```

## View Costs
```bash
engine logs --costs recent
engine logs --costs by-agent
engine logs --costs by-issue
```

## Enable/Disable Providers

```yaml
apis:
  anthropic:
    enabled: true   # Enabled by default
  openai:
    enabled: false  # Disabled
  ollama:
    enabled: true
```

## Branch Scope

Restrict where agents can work:

```yaml
dev_loop:
  branch_scope: "feature/*,fix/*"  # Only these branches
  exclude_branches: "main,develop"  # Never these
```

## Auto-Execute Without Approval

```yaml
dev_loop:
  auto_execute:
    - test           # Always run tests
    - documentation  # Always update docs
  
  require_approval:
    - main_branch_merge  # Always ask before merging main
```

## Slack/Discord Notifications

```yaml
integrations:
  slack:
    enabled: true
    webhook_env: "SLACK_WEBHOOK_URL"
    notify_on:
      - task_complete
      - task_failed
      - approval_needed
```

## Common Configurations

### Startup (Minimal Budget)
```yaml
teams:
  default:
    # All local except Sonnet for architecture
    orchestrator: "ollama:gemma4:31b"
    architect: "anthropic:claude-sonnet-4.6"
    implementer: "ollama:gemma4:31b"
    tester: "ollama:gemma4:31b"
    documenter: "ollama:gemma4:31b"

dev_loop:
  max_cost_per_issue: 0.15
```

### Production (Quality First)
```yaml
teams:
  default:
    # All Opus for maximum quality
    orchestrator: "anthropic:claude-opus-4.6"
    architect: "anthropic:claude-opus-4.6"
    implementer: "anthropic:claude-sonnet-4.6"
    tester: "anthropic:claude-haiku-4.5"
    documenter: "anthropic:claude-haiku-4.5"

dev_loop:
  max_cost_per_issue: 5.00
```

### Balanced (Recommended)
```yaml
# Use default team from config.example.yaml
dev_loop:
  default_team: "default"
  max_cost_per_issue: 1.00
```

## Files

| File | Purpose |
|------|---------|
| `.engine/config.yaml` | Your project configuration |
| `.engine/config.example.yaml` | Example with all options |
| `.engine/logs/` | Execution logs (auto-created) |

## Setup Steps

1. Copy example config
   ```bash
   cp .engine/config.example.yaml .engine/config.yaml
   ```

2. Set API keys in `.env.local`
   ```
   ANTHROPIC_API_KEY=sk-ant-...
   OPENAI_API_KEY=sk-...
   ```

3. Customize `.engine/config.yaml` if needed

4. Validate
   ```bash
   engine config check
   ```

5. Use!
   ```
   "Fix #42"
   ```

## Troubleshooting

### "API not enabled"
Check in `.engine/config.yaml`:
```yaml
apis:
  anthropic:
    enabled: true  # Should be true
```

### "Missing credential"
Set in `.env.local`:
```
ANTHROPIC_API_KEY=...
```

### "Unknown team"
Check team name is spelled correctly, or create custom team

### Costs too high?
Switch to cheaper team:
```
"Use fast team"
```

Or set limit:
```yaml
dev_loop:
  max_cost_per_issue: 0.50
```

## Links

- Full config guide: `.github/CONFIGURABLE_TEAMS.md`
- Technical details: `.github/TEAM_ARCHITECTURE.md`
- Orchestration design: `.github/AUTONOMOUS_ORCHESTRATION.md`
