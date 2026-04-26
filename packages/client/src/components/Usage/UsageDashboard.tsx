import { useCallback, useEffect, useMemo, useState } from 'react';
import { wsClient } from '../../ws/client.js';
import type { ServerMessage, UsageDashboard as UsageDashboardPayload } from '@engine/shared';
import { RefreshCw } from 'lucide-react';

type UsageScope = 'project' | 'user';

type Props = {
  projectPath: string | null;
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

export default function UsageDashboard({ projectPath }: Props) {
  const [scope, setScope] = useState<UsageScope>('project');
  const [modelFilter, setModelFilter] = useState('');
  const [dashboard, setDashboard] = useState<UsageDashboardPayload | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const requestDashboard = useCallback((nextScope: UsageScope, nextModelFilter: string) => {
    setLoading(true);
    setError(null);
    wsClient.send({
      type: 'usage.dashboard.get',
      scope: nextScope,
      projectPath: nextScope === 'project' ? (projectPath ?? undefined) : undefined,
      model: nextModelFilter || undefined,
    });
  }, [projectPath]);

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

  useEffect(() => {
    if (scope === 'project' && !projectPath) {
      setDashboard(null);
      setError('Open a project to view project-specific usage.');
      setLoading(false);
      return;
    }
    requestDashboard(scope, modelFilter);
  }, [scope, projectPath, modelFilter, requestDashboard]);

  const modelOptions = useMemo(() => {
    if (!dashboard) {
      return [];
    }
    return dashboard.models.map((entry) => ({
      value: entry.model,
      label: formatModelLabel(entry.provider, entry.model),
    }));
  }, [dashboard]);

  return (
    <div className="usage-dashboard">
      <div className="usage-dashboard-header">
        <div>
          <div className="usage-dashboard-kicker">API Usage</div>
          <div className="usage-dashboard-title">Spend & Token Analytics</div>
        </div>
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

      <div className="usage-controls">
        <div className="usage-scope-toggle" role="tablist" aria-label="Usage scope">
          <button
            type="button"
            className={`usage-scope-btn ${scope === 'project' ? 'active' : ''}`}
            onClick={() => setScope('project')}
          >
            Project
          </button>
          <button
            type="button"
            className={`usage-scope-btn ${scope === 'user' ? 'active' : ''}`}
            onClick={() => setScope('user')}
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
          <div className="usage-metric-grid">
            <MetricCard label="Total Spend" value={formatUSD(dashboard.totals.costUsd)} />
            <MetricCard label="Input Tokens" value={formatNumber(dashboard.totals.inputTokens)} />
            <MetricCard label="Output Tokens" value={formatNumber(dashboard.totals.outputTokens)} />
            <MetricCard label="Avg Price / Token" value={formatUSD(dashboard.totals.averagePricePerToken)} />
            <MetricCard label="AI Compute Time" value={formatMs(dashboard.totals.aiComputeMs)} />
            <MetricCard label="Active Dev Time" value={formatMs(dashboard.totals.activeDevelopmentMs)} />
          </div>

          <div className="usage-meta-row">
            <span>{formatNumber(dashboard.totals.requests)} requests</span>
            <span>{formatNumber(dashboard.totals.totalTokens)} total tokens</span>
            <span>Updated {new Date(dashboard.generatedAt).toLocaleString()}</span>
          </div>

          <UsageTable
            title="Per Project"
            emptyLabel="No project usage yet."
            headers={['Project', 'Spend', 'Tokens', 'AI Compute', 'Active Time']}
            rows={dashboard.projects.map((entry) => [
              entry.projectPath || 'unknown',
              formatUSD(entry.costUsd),
              formatNumber(entry.totalTokens),
              formatMs(entry.aiComputeMs),
              formatMs(entry.activeDevelopmentMs),
            ])}
          />

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
        </>
      )}
    </div>
  );
}

function MetricCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="usage-metric-card">
      <div className="usage-metric-label">{label}</div>
      <div className="usage-metric-value">{value}</div>
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
      {rows.length === 0 ? (
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
                <tr key={`${title}-${rowIndex}`}>
                  {row.map((value, cellIndex) => (
                    <td key={`${title}-${rowIndex}-${cellIndex}`}>{value}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
