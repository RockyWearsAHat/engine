# Engine Orchestration System — Implementation Status

**Status**: Documentation & Specification Complete | Implementation Phase Starting

**Last Updated**: April 22, 2026

## What We Built

A **flexible, user-configurable agent orchestration system** where:

1. **Users pick their team** — Select which models (Claude, GPT, Ollama) run each agent role
2. **Teams work together** — Orchestrator plans → Specialists execute → Tester validates
3. **Work is autonomous** — No human approval needed by default
4. **Work is cost-conscious** — Local Ollama (free) + Claude (cheap) + GPT (optimal)
5. **Work is transparent** — Full logs and execution history

## Key Design Decision

**Hierarchical configuration with runtime overrides:**

```
1. User says: "Fix #42 with premium team"
   ↓ (use premium team)
   
2. Otherwise, read request: { team: "fast" }
   ↓ (use fast team)
   
3. Otherwise, read project config: default_team: "default"
   ↓ (use default team)
   
4. Otherwise, use global defaults
   ↓ (safe defaults included)
```

Same hierarchy for permissions, autonomy levels, and cost limits.

## Files Created (This Session)

### Documentation (1,969 lines total)

1. **`.engine/QUICKSTART.md`** (310 lines)
   - 5-minute setup guide
   - API key configuration
   - Team selection
   - Cost control
   - Troubleshooting

2. **`.engine/README.md`** (378 lines)
   - Quick reference for all providers
   - Pre-built team definitions
   - User commands and syntax
   - Common configurations
   - Temperature and timeout tuning

3. **`.engine/config.example.yaml`** (345 lines)
   - Full configuration template
   - 4 pre-built teams (default, fast, premium, openai)
   - All configurable options explained
   - Cost estimates per team
   - Example autonomy and permission rules

4. **`.engine/INDEX.md`** (this file)
   - Master index of all documentation
   - Navigation by task
   - Quick reference for everything
   - Cost examples and governance model

5. **`.github/CONFIGURABLE_TEAMS.md`** (457 lines)
   - User guide for team configuration
   - How to create custom teams
   - Runtime team selection patterns
   - Cost/speed tradeoff matrix
   - Real-world examples

6. **`.github/TEAM_ARCHITECTURE.md`** (479 lines)
   - Technical architecture documentation
   - Configuration loading flow
   - Team resolution algorithm
   - TypeScript type definitions
   - WebSocket message formats
   - Agent routing patterns

Plus existing files:
- **`.github/AUTONOMOUS_ORCHESTRATION.md`** — System vision (257 lines)
- **`.github/AGENT_CONFIGURATION.md`** — Agent specs (423 lines)
- **`.github/IMPLEMENTATION_GUIDE.md`** — Code patterns (502 lines)

## What's Fully Specified

✅ **Configuration Schema**
- YAML structure for teams, agents, permissions
- Inheritance hierarchy
- Validation rules
- All options documented

✅ **Team Definitions**
- 4 pre-built teams (default, fast, premium, openai)
- Custom team creation guide
- Per-team cost/speed estimates

✅ **User Workflows**
- How to select teams
- How to override at runtime
- How to set cost limits
- How to configure approval gates

✅ **Agent Specifications**
- Orchestrator role and responsibilities
- Architect, Implementer, Tester, Documenter specs
- When to invoke each agent
- What each agent's output looks like

✅ **Architecture Documentation**
- Configuration loading flow
- Team resolution algorithm
- Agent routing patterns
- TypeScript interfaces
- WebSocket message formats

## What's NOT Yet Implemented (Next Phase)

⏳ **React Component** (PreferencesPanel.tsx)
- Team selector dropdown
- Cost/autonomy badge display
- Team list with descriptions
- "Use this team" button

⏳ **Tauri Backend (Rust)**
- `load_engine_config()` command
- YAML parsing and validation
- Configuration caching
- File watching for changes

⏳ **WebSocket Integration**
- Team selection WebSocket messages
- Permission override messages
- Cost budget notifications
- Autonomy gate prompts

⏳ **Orchestrator Logic (Go)**
- Team resolution based on request/project/global config
- Agent spawning with resolved team
- Model routing
- Cost tracking per team

⏳ **CLI Commands**
- `engine config check` — Validate config
- `engine config teams list` — Show available teams
- `engine logs --costs` — View cost breakdown
- `engine team set <name>` — Change default team

## How to Use This (For Implementation)

### Phase 1: React Component (Immediate)
File: `/packages/client/src/components/Preferences/TeamSelector.tsx`

```typescript
// Import ConfiguredTeam from shared types
// Import available teams from loaded config
// Display team name, autonomy level, estimated cost
// Allow selection and persistence
```

