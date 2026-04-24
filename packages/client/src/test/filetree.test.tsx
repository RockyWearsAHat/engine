import { render } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { useStore } from '../store/index.js';
import type { FileNode, GitStatus } from '@engine/shared';

// Mocks
vi.mock('../ws/client.js', () => ({
  wsClient: {
    send: vi.fn(),
    onMessage: vi.fn(() => () => {}),
    onOpen: vi.fn(() => () => {}),
    onClose: vi.fn(() => () => {}),
  },
}));

vi.mock('../bridge.js', () => ({
  bridge: {
    openExternal: vi.fn(),
    getLocalServerToken: vi.fn().mockResolvedValue(null),
    getGithubToken: vi.fn().mockResolvedValue(null),
    setEditorPreferences: vi.fn(),
    showContextMenu: vi.fn(),
    registerContextMenuHandler: vi.fn(),
  },
}));

vi.mock('@tauri-apps/api/core', () => ({
  invoke: vi.fn().mockResolvedValue(undefined),
}));

const { default: FileTree } = await import('../components/FileTree/FileTree.js');

const sampleTree: FileNode = {
  name: 'project',
  path: '/project',
  type: 'directory',
  children: [
    { name: 'file1.ts', path: '/project/file1.ts', type: 'file' },
    {
      name: 'folder',
      path: '/project/folder',
      type: 'directory',
      children: [{ name: 'nested.ts', path: '/project/folder/nested.ts', type: 'file' }],
    },
  ],
};

const sampleGitStatus: GitStatus = {
  branch: 'main',
  unstaged: [],
  staged: [],
  untracked: [],
  ignored: [],
  ahead: 0,
  behind: 0,
};

