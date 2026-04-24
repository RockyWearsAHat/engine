/**
 * store-agent.test.ts
 *
 * Behaviors: the AI controller manages autonomous agent sessions that work on
 * the user's project in the background. These tests verify the agent session
 * state machine — creating, streaming, tool-call tracking, and resolution.
 */
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../store/index.js';

vi.mock('../ws/client.js', () => ({
  wsClient: {
    send: vi.fn(),
    connect: vi.fn(),
    disconnect: vi.fn(),
    onMessage: vi.fn(),
    onOpen: vi.fn(),
    onClose: vi.fn(),
  },
}));

function reset() {
  useStore.setState({
    connected: false,
    sessions: [],
    activeSession: null,
    chatMessages: [],
    streamingMessageId: null,
    fileTree: null,
    openFiles: [],
    activeFilePath: null,
    gitStatus: null,
    githubToken: null,
    githubUser: null,
    githubIssues: [],
    githubIssuesLoading: false,
    githubIssuesError: null,
    searchQuery: '',
    searchResults: [],
    searchLoading: false,
    searchError: null,
    agentSessions: [],
    activeAgentSessionId: null,
    showDotfiles: false,
  });
}

beforeEach(reset);

// ─── Agent sessions ───────────────────────────────────────────────────────────

describe('agent session creation', () => {
  it('NewSessionOnFirstUpdate_AgentSessionCreated', () => {
    useStore.setState({
      sessions: [{ id: 'sess-1', name: 'Build feature', projectPath: '/project' }],
    });

    useStore.getState().updateAgentSession('sess-1', { isActive: true, currentActivity: 'reading files' });

    const agents = useStore.getState().agentSessions;
    expect(agents).toHaveLength(1);
    expect(agents[0]).toMatchObject({
      id: 'sess-1',
      isActive: true,
      currentActivity: 'reading files',
      isStreaming: false,
      recentToolCalls: [],
    });
  });

  it('UnknownSessionId_NoSessionCreated', () => {
    useStore.getState().updateAgentSession('ghost', { isActive: true });
    expect(useStore.getState().agentSessions).toHaveLength(0);
  });

  it('UpdatedTwice_NoDuplicateSession', () => {
    useStore.setState({
      sessions: [{ id: 's1', name: 'Fix bug', projectPath: '/project' }],
    });
    useStore.getState().updateAgentSession('s1', { isActive: true });
    useStore.getState().updateAgentSession('s1', { currentActivity: 'writing tests' });

    const agents = useStore.getState().agentSessions;
    expect(agents).toHaveLength(1);
    expect(agents[0].currentActivity).toBe('writing tests');
  });

  it('ProjectSession_FieldsCopiedToAgentSession', () => {
    useStore.setState({
      sessions: [{ id: 's2', name: 'My Project', projectPath: '/home/user/proj' }],
    });
    useStore.getState().updateAgentSession('s2', {});

    const agent = useStore.getState().agentSessions[0]!;
    expect(agent.name).toBe('My Project');
    expect(agent.projectPath).toBe('/home/user/proj');
  });
});

