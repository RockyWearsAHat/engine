import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import App from '../App.js';
import { useStore } from '../store/index.js';
import { resetStoreForTests } from './store-test-helpers.js';
import { OPEN_ISSUES_TAB_EVENT } from '../editorEvents.js';

const wsMocks = vi.hoisted(() => ({
  connect: vi.fn(),
  disconnect: vi.fn(),
  send: vi.fn(),
  onMessage: vi.fn(),
  onOpen: vi.fn((handler: () => void | Promise<void>) => {
    void handler();
    return () => {};
  }),
  onClose: vi.fn(() => () => {}),
}));

let capturedMessageHandler: ((message: unknown) => void) | null = null;
let capturedOpenHandler: (() => void) | null = null;

vi.mock('../ws/client.js', () => ({
  wsClient: {
    connect: wsMocks.connect,
    disconnect: wsMocks.disconnect,
    send: wsMocks.send,
    onMessage: vi.fn((handler: (message: unknown) => void) => {
      capturedMessageHandler = handler;
      return () => {
        capturedMessageHandler = null;
      };
    }),
    onOpen: vi.fn((handler: () => void) => {
      capturedOpenHandler = handler;
      return () => {
        capturedOpenHandler = null;
      };
    }),
    onClose: wsMocks.onClose,
  },
}));

vi.mock('../bridge.js', () => ({
  bridge: {
    getGithubToken: vi.fn().mockResolvedValue(null),
    getGithubRepoOwner: vi.fn().mockResolvedValue(null),
    getGithubRepoName: vi.fn().mockResolvedValue(null),
    getAnthropicKey: vi.fn().mockResolvedValue(null),
    getOpenAiKey: vi.fn().mockResolvedValue(null),
    getModelProvider: vi.fn().mockResolvedValue('llamacpp'),
    getOllamaBaseUrl: vi.fn().mockResolvedValue(null),
    getModel: vi.fn().mockResolvedValue('gemma4:31b'),
    getEditorPreferences: vi.fn().mockResolvedValue(null),
    getClonesDir: vi.fn().mockResolvedValue(null),
    getActiveTeam: vi.fn().mockResolvedValue('engine'),
    getProjectPath: vi.fn().mockResolvedValue(''),
    getLocalServerToken: vi.fn().mockResolvedValue('desktop-token'),
    setLastProjectPath: vi.fn().mockResolvedValue(undefined),
    startWindowDrag: vi.fn().mockResolvedValue(undefined),
    closeWindow: vi.fn().mockResolvedValue(undefined),
    minimizeWindow: vi.fn().mockResolvedValue(undefined),
    toggleMaximizeWindow: vi.fn().mockResolvedValue(undefined),
    toggleFullscreenWindow: vi.fn().mockResolvedValue(undefined),
    openFolderDialog: vi.fn().mockResolvedValue(''),
    openFileDialog: vi.fn().mockResolvedValue(''),
    inspectPath: vi.fn(),
    openExternal: vi.fn().mockResolvedValue(undefined),
    installAgentService: vi.fn().mockResolvedValue(''),
    uninstallAgentService: vi.fn().mockResolvedValue(''),
  },
}));

vi.mock('../components/FileTree/FileTree.js', () => ({
  default: ({ activityTab }: { activityTab?: string }) => <div data-testid="file-tree" data-activity-tab={activityTab} />,
}));

vi.mock('../components/Editor/Editor.js', () => ({
  default: () => <div data-testid="editor" />,
}));

vi.mock('../components/Terminal/Terminal.js', () => ({
  default: () => <div data-testid="terminal" />,
}));

vi.mock('../components/AI/AIChat.js', () => ({
  default: () => <div data-testid="ai-chat" />,
}));

vi.mock('../components/AgentPanel/AgentPanel.js', () => ({
  default: () => <div data-testid="agent-panel" />,
}));

vi.mock('../components/StatusBar/StatusBar.js', () => ({
  default: () => <div data-testid="status-bar" />,
}));

vi.mock('../components/Preferences/PreferencesPanel.js', () => ({
  default: () => <div data-testid="preferences-panel" />,
}));

vi.mock('../components/Connections/MachineConnectionsPanel.js', () => ({
  default: () => <div data-testid="machine-connections-panel" />,
}));

vi.mock('../components/CommandPalette/CommandPalette.js', () => ({
  default: ({ open, mode }: { open: boolean; mode: 'commands' | 'files' }) => (
    <div data-testid="command-palette" data-open={String(open)} data-mode={mode} />
  ),
}));

