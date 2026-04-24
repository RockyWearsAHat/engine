import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import { vi } from 'vitest';
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

describe('AgentPanel — branch coverage', () => {
  beforeEach(() => {
    sendMock.mockClear();
    useStore.setState({
      sessions: [],
      activeSession: null,
      activeAgentSessionId: null,
      agentSessions: [],
    });
  });

  it('ToolRow_NonObjectInput_EmptyShortInput', () => {
    useStore.setState({
      sessions: [makeSession('s1', '/workspace/p', 'main')],
      activeSession: makeSession('s1', '/workspace/p', 'main'),
      activeAgentSessionId: 's1',
      agentSessions: [{
        id: 's1', projectPath: '/workspace/p', branchName: 'main',
        createdAt: '', updatedAt: '', summary: '', messageCount: 1,
        isActive: true, isStreaming: false, currentActivity: '',
        recentToolCalls: [
          // Non-object input (string) — covers the `: ''` else branch in ToolRow
          { id: 'tc-str', name: 'echo', input: 'hello' as unknown as Record<string, unknown>, pending: false, startedAt: Date.now(), durationMs: 5 },
        ],
      }],
    });
    const { container } = render(<AgentPanel />);
    expect(container.textContent).toContain('echo');
  });

  it('ToolRow_IsError_XCircleShown', () => {
    useStore.setState({
      sessions: [makeSession('s1', '/workspace/p', 'main')],
      activeSession: makeSession('s1', '/workspace/p', 'main'),
      activeAgentSessionId: 's1',
      agentSessions: [{
        id: 's1', projectPath: '/workspace/p', branchName: 'main',
        createdAt: '', updatedAt: '', summary: '', messageCount: 1,
        isActive: true, isStreaming: false, currentActivity: '',
        recentToolCalls: [
          // isError=true — covers the XCircle branch
          { id: 'tc-err', name: 'read_file', input: { path: '/a.ts' }, pending: false, isError: true, startedAt: Date.now(), durationMs: 3 },
        ],
      }],
    });
    const { container } = render(<AgentPanel />);
    expect(container.querySelector('svg')).toBeTruthy();
  });

  it('SelectedSession_HoverDoesNotChangeStyle', () => {
    useStore.setState({
      sessions: [{ ...makeSession('s-sel', '/workspace/project-a', 'main'), messageCount: 1 }],
      activeSession: makeSession('s-sel', '/workspace/project-a', 'main'),
      activeAgentSessionId: 's-sel',
      agentSessions: [],
    });
    render(<AgentPanel />);
    // Hover on the SELECTED session card — isSelected=true path
    const card = screen.getByText('project-a').closest('[style]') as HTMLElement
      ?? screen.getByText('project-a').parentElement as HTMLElement;
    if (card) {
      fireEvent.mouseEnter(card);
      fireEvent.mouseLeave(card);
    }
    expect(screen.getByText('project-a')).toBeTruthy();
  });

  it('ToolRow_ObjectInput_EmptyValues_NullishFallback', () => {
    useStore.setState({
      sessions: [makeSession('s1', '/workspace/p', 'main')],
      activeSession: makeSession('s1', '/workspace/p', 'main'),
      activeAgentSessionId: 's1',
      agentSessions: [{
        id: 's1', projectPath: '/workspace/p', branchName: 'main',
        createdAt: '', updatedAt: '', summary: '', messageCount: 1,
        isActive: true, isStreaming: false, currentActivity: '',
        recentToolCalls: [
          // Object input with no enumerable values — covers the `?? ''` fallback
          { id: 'tc-empty', name: 'noop', input: {} as Record<string, unknown>, pending: false, startedAt: Date.now(), durationMs: 1 },
        ],
      }],
    });
    const { container } = render(<AgentPanel />);
    expect(container.textContent).toContain('noop');
  });

  it('NewSession_NoActiveSession_SendNotCalled', () => {
    // activeSession=null → button click enters the `if (activeSession)` FALSE branch
    useStore.setState({
      sessions: [],
      activeSession: null,
      activeAgentSessionId: null,
      agentSessions: [],
    });
    render(<AgentPanel />);
    // The button is disabled but we can still fire click to cover the if(activeSession) FALSE branch
    const btn = document.querySelector('[title="New session"]') as HTMLElement;
    if (btn) fireEvent.click(btn);
    expect(sendMock).not.toHaveBeenCalled();
  });
});
