# Engine Quickstart — 5 Minutes to Autonomous Development

This guide gets you from zero to running Engine's orchestrated agent team in 5 minutes.

## What You're Getting

Engine runs a **team of AI agents** that work together autonomously:

- **Orchestrator** — Plans the work, breaks it into tasks, delegates
- **Architect** — Reviews code designs, ensures consistency
- **Implementer** — Writes the actual code changes
- **Tester** — Validates everything works
- **Documenter** — Updates docs and comments

You pick which models (Claude, GPT, local Ollama, etc.) run each role.

## Step 1: Copy the Config (1 minute)

```bash
cd /path/to/engine
cp .engine/config.example.yaml .engine/config.yaml
```

That's it. You now have a working configuration with sensible defaults.

## Step 2: Set Your API Keys (1 minute)

Create `.env.local` in the root:

```bash
# For Anthropic (Claude)
export ANTHROPIC_API_KEY="sk-ant-..."

# For OpenAI (GPT)
export OPENAI_API_KEY="sk-..."

# For local Ollama (free, runs locally)
export OLLAMA_BASE_URL="http://127.0.0.1:11434"
```

Only set keys for providers you want to use.

## Step 3: Validate Your Config (1 minute)

```bash
engine config check
```

Should output:
```
✓ Config is valid
✓ Teams defined: default, fast, openai, local
✓ APIs available: anthropic, openai, ollama
✓ Ready to orchestrate
```

If you see errors, fix them and re-run.

## Step 4: Pick Your First Team (1 minute)

The default team is already chosen for you (balanced: local Gemma orchestrator + Sonnet architect).

### You Have 4 Pre-Built Teams:

| Team | Speed | Cost | Best For |
|------|-------|------|----------|
| `default` | 15min | ~$0.30 | Development (recommended) |
| `fast` | 5min | Free | Testing ideas |
| `openai` | 15min | ~$0.50 | If you prefer GPT |
| `premium` | 20min | ~$1.00 | Critical features |

### Or Create Your Own:

Edit `.engine/config.yaml`:

```yaml
teams:
  my_team:
    orchestrator:
      model: "anthropic:claude-opus-4.6"
    architect:
      model: "anthropic:claude-sonnet-4.6"
    implementer:
      model: "ollama:gemma4:31b"
    tester:
      model: "anthropic:claude-haiku-4.5"
    documenter:
      model: "ollama:gemma4:31b"
```

Then use: `"Implement this with my_team"`

## Step 5: Give It Work (1 minute)

From the Engine chat or command line:

```
"Fix issue #42"
"Add dark mode support"
"Refactor the database layer"
"Write tests for the payment module"
```

Sit back. Your agent team handles it.

## What Happens Next

1. **Orchestrator** reads the request, breaks it into tasks
2. **Architect** reviews the plan, suggests improvements
3. **Implementer** writes the actual code
4. **Tester** runs tests, validates behavior
5. **Documenter** updates comments and docs

All fully autonomous by default. No human approval needed.

## Changing Teams Mid-Work

Don't like the current team? Switch anytime:

```
"Use fast team for the rest"
"Switch orchestrator to opus"
"Let me try the openai team"
```

New configuration takes effect immediately.

## Cost Control

Edit `.engine/config.yaml`:

```yaml
dev_loop:
  max_cost_per_issue: 1.00  # Stop if this task costs more than $1
```

Agent will stop and ask before exceeding the limit.

## Using Local Models (Free)

Ollama is **completely free** if you run it locally:

```bash
# Install Ollama from ollama.ai
# Then pull a model:
ollama pull gemma4:31b
ollama serve

# In another terminal, set in .env.local:
export OLLAMA_BASE_URL="http://127.0.0.1:11434"
```

Then configure:

```yaml
teams:
  local:
    orchestrator:
      model: "ollama:gemma4:31b"
    # ... rest of team uses ollama
```

Use: `"Do this with local team"` (completely free)

## Approval Modes

If you want human approval gates:

```yaml
autonomy:
  default: "observe_after"  # Agent acts, then waits for OK
```

Options:
- `auto` — No approval (fully autonomous)
- `observe_after` — Agent acts, then reports and waits
- `approve` — Ask before acting
- `full_approve` — Ask at every step

## View What Happened

```bash
engine logs --recent
engine logs --costs
engine logs --by-agent
```

## Troubleshooting

### "API key not found"
Set in `.env.local`:
```
ANTHROPIC_API_KEY=sk-ant-...
```

### "Ollama connection failed"
Make sure Ollama is running:
```bash
ollama serve
```

### "Unknown team"
Check spelling in `.engine/config.yaml`. List available:
```bash
engine config teams list
```

### Costs going too high?
Use the `fast` team (free):
```
"Use fast team"
```

Or set a cost limit:
```yaml
dev_loop:
  max_cost_per_issue: 0.25  # Max $0.25 per issue
```

### Agent taking too long?
Switch to faster team:
```
"Switch to fast team"
```

Or increase timeouts:
```yaml
teams:
  default:
    orchestrator:
      timeout_seconds: 300  # Increase from 180
```

## Configuration Reference

Full options available in `.engine/config.example.yaml`.

Quick reference: `.engine/README.md`

Full documentation:
- Team configuration: `.github/CONFIGURABLE_TEAMS.md`
- Technical architecture: `.github/TEAM_ARCHITECTURE.md`
- System design: `.github/AUTONOMOUS_ORCHESTRATION.md`

## Next Steps

1. **Make a change request** and let the agents handle it
2. **Try different teams** — use `fast` for quick testing, `premium` for quality
3. **Customize your team** by editing `.engine/config.yaml`
4. **Set cost limits** to stay within budget
5. **Check the logs** to see what agents actually did

## Pro Tips

### Batch Similar Work
```
"Implement issues #10, #11, #12"
```

Agents will handle them as a batch, more efficiently.

### Get Specific Design Feedback
```
"Design a payment flow for enterprise customers"
```

Architect focuses on the design, not implementation details.

### Test-Driven Requests
```
"Write tests first for the auth module, then implement"
```

Agents follow the workflow you specify.

### Custom Approval Gates
```
"Fix this but get approval before merging"
```

Configured in `.engine/config.yaml` by issue type or branch.

## Questions?

- **How do agents work together?** → `.github/AUTONOMOUS_ORCHESTRATION.md`
- **How do I configure teams?** → `.github/CONFIGURABLE_TEAMS.md`
- **What models should I use?** → `.engine/README.md`
- **How much will this cost?** → `.engine/config.example.yaml` (cost estimates per team)
- **Can I run locally for free?** → Yes, use Ollama (see "Using Local Models" above)

## Quick Links

```
Copy config:     cp .engine/config.example.yaml .engine/config.yaml
Validate:        engine config check
View logs:       engine logs --recent
Available teams: engine config teams list
Full reference:  .engine/README.md
Docs:            .github/CONFIGURABLE_TEAMS.md
```

---

**You're ready. Give the agents some work.**

```
"Fix #42"
```

Let them handle it. They will. Autonomously.
