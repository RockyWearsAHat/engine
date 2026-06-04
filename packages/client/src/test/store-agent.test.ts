/**
 * store-agent.test.ts
 *
 * Behaviors: the AI controller manages autonomous agent sessions that work on
 * the user's project in the background. These tests verify the agent session
 * state machine — creating, streaming, tool-call tracking, and resolution.
 */
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { AgentSession, LiveToolCall, Message, Session } from '@engine/shared';
import { useStore } from '../store/index.js';
import { resetStoreForProjectAndAgentTests } from './store-test-helpers.js';

vi.mock('../ws/client.js', async () => {
  const { mockWsClientModule } = await import('./ws-client-mock.js');
  return mockWsClientModule();
});

beforeEach(resetStoreForProjectAndAgentTests);

const FIXED_TIME = '2026-06-03T00:00:00.000Z';

const makeSession = (id: string, summary = 'Session', projectPath = '/'): Session => ({
  id,
  projectPath,
  branchName: 'main',
  createdAt: FIXED_TIME,
  updatedAt: FIXED_TIME,
  summary,
  messageCount: 0,
});

const makeAgentSession = (id: string, summary = 'Session', projectPath = '/'): AgentSession => ({
  ...makeSession(id, summary, projectPath),
  isActive: false,
  isStreaming: false,
  currentActivity: '',
  recentToolCalls: [],
});

const makeLiveToolCall = (id: string, name: string, input: unknown): LiveToolCall => ({
  id,
  name,
  input,
  pending: true,
  startedAt: Date.now(),
});

const makeMessage = (id: string, role: Message['role'], content: string): Message => ({
  id,
  sessionId: 's1',
  role,
  content,
  createdAt: FIXED_TIME,
  toolCalls: [],
});

// ─── Agent sessions ───────────────────────────────────────────────────────────

describe('agent session creation', () => {
  it('NewSessionOnFirstUpdate_AgentSessionCreated', () => {
    useStore.setState({
      sessions: [makeSession('sess-1', 'Build feature', '/project')],
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
      sessions: [makeSession('s1', 'Fix bug', '/project')],
    });
    useStore.getState().updateAgentSession('s1', { isActive: true });
    useStore.getState().updateAgentSession('s1', { currentActivity: 'writing tests' });

    const agents = useStore.getState().agentSessions;
    expect(agents).toHaveLength(1);
    expect(agents[0].currentActivity).toBe('writing tests');
  });

  it('ProjectSession_FieldsCopiedToAgentSession', () => {
    useStore.setState({
      sessions: [makeSession('s2', 'My Project', '/home/user/proj')],
    });
    useStore.getState().updateAgentSession('s2', {});

    const agent = useStore.getState().agentSessions[0]!;
    expect(agent.summary).toBe('My Project');
    expect(agent.projectPath).toBe('/home/user/proj');
  });
});

