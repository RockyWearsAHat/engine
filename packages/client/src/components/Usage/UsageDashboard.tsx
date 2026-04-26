import { useCallback, useEffect, useMemo, useState } from 'react';
import { wsClient } from '../../ws/client.js';
import type { ServerMessage, UsageDashboard as UsageDashboardPayload } from '@engine/shared';
import { RefreshCw } from 'lucide-react';

type UsageScope = 'project' | 'user';

type Props = {
  projectPath: string | null;
  mode?: 'sidebar' | 'workspace';
};

function formatUSD(value: number): string {
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
    minimumFractionDigits: value < 1 ? 4 : 2,
    maximumFractionDigits: value < 1 ? 6 : 2,
  }).format(value);
}

function formatNumber(value: number): string {
  return new Intl.NumberFormat('en-US').format(value);
}

function formatMs(ms: number): string {
  if (ms <= 0) {
    return '0s';
  }
  const totalSeconds = Math.floor(ms / 1000);
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;

  if (hours > 0) {
    return `${hours}h ${minutes}m`;
  }
  if (minutes > 0) {
    return `${minutes}m ${seconds}s`;
  }
  return `${seconds}s`;
}

function formatModelLabel(provider: string, model: string): string {
  if (!provider) {
    return model;
  }
  return `${model} (${provider})`;
}

