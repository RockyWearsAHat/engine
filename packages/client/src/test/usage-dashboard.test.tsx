import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import UsageDashboard from '../components/Usage/UsageDashboard.js';
import type { ServerMessage, UsageDashboard as UsageDashboardPayload } from '@engine/shared';

const { sendMock, handlers } = vi.hoisted(() => ({
  sendMock: vi.fn(),
  handlers: new Set<(msg: ServerMessage) => void>(),
}));

vi.mock('../ws/client.js', () => ({
  wsClient: {
    send: sendMock,
    onMessage: (handler: (msg: ServerMessage) => void) => {
      handlers.add(handler);
      return () => handlers.delete(handler);
    },
  },
}));

function emitMessage(msg: ServerMessage): void {
  handlers.forEach((handler) => handler(msg));
}

function makeDashboard(overrides?: Partial<UsageDashboardPayload>): UsageDashboardPayload {
  return {
    scope: 'project',
    projectPath: '/workspace/project-a',
    modelFilter: '',
    generatedAt: '2026-04-26T12:00:00Z',
    totals: {
      requests: 8,
      inputTokens: 3000,
      outputTokens: 1800,
      totalTokens: 4800,
      costUsd: 1.2345,
      aiComputeMs: 91234,
      activeDevelopmentMs: 256000,
      averagePricePerToken: 0.000257,
    },
    models: [
      {
        provider: 'anthropic',
        model: 'claude-sonnet-4.6',
        requests: 6,
        inputTokens: 2500,
        outputTokens: 1400,
        totalTokens: 3900,
        costUsd: 1.1,
        aiComputeMs: 81234,
        averagePricePerToken: 0.000282,
      },
      {
        provider: 'openai',
        model: 'gpt-4o',
        requests: 2,
        inputTokens: 500,
        outputTokens: 400,
        totalTokens: 900,
        costUsd: 0.1345,
        aiComputeMs: 10000,
        averagePricePerToken: 0.000149,
      },
    ],
    projects: [
      {
        projectPath: '/workspace/project-a',
        requests: 8,
        inputTokens: 3000,
        outputTokens: 1800,
        totalTokens: 4800,
        costUsd: 1.2345,
        aiComputeMs: 91234,
        activeDevelopmentMs: 256000,
        averagePricePerToken: 0.000257,
      },
    ],
    ...overrides,
  };
}

