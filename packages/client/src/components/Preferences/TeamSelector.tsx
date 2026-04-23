import { useCallback, useEffect, useState } from 'react';
import { Check, Cpu, Zap, Star, DollarSign } from 'lucide-react';
import { bridge } from '../../bridge.js';
import { wsClient } from '../../ws/client.js';
import type { EngineAgentModel, EngineTeamConfig, EngineConfig } from '@engine/shared';

// ── Inline YAML parser for the specific .engine/config.yaml structure ─────────
// Handles teams block only — no full YAML spec needed.

function extractYamlValue(line: string, prefix: string): string {
  const rest = line.slice(prefix.length).trim();
  if (
    (rest.startsWith('"') && rest.endsWith('"')) ||
    (rest.startsWith("'") && rest.endsWith("'"))
  ) {
    return rest.slice(1, -1);
  }
  return rest;
}

function emptyAgentModel(): EngineAgentModel {
  return { model: '', modelDisplay: '' };
}

function parseEngineConfigYaml(yaml: string): EngineConfig {
  const config: EngineConfig = { teams: {} };
  if (!yaml.trim()) return config;

  const lines = yaml.split('\n');
  let inTeams = false;
  let currentTeam = '';
  let currentRole = '';

  for (const rawLine of lines) {
    const stripped = rawLine.trimEnd();
    if (!stripped || stripped.trimStart().startsWith('#')) continue;

    const indent = rawLine.length - rawLine.trimStart().length;
    const trimmed = stripped.trimStart();

    if (indent === 0) {
      inTeams = trimmed === 'teams:';
      continue;
    }

    if (!inTeams) continue;

    if (indent === 2) {
      currentTeam = trimmed.replace(/:$/, '');
      currentRole = '';
      config.teams[currentTeam] = {
        name: currentTeam,
        description: '',
        orchestrator: emptyAgentModel(),
        architect: emptyAgentModel(),
        implementer: emptyAgentModel(),
        tester: emptyAgentModel(),
        documenter: emptyAgentModel(),
      };
      continue;
    }

    if (!currentTeam) continue;

    if (indent === 4) {
      if (trimmed.startsWith('description:')) {
        config.teams[currentTeam].description = extractYamlValue(trimmed, 'description:');
      } else {
        currentRole = trimmed.replace(/:$/, '');
      }
      continue;
    }

    if (indent === 6 && currentRole) {
      const team = config.teams[currentTeam];
      const roleKey = currentRole as keyof Pick<
        EngineTeamConfig,
        'orchestrator' | 'architect' | 'implementer' | 'tester' | 'documenter'
      >;
      if (!team[roleKey]) continue;
      const agentModel = team[roleKey] as EngineAgentModel;

      if (trimmed.startsWith('model_display:')) {
        agentModel.modelDisplay = extractYamlValue(trimmed, 'model_display:');
      } else if (trimmed.startsWith('model:')) {
        agentModel.model = extractYamlValue(trimmed, 'model:');
      }
    }
  }

  return config;
}

// ── Cost estimation ───────────────────────────────────────────────────────────

type CostTier = 'free' | 'low' | 'medium' | 'high';

function teamCostTier(team: EngineTeamConfig): CostTier {
  const models = [
    team.orchestrator.model,
    team.architect.model,
    team.implementer.model,
    team.tester.model,
    team.documenter.model,
  ];
  const hasOpus = models.some(m => m.includes('opus'));
  const hasAnthropicOrOpenAI = models.some(
    m => m.startsWith('anthropic:') || m.startsWith('openai:'),
  );
  const allOllama = models.every(m => m.startsWith('ollama:'));

  if (allOllama) return 'free';
  if (hasOpus) return 'high';
  if (hasAnthropicOrOpenAI) return 'low';
  return 'medium';
}

function costLabel(tier: CostTier): string {
  switch (tier) {
    case 'free': return 'Free';
    case 'low': return '~$0.30/run';
    case 'medium': return '~$0.50/run';
    case 'high': return '~$1.05/run';
  }
}

function teamIcon(teamName: string) {
  switch (teamName) {
    case 'fast': return <Zap size={15} />;
    case 'premium': return <Star size={15} />;
    case 'openai': return <Cpu size={15} />;
    default: return <Cpu size={15} />;
  }
}

// ── Helper: resolve provider/model from a model string ───────────────────────

function splitModelString(modelString: string): { provider: string; model: string } {
  const idx = modelString.indexOf(':');
  if (idx === -1) return { provider: 'anthropic', model: modelString };
  return {
    provider: modelString.slice(0, idx),
    model: modelString.slice(idx + 1),
  };
}

// ── Component ─────────────────────────────────────────────────────────────────

