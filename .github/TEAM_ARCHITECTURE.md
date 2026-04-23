# Team-Based Orchestration — Technical Architecture

This document explains how the configuration system works under the hood and how to implement team-based agent routing.

## Architecture Overview

```
User Request: "Fix #42 with premium team"
    ↓
┌─────────────────────────────────────────────┐
│ 1. PARSE REQUEST                            │
│    - Detect action: "Fix #42"               │
│    - Detect team override: "premium team"   │
│    - Extract intent, branch scope           │
└─────────────────────────────────────────────┘
    ↓
┌─────────────────────────────────────────────┐
│ 2. LOAD PROJECT CONFIG                      │
│    - Read .engine/config.yaml               │
│    - Validate API credentials               │
│    - Resolve team definition                │
└─────────────────────────────────────────────┘
    ↓
┌─────────────────────────────────────────────┐
│ 3. BUILD AGENT TEAM                         │
│    - Instantiate agents with selected models│
│    - Validate all APIs are available        │
│    - Set timeouts and cost limits           │
└─────────────────────────────────────────────┘
    ↓
┌─────────────────────────────────────────────┐
│ 4. EXECUTE WITH TEAM                        │
│    - Orchestrator routes to other agents    │
│    - Each agent uses its configured model   │
│    - Results logged with costs              │
└─────────────────────────────────────────────┘
    ↓
Result: Issue fixed with premium team models
```

---

## Configuration Resolution Flow

```typescript
// 1. Parse user request
const parseRequest = (userMessage: string) => {
  const actionMatch = userMessage.match(/fix|refactor|implement|test/i);
  const teamMatch = userMessage.match(/with (\w+) team/i);
  const modelMatch = userMessage.match(/with (\w+) (as|for) (\w+)/i);
  
  return {
    action: actionMatch?.[0],
    team: teamMatch?.[1] || "default",  // Use default if not specified
    modelOverrides: modelMatch ? [{ role: modelMatch[3], model: modelMatch[1] }] : [],
  };
};

// 2. Load project config
const loadProjectConfig = async (projectRoot: string) => {
  const configPath = join(projectRoot, ".engine/config.yaml");
  const config = yaml.parse(await readFile(configPath, "utf-8"));
  validateConfig(config);
  return config;
};

// 3. Resolve team definition
const resolveTeam = (config: Config, teamName: string, overrides?: ModelOverride[]) => {
  const teamConfig = config.teams[teamName];
  if (!teamConfig) throw new Error(`Unknown team: ${teamName}`);
  
  // Apply user overrides
  const resolvedTeam = { ...teamConfig };
  if (overrides) {
    for (const override of overrides) {
      resolvedTeam[override.role] = { model: override.model };
    }
  }
  
  return resolvedTeam;
};

// 4. Validate APIs
const validateApis = (config: Config, team: ResolvedTeam) => {
  for (const role in team) {
    const modelString = team[role].model;
    const [provider, model] = modelString.split(":");
    
    if (!config.apis[provider]?.enabled) {
      throw new Error(`API ${provider} is not enabled`);
    }
    
    // Check credentials exist
    const credentialKey = `${provider.toUpperCase()}_API_KEY`;
    if (provider !== "ollama" && !process.env[credentialKey]) {
      throw new Error(`Missing credential: ${credentialKey}`);
    }
  }
};

// 5. Instantiate agent team
const instantiateTeam = (config: Config, team: ResolvedTeam) => {
  return {
    orchestrator: createAgent({
      name: "Orchestrator",
      model: resolveModel(team.orchestrator.model),
      timeout: team.orchestrator.timeout_seconds * 1000,
      costLimit: config.apis[getProvider(team.orchestrator.model)].max_cost_per_execution,
    }),
    architect: createAgent({
      name: "Architect",
      model: resolveModel(team.architect.model),
      timeout: team.architect.timeout_seconds * 1000,
      costLimit: config.apis[getProvider(team.architect.model)].max_cost_per_execution,
    }),
    implementer: createAgent({
      name: "Implementer",
      model: resolveModel(team.implementer.model),
      timeout: team.implementer.timeout_seconds * 1000,
      costLimit: config.apis[getProvider(team.implementer.model)].max_cost_per_execution,
    }),
    tester: createAgent({
      name: "Tester",
      model: resolveModel(team.tester.model),
      timeout: team.tester.timeout_seconds * 1000,
      costLimit: config.apis[getProvider(team.tester.model)].max_cost_per_execution,
    }),
    documenter: createAgent({
      name: "Documenter",
      model: resolveModel(team.documenter.model),
      timeout: team.documenter.timeout_seconds * 1000,
      costLimit: config.apis[getProvider(team.documenter.model)].max_cost_per_execution,
    }),
  };
};
```