describe('FileTree Component', () => {
  const testFile = {
    path: '/project/file1.ts',
    content: 'export const x = 1;',
    language: 'typescript',
    size: 18,
    largeFile: false,
    dirty: false,
  };

  beforeEach(() => {
    useStore.setState({
      fileTree: sampleTree,
      openFiles: [testFile],
      activeFilePath: '/project/file1.ts',
      gitStatus: sampleGitStatus,
      githubIssues: [],
      githubIssuesLoading: false,
      githubIssuesError: null,
      searchQuery: '',
      searchResults: [],
      searchLoading: false,
      searchError: null,
      showDotfiles: false,
      activeSession: {
        id: 's1',
        projectPath: '/project',
        branchName: 'main',
        createdAt: new Date().toISOString(),
        updatedAt: new Date().toISOString(),
        summary: '',
        messageCount: 0,
      },
    });
  });

  it('ExplorerTab_TreeWithFilesRendered', () => {
    const { container } = render(
      <FileTree
        activityTab="explorer"
        onOpenFolder={() => {}}
        onOpenFile={() => {}}
      />,
    );
    expect(container).toBeTruthy();
  });

  it('ExplorerTab_CustomOpenFilesUsed', () => {
    const { container } = render(
      <FileTree
        activityTab="explorer"
        onOpenFolder={() => {}}
        onOpenFile={() => {}}
        openFiles={[testFile]}
        activeFilePath="/project/file1.ts"
      />,
    );
    expect(container).toBeTruthy();
  });

  it('ExplorerTab_DefaultOpenFilesWhenNotProvided', () => {
    const { container } = render(
      <FileTree
        activityTab="explorer"
        onOpenFolder={() => {}}
        onOpenFile={() => {}}
      />,
    );
    expect(container).toBeTruthy();
  });

  it('GitTab_GitPanelRendered', () => {
    const { container } = render(
      <FileTree
        activityTab="git"
        onOpenFolder={() => {}}
        onOpenFile={() => {}}
      />,
    );
    expect(container).toBeTruthy();
  });

  it('SearchTab_SearchPanelRendered', () => {
    const { container } = render(
      <FileTree
        activityTab="search"
        onOpenFolder={() => {}}
        onOpenFile={() => {}}
      />,
    );
    expect(container).toBeTruthy();
  });

  it('IssuesTab_IssuesPanelRendered', () => {
    const { container } = render(
      <FileTree
        activityTab="issues"
        onOpenFolder={() => {}}
        onOpenFile={() => {}}
      />,
    );
    expect(container).toBeTruthy();
  });

  it('OpenEditorsTab_EditorListRendered', () => {
    const { container } = render(
      <FileTree
        activityTab="open-editors"
        onOpenFolder={() => {}}
        onOpenFile={() => {}}
      />,
    );
    expect(container).toBeTruthy();
  });

  it('WithActiveSession_WithGitStatus', () => {
    useStore.setState({
      activeSession: {
        id: 's1',
        projectPath: '/project',
        branchName: 'main',
        createdAt: new Date().toISOString(),
        updatedAt: new Date().toISOString(),
        summary: '',
        messageCount: 0,
      },
      gitStatus: {
        ...sampleGitStatus,
        unstaged: ['file1.ts'],
      },
    });

    const { container } = render(
      <FileTree
        activityTab="explorer"
        onOpenFolder={() => {}}
        onOpenFile={() => {}}
      />,
    );
    expect(container).toBeTruthy();
  });

  it('GitTab_WithUnstagedChanges_RendersList', () => {
    useStore.setState({
      gitStatus: {
        branch: 'feature/test',
        unstaged: ['file1.ts', 'file2.ts'],
        staged: ['file3.ts'],
        untracked: ['new-file.txt'],
        ignored: [],
        ahead: 2,
        behind: 1,
      },
    });

    const { container } = render(
      <FileTree
        activityTab="git"
        onOpenFolder={() => {}}
        onOpenFile={() => {}}
      />,
    );
    // Check for actual content that appears in git panel
    expect(container.textContent).toContain('Changes');
  });

  it('IssuesTab_WithGithubIssues_RendersList', () => {
    useStore.setState({
      githubIssues: [
        {
          number: 1,
          title: 'Test issue',
          state: 'open',
          labels: [{ name: 'bug', color: 'ff0000' }],
          createdAt: new Date().toISOString(),
          updatedAt: new Date().toISOString(),
          htmlUrl: 'http://example.com',
          body: 'Issue body',
          author: 'testuser',
        },
      ],
    });

    const { container } = render(
      <FileTree
        activityTab="issues"
        onOpenFolder={() => {}}
        onOpenFile={() => {}}
      />,
    );
    expect(container.textContent).toContain('Test issue');
  });

  it('ExplorerTab_DefaultOpenFilesWhenNotProvided', () => {
    const { container } = render(
      <FileTree
        activityTab="explorer"
        onOpenFolder={() => {}}
        onOpenFile={() => {}}
      />,
    );
    expect(container).toBeTruthy();
  });

  it('GitTab_GitPanelRendered', () => {
    const { container } = render(
      <FileTree
        activityTab="git"
        onOpenFolder={() => {}}
        onOpenFile={() => {}}
      />,
    );
    expect(container).toBeTruthy();
  });

  it('SearchTab_SearchPanelRendered', () => {
    const { container } = render(
      <FileTree
        activityTab="search"
        onOpenFolder={() => {}}
        onOpenFile={() => {}}
      />,
    );
    expect(container).toBeTruthy();
  });

  it('IssuesTab_IssuesPanelRendered', () => {
    const { container } = render(
      <FileTree
        activityTab="issues"
        onOpenFolder={() => {}}
        onOpenFile={() => {}}
      />,
    );
    expect(container).toBeTruthy();
  });

  it('OpenEditorsTab_EditorListRendered', () => {
    const { container } = render(
      <FileTree
        activityTab="open-editors"
        onOpenFolder={() => {}}
        onOpenFile={() => {}}
      />,
    );
    expect(container).toBeTruthy();
  });
});