Requirements:
- Get available teams from config (via bridge)
- Show team details (name, cost, autonomy, agents)
- Allow selection (update preferences)
- Show "most recent team" as default

### Phase 2: Tauri Backend
File: `desktop-tauri/src-tauri/src/config.rs`

```rust
// Load .engine/config.yaml
// Parse with serde_yaml
// Validate schema
// Cache in Arc<Mutex<Config>>
// Watch for changes
```

Tauri command: `load_engine_config() → Result<EngineConfig>`

### Phase 3: WebSocket Integration
File: `packages/client/src/ws/handler.ts`

```typescript
// Handle team selection messages
// Update UI on team change
// Send team override in requests
// Track permission overrides
```

Message format (from TEAM_ARCHITECTURE.md):
```json
{
  "type": "team.select",
  "team": "premium",
  "scope": "current_issue"
}
```

### Phase 4: Orchestrator Integration
File: `server-go/ai/orchestrator.go`

```go
// Load team config from Tauri cache
// Resolve team: request.team || project.defaultTeam || global.defaultTeam
// Spawn agents with resolved team
// Track cost per resolved team
// Enforce cost limits
```

Function signature:
```go
func (o *Orchestrator) SelectTeam(ctx context.Context, req *TeamRequest) (*ConfiguredTeam, error)
```

## Team Configuration File

Location: `.engine/config.yaml` (user creates from template)

Example:
```yaml
teams:
  default:
    orchestrator:
      model: "ollama:gemma4:31b"
      timeout_seconds: 180
    architect:
      model: "anthropic:claude-sonnet-4.6"
      timeout_seconds: 120
    # ... other agents

apis:
  anthropic:
    enabled: true
    max_cost_per_execution: 5.00

autonomy:
  default: "auto"
  bug_fixes:
    level: "observe_after"
    team: "default"
```

## Cost Examples

### Startup Budget
```yaml
dev_loop:
  default_team: "fast"  # All local Ollama
  max_cost_per_issue: 0.00  # Free
```

### Development (Recommended)
```yaml
dev_loop:
  default_team: "default"  # Local + Sonnet
  max_cost_per_issue: 1.00  # $1 per issue
```

### Production
```yaml
dev_loop:
  default_team: "premium"  # All Claude
  max_cost_per_issue: 5.00  # $5 per issue
```

## Pre-Built Teams Reference

| Team | Orchestrator | Architect | Implementer | Tester | Documenter | Cost/Issue | Time | Use Case |
|------|--------------|-----------|-------------|--------|------------|-----------|------|----------|
| default | Gemma (local) | Sonnet | Gemma (local) | Haiku | Gemma (local) | $0.30 | 15m | Development |
| fast | Gemma (local) | Gemma (local) | Gemma (local) | Gemma (local) | Gemma (local) | Free | 5m | Quick testing |
| premium | Opus | Opus | Sonnet | Haiku | Haiku | $1.05 | 20m | Quality-critical |
| openai | GPT-5.4 | GPT-5.4 | GPT-4o | GPT-4o-mini | GPT-4o-mini | $0.50 | 15m | OpenAI preference |

## Autonomy Levels

```yaml
autonomy:
  default: "auto"  # Execute without asking

autonomy_levels:
  auto: "No approval needed, execute immediately"
  observe_after: "Execute, report, wait for OK to continue"
  approve: "Ask before executing"
  full_approve: "Ask at every step"
```

Set per task type:
```yaml
autonomy:
  tests: "auto"  # Always run tests
  bug_fixes: "observe_after"  # Run, then wait
  new_features: "full_approve"  # Ask about everything
```

## Quick Start for Users

```bash
# 1. Copy config
cp .engine/config.example.yaml .engine/config.yaml

# 2. Set API keys in .env.local
export ANTHROPIC_API_KEY="sk-ant-..."
export OPENAI_API_KEY="sk-..."

# 3. Validate
engine config check

# 4. Use!
"Fix issue #42"

# 5. Switch teams anytime
"Use fast team"
"Use premium team"
"Use local team"
```

## Provider Support

| Provider | Models | Cost | Setup | Speed |
|----------|--------|------|-------|-------|
| Anthropic | Claude Opus, Sonnet, Haiku | $$$ | API key | Medium |
| OpenAI | GPT-5.4, GPT-4o, GPT-4o-mini | $$$ | API key | Medium |
| Ollama | Gemma, Llama, Qwen, etc. | Free | Local install | Fast |
| Custom | Any custom endpoint | Varies | Custom endpoint | Varies |

## Next Steps (Priority Order)