---

## Model Resolution

Each model string has format: `provider:model-name`

```typescript
const resolveModel = (modelString: string) => {
  const [provider, modelName] = modelString.split(":");
  
  switch (provider) {
    case "anthropic":
      return {
        type: "anthropic",
        model: modelName,  // claude-opus-4.6, claude-sonnet-4.6, etc.
        apiKey: process.env.ANTHROPIC_API_KEY,
      };
    
    case "openai":
      return {
        type: "openai",
        model: modelName,  // gpt-5.4, gpt-4o, gpt-4o-mini
        apiKey: process.env.OPENAI_API_KEY,
      };
    
    case "ollama":
      return {
        type: "ollama",
        model: modelName,  // gemma4:31b, llama2:13b, etc.
        baseUrl: process.env.OLLAMA_BASE_URL || "http://localhost:11434",
      };
    
    case "custom":
      return {
        type: "custom",
        model: modelName,
        baseUrl: process.env.CUSTOM_API_BASE_URL,
        apiKey: process.env.CUSTOM_API_KEY,
      };
    
    default:
      throw new Error(`Unknown provider: ${provider}`);
  }
};
```

---

## Request-Time Team Selection

When users say "use premium team", this is how it's handled:

```typescript
const handleUserRequest = async (message: string) => {
  // 1. Parse the request
  const parsed = parseRequest(message);
  
  // 2. Load config
  const config = await loadProjectConfig(".");
  
  // 3. Resolve team (default if not specified)
  const team = resolveTeam(config, parsed.team, parsed.modelOverrides);
  
  // 4. Validate all APIs are available
  validateApis(config, team);
  
  // 5. Instantiate agents
  const agents = instantiateTeam(config, team);
  
  // 6. Execute orchestration
  const result = await executeOrchestration(agents, parsed.action);
  
  return result;
};

// Examples:
// "Fix #42"
//   → Uses default team (from config)
// "Fix #42 with premium team"
//   → Uses premium team (Opus + Sonnet + Haiku)
// "Use openai team for this"
//   → Uses openai team (GPT-5.4 orchestrator)
// "Use opus as orchestrator"
//   → Uses default team but replaces orchestrator with Opus
```

---

## Cost Tracking

Every agent execution logs its cost:

```typescript
const executeAgent = async (agent: Agent, task: Task) => {
  const startTime = Date.now();
  const startTokens = agent.tokenCount;
  
  try {
    const result = await agent.execute(task);
    
    const endTokens = agent.tokenCount;
    const inputTokens = endTokens.input - startTokens.input;
    const outputTokens = endTokens.output - startTokens.output;
    
    // Calculate cost based on provider pricing
    const cost = calculateCost(agent.model.provider, inputTokens, outputTokens);
    
    // Log execution
    await logExecution({
      agent: agent.name,
      team: currentTeamName,
      model: agent.model.name,
      task: task.description,
      inputTokens,
      outputTokens,
      cost,
      duration_ms: Date.now() - startTime,
      status: "success",
    });
    
    return { result, cost };
  } catch (error) {
    await logExecution({
      agent: agent.name,
      team: currentTeamName,
      model: agent.model.name,
      task: task.description,
      cost: 0,
      duration_ms: Date.now() - startTime,
      status: "failed",
      error: error.message,
    });
    throw error;
  }
};
```

---

## Pricing Reference

Used for cost calculation:

```typescript
const PRICING = {
  anthropic: {
    "claude-opus-4.6": {
      input: 0.00003,   // $0.03 per 1K tokens
      output: 0.00015,  // $0.15 per 1K tokens
    },
    "claude-sonnet-4.6": {
      input: 0.000003,  // $0.003 per 1K tokens
      output: 0.00004,  // $0.04 per 1K tokens
    },
    "claude-haiku-4.5": {
      input: 0.00000080,  // $0.80 per 1M tokens
      output: 0.000004,   // $4 per 1M tokens
    },
  },
  
  openai: {
    "gpt-5.4": {
      input: 0.000003,   // $3 per 1M tokens
      output: 0.000012,  // $12 per 1M tokens
    },
    "gpt-4o": {
      input: 0.000005,   // $5 per 1M tokens
      output: 0.000015,  // $15 per 1M tokens
    },
    "gpt-4o-mini": {
      input: 0.00000015, // $0.15 per 1M tokens
      output: 0.0000006, // $0.60 per 1M tokens
    },
  },
  
  ollama: {
    "*": {
      input: 0.0,  // Free (local)
      output: 0.0,
    },
  },
};

const calculateCost = (provider: string, model: string, inputTokens: number, outputTokens: number) => {
  const prices = PRICING[provider][model];
  return (inputTokens * prices.input) + (outputTokens * prices.output);
};
```

