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
  it('True_ConnectedIsTrue', () => {
    useStore.getState().setConnected(true);
    expect(useStore.getState().connected).toBe(true);
  });

  it('FalseAfterTrue_ConnectedIsFalse', () => {
    useStore.getState().setConnected(true);
    useStore.getState().setConnected(false);
    expect(useStore.getState().connected).toBe(false);
  });
});

// ─── syncTabs / wsClient.send integration ────────────────────────────────────

describe('syncTabs — openFile triggers editor.tabs.sync', () => {
  it('NewFile_TabSyncSentWithNewTab', () => {
    useStore.getState().openFile('/src/main.ts', 'console.log(1)', 'typescript', 100);
    expect(wsClient.send).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'editor.tabs.sync',
        tabs: [expect.objectContaining({ path: '/src/main.ts', isActive: true, isDirty: false })],
      }),
    );
  });

  it('TwoFilesOpened_SecondFileIsActive', () => {
    useStore.getState().openFile('/a.ts', 'a', 'typescript', 10);
    useStore.getState().openFile('/b.ts', 'b', 'typescript', 10);
    const lastCall = vi.mocked(wsClient.send).mock.calls.at(-1)![0] as { type: string; tabs: { path: string; isActive: boolean }[] };
    expect(lastCall.type).toBe('editor.tabs.sync');
    const activeTab = lastCall.tabs.find(t => t.isActive);
    expect(activeTab?.path).toBe('/b.ts');
    const inactiveTab = lastCall.tabs.find(t => t.path === '/a.ts');
    expect(inactiveTab?.isActive).toBe(false);
  });

  it('SamePathOpenedTwice_TabCountIsOne', () => {
    useStore.getState().openFile('/a.ts', 'v1', 'typescript', 10);
    useStore.getState().openFile('/a.ts', 'v2', 'typescript', 10);
    const lastCall = vi.mocked(wsClient.send).mock.calls.at(-1)![0] as { tabs: unknown[] };
    expect(lastCall.tabs).toHaveLength(1);
  });
});

describe('syncTabs — setActiveFile triggers editor.tabs.sync', () => {
  it('SwitchActiveFile_TabSyncUpdatesActiveFlag', () => {
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
  it('ClearOpenFiles_TabSyncSentWithEmptyTabs', () => {
    useStore.getState().openFile('/a.ts', '', 'typescript', 10);
    vi.mocked(wsClient.send).mockClear();

    useStore.getState().clearOpenFiles();
    expect(wsClient.send).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'editor.tabs.sync', tabs: [] }),
    );
  });
});

