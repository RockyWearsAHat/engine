/**
 * store-project.test.ts
 *
 * Behaviors: the AI controller tracks the state of the user's project —
 * file tree, git status, GitHub issues to work on, and search results.
 * These tests verify that the store correctly manages project context.
 */
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { FileNode } from '@engine/shared';
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

// ─── File tree ────────────────────────────────────────────────────────────────

const makeDir = (path: string, children: FileNode[] = []): FileNode => ({
  name: path.split('/').pop()!,
  path,
  type: 'directory',
  children,
});

const makeFile = (path: string): FileNode => ({
  name: path.split('/').pop()!,
  path,
  type: 'file',
});

describe('setFileTree', () => {
  it('EntireFileTree_Replaced', () => {
    const tree = makeDir('/project', [makeFile('/project/main.ts')]);
    useStore.getState().setFileTree(tree);
    expect(useStore.getState().fileTree?.path).toBe('/project');
    expect(useStore.getState().fileTree?.children).toHaveLength(1);
  });

  it('NullTree_SetToNull', () => {
    useStore.getState().setFileTree(makeDir('/project'));
    useStore.getState().setFileTree(null);
    expect(useStore.getState().fileTree).toBeNull();
  });
});

describe('mergeFileTree', () => {
  it('NoExistingTree_TreeSet', () => {
    const tree = makeDir('/project', [makeFile('/project/index.ts')]);
    useStore.getState().mergeFileTree(tree);
    expect(useStore.getState().fileTree?.path).toBe('/project');
  });

  it('RootPathChanges_WholeTreeReplaced', () => {
    useStore.getState().mergeFileTree(makeDir('/old-project'));
    useStore.getState().mergeFileTree(makeDir('/new-project'));
    expect(useStore.getState().fileTree?.path).toBe('/new-project');
  });

  it('NewChildNode_AttachedIntoExistingTree', () => {
    useStore.getState().mergeFileTree(makeDir('/project', [makeDir('/project/src')]));
    const newFile = makeFile('/project/src/utils.ts');
    useStore.getState().mergeFileTree(newFile);
    const src = useStore.getState().fileTree?.children?.find(c => c.name === 'src');
    expect(src?.children?.some(c => c.name === 'utils.ts')).toBe(true);
  });

  it('SameNodeMergedTwice_NoDuplicate', () => {
    useStore.getState().mergeFileTree(makeDir('/project', [makeDir('/project/src')]));
    const file = makeFile('/project/src/index.ts');
    useStore.getState().mergeFileTree(file);
    useStore.getState().mergeFileTree(file);
    const src = useStore.getState().fileTree?.children?.find(c => c.name === 'src');
    expect(src?.children?.filter(c => c.name === 'index.ts')).toHaveLength(1);
  });

  it('SameNodeReMerged_TreeStructurePreserved', () => {
    const root = makeDir('/project', [makeDir('/project/src')]);
    useStore.getState().mergeFileTree(root);
    useStore.getState().mergeFileTree(root);
    expect(useStore.getState().fileTree).toEqual(root);
  });

  it('SameTypeSiblings_SortedAlphabetically', () => {
    useStore.getState().mergeFileTree(makeDir('/project', [makeDir('/project/src')]));
    useStore.getState().mergeFileTree(makeFile('/project/src/zebra.ts'));
    useStore.getState().mergeFileTree(makeFile('/project/src/alpha.ts'));
    const src = useStore.getState().fileTree?.children?.find(c => c.name === 'src');
    const names = src?.children?.map(c => c.name);
    expect(names).toEqual(['alpha.ts', 'zebra.ts']);
  });

  it('DeeplyNestedNewNode_FileNodeSkipped', () => {
    const root = makeDir('/project', [
      makeFile('/project/readme.md'),
      makeDir('/project/src'),
    ]);
    useStore.getState().mergeFileTree(root);
    useStore.getState().mergeFileTree(makeFile('/project/src/App.tsx'));
    const src = useStore.getState().fileTree?.children?.find(c => c.name === 'src');
    expect(src?.children?.some(c => c.name === 'App.tsx')).toBe(true);
  });

  it('DirectoryNoChildren_NodeUnchanged', () => {
    const root = makeDir('/project', [makeDir('/project/empty')]);
    useStore.getState().mergeFileTree(root);
    useStore.getState().mergeFileTree(makeFile('/project/empty/sub/deep.ts'));
    const empty = useStore.getState().fileTree?.children?.find(c => c.name === 'empty');
    expect(empty?.children).toHaveLength(0);
  });

  it('NoChildrenMatchPath_NodeUnchanged', () => {
    const root = makeDir('/project', [makeDir('/project/src', [makeFile('/project/src/a.ts')])]);
    useStore.getState().mergeFileTree(root);
    useStore.getState().mergeFileTree(makeFile('/project/other/b.ts'));
    const src = useStore.getState().fileTree?.children?.find(c => c.name === 'src');
    expect(src?.children?.some(c => c.name === 'b.ts')).toBe(false);
  });
});

// ─── syncFileContent ──────────────────────────────────────────────────────────