export default function TeamSelector() {
  const [engineConfig, setEngineConfig] = useState<EngineConfig | null>(null);
  const [activeTeam, setActiveTeam] = useState<string | null>(null);
  const [saving, setSaving] = useState<string | null>(null);
  const [configError, setConfigError] = useState<string | null>(null);

  // Load active team from Tauri config on mount.
  useEffect(() => {
    bridge.getActiveTeam().then(team => {
      if (team) setActiveTeam(team);
    });
  }, []);

  // Request engine config over WebSocket on mount.
  useEffect(() => {
    const unsub = wsClient.onMessage((msg: unknown) => {
      const m = msg as { type?: string; yaml?: string; error?: string; team?: string };
      if (!m || typeof m.type !== 'string') return;

      if (m.type === 'engine.config') {
        if (m.error && !m.yaml) {
          setConfigError(m.error);
          return;
        }
        if (m.yaml) {
          const parsed = parseEngineConfigYaml(m.yaml);
          setEngineConfig(parsed);
          setConfigError(null);
        }
      }

      if (m.type === 'engine.team.updated' && m.team) {
        setActiveTeam(m.team);
      }
    });

    const request = () => wsClient.send({ type: 'engine.config.get' } as never);
    request();
    const unsubOpen = wsClient.onOpen(() => request());

    return () => {
      unsub();
      unsubOpen();
    };
  }, []);

  const selectTeam = useCallback(
    async (teamName: string, team: EngineTeamConfig) => {
      setSaving(teamName);
      const { provider, model } = splitModelString(team.orchestrator.model);

      // Persist to Tauri AppConfig.
      await bridge.setActiveTeam(teamName);

      // Tell the Go server to switch its model/provider env vars.
      wsClient.send({
        type: 'engine.team.set',
        team: teamName,
        provider,
        model,
      } as never);

      setActiveTeam(teamName);
      setSaving(null);
    },
    [],
  );

  if (configError) {
    return (
      <div className="preferences-muted" style={{ padding: '8px 0', fontSize: 12 }}>
        No team config found. Copy{' '}
        <code style={{ fontFamily: 'monospace', background: 'var(--surface-3)', padding: '1px 4px', borderRadius: 3 }}>
          .engine/config.example.yaml
        </code>{' '}
        to{' '}
        <code style={{ fontFamily: 'monospace', background: 'var(--surface-3)', padding: '1px 4px', borderRadius: 3 }}>
          .engine/config.yaml
        </code>{' '}
        to enable team selection.
      </div>
    );
  }

  if (!engineConfig) {
    return (
      <div className="preferences-muted" style={{ padding: '8px 0', fontSize: 12 }}>
        Loading team config…
      </div>
    );
  }

  const teams = Object.entries(engineConfig.teams);
  if (teams.length === 0) {
    return (
      <div className="preferences-muted" style={{ padding: '8px 0', fontSize: 12 }}>
        No teams defined in <code>.engine/config.yaml</code>.
      </div>
    );
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      {teams.map(([name, team]) => {
        const isActive = activeTeam === name;
        const isSaving = saving === name;
        const tier = teamCostTier(team);

        return (
          <button
            key={name}
            onClick={() => void selectTeam(name, team)}
            disabled={isSaving}
            style={{
              display: 'flex',
              alignItems: 'flex-start',
              gap: 10,
              padding: '10px 12px',
              background: isActive ? 'var(--accent-dim)' : 'var(--surface-2)',
              border: `1px solid ${isActive ? 'var(--accent)' : 'var(--border)'}`,
              borderRadius: 'var(--radius-lg)',
              cursor: 'pointer',
              textAlign: 'left',
              width: '100%',
              transition: 'border-color 0.15s, background 0.15s',
              opacity: isSaving ? 0.7 : 1,
            }}
          >
            {/* Icon */}
            <span
              style={{
                color: isActive ? 'var(--accent)' : 'var(--tx-3)',
                marginTop: 1,
                flexShrink: 0,
              }}
            >
              {teamIcon(name)}
            </span>

            {/* Text */}
            <div style={{ flex: 1, minWidth: 0 }}>
              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 6,
                  marginBottom: 3,
                }}
              >
                <span
                  style={{
                    fontWeight: 600,
                    fontSize: 13,
                    color: isActive ? 'var(--accent-2)' : 'var(--tx)',
                    textTransform: 'capitalize',
                  }}
                >
                  {name}
                </span>
                <span
                  style={{
                    fontSize: 10,
                    padding: '1px 5px',
                    borderRadius: 3,
                    background: tier === 'free' ? 'rgba(34,197,94,0.12)' : tier === 'high' ? 'rgba(244,63,94,0.12)' : 'rgba(124,140,255,0.12)',
                    color: tier === 'free' ? 'var(--green)' : tier === 'high' ? 'var(--red)' : 'var(--accent)',
                    display: 'flex',
                    alignItems: 'center',
                    gap: 3,
                  }}
                >
                  <DollarSign size={9} />
                  {costLabel(tier)}
                </span>
              </div>
              {team.description && (
                <div style={{ fontSize: 11, color: 'var(--tx-3)', marginBottom: 5 }}>
                  {team.description}
                </div>
              )}
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                {(
                  [
                    ['Orchestrator', team.orchestrator],
                    ['Architect', team.architect],
                    ['Implementer', team.implementer],
                    ['Tester', team.tester],
                    ['Documenter', team.documenter],
                  ] as [string, EngineAgentModel][]
                ).map(([role, agentModel]) => (
                  <span
                    key={role}
                    title={`${role}: ${agentModel.model}`}
                    style={{
                      fontSize: 10,
                      color: 'var(--tx-3)',
                      background: 'var(--surface-3)',
                      padding: '1px 5px',
                      borderRadius: 3,
                      fontFamily: 'monospace',
                    }}
                  >
                    {role[0]}:{' '}
                    {agentModel.modelDisplay ||
                      agentModel.model.split(':').pop() ||
                      agentModel.model}
                  </span>
                ))}
              </div>
            </div>

            {/* Selection indicator */}
            {isActive && (
              <span style={{ color: 'var(--accent)', flexShrink: 0, marginTop: 1 }}>
                <Check size={14} />
              </span>
            )}
          </button>
        );
      })}
    </div>
  );
}