---

## Fallback & Escalation

If a model fails, automatically try a better one:

```typescript
const executeWithFallback = async (agent: Agent, task: Task) => {
  try {
    return await executeAgent(agent, task);
  } catch (error) {
    if (error.code === "TIMEOUT") {
      // Model took too long, use a faster one
      const escalationModel = config.advanced.escalation_model_map[agent.model.name];
      if (escalationModel) {
        console.log(`Escalating ${agent.name} from ${agent.model.name} to ${escalationModel}`);
        
        const betterAgent = createAgent({
          ...agent,
          model: resolveModel(escalationModel),
        });
        
        return await executeAgent(betterAgent, task);
      }
    }
    
    if (error.code === "RATE_LIMIT") {
      // Hit rate limit, wait and retry
      await sleep(60000);  // Wait 1 minute
      return await executeWithFallback(agent, task);
    }
    
    throw error;
  }
};
```

---

## Team Selection Flow in Chat

```
User: "Fix #42 with premium team"
    ↓
Chat handler detects team override
    ↓
Orchestrator loads config and team definition
    ↓
"Switching to premium team..."
    ↓
Instantiate: Opus (orchestrator), Opus (architect), Sonnet (implementer), Haiku (tester)
    ↓
"Premium team ready. Analyzing issue..."
    ↓
[Execute with premium agents]
    ↓
"Complete. Cost: $1.05. (5 min human time)"
```

---

## Configuration Validation

When loading config, validate everything:

```typescript
const validateConfig = (config: Config) => {
  // Check teams exist
  for (const team of Object.values(config.teams)) {
    for (const role of ["orchestrator", "architect", "implementer", "tester", "documenter"]) {
      if (!team[role]) throw new Error(`Team missing ${role}`);
    }
  }
  
  // Check models are valid
  for (const [teamName, team] of Object.entries(config.teams)) {
    for (const [role, agent] of Object.entries(team)) {
      const [provider, model] = agent.model.split(":");
      if (!provider || !model) {
        throw new Error(`Invalid model format in ${teamName}.${role}: ${agent.model}`);
      }
    }
  }
  
  // Check API configs exist for used providers
  const usedProviders = new Set();
  for (const team of Object.values(config.teams)) {
    for (const agent of Object.values(team)) {
      usedProviders.add(agent.model.split(":")[0]);
    }
  }
  
  for (const provider of usedProviders) {
    if (!config.apis[provider]) {
      throw new Error(`API config missing for provider: ${provider}`);
    }
  }
  
  // Check autonomy levels are valid
  const validLevels = ["auto", "observe_after", "approve", "architect_approve", "full_approve"];
  for (const [taskType, autonomy] of Object.entries(config.autonomy)) {
    if (taskType !== "default" && !validLevels.includes(autonomy.level)) {
      throw new Error(`Invalid autonomy level: ${autonomy.level}`);
    }
  }
};
```

---

## Environment Setup

Users need to set up their environment:

```bash
# 1. Create .engine directory
mkdir -p .engine

# 2. Copy example config
cp .engine/config.example.yaml .engine/config.yaml

# 3. Set API keys (in .env.local, never commit!)
echo "ANTHROPIC_API_KEY=sk-ant-..." > .env.local
echo "OPENAI_API_KEY=sk-..." >> .env.local
echo "OLLAMA_BASE_URL=http://localhost:11434" >> .env.local

# 4. Load environment
source .env.local

# 5. Validate setup
engine config check
```

---

## Implementation Checklist

To implement team-based orchestration:

- [ ] Load config from `.engine/config.yaml`
- [ ] Parse user request for team selection
- [ ] Resolve team definition from config
- [ ] Validate all required APIs are configured
- [ ] Instantiate agents with correct models
- [ ] Execute orchestration with selected agents
- [ ] Track costs per agent
- [ ] Log execution details
- [ ] Provide fallback if agents fail
- [ ] Support model overrides at runtime
- [ ] Support autonomy level overrides
- [ ] Provide cost warnings before executing
- [ ] Allow users to cancel before spending

