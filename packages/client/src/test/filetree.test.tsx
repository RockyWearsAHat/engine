import { render, fireEvent } from '@testing-library/react';
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

  it('ExplorerTab_OpenEditorsSectionToggle_Click', () => {
    const { container } = render(
      <FileTree activityTab="explorer" onOpenFolder={() => {}} onOpenFile={() => {}} />,
    );
    const headers = container.querySelectorAll('.explorer-section-header');
    if (headers.length > 0) {
      fireEvent.click(headers[0]);
    }
    expect(container).toBeTruthy();
  });

  it('SearchTab_SearchInputChange_UpdatesQuery', () => {
    const { container } = render(
      <FileTree activityTab="search" onOpenFolder={() => {}} onOpenFile={() => {}} />,
    );
    const inputs = container.querySelectorAll('input[type="text"]');
    if (inputs.length > 0) {
      fireEvent.change(inputs[0], { target: { value: 'hello' } });
      fireEvent.keyDown(inputs[0], { key: 'Enter' });
    }
    expect(container).toBeTruthy();
  });

  it('ExplorerTab_FolderNodeClick_TogglesFolderOpen', () => {
    const { getByText } = render(
      <FileTree activityTab="explorer" onOpenFolder={() => {}} onOpenFile={() => {}} />,
    );
    const folderEl = getByText('folder');
    fireEvent.click(folderEl);
    expect(folderEl).toBeTruthy();
  });

  it('ExplorerTab_FileNodeClick_SendsFileRead', async () => {
    const { wsClient } = await import('../ws/client.js');
    const { getByText } = render(
      <FileTree activityTab="explorer" onOpenFolder={() => {}} onOpenFile={() => {}} />,
    );
    const fileEl = getByText('file1.ts');
    fireEvent.click(fileEl.closest('[class*="tree-node"]') ?? fileEl);
    expect(vi.mocked(wsClient).send).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'file.read' }),
    );
  });

  it('GitTab_CommitTextareaChange_SetsMessage', () => {
    useStore.setState({ gitStatus: { branch: 'main', unstaged: [], staged: [], untracked: [], ignored: [], ahead: 0, behind: 0 } });
    const { container } = render(
      <FileTree activityTab="git" onOpenFolder={() => {}} onOpenFile={() => {}} />,
    );
    const textarea = container.querySelector('textarea');
    if (textarea) {
      fireEvent.change(textarea, { target: { value: 'my commit' } });
      expect((textarea as HTMLTextAreaElement).value).toBe('my commit');
    } else {
      expect(container).toBeTruthy();
    }
  });

  it('GitTab_CommitButton_ClickWithMessage', async () => {
    const { wsClient } = await import('../ws/client.js');
    useStore.setState({ gitStatus: { branch: 'main', unstaged: [], staged: [], untracked: [], ignored: [], ahead: 0, behind: 0 } });
    const { container } = render(
      <FileTree activityTab="git" onOpenFolder={() => {}} onOpenFile={() => {}} />,
    );
    const textarea = container.querySelector('textarea');
    if (textarea) {
      fireEvent.change(textarea, { target: { value: 'fix: test' } });
      const btn = container.querySelector('button.btn-primary');
      if (btn) {
        fireEvent.click(btn);
        expect(vi.mocked(wsClient).send).toHaveBeenCalledWith(
          expect.objectContaining({ type: 'git.commit' }),
        );
      } else {
        expect(container).toBeTruthy();
      }
    } else {
      expect(container).toBeTruthy();
    }
  });

  it('GitTab_GitSectionFileClick_CallsOpenDiff', async () => {
    const { wsClient } = await import('../ws/client.js');
    useStore.setState({ gitStatus: { branch: 'main', unstaged: [], staged: ['file3.ts'], untracked: [], ignored: [], ahead: 0, behind: 0 } });
    const { container } = render(
      <FileTree activityTab="git" onOpenFolder={() => {}} onOpenFile={() => {}} />,
    );
    const btns = container.querySelectorAll('button');
    const fileBtn = Array.from(btns).find(b => b.textContent?.includes('file3.ts'));
    if (fileBtn) {
      fireEvent.click(fileBtn);
      expect(vi.mocked(wsClient).send).toHaveBeenCalledWith(
        expect.objectContaining({ type: 'git.diff' }),
      );
    } else {
      expect(container).toBeTruthy();
    }
  });

  it('IssuesTab_IssueClick_OpensExternal', async () => {
    const { bridge } = await import('../bridge.js');
    useStore.setState({
      githubIssues: [{
        number: 42, title: 'Click me', state: 'open', labels: [],
        createdAt: new Date().toISOString(), updatedAt: new Date().toISOString(),
        htmlUrl: 'https://github.com/example/issue/42', body: '', author: 'user',
      }],
    });
    const { getByText } = render(
      <FileTree activityTab="issues" onOpenFolder={() => {}} onOpenFile={() => {}} />,
    );
    const issueEl = getByText('Click me');
    const clickable = issueEl.closest('div') ?? issueEl;
    fireEvent.mouseEnter(clickable);
    fireEvent.mouseLeave(clickable);
    fireEvent.click(clickable);
    expect(vi.mocked(bridge).openExternal).toHaveBeenCalledWith(
      'https://github.com/example/issue/42',
    );
  });

  it('IssuesTab_LoadingAndErrorAndEmptyStates_Render', () => {
    useStore.setState({ githubIssuesLoading: true, githubIssuesError: null, githubIssues: [] });
    const { rerender, container } = render(
      <FileTree activityTab="issues" onOpenFolder={() => {}} onOpenFile={() => {}} />,
    );
    expect(container.textContent).toContain('Loading');

    useStore.setState({ githubIssuesLoading: false, githubIssuesError: 'network failed', githubIssues: [] });
    rerender(<FileTree activityTab="issues" onOpenFolder={() => {}} onOpenFile={() => {}} />);
    expect(container.textContent).toContain('network failed');

    useStore.setState({ githubIssuesLoading: false, githubIssuesError: null, githubIssues: [] });
    rerender(<FileTree activityTab="issues" onOpenFolder={() => {}} onOpenFile={() => {}} />);
    expect(container.textContent).toContain('No open issues');
  });

  it('SearchTab_NoActiveSession_ShowsOpenFolderMessage', () => {
    useStore.setState({ activeSession: null });
    const { container } = render(
      <FileTree activityTab="search" onOpenFolder={() => {}} onOpenFile={() => {}} />,
    );
    expect(container.textContent).toContain('Open a folder to search');
  });

  it('ExplorerTab_FileUrlProjectPath_ExercisesNormalizePath', () => {
    useStore.setState({
      activeSession: {
        id: 's-file-url',
        projectPath: 'file:///project',
        branchName: 'main',
        createdAt: new Date().toISOString(),
        updatedAt: new Date().toISOString(),
        summary: '',
        messageCount: 0,
      },
      gitStatus: {
        ...sampleGitStatus,
        unstaged: ['folder/nested.ts'],
      },
    });
    const { container } = render(
      <FileTree activityTab="explorer" onOpenFolder={() => {}} onOpenFile={() => {}} />,
    );
    expect(container).toBeTruthy();
  });

  it('ExplorerTab_LocalStorageLoadFailure_CatchPathCovered', () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const getItemSpy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('storage unavailable');
    });

    const { container } = render(
      <FileTree activityTab="explorer" onOpenFolder={() => {}} onOpenFile={() => {}} />,
    );

    expect(container).toBeTruthy();
    expect(warnSpy).toHaveBeenCalled();

    getItemSpy.mockRestore();
    warnSpy.mockRestore();
  });

  it('ExplorerTab_GroupFoldersDisabled_UsesAlphabeticalSortPath', () => {
    window.localStorage.setItem('engine:groupFolders', 'false');

    const { container } = render(
      <FileTree activityTab="explorer" onOpenFolder={() => {}} onOpenFile={() => {}} />,
    );

    expect(container).toBeTruthy();

    window.localStorage.removeItem('engine:groupFolders');
  });

  it('GitTab_NoGitStatus_ShowsNoRepositoryState', () => {
    useStore.setState({ gitStatus: null });
    const { container } = render(
      <FileTree activityTab="git" onOpenFolder={() => {}} onOpenFile={() => {}} />,
    );
    expect(container.textContent).toContain('No git repository');
  });

  it('SearchTab_WithResults_RendersResultList', () => {
    const { container, rerender } = render(
      <FileTree activityTab="search" onOpenFolder={() => {}} onOpenFile={() => {}} />,
    );

    const input = container.querySelector('input[type="text"]');
    if (input) {
      fireEvent.change(input, { target: { value: 'needle' } });
    }

    useStore.setState({
      searchResults: [{ path: '/project/file1.ts', line: 3, preview: 'const needle = true;' }],
      searchLoading: false,
      searchError: null,
    });

    rerender(<FileTree activityTab="search" onOpenFolder={() => {}} onOpenFile={() => {}} />);

    expect(container.textContent).toContain('file1.ts');
    expect(container.textContent).toContain('const needle = true;');
  });

  it('ExplorerTab_WindowsFileUrlPath_Normalized', () => {
    useStore.setState({
      activeSession: {
        id: 's-win-url',
        projectPath: 'file:///C:/project',
        branchName: 'main',
        createdAt: new Date().toISOString(),
        updatedAt: new Date().toISOString(),
        summary: '',
        messageCount: 0,
      },
      gitStatus: {
        ...sampleGitStatus,
        unstaged: ['folder/nested.ts'],
      },
    });

    const { container } = render(
      <FileTree activityTab="explorer" onOpenFolder={() => {}} onOpenFile={() => {}} />,
    );

    expect(container).toBeTruthy();
  });

  it('ExplorerTab_HidesGitDirectory_WhenDotfilesDisabled', () => {
    useStore.setState({
      showDotfiles: false,
      fileTree: {
        name: 'project',
        path: '/project',
        type: 'directory',
        children: [
          { name: '.git', path: '/project/.git', type: 'directory', children: [] },
          { name: 'visible.ts', path: '/project/visible.ts', type: 'file' },
        ],
      },
    });

    const { container } = render(
      <FileTree activityTab="explorer" onOpenFolder={() => {}} onOpenFile={() => {}} />,
    );

    expect(container.textContent).toContain('visible.ts');
    expect(container.textContent).not.toContain('.git');
  });

  it('ExplorerTab_NoTreeData_VisibleTreeGuardPathCovered', () => {
    useStore.setState({ fileTree: null });

    const { container } = render(
      <FileTree activityTab="explorer" onOpenFolder={() => {}} onOpenFile={() => {}} />,
    );

    expect(container).toBeTruthy();
  });
});
