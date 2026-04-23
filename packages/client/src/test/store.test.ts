/**
 * store.test.ts — verifies that every store action produces the correct state
 * transition AND that syncTabs fires wsClient.send with the right payload.
 *
 * wsClient is mocked so we can spy on send() without a real WebSocket.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../store/index.js';

// ─── Mock wsClient ────────────────────────────────────────────────────────────
// Must be hoisted so the mock is in place before the store module is imported.
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

// Import AFTER the mock so we get the mocked version.
import { wsClient } from '../ws/client.js';

// ─── Store Reset ──────────────────────────────────────────────────────────────

function resetStore() {
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

beforeEach(() => {
  resetStore();
  vi.mocked(wsClient.send).mockClear();
});

afterEach(() => {
  vi.restoreAllMocks();
});

// ─── Connection state ─────────────────────────────────────────────────────────

describe('setConnected', () => {
  it('sets connected to true', () => {
    useStore.getState().setConnected(true);
    expect(useStore.getState().connected).toBe(true);
  });

  it('sets connected back to false', () => {
    useStore.getState().setConnected(true);
    useStore.getState().setConnected(false);
    expect(useStore.getState().connected).toBe(false);
  });
});

// ─── syncTabs / wsClient.send integration ────────────────────────────────────

describe('syncTabs — openFile triggers editor.tabs.sync', () => {
  it('sends editor.tabs.sync with the new tab on openFile', () => {
    useStore.getState().openFile('/src/main.ts', 'console.log(1)', 'typescript', 100);
    expect(wsClient.send).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'editor.tabs.sync',
        tabs: [expect.objectContaining({ path: '/src/main.ts', isActive: true, isDirty: false })],
      }),
    );
  });

  it('sends editor.tabs.sync with isActive:true for the most recently opened file', () => {
    useStore.getState().openFile('/a.ts', 'a', 'typescript', 10);
    useStore.getState().openFile('/b.ts', 'b', 'typescript', 10);
    const lastCall = vi.mocked(wsClient.send).mock.calls.at(-1)![0] as { type: string; tabs: { path: string; isActive: boolean }[] };
    expect(lastCall.type).toBe('editor.tabs.sync');
    const activeTab = lastCall.tabs.find(t => t.isActive);
    expect(activeTab?.path).toBe('/b.ts');
    const inactiveTab = lastCall.tabs.find(t => t.path === '/a.ts');
    expect(inactiveTab?.isActive).toBe(false);
  });

  it('does NOT duplicate the tab when openFile is called twice for the same path', () => {
    useStore.getState().openFile('/a.ts', 'v1', 'typescript', 10);
    useStore.getState().openFile('/a.ts', 'v2', 'typescript', 10);
    const lastCall = vi.mocked(wsClient.send).mock.calls.at(-1)![0] as { tabs: unknown[] };
    expect(lastCall.tabs).toHaveLength(1);
  });
});

describe('syncTabs — setActiveFile triggers editor.tabs.sync', () => {
  it('sends editor.tabs.sync with the switched active tab', () => {
    useStore.getState().openFile('/a.ts', 'a', 'typescript', 10);
    useStore.getState().openFile('/b.ts', 'b', 'typescript', 10);
    vi.mocked(wsClient.send).mockClear();

    useStore.getState().setActiveFile('/a.ts');
    expect(wsClient.send).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'editor.tabs.sync',
        tabs: expect.arrayContaining([
          expect.objectContaining({ path: '/a.ts', isActive: true }),
          expect.objectContaining({ path: '/b.ts', isActive: false }),
        ]),
      }),
    );
  });
});

describe('syncTabs — clearOpenFiles triggers editor.tabs.sync', () => {
  it('sends editor.tabs.sync with an empty tabs array', () => {
    useStore.getState().openFile('/a.ts', '', 'typescript', 10);
    vi.mocked(wsClient.send).mockClear();

    useStore.getState().clearOpenFiles();
    expect(wsClient.send).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'editor.tabs.sync', tabs: [] }),
    );
  });
});

describe('syncTabs — markFileDirty triggers editor.tabs.sync only on state change', () => {
  it('sends editor.tabs.sync when a clean file is marked dirty', () => {
    useStore.getState().openFile('/a.ts', '', 'typescript', 10);
    vi.mocked(wsClient.send).mockClear();

    useStore.getState().markFileDirty('/a.ts');
    expect(wsClient.send).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'editor.tabs.sync',
        tabs: [expect.objectContaining({ path: '/a.ts', isDirty: true })],
      }),
    );
  });

  it('does NOT send editor.tabs.sync when file is already dirty', () => {
    useStore.getState().openFile('/a.ts', '', 'typescript', 10);
    useStore.getState().markFileDirty('/a.ts');
    vi.mocked(wsClient.send).mockClear();

    useStore.getState().markFileDirty('/a.ts');
    expect(wsClient.send).not.toHaveBeenCalled();
  });
});

describe('syncTabs — markFileSaved triggers editor.tabs.sync only on state change', () => {
  it('sends editor.tabs.sync when a dirty file is saved', () => {
    useStore.getState().openFile('/a.ts', '', 'typescript', 10);
    useStore.getState().markFileDirty('/a.ts');
    vi.mocked(wsClient.send).mockClear();

    useStore.getState().markFileSaved('/a.ts');
    expect(wsClient.send).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'editor.tabs.sync',
        tabs: [expect.objectContaining({ path: '/a.ts', isDirty: false })],
      }),
    );
  });

  it('does NOT send editor.tabs.sync when file is already clean', () => {
    useStore.getState().openFile('/a.ts', '', 'typescript', 10);
    vi.mocked(wsClient.send).mockClear();

    useStore.getState().markFileSaved('/a.ts');
    expect(wsClient.send).not.toHaveBeenCalled();
  });
});

describe('syncTabs — closeFile triggers editor.tabs.sync asynchronously', () => {
  it('sends editor.tabs.sync after close via setTimeout', async () => {
    vi.useFakeTimers();
    useStore.getState().openFile('/a.ts', '', 'typescript', 10);
    vi.mocked(wsClient.send).mockClear();

    useStore.getState().closeFile('/a.ts');
    // Not yet — closeFile uses setTimeout
    expect(wsClient.send).not.toHaveBeenCalled();
    await vi.runAllTimersAsync();
    expect(wsClient.send).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'editor.tabs.sync', tabs: [] }),
    );
    vi.useRealTimers();
  });
});

// ─── Chat state machine ───────────────────────────────────────────────────────

describe('chat state machine', () => {
  it('addUserMessage appends a user message with streaming=false', () => {
    useStore.getState().addUserMessage('u1', 'Hello AI');
    const msgs = useStore.getState().chatMessages;
    expect(msgs).toHaveLength(1);
    expect(msgs[0]).toMatchObject({ id: 'u1', role: 'user', content: 'Hello AI', streaming: false, toolCalls: [] });
  });

  it('startAssistantMessage appends a streaming assistant message and sets streamingMessageId', () => {
    useStore.getState().startAssistantMessage('a1');
    const state = useStore.getState();
    expect(state.chatMessages.at(-1)).toMatchObject({ id: 'a1', role: 'assistant', streaming: true, content: '' });
    expect(state.streamingMessageId).toBe('a1');
  });

  it('appendChunk accumulates text into the correct message', () => {
    useStore.getState().startAssistantMessage('a1');
    useStore.getState().appendChunk('a1', 'Hello');
    useStore.getState().appendChunk('a1', ' World');
    expect(useStore.getState().chatMessages.at(-1)?.content).toBe('Hello World');
  });

  it('appendChunk does not affect other messages', () => {
    useStore.getState().addUserMessage('u1', 'prompt');
    useStore.getState().startAssistantMessage('a1');
    useStore.getState().appendChunk('a1', 'response');
    const userMsg = useStore.getState().chatMessages.find(m => m.id === 'u1');
    expect(userMsg?.content).toBe('prompt');
  });

  it('finalizeMessage clears streaming and clears streamingMessageId', () => {
    useStore.getState().startAssistantMessage('a1');
    useStore.getState().finalizeMessage('a1');
    const state = useStore.getState();
    expect(state.chatMessages.at(-1)?.streaming).toBe(false);
    expect(state.streamingMessageId).toBeNull();
  });

  it('markMessageFailed sets failed=true, streaming=false, clears streamingMessageId', () => {
    useStore.getState().startAssistantMessage('a1');
    useStore.getState().markMessageFailed('a1');
    const state = useStore.getState();
    const msg = state.chatMessages.at(-1)!;
    expect(msg.failed).toBe(true);
    expect(msg.streaming).toBe(false);
    expect(state.streamingMessageId).toBeNull();
  });

  it('complete cycle: user → assistant streaming → finalize', () => {
    const { addUserMessage, startAssistantMessage, appendChunk, finalizeMessage } = useStore.getState();
    addUserMessage('u1', 'ping');
    startAssistantMessage('a1');
    appendChunk('a1', 'pong');
    finalizeMessage('a1');

    const msgs = useStore.getState().chatMessages;
    expect(msgs).toHaveLength(2);
    expect(msgs[0]).toMatchObject({ id: 'u1', role: 'user', content: 'ping' });
    expect(msgs[1]).toMatchObject({ id: 'a1', role: 'assistant', content: 'pong', streaming: false });
    expect(useStore.getState().streamingMessageId).toBeNull();
  });
});

// ─── Tool calls ───────────────────────────────────────────────────────────────

describe('addToolCall / resolveToolCall', () => {
  it('addToolCall appends a tool call to the correct message', () => {
    useStore.getState().startAssistantMessage('a1');
    useStore.getState().addToolCall('a1', { id: 'tc1', name: 'read_file', input: {}, pending: true });
    const msg = useStore.getState().chatMessages.at(-1)!;
    expect(msg.toolCalls).toHaveLength(1);
    expect(msg.toolCalls[0]).toMatchObject({ id: 'tc1', name: 'read_file', pending: true });
  });

  it('resolveToolCall marks the correct tool call as resolved with result', () => {
    useStore.getState().startAssistantMessage('a1');
    useStore.getState().addToolCall('a1', { id: 'tc1', name: 'read_file', input: {}, pending: true });
    useStore.getState().resolveToolCall('a1', 'tc1', 'file contents', false, 120);
    const tc = useStore.getState().chatMessages.at(-1)!.toolCalls[0];
    expect(tc.pending).toBe(false);
    expect(tc.result).toBe('file contents');
    expect(tc.isError).toBe(false);
    expect(tc.durationMs).toBe(120);
  });

  it('resolveToolCall marks tool call as error when isError=true', () => {
    useStore.getState().startAssistantMessage('a1');
    useStore.getState().addToolCall('a1', { id: 'tc1', name: 'run_cmd', input: {}, pending: true });
    useStore.getState().resolveToolCall('a1', 'tc1', 'permission denied', true, 50);
    expect(useStore.getState().chatMessages.at(-1)!.toolCalls[0].isError).toBe(true);
  });

  it('resolveToolCall does not affect tool calls in other messages', () => {
    useStore.getState().startAssistantMessage('a1');
    useStore.getState().addToolCall('a1', { id: 'tc1', name: 'tool', input: {}, pending: true });
    useStore.getState().startAssistantMessage('a2');
    useStore.getState().addToolCall('a2', { id: 'tc2', name: 'tool', input: {}, pending: true });

    useStore.getState().resolveToolCall('a1', 'tc1', 'done', false);
    const a2tc = useStore.getState().chatMessages.find(m => m.id === 'a2')!.toolCalls[0];
    expect(a2tc.pending).toBe(true);
  });
});

// ─── setMessages ──────────────────────────────────────────────────────────────

describe('setMessages', () => {
  it('replaces chatMessages and clears streamingMessageId', () => {
    useStore.getState().startAssistantMessage('a1');
    useStore.getState().setMessages([
      { id: 'm1', role: 'user', content: 'hi', toolCalls: [] },
      { id: 'm2', role: 'assistant', content: 'there', toolCalls: [] },
    ]);
    const state = useStore.getState();
    expect(state.chatMessages).toHaveLength(2);
    expect(state.chatMessages[0].id).toBe('m1');
    expect(state.streamingMessageId).toBeNull();
  });

  it('converts message toolCalls to the display format (pending=false)', () => {
    useStore.getState().setMessages([
      {
        id: 'm1', role: 'assistant', content: 'ok',
        toolCalls: [{ id: 'tc1', name: 'list_files', input: {}, result: '[]', isError: false }],
      },
    ]);
    const tc = useStore.getState().chatMessages[0].toolCalls[0];
    expect(tc.pending).toBe(false);
    expect(tc.result).toBe('[]');
  });
});

// ─── openFile / closeFile ─────────────────────────────────────────────────────

describe('openFile / closeFile', () => {
  it('openFile sets activeFilePath to the opened file', () => {
    useStore.getState().openFile('/a.ts', 'a', 'typescript', 10);
    expect(useStore.getState().activeFilePath).toBe('/a.ts');
  });

  it('openFile marks largeFile=true for files >= 2 MB', () => {
    useStore.getState().openFile('/big.bin', '', 'binary', 2 * 1024 * 1024);
    expect(useStore.getState().openFiles[0].largeFile).toBe(true);
  });

  it('openFile marks largeFile=false for files < 2 MB', () => {
    useStore.getState().openFile('/small.ts', '', 'typescript', 100);
    expect(useStore.getState().openFiles[0].largeFile).toBe(false);
  });

  it('closeFile removes the file from openFiles', () => {
    vi.useFakeTimers();
    useStore.getState().openFile('/a.ts', '', 'typescript', 10);
    useStore.getState().closeFile('/a.ts');
    expect(useStore.getState().openFiles).toHaveLength(0);
    vi.useRealTimers();
  });

  it('closeFile selects the last remaining file as active', () => {
    vi.useFakeTimers();
    useStore.getState().openFile('/a.ts', '', 'typescript', 10);
    useStore.getState().openFile('/b.ts', '', 'typescript', 10);
    useStore.getState().closeFile('/b.ts');
    expect(useStore.getState().activeFilePath).toBe('/a.ts');
    vi.useRealTimers();
  });

  it('closeFile sets activeFilePath to null when no files remain', () => {
    vi.useFakeTimers();
    useStore.getState().openFile('/a.ts', '', 'typescript', 10);
    useStore.getState().closeFile('/a.ts');
    expect(useStore.getState().activeFilePath).toBeNull();
    vi.useRealTimers();
  });

  it('clearOpenFiles removes all open files and clears activeFilePath', () => {
    useStore.getState().openFile('/a.ts', '', 'typescript', 10);
    useStore.getState().openFile('/b.ts', '', 'typescript', 10);
    useStore.getState().clearOpenFiles();
    expect(useStore.getState().openFiles).toHaveLength(0);
    expect(useStore.getState().activeFilePath).toBeNull();
  });
});

// ─── mergeFileTree ────────────────────────────────────────────────────────────

describe('mergeFileTree', () => {
  it('sets fileTree when none existed', () => {
    const tree = { path: '/root', name: 'root', type: 'directory' as const, loaded: true, children: [] };
    useStore.getState().mergeFileTree(tree);
    expect(useStore.getState().fileTree).toEqual(tree);
  });

  it('preserves deep children that the shallow refresh lost', () => {
    const deep: Parameters<typeof useStore.getState>['fileTree'] extends infer T ? T : never =
      { path: '/root/src', name: 'src', type: 'directory' as const, loaded: true, children: [
        { path: '/root/src/main.ts', name: 'main.ts', type: 'file' as const, loaded: true },
      ] };

    const initial = {
      path: '/root', name: 'root', type: 'directory' as const, loaded: true,
      children: [{ path: '/root/src', name: 'src', type: 'directory' as const, loaded: true, children: [
        { path: '/root/src/main.ts', name: 'main.ts', type: 'file' as const, loaded: true },
      ] }],
    };
    useStore.getState().setFileTree(initial);

    // Shallow update — src has no children loaded
    const shallow = {
      path: '/root', name: 'root', type: 'directory' as const, loaded: true,
      children: [{ path: '/root/src', name: 'src', type: 'directory' as const, loaded: false, children: [] }],
    };
    useStore.getState().mergeFileTree(shallow);

    const srcNode = useStore.getState().fileTree!.children![0];
    // Preserved children from the previous deep load
    expect(srcNode.children?.some(c => c.path === '/root/src/main.ts')).toBe(true);
  });
});

// ─── setSessions / agentSessions merge ───────────────────────────────────────

describe('setSessions', () => {
  it('replaces sessions with the new list', () => {
    useStore.getState().setSessions([{ id: 's1', projectPath: '/p', branch: 'main', summary: '', createdAt: '', updatedAt: '' }]);
    expect(useStore.getState().sessions).toHaveLength(1);
  });

  it('merges agentSessions — creates new AgentSession for sessions not previously tracked', () => {
    useStore.getState().setSessions([
      { id: 's1', projectPath: '/p', branch: 'main', summary: '', createdAt: '', updatedAt: '' },
    ]);
    const agentSessions = useStore.getState().agentSessions;
    expect(agentSessions).toHaveLength(1);
    expect(agentSessions[0].id).toBe('s1');
    expect(agentSessions[0].isStreaming).toBe(false);
    expect(agentSessions[0].recentToolCalls).toHaveLength(0);
  });

  it('merges agentSessions — preserves live tool call state for existing sessions', () => {
    // First set triggers creation of agentSessions
    useStore.getState().setSessions([
      { id: 's1', projectPath: '/p', branch: 'main', summary: '', createdAt: '', updatedAt: '' },
    ]);
    // Simulate AI activity
    useStore.getState().addLiveToolCall('s1', { id: 'tc1', name: 'read_file', input: {}, pending: true });
    // Second set (e.g., server refresh) — should NOT wipe live tool calls
    useStore.getState().setSessions([
      { id: 's1', projectPath: '/p', branch: 'main', summary: 'updated', createdAt: '', updatedAt: '' },
    ]);
    const session = useStore.getState().agentSessions.find(a => a.id === 's1');
    expect(session?.recentToolCalls).toHaveLength(1);
    // summary from the fresh server data should be merged in
    expect(session?.summary).toBe('updated');
  });
});

// ─── toggleDotfiles ───────────────────────────────────────────────────────────

describe('toggleDotfiles', () => {
  it('flips showDotfiles from false to true', () => {
    useStore.getState().toggleDotfiles();
    expect(useStore.getState().showDotfiles).toBe(true);
  });

  it('flips showDotfiles back to false', () => {
    useStore.getState().toggleDotfiles();
    useStore.getState().toggleDotfiles();
    expect(useStore.getState().showDotfiles).toBe(false);
  });
});