### 🔴 Immediate (This week)
1. Create `TeamSelector.tsx` component in PreferencesPanel
2. Implement `load_engine_config` Tauri command
3. Wire team selection to preferences storage

### 🟡 High (Next week)
4. Add team selection to WebSocket handshake
5. Implement team resolution in orchestrator
6. Add cost tracking per team
7. Implement team override at request time

### 🟢 Medium (Ongoing)
8. CLI commands for team management
9. Cost reporting dashboard
10. Permission override UI
11. Custom team templates

### 🔵 Nice-to-Have
12. Team sharing / team presets
13. A/B testing different teams
14. Auto-team selection based on issue type
15. Cost predictions per team

## Validation Checklist

Before starting implementation:

- [ ] Read `.engine/QUICKSTART.md` — understand user workflow
- [ ] Read `.github/TEAM_ARCHITECTURE.md` — understand implementation details
- [ ] Review `.engine/config.example.yaml` — understand config schema
- [ ] Check PreferencesPanel.tsx — where to add team selector
- [ ] Review Tauri bridge.ts — how to add config loading command
- [ ] Review orchestrator.go — how to integrate team selection

## Success Criteria

**For users:**
- ✓ Can copy config, set keys, validate in 5 minutes
- ✓ Can select default team from PreferencesPanel
- ✓ Can override team at request time ("Use fast team")
- ✓ Can see cost/autonomy info for each team
- ✓ System respects autonomy level (auto/semi/approval)
- ✓ System respects cost limits

**For developers:**
- ✓ Team config loads from `.engine/config.yaml`
- ✓ Team resolution follows hierarchy (request → project → global)
- ✓ Agent spawning uses resolved team
- ✓ Cost is tracked and enforced
- ✓ All 4 pre-built teams work without extra config

## Code Review Checklist

When reviewing implementation PRs:

- [ ] Configuration loads and validates correctly
- [ ] Team resolution follows specified hierarchy
- [ ] Cost limits are enforced (agent stops if exceeded)
- [ ] Autonomy gates work as configured
- [ ] WebSocket messages use defined formats
- [ ] TypeScript types match TEAM_ARCHITECTURE.md
- [ ] User can select team from UI
- [ ] User can override team at runtime
- [ ] Logs show which team was used
- [ ] All 4 pre-built teams work

## Documentation for Users

When the implementation is complete, users will see:

1. `.engine/QUICKSTART.md` — 5-minute setup
2. `.engine/README.md` — Quick reference
3. `.engine/config.example.yaml` — Full config template
4. `.engine/INDEX.md` — Master index (this file)

Plus technical docs for developers:
5. `.github/CONFIGURABLE_TEAMS.md` — Team customization guide
6. `.github/TEAM_ARCHITECTURE.md` — Technical architecture
7. `.github/AUTONOMOUS_ORCHESTRATION.md` — System vision
8. `.github/AGENT_CONFIGURATION.md` — Agent specs
9. `.github/IMPLEMENTATION_GUIDE.md` — Code patterns

## Lessons Learned (This Session)

1. **Configuration hierarchy prevents user fatigue** — Users don't have to configure everything; sensible defaults work out of the box
2. **YAML is more accessible than JSON** — Easier for users to edit team definitions
3. **Team-based thinking is clearer than agent-by-agent** — Users care about "which team for this job" not "which agents on which models"
4. **Cost visibility matters** — Show estimated cost per team, allow budgets, track spend
5. **Autonomy is a spectrum** — Not binary (auto vs not); let users choose their comfort level
6. **Composition > Customization** — Pre-built teams for 90% of use cases; custom teams for the rest

## Related Issues & PRs

- `feat: refine settings menu and add orchestration docs` (4d1970f)
- `.github/AUTONOMOUS_ORCHESTRATION.md` created (vision)
- `.github/AGENT_CONFIGURATION.md` created (specs)
- `.github/IMPLEMENTATION_GUIDE.md` created (patterns)
- `.github/CONFIGURABLE_TEAMS.md` created (user guide)
- `.github/TEAM_ARCHITECTURE.md` created (technical spec)
- `.engine/config.example.yaml` created (template)
- `.engine/QUICKSTART.md` created (5-min guide)
- `.engine/README.md` created (reference)
- `.engine/INDEX.md` created (master index)

## Questions?

**For users**: See `.engine/QUICKSTART.md` or `.engine/README.md`

**For developers**: See `.github/TEAM_ARCHITECTURE.md` or `.github/IMPLEMENTATION_GUIDE.md`

**For the system vision**: See `.github/AUTONOMOUS_ORCHESTRATION.md`

---

**The specification is complete. Implementation can begin.**