describe('agent streaming tool calls', () => {
  beforeEach(() => {
    useStore.setState({
      sessions: [{ id: 'a1', name: 'Agent', projectPath: '/' }],
    });
    useStore.getState().updateAgentSession('a1', {});
  });

  it('AddLiveToolCall_IsStreamingTrueAndToolCallRecorded', () => {
    useStore.getState().addLiveToolCall('a1', {
      id: 'tc1',
      name: 'read_file',
      input: { path: '/src/index.ts' },
      pending: true,
    });

    const agent = useStore.getState().agentSessions.find(a => a.id === 'a1')!;
    expect(agent.isStreaming).toBe(true);
    expect(agent.recentToolCalls).toHaveLength(1);
    expect(agent.recentToolCalls[0]).toMatchObject({ id: 'tc1', name: 'read_file', pending: true });
    expect(agent.currentActivity).toBe('read_file...');
  });

  it('TwentyFiveToolCalls_RecentToolCallsCappedAt20', () => {
    for (let i = 0; i < 25; i++) {
      useStore.getState().addLiveToolCall('a1', {
        id: `tc${i}`,
        name: 'search_files',
        input: {},
        pending: true,
      });
    }
    const agent = useStore.getState().agentSessions.find(a => a.id === 'a1')!;
    expect(agent.recentToolCalls.length).toBeLessThanOrEqual(20);
  });

  it('PendingToolCall_ResolvedWithResult', () => {
    useStore.getState().addLiveToolCall('a1', {
      id: 'tc1',
      name: 'write_file',
      input: { path: '/out.ts', content: '' },
      pending: true,
    });
    useStore.getState().resolveLiveToolCall('a1', 'tc1', 'written', false, 88);

    const tc = useStore.getState().agentSessions.find(a => a.id === 'a1')!.recentToolCalls[0]!;
    expect(tc.pending).toBe(false);
    expect(tc.result).toBe('written');
    expect(tc.isError).toBe(false);
    expect(tc.durationMs).toBe(88);
  });

  it('FailedToolCall_IsErrorTrue', () => {
    useStore.getState().addLiveToolCall('a1', {
      id: 'tc2',
      name: 'run_terminal',
      input: { cmd: 'rm -rf /' },
      pending: true,
    });
    useStore.getState().resolveLiveToolCall('a1', 'tc2', 'permission denied', true, 5);

    const tc = useStore.getState().agentSessions.find(a => a.id === 'a1')!.recentToolCalls[0]!;
    expect(tc.isError).toBe(true);
  });

  it('ResolveOneAgent_OtherAgentToolCallsUnchanged', () => {
    useStore.setState({
      sessions: [
        { id: 'a1', name: 'Agent 1', projectPath: '/' },
        { id: 'a2', name: 'Agent 2', projectPath: '/' },
      ],
    });
    useStore.getState().updateAgentSession('a1', {});
    useStore.getState().updateAgentSession('a2', {});

    useStore.getState().addLiveToolCall('a1', { id: 'tc1', name: 'list_dir', input: {}, pending: true });
    useStore.getState().addLiveToolCall('a2', { id: 'tc2', name: 'list_dir', input: {}, pending: true });

    useStore.getState().resolveLiveToolCall('a1', 'tc1', 'ok', false, 10);

    const a2 = useStore.getState().agentSessions.find(a => a.id === 'a2')!;
    expect(a2.recentToolCalls[0]?.pending).toBe(true);
  });

  it('store_resolveLiveToolCall_nonMatchingToolCallRemainingPending', () => {
    useStore.setState({
      sessions: [{ id: 'a1', name: 'Agent 1', projectPath: '/' }],
    });
    useStore.getState().updateAgentSession('a1', {});
    useStore.getState().addLiveToolCall('a1', { id: 'tc1', name: 'read_file', input: {}, pending: true });
    useStore.getState().addLiveToolCall('a1', { id: 'tc2', name: 'write_file', input: {}, pending: true });

    useStore.getState().resolveLiveToolCall('a1', 'tc1', 'content', false, 20);

    const agent = useStore.getState().agentSessions.find(a => a.id === 'a1')!;
    expect(agent.recentToolCalls.find(tc => tc.id === 'tc1')?.pending).toBe(false);
    expect(agent.recentToolCalls.find(tc => tc.id === 'tc2')?.pending).toBe(true);
  });
});

describe('store_updateAgentSession_multipleSessionsOnlyTargetUpdated', () => {
  beforeEach(reset);

  it('store_updateAgentSession_otherAgentSessionsUnchanged', () => {
    useStore.setState({
      sessions: [
        { id: 'a1', name: 'Agent 1', projectPath: '/' },
        { id: 'a2', name: 'Agent 2', projectPath: '/' },
      ],
    });
    useStore.getState().updateAgentSession('a1', {});
    useStore.getState().updateAgentSession('a2', {});

    useStore.getState().updateAgentSession('a1', { currentActivity: 'writing code' });

    const a1 = useStore.getState().agentSessions.find(a => a.id === 'a1')!;
    const a2 = useStore.getState().agentSessions.find(a => a.id === 'a2')!;
    expect(a1.currentActivity).toBe('writing code');
    expect(a2.currentActivity).toBe('');
  });
});

describe('active agent session tracking', () => {
  it('SetActiveAgent_ActiveSessionIdUpdated', () => {
    useStore.getState().setActiveAgentSession('sess-3');
    expect(useStore.getState().activeAgentSessionId).toBe('sess-3');
  });

  it('SetActiveAgentNull_ActiveSessionIdNull', () => {
    useStore.getState().setActiveAgentSession('sess-3');
    useStore.getState().setActiveAgentSession(null);
    expect(useStore.getState().activeAgentSessionId).toBeNull();
  });
});