describe('App websocket lifecycle', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    capturedMessageHandler = null;
    capturedOpenHandler = null;
    wsMocks.connect.mockClear();
    wsMocks.disconnect.mockClear();
    wsMocks.send.mockClear();
    wsMocks.onMessage.mockClear();
    wsMocks.onOpen.mockClear();
    wsMocks.onClose.mockClear();

    resetStoreForTests();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('OpenFileUiStateChanges_WebSocketStaysConnected', async () => {
    render(<App />);
    await vi.advanceTimersByTimeAsync(1100);
    act(() => {
      capturedOpenHandler?.();
    });

    expect(wsMocks.connect).toHaveBeenCalledTimes(1);

    act(() => {
      useStore.setState({
        openFiles: [{
          path: '/tmp/example.ts',
          content: 'export const cave = true;\n',
          language: 'typescript',
          size: 26,
          largeFile: false,
          dirty: false,
        }],
        activeFilePath: '/tmp/example.ts',
      });
    });

    expect(wsMocks.connect).toHaveBeenCalledTimes(1);
    expect(wsMocks.disconnect).not.toHaveBeenCalled();
  });

  it('StartupSync_SendsSavedActiveTeamWhenWebSocketOpens', async () => {
    render(<App />);
    await vi.advanceTimersByTimeAsync(1100);

    await act(async () => {
      capturedOpenHandler?.();
      await Promise.resolve();
    });

    expect(wsMocks.send).toHaveBeenCalledWith(expect.objectContaining({
      type: 'config.sync',
      config: expect.objectContaining({
        activeTeam: 'engine',
      }),
    }));
  });

  it('KeyboardShortcut_PreferencesOpened', async () => {
    render(<App />);

    await act(async () => {
      fireEvent.keyDown(window, { key: ',', ctrlKey: true });
    });

    expect(screen.getByTestId('preferences-panel')).toBeTruthy();
  });

  it('OpenIssuesTabEvent_ShowsIssuesSidebar', async () => {
    render(<App />);

    await act(async () => {
      window.dispatchEvent(new Event(OPEN_ISSUES_TAB_EVENT));
    });

    expect(screen.getByTestId('file-tree').getAttribute('data-activity-tab')).toBe('issues');
  });

  it('KeyboardShortcuts_CommandPaletteOpenedInFileOrCommandMode', async () => {
    render(<App />);

    await act(async () => {
      fireEvent.keyDown(window, { key: 'p', ctrlKey: true });
    });
    expect(screen.getByTestId('command-palette').getAttribute('data-open')).toBe('true');
    expect(screen.getByTestId('command-palette').getAttribute('data-mode')).toBe('files');

    await act(async () => {
      fireEvent.keyDown(window, { key: 'p', ctrlKey: true, shiftKey: true });
    });
    expect(screen.getByTestId('command-palette').getAttribute('data-mode')).toBe('commands');
  });

  it('ApprovalRequestsFromWs_RespondsCorrectly', async () => {
    render(<App />);
    expect(capturedMessageHandler).not.toBeNull();

    await act(async () => {
      capturedMessageHandler?.({
        type: 'approval.request',
        request: {
          id: 'approval-1',
          title: 'Run shell command',
          message: 'Allow this action?',
          kind: 'shell_command',
          sessionId: 'session-approval-1',
          command: 'echo hi',
        },
      });
    });

    fireEvent.click(screen.getByRole('button', { name: /allow/i }));
    expect(wsMocks.send).toHaveBeenCalledWith({ type: 'approval.respond', id: 'approval-1', allow: true });
  });

  it('FileSavedMessage_RequestsQualityRefreshForActiveSession', async () => {
    useStore.setState({
      activeSession: {
        id: 'session-1',
        projectPath: '/tmp/project',
        branchName: 'main',
        createdAt: '',
        updatedAt: '',
        summary: '',
        messageCount: 0,
      },
    });

    render(<App />);
    expect(capturedMessageHandler).not.toBeNull();

    wsMocks.send.mockClear();

    await act(async () => {
      capturedMessageHandler?.({ type: 'file.saved', path: '/tmp/project/src/example.ts' });
    });

    expect(wsMocks.send).toHaveBeenCalledWith({
      type: 'quality.report.get',
      projectPath: '/tmp/project',
      maxIssues: 0,
    });
  });

  it('SessionLoaded_RequestsQualityOnceForProjectPath', async () => {
    render(<App />);
    expect(capturedMessageHandler).not.toBeNull();

    wsMocks.send.mockClear();

    await act(async () => {
      capturedMessageHandler?.({
        type: 'session.loaded',
        session: {
          id: 'session-1',
          projectPath: '/tmp/project',
          branchName: 'main',
          createdAt: '',
          updatedAt: '',
          summary: '',
          messageCount: 0,
        },
        messages: [],
      });
    });

    const qualityCalls = wsMocks.send.mock.calls.filter(([msg]) => (msg as { type?: string }).type === 'quality.report.get');
    expect(qualityCalls).toHaveLength(1);
    expect(qualityCalls[0][0]).toEqual({
      type: 'quality.report.get',
      projectPath: '/tmp/project',
      maxIssues: 0,
    });
  });

  it('ChatNotice_UpdatesAgentActivityAndShowsShellNotice', async () => {
    useStore.setState({
      activeSession: {
        id: 'session-1',
        projectPath: '/tmp/project',
        branchName: 'main',
        createdAt: '',
        updatedAt: '',
        summary: '',
        messageCount: 0,
      },
      sessions: [{
        id: 'session-1',
        projectPath: '/tmp/project',
        branchName: 'main',
        createdAt: '',
        updatedAt: '',
        summary: '',
        messageCount: 0,
      }],
    });

    render(<App />);
    expect(capturedMessageHandler).not.toBeNull();

    await act(async () => {
      capturedMessageHandler?.({
        type: 'chat.notice',
        sessionId: 'session-1',
        notice: 'Orchestrator plan: generating TDD-style plan',
      });
    });

    expect(screen.getByText(/orchestrator plan: generating tdd-style plan/i)).toBeTruthy();
    expect(useStore.getState().agentSessions.find(session => session.id === 'session-1')?.currentActivity)
      .toBe('Orchestrator plan: generating TDD-style plan');
  });
});
