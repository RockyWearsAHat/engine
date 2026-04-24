import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import AgentPanel from '../components/AgentPanel/AgentPanel.js';
import { useStore } from '../store/index.js';

const { sendMock } = vi.hoisted(() => ({
  sendMock: vi.fn(),
}));

vi.mock('../ws/client.js', () => ({
  wsClient: {
    send: sendMock,
  },
}));

function makeSession(id: string, projectPath: string, branchName: string) {
  return {
    id,
    projectPath,
    branchName,
    createdAt: '',
    updatedAt: '',
    summary: '',
    messageCount: 0,
  };
}

describe('AgentPanel interactions', () => {
  beforeEach(() => {
    sendMock.mockClear();
    useStore.setState({
      sessions: [],
      activeSession: null,
      activeAgentSessionId: null,
      agentSessions: [],
    });
  });

  it('NoActiveProjectSession_SessionCreationDisabled', () => {
    render(<AgentPanel />);
    expect(screen.getByTitle(/new session/i)).toBeDisabled();
  });

  it('ActiveProject_NewSessionRequested', () => {
    useStore.setState({
      activeSession: makeSession('active-1', '/workspace/project-a', 'main'),
    });
    render(<AgentPanel />);

    fireEvent.click(screen.getByTitle(/new session/i));

    expect(sendMock).toHaveBeenCalledWith({
      type: 'session.create',
      projectPath: '/workspace/project-a',
    });
  });

  it('SessionClicked_LoadedAndMarkedSelected', () => {
    useStore.setState({
      sessions: [
        { ...makeSession('s-1', '/workspace/project-a', 'main'), messageCount: 3 },
        { ...makeSession('s-2', '/workspace/project-b', 'feature/x'), messageCount: 1 },
      ],
      activeSession: makeSession('s-1', '/workspace/project-a', 'main'),
      activeAgentSessionId: null,
      agentSessions: [
        {
          id: 's-1',
          projectPath: '/workspace/project-a',
          branchName: 'main',
          createdAt: '',
          updatedAt: '',
          summary: '',
          messageCount: 3,
          isActive: true,
          isStreaming: false,
          currentActivity: '',
          recentToolCalls: [],
        },
        {
          id: 's-2',
          projectPath: '/workspace/project-b',
          branchName: 'feature/x',
          createdAt: '',
          updatedAt: '',
          summary: 'Working on branch tests',
          messageCount: 1,
          isActive: false,
          isStreaming: false,
          currentActivity: '',
          recentToolCalls: [],
        },
      ],
    });

    const { container } = render(<AgentPanel />);
    fireEvent.click(screen.getByText('project-b'));

    expect(useStore.getState().activeAgentSessionId).toBe('s-2');
    expect(sendMock).toHaveBeenCalledWith({ type: 'session.load', sessionId: 's-2' });
    expect(container.textContent).toContain('feature/x');
  });

  it('ActiveAgents_StreamingActivityAndToolCallsShown', () => {
    useStore.setState({
      sessions: [{ ...makeSession('s-1', '/workspace/project-a', 'main'), messageCount: 4 }],
      activeSession: makeSession('s-1', '/workspace/project-a', 'main'),
      activeAgentSessionId: 's-1',
      agentSessions: [
        {
          id: 's-1',
          projectPath: '/workspace/project-a',
          branchName: 'main',
          createdAt: '',
          updatedAt: '',
          summary: 'Investigating failure',
          messageCount: 4,
          isActive: true,
          isStreaming: true,
          currentActivity: 'reading files',
          recentToolCalls: [
            { id: 'tc-1', name: 'read_file', input: { path: '/a.ts' }, pending: true },
            { id: 'tc-2', name: 'runTests', input: { file: 'a.test.ts' }, pending: false, durationMs: 20 },
          ],
        },
      ],
    });

    const { container } = render(<AgentPanel />);

    expect(container.textContent).toContain('reading files');
    expect(container.textContent).toContain('read_file');
    expect(container.textContent).toContain('runTests');
    expect(container.textContent).toContain('active');
  });

  it('NonSelectedSession_HoverStylesFired', () => {
    useStore.setState({
      sessions: [
        { ...makeSession('s-1', '/workspace/project-a', 'main'), messageCount: 1 },
        { ...makeSession('s-2', '/workspace/project-b', 'dev'), messageCount: 1 },
      ],
      activeSession: makeSession('s-1', '/workspace/project-a', 'main'),
      activeAgentSessionId: 's-1',
      agentSessions: [],
    });

    render(<AgentPanel />);

    const projectBCard = screen.getByText('project-b').closest('[style*="cursor"]') as HTMLElement
      ?? screen.getByText('project-b').parentElement as HTMLElement;
    if (projectBCard) {
      fireEvent.mouseEnter(projectBCard);
      fireEvent.mouseLeave(projectBCard);
    }

    expect(screen.getByText('project-b')).toBeTruthy();
  });

  it('NewSessionButton_HoverStylesFired', () => {
    useStore.setState({
      activeSession: makeSession('s-1', '/workspace/project-a', 'main'),
    });

    render(<AgentPanel />);

    const newSessionBtn = screen.getByTitle(/new session/i);
    fireEvent.mouseEnter(newSessionBtn);
    fireEvent.mouseLeave(newSessionBtn);

    expect(newSessionBtn).toBeTruthy();
  });

  it('SummaryProvided_SessionSummaryTextRendered', () => {
    useStore.setState({
      sessions: [
        {
          ...makeSession('s-sum', '/workspace/proj', 'main'),
          summary: 'Fixed the login bug',
          messageCount: 2,
          updatedAt: new Date().toISOString(),
        },
      ],
      activeSession: makeSession('s-sum', '/workspace/proj', 'main'),
      activeAgentSessionId: null,
      agentSessions: [],
    });

    const { container } = render(<AgentPanel />);
    expect(container.textContent).toContain('Fixed the login bug');
  });
});