// ─── Session list ─────────────────────────────────────────────────────────────

describe('setSessions', () => {
  it('SessionList_SessionsSetDirectly', () => {
    useStore.getState().setSessions([
      { id: 's1', name: 'Project Alpha', projectPath: '/alpha' },
    ]);
    expect(useStore.getState().sessions).toHaveLength(1);
  });

  it('FunctionUpdater_SessionsAppended', () => {
    useStore.getState().setSessions([{ id: 's1', name: 'Old', projectPath: '/' }]);
    useStore.getState().setSessions(prev => [...prev, { id: 's2', name: 'New', projectPath: '/' }]);
    expect(useStore.getState().sessions).toHaveLength(2);
  });

  it('UpdatedSessionList_AgentSessionNameMerged', () => {
    useStore.setState({
      sessions: [{ id: 's1', name: 'Old Name', projectPath: '/' }],
      agentSessions: [{ id: 's1', name: 'Old Name', projectPath: '/', isActive: true, isStreaming: false, currentActivity: '', recentToolCalls: [] }],
    });

    useStore.getState().setSessions([{ id: 's1', name: 'Updated Name', projectPath: '/' }]);

    // agentSessions should reflect the updated session name
    const agent = useStore.getState().agentSessions.find(a => a.id === 's1');
    expect(agent?.name).toBe('Updated Name');
  });

  it('SetActiveSession_ActiveSessionUpdated', () => {
    useStore.getState().setActiveSession({ id: 's1', name: 'Active', projectPath: '/' });
    expect(useStore.getState().activeSession?.id).toBe('s1');
  });
});

// ─── Dotfiles toggle ──────────────────────────────────────────────────────────

describe('toggleDotfiles', () => {
  it('FalseDefault_ToggleToTrue', () => {
    expect(useStore.getState().showDotfiles).toBe(false);
    useStore.getState().toggleDotfiles();
    expect(useStore.getState().showDotfiles).toBe(true);
  });

  it('ToggledTwice_ShowDotfilesFalse', () => {
    useStore.getState().toggleDotfiles();
    useStore.getState().toggleDotfiles();
    expect(useStore.getState().showDotfiles).toBe(false);
  });
});

// ─── Editor preferences ───────────────────────────────────────────────────────

describe('setEditorPreferences', () => {
  it('ValidPreferences_StoredInState', () => {
    useStore.getState().setEditorPreferences({
      fontFamily: 'JetBrains Mono',
      fontSize: 14,
      lineHeight: 1.6,
      tabSize: 2,
      markdownViewMode: 'split',
      wordWrap: true,
    });

    const prefs = useStore.getState().editorPreferences;
    expect(prefs.fontFamily).toBe('JetBrains Mono');
    expect(prefs.fontSize).toBe(14);
    expect(prefs.markdownViewMode).toBe('split');
  });
});

// ─── Chat: setMessages bulk load ──────────────────────────────────────────────

describe('setMessages', () => {
  it('BulkLoad_MessagesReplacedAndStreamingCleared', () => {
    useStore.getState().startAssistantMessage('streaming-1');
    useStore.getState().setMessages([
      { id: 'm1', role: 'user', content: 'build the feature', toolCalls: [] },
      { id: 'm2', role: 'assistant', content: 'working on it', toolCalls: [] },
    ]);

    const state = useStore.getState();
    expect(state.chatMessages).toHaveLength(2);
    expect(state.streamingMessageId).toBeNull();
    expect(state.chatMessages[0]).toMatchObject({ id: 'm1', role: 'user', streaming: false });
    expect(state.chatMessages[1]).toMatchObject({ id: 'm2', role: 'assistant', streaming: false });
  });

  it('HistoricalToolCalls_PendingFalse', () => {
    useStore.getState().setMessages([
      {
        id: 'm1',
        role: 'assistant',
        content: 'creating file',
        toolCalls: [{ id: 'tc1', name: 'write_file', input: {}, result: 'done', isError: false, pending: true }],
      },
    ]);

    const tc = useStore.getState().chatMessages[0]!.toolCalls[0]!;
    expect(tc.pending).toBe(false);
    expect(tc.result).toBe('done');
  });
});