describe('syncTabs — markFileDirty triggers editor.tabs.sync only on state change', () => {
  it('CleanFileMarkedDirty_TabSyncSentWithDirtyFlag', () => {
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

  it('AlreadyDirtyFile_NoTabSyncSent', () => {
    useStore.getState().openFile('/a.ts', '', 'typescript', 10);
    useStore.getState().markFileDirty('/a.ts');
    vi.mocked(wsClient.send).mockClear();

    useStore.getState().markFileDirty('/a.ts');
    expect(wsClient.send).not.toHaveBeenCalled();
  });
});

describe('syncTabs — markFileSaved triggers editor.tabs.sync only on state change', () => {
  it('DirtyFileSaved_TabSyncSentWithCleanFlag', () => {
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

  it('AlreadyCleanFile_NoTabSyncSent', () => {
    useStore.getState().openFile('/a.ts', '', 'typescript', 10);
    vi.mocked(wsClient.send).mockClear();

    useStore.getState().markFileSaved('/a.ts');
    expect(wsClient.send).not.toHaveBeenCalled();
  });
});

describe('syncTabs — closeFile triggers editor.tabs.sync asynchronously', () => {
  it('CloseFile_TabSyncSentAfterTimer', async () => {
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
  it('UserMessage_AppendedWithStreamingFalse', () => {
    useStore.getState().addUserMessage('u1', 'Hello AI');
    const msgs = useStore.getState().chatMessages;
    expect(msgs).toHaveLength(1);
    expect(msgs[0]).toMatchObject({ id: 'u1', role: 'user', content: 'Hello AI', streaming: false, toolCalls: [] });
  });

  it('AssistantMessage_AppendedWithStreamingTrueAndId', () => {
    useStore.getState().startAssistantMessage('a1');
    const state = useStore.getState();
    expect(state.chatMessages.at(-1)).toMatchObject({ id: 'a1', role: 'assistant', streaming: true, content: '' });
    expect(state.streamingMessageId).toBe('a1');
  });

  it('MultipleChunks_ContentAccumulated', () => {
    useStore.getState().startAssistantMessage('a1');
    useStore.getState().appendChunk('a1', 'Hello');
    useStore.getState().appendChunk('a1', ' World');
    expect(useStore.getState().chatMessages.at(-1)?.content).toBe('Hello World');
  });

  it('ChunkForOneMessage_OtherMessagesUnchanged', () => {
    useStore.getState().addUserMessage('u1', 'prompt');
    useStore.getState().startAssistantMessage('a1');
    useStore.getState().appendChunk('a1', 'response');
    const userMsg = useStore.getState().chatMessages.find(m => m.id === 'u1');
    expect(userMsg?.content).toBe('prompt');
  });

  it('StreamingMessage_StreamingFalseAndIdNull', () => {
    useStore.getState().startAssistantMessage('a1');
    useStore.getState().finalizeMessage('a1');
    const state = useStore.getState();
    expect(state.chatMessages.at(-1)?.streaming).toBe(false);
    expect(state.streamingMessageId).toBeNull();
  });

  it('StreamingMessage_FailedTrueStreamingFalseIdNull', () => {
    useStore.getState().startAssistantMessage('a1');
    useStore.getState().markMessageFailed('a1');
    const state = useStore.getState();
    const msg = state.chatMessages.at(-1)!;
    expect(msg.failed).toBe(true);
    expect(msg.streaming).toBe(false);
    expect(state.streamingMessageId).toBeNull();
  });

  it('FullCycle_UserAndFinalizedAssistantInMessages', () => {
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
  it('ToolCall_AppendedToCorrectMessage', () => {
    useStore.getState().startAssistantMessage('a1');
    useStore.getState().addToolCall('a1', { id: 'tc1', name: 'read_file', input: {}, pending: true });
    const msg = useStore.getState().chatMessages.at(-1)!;
    expect(msg.toolCalls).toHaveLength(1);
    expect(msg.toolCalls[0]).toMatchObject({ id: 'tc1', name: 'read_file', pending: true });
  });

  it('PendingToolCall_ResolvedWithResult', () => {
    useStore.getState().startAssistantMessage('a1');
    useStore.getState().addToolCall('a1', { id: 'tc1', name: 'read_file', input: {}, pending: true });
    useStore.getState().resolveToolCall('a1', 'tc1', 'file contents', false, 120);
    const tc = useStore.getState().chatMessages.at(-1)!.toolCalls[0];
    expect(tc.pending).toBe(false);
    expect(tc.result).toBe('file contents');
    expect(tc.isError).toBe(false);
    expect(tc.durationMs).toBe(120);
  });

  it('ToolCallResolvedWithError_IsErrorTrue', () => {
    useStore.getState().startAssistantMessage('a1');
    useStore.getState().addToolCall('a1', { id: 'tc1', name: 'run_cmd', input: {}, pending: true });
    useStore.getState().resolveToolCall('a1', 'tc1', 'permission denied', true, 50);
    expect(useStore.getState().chatMessages.at(-1)!.toolCalls[0].isError).toBe(true);
  });

  it('ResolveOneToolCall_OtherMessageToolCallsUnchanged', () => {
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
  it('SetMessages_ChatMessagesReplacedAndIdNull', () => {
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

  it('HistoricalMessage_ToolCallsPendingFalse', () => {
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
  it('OpenFile_ActiveFilePathUpdated', () => {
    useStore.getState().openFile('/a.ts', 'a', 'typescript', 10);
    expect(useStore.getState().activeFilePath).toBe('/a.ts');
  });

  it('FileAtOrAbove2MB_LargeFileFlagTrue', () => {
    useStore.getState().openFile('/big.bin', '', 'binary', 2 * 1024 * 1024);
    expect(useStore.getState().openFiles[0].largeFile).toBe(true);
  });

  it('FileBelow2MB_LargeFileFlagFalse', () => {
    useStore.getState().openFile('/small.ts', '', 'typescript', 100);
    expect(useStore.getState().openFiles[0].largeFile).toBe(false);
  });

  it('CloseFile_RemovedFromOpenFiles', () => {
    vi.useFakeTimers();
    useStore.getState().openFile('/a.ts', '', 'typescript', 10);
    useStore.getState().closeFile('/a.ts');
    expect(useStore.getState().openFiles).toHaveLength(0);
    vi.useRealTimers();
  });

  it('CloseActiveFile_PreviousFileBecomesActive', () => {
    vi.useFakeTimers();
    useStore.getState().openFile('/a.ts', '', 'typescript', 10);
    useStore.getState().openFile('/b.ts', '', 'typescript', 10);
    useStore.getState().closeFile('/b.ts');
    expect(useStore.getState().activeFilePath).toBe('/a.ts');
    vi.useRealTimers();
  });

  it('CloseOnlyFile_ActiveFilePathNull', () => {
    vi.useFakeTimers();
    useStore.getState().openFile('/a.ts', '', 'typescript', 10);
    useStore.getState().closeFile('/a.ts');
    expect(useStore.getState().activeFilePath).toBeNull();
    vi.useRealTimers();
  });

  it('ClearOpenFiles_OpenFilesEmptyAndPathNull', () => {
    useStore.getState().openFile('/a.ts', '', 'typescript', 10);
    useStore.getState().openFile('/b.ts', '', 'typescript', 10);
    useStore.getState().clearOpenFiles();
    expect(useStore.getState().openFiles).toHaveLength(0);
    expect(useStore.getState().activeFilePath).toBeNull();
  });
});

// ─── mergeFileTree ────────────────────────────────────────────────────────────

describe('mergeFileTree', () => {
  it('NoPriorTree_FileTreeSet', () => {
    const tree = { path: '/root', name: 'root', type: 'directory' as const, loaded: true, children: [] };
    useStore.getState().mergeFileTree(tree);
    expect(useStore.getState().fileTree).toEqual(tree);
  });

  it('SubtreeForExistingChild_MergedIntoParent', () => {
    const initial = {
      path: '/root', name: 'root', type: 'directory' as const, loaded: true,
      children: [{ path: '/root/src', name: 'src', type: 'directory' as const, loaded: false, children: [] }],
    };
    useStore.getState().setFileTree(initial);

    const subTree = {
      path: '/root/src', name: 'src', type: 'directory' as const, loaded: true,
      children: [{ path: '/root/src/index.ts', name: 'index.ts', type: 'file' as const, loaded: true }],
    };
    useStore.getState().mergeFileTree(subTree);

    const src = useStore.getState().fileTree!.children![0];
    expect(src.loaded).toBe(true);
    expect(src.children?.some(c => c.path === '/root/src/index.ts')).toBe(true);
  });

  it('OutsideCurrentTree_FileTreeReplaced', () => {
    const initial = {
      path: '/root', name: 'root', type: 'directory' as const, loaded: true, children: [],
    };
    useStore.getState().setFileTree(initial);

    const other = {
      path: '/other', name: 'other', type: 'directory' as const, loaded: true, children: [],
    };
    useStore.getState().mergeFileTree(other);
    expect(useStore.getState().fileTree?.path).toBe('/other');
  });

  it('NewNodeForDirectoryWithFileSiblings_NodeAttached', () => {
    const initial = {
      path: '/root', name: 'root', type: 'directory' as const, loaded: true,
      children: [
        { path: '/root/README.md', name: 'README.md', type: 'file' as const, loaded: true },
        {
          path: '/root/src', name: 'src', type: 'directory' as const, loaded: true,
          children: [
            { path: '/root/src/lib', name: 'lib', type: 'directory' as const, loaded: true, children: [] },
          ],
        },
      ],
    };
    useStore.getState().setFileTree(initial);

    const newFile = {
      path: '/root/src/main.ts', name: 'main.ts', type: 'file' as const, loaded: true,
    };
    useStore.getState().mergeFileTree(newFile);

    const src = useStore.getState().fileTree!.children!.find(c => c.path === '/root/src');
    expect(src?.children?.some(c => c.path === '/root/src/main.ts')).toBe(true);
  });

  it('ShallowRefresh_DeepChildrenPreserved', () => {
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
  it('NewList_SessionsReplaced', () => {
    useStore.getState().setSessions([{ id: 's1', projectPath: '/p', branch: 'main', summary: '', createdAt: '', updatedAt: '' }]);
    expect(useStore.getState().sessions).toHaveLength(1);
  });

  it('NewSession_AgentSessionCreated', () => {
    useStore.getState().setSessions([
      { id: 's1', projectPath: '/p', branch: 'main', summary: '', createdAt: '', updatedAt: '' },
    ]);
    const agentSessions = useStore.getState().agentSessions;
    expect(agentSessions).toHaveLength(1);
    expect(agentSessions[0].id).toBe('s1');
    expect(agentSessions[0].isStreaming).toBe(false);
    expect(agentSessions[0].recentToolCalls).toHaveLength(0);
  });

  it('ExistingSession_LiveToolCallsPreservedOnRefresh', () => {
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
  it('FalseDefault_ShowDotfilesTrue', () => {
    useStore.getState().toggleDotfiles();
    expect(useStore.getState().showDotfiles).toBe(true);
  });

  it('ToggledTwice_ShowDotfilesFalse', () => {
    useStore.getState().toggleDotfiles();
    useStore.getState().toggleDotfiles();
    expect(useStore.getState().showDotfiles).toBe(false);
  });
});

// ─── store branch coverage ────────────────────────────────────────────────────

describe('markMessageFailed — non-matching message untouched', () => {
    it('NonMatchingId_OtherMessagesUnchanged', () => {
      useStore.getState().startAssistantMessage('msg-a');
      useStore.getState().startAssistantMessage('msg-b');
      const before = useStore.getState().chatMessages.find(m => m.id === 'msg-a');
      useStore.getState().markMessageFailed('msg-b');
      const after = useStore.getState().chatMessages.find(m => m.id === 'msg-a');
      // msg-a should be unchanged (the non-matching map branch)
      expect(after?.failed).toBeFalsy();
      expect(after?.content).toBe(before?.content);
    });
  });

  describe('openFile — non-matching files unchanged in map', () => {
    it('ReopenOneOfMultiple_OtherFileUnchanged', () => {
      useStore.getState().openFile('/a.ts', 'original-a', 'typescript', 10);
      useStore.getState().openFile('/b.ts', 'original-b', 'typescript', 10);
      useStore.getState().openFile('/a.ts', 'updated-a', 'typescript', 20);
      // /b.ts goes through the `f.path === path ? ... : f` FALSE branch
      expect(useStore.getState().openFiles.find(f => f.path === '/b.ts')?.content).toBe('original-b');
    });
  });

  describe('closeFile — non-active file leaves active path unchanged', () => {
    it('CloseNonActiveFile_ActivePathUnchanged', () => {
      vi.useFakeTimers();
      useStore.getState().openFile('/a.ts', '', 'typescript', 10);
      useStore.getState().openFile('/b.ts', '', 'typescript', 10);
      // /b.ts is active now; close /a.ts (non-active)
      useStore.getState().closeFile('/a.ts');
      expect(useStore.getState().activeFilePath).toBe('/b.ts');
      vi.useRealTimers();
    });
  });

  describe('parentPathForTreeNode — no separator returns empty string', () => {
    it('NoSlash_ReturnsEmpty', () => {
      // Exercise parentPathForTreeNode indirectly via mergeFileTree
      // A node whose path has no separator: parentPathForTreeNode returns ''
      // Use attachFileTreeNode path: set up a tree where a shallow node is attached
      useStore.getState().setFileTree({
        path: '',
        name: '',
        type: 'directory',
        loaded: true,
        children: [],
      });
      useStore.getState().mergeFileTree({
        path: 'child',
        name: 'child',
        type: 'file',
        loaded: true,
      });
      // Should not throw — the empty-string separator path is exercised
      expect(useStore.getState().fileTree).toBeTruthy();
    });
  });

  describe('compareTreeNodes — file sorted after directory', () => {
    it('FileAndDirectory_DirectoryFirst', () => {
      const dir = { path: '/root/src', name: 'src', type: 'directory' as const, loaded: true, children: [] };
      const fileNode = { path: '/root/file.ts', name: 'file.ts', type: 'file' as const, loaded: true };
      useStore.getState().setFileTree({
        path: '/root',
        name: 'root',
        type: 'directory',
        loaded: true,
        children: [fileNode, dir],
      });
      // attachFileTreeNode will call compareTreeNodes when sorting children
      useStore.getState().mergeFileTree({
        path: '/root/new.ts',
        name: 'new.ts',
        type: 'file',
        loaded: true,
      });
      const children = useStore.getState().fileTree?.children ?? [];
      // Directories should come before files
      expect(children[0]?.type).toBe('directory');
    });
  });

  describe('attachFileTreeNode — parent with undefined children', () => {
    it('UndefinedChildren_NodeAttachedViaEmptyFallback', () => {
      useStore.getState().setFileTree({
        path: '/root',
        name: 'root',
        type: 'directory',
        loaded: true,
        // children intentionally omitted (undefined)
      });
      useStore.getState().mergeFileTree({
        path: '/root/a.ts',
        name: 'a.ts',
        type: 'file',
        loaded: true,
      });
      expect(useStore.getState().fileTree?.children?.some(c => c.name === 'a.ts')).toBe(true);
    });
  });