describe('UsageDashboard', () => {
  beforeEach(() => {
    sendMock.mockReset();
    handlers.clear();
  });

  it('shows guidance when project scope has no active project', async () => {
    render(<UsageDashboard projectPath={null} />);

    expect(await screen.findByText(/Open a project to view project-specific usage/i)).toBeTruthy();
    expect(sendMock).not.toHaveBeenCalled();

    fireEvent.click(screen.getByTitle(/Refresh usage analytics/i));
    expect(sendMock).toHaveBeenCalledWith({
      type: 'usage.dashboard.get',
      scope: 'project',
      projectPath: undefined,
      model: undefined,
    });
  });

  it('requests and renders project usage metrics', async () => {
    render(<UsageDashboard projectPath="/workspace/project-a" />);

    expect(screen.getByText(/Building usage dashboard/i)).toBeTruthy();
    expect(sendMock).toHaveBeenCalledWith({
      type: 'usage.dashboard.get',
      scope: 'project',
      projectPath: '/workspace/project-a',
      model: undefined,
    });

    emitMessage({ type: 'usage.dashboard', dashboard: makeDashboard() });

    expect(await screen.findByText('Spend & Token Analytics')).toBeTruthy();
    expect(screen.getByText('Total Spend')).toBeTruthy();
    expect(screen.getByText('Input Tokens')).toBeTruthy();
    expect(screen.getByText('Output Tokens')).toBeTruthy();
    expect(screen.getByText('AI Compute Time')).toBeTruthy();
    expect(screen.getByText('Active Dev Time')).toBeTruthy();
    expect(screen.getByText('Per Project')).toBeTruthy();
    expect(screen.getByText('Per Model')).toBeTruthy();
    expect(screen.getAllByText(/claude-sonnet-4.6/i).length).toBeGreaterThan(0);
  });

  it('switches scope in both directions and re-requests dashboard', async () => {
    render(<UsageDashboard projectPath="/workspace/project-a" />);
    emitMessage({ type: 'usage.dashboard', dashboard: makeDashboard() });

    fireEvent.click(await screen.findByRole('button', { name: 'User' }));

    await waitFor(() => {
      expect(sendMock).toHaveBeenCalledWith({
        type: 'usage.dashboard.get',
        scope: 'user',
        projectPath: undefined,
        model: undefined,
      });
    });

    fireEvent.click(screen.getByRole('button', { name: 'Project' }));

    await waitFor(() => {
      expect(sendMock).toHaveBeenCalledWith({
        type: 'usage.dashboard.get',
        scope: 'project',
        projectPath: '/workspace/project-a',
        model: undefined,
      });
    });
  });

  it('filters by model and includes model in subsequent request', async () => {
    render(<UsageDashboard projectPath="/workspace/project-a" />);
    emitMessage({ type: 'usage.dashboard', dashboard: makeDashboard() });

    await screen.findByText('Per Model');

    fireEvent.change(screen.getByLabelText('Model'), {
      target: { value: 'gpt-4o' },
    });

    await waitFor(() => {
      expect(sendMock).toHaveBeenCalledWith({
        type: 'usage.dashboard.get',
        scope: 'project',
        projectPath: '/workspace/project-a',
        model: 'gpt-4o',
      });
    });
  });

  it('refresh button re-requests with current scope and filter', async () => {
    render(<UsageDashboard projectPath="/workspace/project-a" />);
    emitMessage({ type: 'usage.dashboard', dashboard: makeDashboard() });
    await screen.findByText('Per Project');

    fireEvent.change(screen.getByLabelText('Model'), {
      target: { value: 'claude-sonnet-4.6' },
    });

    await waitFor(() => {
      expect(sendMock).toHaveBeenCalledWith({
        type: 'usage.dashboard.get',
        scope: 'project',
        projectPath: '/workspace/project-a',
        model: 'claude-sonnet-4.6',
      });
    });

    fireEvent.click(screen.getByTitle(/Refresh usage analytics/i));

    await waitFor(() => {
      expect(sendMock).toHaveBeenLastCalledWith({
        type: 'usage.dashboard.get',
        scope: 'project',
        projectPath: '/workspace/project-a',
        model: 'claude-sonnet-4.6',
      });
    });
  });

  it('renders empty table states and unknown project/provider fallbacks', async () => {
    render(<UsageDashboard projectPath="/workspace/project-a" />);

    emitMessage({
      type: 'usage.dashboard',
      dashboard: makeDashboard({
        projects: [
          {
            projectPath: '',
            requests: 1,
            inputTokens: 0,
            outputTokens: 0,
            totalTokens: 0,
            costUsd: 0,
            aiComputeMs: 0,
            activeDevelopmentMs: 0,
            averagePricePerToken: 0,
          },
        ],
        models: [
          {
            provider: '',
            model: 'offline-local',
            requests: 1,
            inputTokens: 1,
            outputTokens: 1,
            totalTokens: 2,
            costUsd: 0,
            aiComputeMs: 10,
            averagePricePerToken: 0,
          },
        ],
      }),
    });

    expect(await screen.findByText('unknown')).toBeTruthy();
    expect(screen.getAllByText('offline-local').length).toBeGreaterThan(0);
  });

  it('shows project empty state while model table still has rows', async () => {
    render(<UsageDashboard projectPath="/workspace/project-a" />);

    emitMessage({
      type: 'usage.dashboard',
      dashboard: makeDashboard({
        projects: [],
        models: [
          {
            provider: 'anthropic',
            model: 'claude-sonnet-4.6',
            requests: 3,
            inputTokens: 120,
            outputTokens: 80,
            totalTokens: 200,
            costUsd: 0.012,
            aiComputeMs: 1000,
            averagePricePerToken: 0.00006,
          },
        ],
      }),
    });

    expect(await screen.findByText(/No project usage yet\./i)).toBeTruthy();
    expect(screen.getAllByText(/claude-sonnet-4.6/i).length).toBeGreaterThan(0);
  });

  it('ignores unrelated WS messages and handles missing dashboard payload', async () => {
    render(<UsageDashboard projectPath="/workspace/project-a" />);

    emitMessage({ type: 'chat.started', sessionId: 'abc' });
    emitMessage({ type: 'usage.dashboard' });

    await waitFor(() => {
      expect(screen.queryByText('Per Project')).toBeNull();
    });
  });

  it('formats zero, second, and hour-scale durations', async () => {
    render(<UsageDashboard projectPath="/workspace/project-a" />);

    emitMessage({
      type: 'usage.dashboard',
      dashboard: makeDashboard({
        totals: {
          requests: 1,
          inputTokens: 1,
          outputTokens: 1,
          totalTokens: 2,
          costUsd: 0,
          aiComputeMs: 5_000,
          activeDevelopmentMs: 3_700_000,
          averagePricePerToken: 0,
        },
        projects: [
          {
            projectPath: '/workspace/project-a',
            requests: 1,
            inputTokens: 1,
            outputTokens: 1,
            totalTokens: 2,
            costUsd: 0,
            aiComputeMs: 5_000,
            activeDevelopmentMs: 0,
            averagePricePerToken: 0,
          },
        ],
      }),
    });

    expect((await screen.findAllByText('5s')).length).toBeGreaterThan(0);
    expect(screen.getAllByText('1h 1m').length).toBeGreaterThan(0);
    expect(screen.getAllByText('0s').length).toBeGreaterThan(0);
  });

  it('refresh click triggers explicit request when idle', async () => {
    render(<UsageDashboard projectPath="/workspace/project-a" />);
    emitMessage({ type: 'usage.dashboard', dashboard: makeDashboard() });

    await screen.findByText('Per Project');
    fireEvent.click(screen.getByTitle(/Refresh usage analytics/i));

    await waitFor(() => {
      expect(sendMock).toHaveBeenLastCalledWith({
        type: 'usage.dashboard.get',
        scope: 'project',
        projectPath: '/workspace/project-a',
        model: undefined,
      });
    });
  });

  it('shows dashboard request errors', async () => {
    render(<UsageDashboard projectPath="/workspace/project-a" />);

    emitMessage({ type: 'usage.dashboard', error: 'dashboard unavailable' });

    expect(await screen.findByText('dashboard unavailable')).toBeTruthy();
  });
});