describe('syncFileContent', () => {
  it('OpenFile_InMemoryContentUpdated', () => {
    useStore.getState().openFile('/project/main.ts', 'const a = 1;', 'typescript', 12);
    useStore.getState().syncFileContent('/project/main.ts', 'const a = 2;');
    const file = useStore.getState().openFiles.find(f => f.path === '/project/main.ts');
    expect(file?.content).toBe('const a = 2;');
  });

  it('OtherOpenFiles_Unaffected', () => {
    useStore.getState().openFile('/a.ts', 'a', 'typescript', 1);
    useStore.getState().openFile('/b.ts', 'b', 'typescript', 1);
    useStore.getState().syncFileContent('/a.ts', 'updated');
    expect(useStore.getState().openFiles.find(f => f.path === '/b.ts')?.content).toBe('b');
  });
});

// ─── Git status ───────────────────────────────────────────────────────────────

describe('setGitStatus', () => {
  it('GitStatus_Stored', () => {
    useStore.getState().setGitStatus({ branch: 'main', dirty: false, ahead: 0, behind: 0 });
    expect(useStore.getState().gitStatus?.branch).toBe('main');
  });

  it('DirtyWorkingTree_Reflected', () => {
    useStore.getState().setGitStatus({ branch: 'feature/ai-work', dirty: true, ahead: 3, behind: 0 });
    expect(useStore.getState().gitStatus?.dirty).toBe(true);
    expect(useStore.getState().gitStatus?.ahead).toBe(3);
  });

  it('GitStatus_Cleared', () => {
    useStore.getState().setGitStatus({ branch: 'main', dirty: false, ahead: 0, behind: 0 });
    useStore.getState().setGitStatus(null);
    expect(useStore.getState().gitStatus).toBeNull();
  });
});

// ─── GitHub integration ───────────────────────────────────────────────────────

describe('GitHub token and user', () => {
  it('GithubToken_StoredForApiAccess', () => {
    useStore.getState().setGithubToken('ghp_abc123');
    expect(useStore.getState().githubToken).toBe('ghp_abc123');
  });

  it('AuthenticatedUser_Stored', () => {
    useStore.getState().setGithubUser({ login: 'octocat', avatarUrl: 'https://github.com/octocat.png' });
    expect(useStore.getState().githubUser?.login).toBe('octocat');
  });

  it('SignOut_TokenAndUserCleared', () => {
    useStore.getState().setGithubToken('tok');
    useStore.getState().setGithubUser({ login: 'me', avatarUrl: '' });
    useStore.getState().setGithubToken(null);
    useStore.getState().setGithubUser(null);
    expect(useStore.getState().githubToken).toBeNull();
    expect(useStore.getState().githubUser).toBeNull();
  });
});

describe('GitHub issues — the AI work queue', () => {
  const mockIssues = [
    { id: 1, number: 42, title: 'Fix the bug', body: '', state: 'open', labels: [], url: '' },
    { id: 2, number: 43, title: 'Add feature', body: '', state: 'open', labels: [], url: '' },
  ];

  it('IssuesFetched_Stored', () => {
    useStore.getState().setGithubIssues(mockIssues);
    expect(useStore.getState().githubIssues).toHaveLength(2);
    expect(useStore.getState().githubIssuesLoading).toBe(false);
  });

  it('FetchingIssues_LoadingFlagSet', () => {
    useStore.getState().setGithubIssuesLoading(true);
    expect(useStore.getState().githubIssuesLoading).toBe(true);
    useStore.getState().setGithubIssuesLoading(false);
    expect(useStore.getState().githubIssuesLoading).toBe(false);
  });

  it('LoadingTrueParam_LoadingFlagSet', () => {
    useStore.getState().setGithubIssues(mockIssues, true);
    expect(useStore.getState().githubIssuesLoading).toBe(true);
  });

  it('FetchFails_ErrorStored', () => {
    useStore.getState().setGithubIssuesError('rate limit exceeded');
    expect(useStore.getState().githubIssuesError).toBe('rate limit exceeded');
  });

  it('ClearIssueError_ErrorCleared', () => {
    useStore.getState().setGithubIssuesError('oops');
    useStore.getState().setGithubIssuesError(null);
    expect(useStore.getState().githubIssuesError).toBeNull();
  });
});

// ─── Project search ───────────────────────────────────────────────────────────

describe('search', () => {
  it('UserTyping_SearchQuerySet', () => {
    useStore.getState().setSearchQuery('async function');
    expect(useStore.getState().searchQuery).toBe('async function');
  });

  it('WaitingForResults_MarkedLoading', () => {
    useStore.getState().setSearchLoading(true);
    expect(useStore.getState().searchLoading).toBe(true);
  });

  it('ResultsWithQuery_Stored', () => {
    useStore.getState().setSearchResults('handleOpen', [
      { file: '/src/ws/client.ts', line: 42, snippet: 'handleOpen(ws)' },
    ]);
    expect(useStore.getState().searchResults).toHaveLength(1);
    expect(useStore.getState().searchQuery).toBe('handleOpen');
    expect(useStore.getState().searchLoading).toBe(false);
  });

  it('SearchFails_ErrorStored', () => {
    useStore.getState().setSearchResults('broken query', [], 'server error');
    expect(useStore.getState().searchError).toBe('server error');
  });

  it('ClearSearch_AllSearchStateCleared', () => {
    useStore.getState().setSearchQuery('something');
    useStore.getState().setSearchLoading(true);
    useStore.getState().setSearchResults('something', [{ file: '/x.ts', line: 1, snippet: 'x' }]);
    useStore.getState().clearSearch();

    const state = useStore.getState();
    expect(state.searchQuery).toBe('');
    expect(state.searchResults).toHaveLength(0);
    expect(state.searchLoading).toBe(false);
    expect(state.searchError).toBeNull();
  });
});