function formatPercent(value: number): string {
  return `${(value * 100).toFixed(1)}%`;
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

function toSparklinePath(values: number[]): string {
  const max = Math.max(...values, 1);
  const min = Math.min(...values, 0);
  const range = Math.max(max - min, 1e-9);

  return values
    .map((value, index) => {
      const x = (index / Math.max(values.length - 1, 1)) * 100;
      const normalized = (value - min) / range;
      const y = 100 - normalized * 100;
      return `${x.toFixed(2)},${clamp(y, 0, 100).toFixed(2)}`;
    })
    .join(' ');
}

export default function UsageDashboard({ projectPath, mode = 'sidebar' }: Props) {
  const [scope, setScope] = useState<UsageScope>('project');
  const [modelFilter, setModelFilter] = useState('');
  const [showProjectBreakdown, setShowProjectBreakdown] = useState(true);
  const [dashboard, setDashboard] = useState<UsageDashboardPayload | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [drillProjectPath, setDrillProjectPath] = useState<string | null>(null);

  const requestDashboard = useCallback((nextScope: UsageScope, nextModelFilter: string) => {
    setLoading(true);
    setError(null);
    wsClient.send({
      type: 'usage.dashboard.get',
      scope: nextScope,
      projectPath: nextScope === 'project' ? (drillProjectPath ?? projectPath ?? undefined) : undefined,
      model: nextModelFilter || undefined,
    });
  }, [projectPath, drillProjectPath]);

  useEffect(() => {
    const off = wsClient.onMessage((msg: ServerMessage) => {
      if (msg.type !== 'usage.dashboard') {
        return;
      }
      setLoading(false);
      if (msg.error) {
        setDashboard(null);
        setError(msg.error);
        return;
      }
      setDashboard(msg.dashboard ?? null);
      setError(null);
    });

    return () => off();
  }, []);

  const handleProjectDrill = useCallback((rawPath: string) => {
    setDrillProjectPath(rawPath);
    setScope('project');
  }, []);

  const handleDrillBack = useCallback(() => {
    setDrillProjectPath(null);
    setScope('user');
  }, []);

  useEffect(() => {
    if (scope === 'project' && !drillProjectPath && !projectPath) {
      setDashboard(null);
      setError('Open a project to view project-specific usage.');
      setLoading(false);
      return;
    }
    requestDashboard(scope, modelFilter);
  }, [scope, projectPath, modelFilter, requestDashboard, drillProjectPath]);

  const modelOptions = useMemo(() => {
    if (!dashboard) {
      return [];
    }
    return dashboard.models.map((entry) => ({
      value: entry.model,
      label: formatModelLabel(entry.provider, entry.model),
    }));
  }, [dashboard]);

  const sortedModels = useMemo(() => {
    if (!dashboard) {
      return [];
    }
    return [...dashboard.models].sort((a, b) => b.costUsd - a.costUsd);
  }, [dashboard]);

  const spendSeries = useMemo(() => sortedModels.map((entry) => entry.costUsd), [sortedModels]);

  const topModelShare = useMemo(() => {
    if (!dashboard || sortedModels.length === 0 || dashboard.totals.costUsd <= 0) {
      return 0;
    }
    return sortedModels[0].costUsd / dashboard.totals.costUsd;
  }, [dashboard, sortedModels]);

  const inputShare = useMemo(() => {
    if (!dashboard || dashboard.totals.totalTokens <= 0) {
      return 0;
    }
    return dashboard.totals.inputTokens / dashboard.totals.totalTokens;
  }, [dashboard]);

  const outputShare = useMemo(() => {
    if (!dashboard || dashboard.totals.totalTokens <= 0) {
      return 0;
    }
    return dashboard.totals.outputTokens / dashboard.totals.totalTokens;
  }, [dashboard]);

  const tokenSplitStyle = useMemo(() => {
    const inputDegrees = clamp(inputShare, 0, 1) * 360;
    return {
      background: `conic-gradient(var(--usage-accent-strong) 0deg ${inputDegrees}deg, var(--usage-accent-2) ${inputDegrees}deg 360deg)`,
    };
  }, [inputShare]);

  const projectRowsWithPaths = useMemo(() => {
    if (!dashboard) {
      return [] as { display: string[]; rawPath: string; share: number }[];
    }
    const totalCost = dashboard.totals.costUsd;
    return dashboard.projects
      .slice(0, showProjectBreakdown ? undefined : 8)
      .map((entry) => ({
        display: [
          compactProjectPath(entry.projectPath),
          formatUSD(entry.costUsd),
          formatNumber(entry.totalTokens),
          formatMs(entry.aiComputeMs),
          formatMs(entry.activeDevelopmentMs),
        ],
        rawPath: entry.projectPath,
        share: totalCost > 0 ? clamp(entry.costUsd / totalCost, 0, 1) : 0,
      }));
  }, [dashboard, showProjectBreakdown]);

  useEffect(() => {
    setShowProjectBreakdown(scope === 'project');
  }, [scope]);

  return (
    <div className={`usage-dashboard usage-dashboard-${mode}`}>
      <div className="usage-dashboard-header">
        <div className="usage-dashboard-header-copy">
          <div className="usage-dashboard-kicker">API Usage</div>
          <div className="usage-dashboard-title">Spend & Token Analytics</div>
          {dashboard && (
            <div className="usage-dashboard-subtitle">
              {scope === 'project' ? 'Project telemetry' : 'User telemetry'} from {formatNumber(dashboard.totals.requests)} requests.
            </div>
          )}
        </div>
        <div className="usage-dashboard-header-actions">
          <div className="usage-dashboard-scope-pill">{scope === 'project' ? 'Project Scope' : 'User Scope'}</div>
          <button
            type="button"
            className="usage-dashboard-refresh"
            onClick={() => requestDashboard(scope, modelFilter)}
            title="Refresh usage analytics"
            disabled={loading}
          >
            <RefreshCw size={13} className={loading ? 'animate-spin' : ''} />
          </button>
        </div>
      </div>

      <div className="usage-controls usage-panel">
        <div className="usage-scope-toggle" role="tablist" aria-label="Usage scope">
          <button
            type="button"
            className={`usage-scope-btn ${scope === 'project' ? 'active' : ''}`}
            onClick={() => { setDrillProjectPath(null); setScope('project'); }}
          >
            Project
          </button>
          <button
            type="button"
            className={`usage-scope-btn ${scope === 'user' ? 'active' : ''}`}
            onClick={() => { setDrillProjectPath(null); setScope('user'); }}
          >
            User
          </button>
        </div>

        <label className="usage-filter">
          <span className="usage-filter-label">Model</span>
          <select
            value={modelFilter}
            onChange={(event) => setModelFilter(event.target.value)}
            className="usage-filter-select"
          >
            <option value="">All models</option>
            {modelOptions.map((option) => (
              <option key={option.value} value={option.value}>{option.label}</option>
            ))}
          </select>
        </label>
      </div>

      {error && <div className="usage-state error">{error}</div>}
      {!error && loading && <div className="usage-state">Building usage dashboard…</div>}

      {!error && !loading && dashboard && (
        <>
          <div className="usage-metric-grid usage-panel">
            <MetricCard label="Total Spend" value={formatUSD(dashboard.totals.costUsd)} tone="strong" />
            <MetricCard label="Input Tokens" value={formatNumber(dashboard.totals.inputTokens)} tone="soft" />
            <MetricCard label="Output Tokens" value={formatNumber(dashboard.totals.outputTokens)} tone="soft" />
            <MetricCard label="Avg Price / Token" value={formatUSD(dashboard.totals.averagePricePerToken)} tone="soft" />
            <MetricCard label="AI Compute Time" value={formatMs(dashboard.totals.aiComputeMs)} tone="soft" />
            <MetricCard label="Active Dev Time" value={formatMs(dashboard.totals.activeDevelopmentMs)} tone="soft" />
          </div>

          <div className="usage-meta-row">
            <span>{formatNumber(dashboard.totals.requests)} requests</span>
            <span>{formatNumber(dashboard.totals.totalTokens)} total tokens</span>
            <span>Updated {new Date(dashboard.generatedAt).toLocaleString()}</span>
          </div>

          <div className="usage-visual-grid">
            <section className="usage-panel usage-visual-card">
              <div className="usage-visual-title">Model Spend Curve</div>
              <div className="usage-visual-copy">
                Top model currently contributes {formatPercent(topModelShare)} of spend.
              </div>
              {spendSeries.length > 0 ? (
                <svg viewBox="0 0 100 100" className="usage-sparkline" aria-label="Model spend curve">
                  <polyline points={`0,100 ${toSparklinePath(spendSeries)} 100,100`} className="usage-sparkline-area" />
                  <polyline points={toSparklinePath(spendSeries)} className="usage-sparkline-line" />
                </svg>
              ) : (
                <div className="usage-state">No model spend data yet.</div>
              )}
            </section>

            <section className="usage-panel usage-visual-card">
              <div className="usage-visual-title">Token Mix</div>
              <div className="usage-token-ring-wrap">
                <div className="usage-token-ring" style={tokenSplitStyle}>
                  <div className="usage-token-ring-inner">{formatNumber(dashboard.totals.totalTokens)}</div>
                </div>
                <div className="usage-token-legend">
                  <div className="usage-token-row">
                    <span className="usage-token-dot input" />
                    <span>Input</span>
                    <span>{formatPercent(inputShare)}</span>
                  </div>
                  <div className="usage-token-row">
                    <span className="usage-token-dot output" />
                    <span>Output</span>
                    <span>{formatPercent(outputShare)}</span>
                  </div>
                </div>
              </div>
            </section>

            <section className="usage-panel usage-visual-card usage-bar-card">
              <div className="usage-visual-title">Model Spend Breakdown</div>
              <div className="usage-model-bars">
                {sortedModels.slice(0, 6).map((entry) => {
                  const share = dashboard.totals.costUsd <= 0 ? 0 : entry.costUsd / dashboard.totals.costUsd;
                  return (
                    <div className="usage-model-row" key={`${entry.provider}:${entry.model}`}>
                      <div className="usage-model-label">{formatModelLabel(entry.provider, entry.model)}</div>
                      <div className="usage-model-meter">
                        <div className="usage-model-fill" style={{ width: `${clamp(share, 0, 1) * 100}%` }} />
                      </div>
                      <div className="usage-model-value">{formatUSD(entry.costUsd)}</div>
                    </div>
                  );
                })}
              </div>
            </section>
          </div>

          <div className="usage-table-grid">
            <section className="usage-table-block">
              <div className="usage-table-title-row">
                <div className="usage-table-title">Per Project</div>
                <div className="usage-table-title-actions">
                  {drillProjectPath && (
                    <button
                      type="button"
                      className="usage-drill-back"
                      onClick={handleDrillBack}
                    >
                      ← Back
                    </button>
                  )}
                  {scope === 'user' && !drillProjectPath && dashboard.projects.length > 0 && (
                    <button
                      type="button"
                      className="usage-table-toggle"
                      onClick={() => setShowProjectBreakdown((current) => !current)}
                    >
                      {showProjectBreakdown ? 'Show Top 8' : 'Show All'}
                    </button>
                  )}
                </div>
              </div>
              <ProjectBreakdownTable
                rows={projectRowsWithPaths}
                emptyLabel="No project usage yet."
                clickable={scope === 'user' && !drillProjectPath}
                onRowClick={handleProjectDrill}
              />
            </section>

            <UsageTable
              title="Per Model"
              emptyLabel="No model usage yet."
              headers={['Model', 'Spend', 'Input', 'Output', 'Avg / Token']}
              rows={dashboard.models.map((entry) => [
                formatModelLabel(entry.provider, entry.model),
                formatUSD(entry.costUsd),
                formatNumber(entry.inputTokens),
                formatNumber(entry.outputTokens),
                formatUSD(entry.averagePricePerToken),
              ])}
            />
          </div>
        </>
      )}
    </div>
  );
}

function MetricCard({ label, value, tone }: { label: string; value: string; tone: 'strong' | 'soft' }) {
  return (
    <div className={`usage-metric-card ${tone === 'strong' ? 'strong' : ''}`}>
      <div className="usage-metric-label">{label}</div>
      <div className="usage-metric-value">{value}</div>
    </div>
  );
}

function compactProjectPath(path: string): string {
  if (!path) {
    return 'unknown';
  }

  const normalized = path.replace(/\\/g, '/');
  const parts = normalized.split('/').filter(Boolean);
  if (parts.length === 0) {
    return 'unknown';
  }

  const tail = parts.slice(-2).join('/');
  if (tail.length <= 44) {
    return tail;
  }
  return `${tail.slice(0, 44)}...`;
}

function ProjectBreakdownTable({
  rows,
  emptyLabel,
  clickable,
  onRowClick,
}: {
  rows: { display: string[]; rawPath: string; share: number }[];
  emptyLabel: string;
  clickable: boolean;
  onRowClick: (rawPath: string) => void;
}) {
  const headers = ['Project', 'Spend', 'Tokens', 'AI Compute', 'Active Time'];
  if (rows.length === 0) {
    return <div className="usage-state">{emptyLabel}</div>;
  }
  return (
    <div className="usage-table-wrap">
      <table className="usage-table">
        <thead>
          <tr>
            {headers.map((header) => (
              <th key={header}>{header}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, rowIndex) => (
            <tr
              key={`row-${rowIndex}`}
              className={clickable ? 'usage-project-row-clickable' : undefined}
              onClick={clickable ? () => onRowClick(row.rawPath) : undefined}
              title={clickable ? `View breakdown for ${row.display[0]}` : undefined}
            >
              {row.display.map((value, cellIndex) => (
                <td key={`cell-${rowIndex}-${cellIndex}`}>
                  {cellIndex === 1 && clickable ? (
                    <div className="usage-project-spend-cell">
                      <span>{value}</span>
                      <div className="usage-project-share-bar-track">
                        <div className="usage-project-share-bar-fill" style={{ width: `${row.share * 100}%` }} />
                      </div>
                    </div>
                  ) : value}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function UsageTable({
  title,
  headers,
  rows,
  emptyLabel,
}: {
  title: string;
  headers: string[];
  rows: string[][];
  emptyLabel: string;
}) {
  return (
    <section className="usage-table-block">
      <div className="usage-table-title">{title}</div>
      <UsageTableBody headers={headers} rows={rows} emptyLabel={emptyLabel} />
    </section>
  );
}

function UsageTableBody({
  headers,
  rows,
  emptyLabel,
}: {
  headers: string[];
  rows: string[][];
  emptyLabel: string;
}) {
  return rows.length === 0 ? (
    <div className="usage-state">{emptyLabel}</div>
  ) : (
    <div className="usage-table-wrap">
      <table className="usage-table">
        <thead>
          <tr>
            {headers.map((header) => (
              <th key={header}>{header}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, rowIndex) => (
            <tr key={`row-${rowIndex}`}>
              {row.map((value, cellIndex) => (
                <td key={`cell-${rowIndex}-${cellIndex}`}>{value}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