describe('agent streaming tool calls', () => {
  beforeEach(() => {
    useStore.setState({
      sessions: [makeSession('a1', 'Agent', '/')],
    });
    useStore.getState().updateAgentSession('a1', {});
  });

  it('AddLiveToolCall_IsStreamingTrueAndToolCallRecorded', () => {
    useStore.getState().addLiveToolCall('a1', makeLiveToolCall('tc1', 'read_file', { path: '/src/index.ts' }));

    const agent = useStore.getState().agentSessions.find(a => a.id === 'a1')!;
    expect(agent.isStreaming).toBe(true);
    expect(agent.recentToolCalls).toHaveLength(1);
    expect(agent.recentToolCalls[0]).toMatchObject({ id: 'tc1', name: 'read_file', pending: true });
    expect(agent.currentActivity).toBe('read_file...');
  });

  it('TwentyFiveToolCalls_RecentToolCallsCappedAt20', () => {
    for (let i = 0; i < 25; i++) {
      useStore.getState().addLiveToolCall('a1', makeLiveToolCall(`tc${i}`, 'search_files', {}));
    }
    const agent = useStore.getState().agentSessions.find(a => a.id === 'a1')!;
    expect(agent.recentToolCalls.length).toBeLessThanOrEqual(20);
  });

  it('PendingToolCall_ResolvedWithResult', () => {
    useStore.getState().addLiveToolCall('a1', makeLiveToolCall('tc1', 'write_file', { path: '/out.ts', content: '' }));
    useStore.getState().resolveLiveToolCall('a1', 'tc1', 'written', false, 88);

    const tc = useStore.getState().agentSessions.find(a => a.id === 'a1')!.recentToolCalls[0]!;
    expect(tc.pending).toBe(false);
    expect(tc.result).toBe('written');
    expect(tc.isError).toBe(false);
    expect(tc.durationMs).toBe(88);
  });

  it('FailedToolCall_IsErrorTrue', () => {
    useStore.getState().addLiveToolCall('a1', makeLiveToolCall('tc2', 'run_terminal', { cmd: 'rm -rf /' }));
    useStore.getState().resolveLiveToolCall('a1', 'tc2', 'permission denied', true, 5);

    const tc = useStore.getState().agentSessions.find(a => a.id === 'a1')!.recentToolCalls[0]!;
    expect(tc.isError).toBe(true);
  });

  it('ResolveOneAgent_OtherAgentToolCallsUnchanged', () => {
    useStore.setState({
      sessions: [
        makeSession('a1', 'Agent 1', '/'),
        makeSession('a2', 'Agent 2', '/'),
      ],
    });
    useStore.getState().updateAgentSession('a1', {});
    useStore.getState().updateAgentSession('a2', {});

    useStore.getState().addLiveToolCall('a1', makeLiveToolCall('tc1', 'list_dir', {}));
    useStore.getState().addLiveToolCall('a2', makeLiveToolCall('tc2', 'list_dir', {}));

    useStore.getState().resolveLiveToolCall('a1', 'tc1', 'ok', false, 10);

    const a2 = useStore.getState().agentSessions.find(a => a.id === 'a2')!;
    expect(a2.recentToolCalls[0]?.pending).toBe(true);
  });

  it('store_resolveLiveToolCall_nonMatchingToolCallRemainingPending', () => {
    useStore.setState({
      sessions: [makeSession('a1', 'Agent 1', '/')],
    });
    useStore.getState().updateAgentSession('a1', {});
    useStore.getState().addLiveToolCall('a1', makeLiveToolCall('tc1', 'read_file', {}));
    useStore.getState().addLiveToolCall('a1', makeLiveToolCall('tc2', 'write_file', {}));

    useStore.getState().resolveLiveToolCall('a1', 'tc1', 'content', false, 20);

    const agent = useStore.getState().agentSessions.find(a => a.id === 'a1')!;
    expect(agent.recentToolCalls.find(tc => tc.id === 'tc1')?.pending).toBe(false);
    expect(agent.recentToolCalls.find(tc => tc.id === 'tc2')?.pending).toBe(true);
  });
});

describe('store_updateAgentSession_multipleSessionsOnlyTargetUpdated', () => {
  beforeEach(resetStoreForProjectAndAgentTests);

  it('store_updateAgentSession_otherAgentSessionsUnchanged', () => {
    useStore.setState({
      sessions: [
        makeSession('a1', 'Agent 1', '/'),
        makeSession('a2', 'Agent 2', '/'),
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
    useStore.getState().setSessions([makeSession('s1', 'Project Alpha', '/alpha')]);
    expect(useStore.getState().sessions).toHaveLength(1);
  });

  it('FunctionUpdater_SessionsAppended', () => {
    useStore.getState().setSessions([makeSession('s1', 'Old', '/')]);
    useStore.getState().setSessions(prev => [...prev, makeSession('s2', 'New', '/')]);
    expect(useStore.getState().sessions).toHaveLength(2);
  });

  it('UpdatedSessionList_AgentSessionNameMerged', () => {
    useStore.setState({
      sessions: [makeSession('s1', 'Old Name', '/')],
      agentSessions: [{ ...makeAgentSession('s1', 'Old Name', '/'), isActive: true }],
    });

    useStore.getState().setSessions([makeSession('s1', 'Updated Name', '/')]);

    // agentSessions should reflect the updated session summary
    const agent = useStore.getState().agentSessions.find(a => a.id === 's1');
    expect(agent?.summary).toBe('Updated Name');
  });

  it('SetActiveSession_ActiveSessionUpdated', () => {
    useStore.getState().setActiveSession(makeSession('s1', 'Active', '/'));
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
      makeMessage('m1', 'user', 'build the feature'),
      makeMessage('m2', 'assistant', 'working on it'),
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
        ...makeMessage('m1', 'assistant', 'creating file'),
        id: 'm1',
        toolCalls: [{ id: 'tc1', name: 'write_file', input: {}, result: 'done', isError: false }],
      },
    ]);

    const tc = useStore.getState().chatMessages[0]!.toolCalls[0]!;
    expect(tc.pending).toBe(false);
    expect(tc.result).toBe('done');
  });
});
