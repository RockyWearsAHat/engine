import { act, fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import StatusBar from '../components/StatusBar/StatusBar.js';
import { useStore } from '../store/index.js';
import { EDITOR_STATUS_EVENT } from '../editorEvents.js';

const { setEditorPreferencesMock } = vi.hoisted(() => ({
  setEditorPreferencesMock: vi.fn().mockResolvedValue(true),
}));

vi.mock('../bridge.js', () => ({
  bridge: {
    setEditorPreferences: setEditorPreferencesMock,
  },
}));

function seedStore() {
  useStore.setState({
    connected: true,
    gitStatus: {
      branch: 'main',
      staged: ['a.ts'],
      unstaged: ['b.ts'],
      untracked: ['c.ts'],
      ignored: [],
      ahead: 2,
      behind: 1,
    },
    activeFilePath: '/project/README.md',
    openFiles: [
      { path: '/project/README.md', content: '# hi', language: 'markdown', size: 10, largeFile: false, dirty: true },
    ],
    githubUser: { login: 'octocat' },
    editorPreferences: {
      fontFamily: 'mono',
      fontSize: 13,
      lineHeight: 1.6,
      tabSize: 2,
      markdownViewMode: 'text',
      wordWrap: false,
    },
  });
}

describe('StatusBar interactions', () => {
  beforeEach(() => {
    setEditorPreferencesMock.mockClear();
    seedStore();
  });

  it('Connection_Git_Github_EditorStatus_Rendered', () => {
    render(<StatusBar />);
    act(() => {
      window.dispatchEvent(new CustomEvent(EDITOR_STATUS_EVENT, {
        detail: {
          language: 'Markdown',
          fileSizeLabel: '10 B',
          syntaxStatus: 'syntax on',
          wrapLabel: 'wrap off',
          locationLabel: 'Ln 1, Col 1',
          markdownFileActive: true,
          canSave: true,
        },
      }));
    });

    expect(screen.getByText(/connected/i)).toBeTruthy();
    expect(screen.getByText('main')).toBeTruthy();
    expect(screen.getByText('octocat')).toBeTruthy();
    expect(screen.getByText('Markdown')).toBeTruthy();
    expect(screen.getByText(/unsaved/i)).toBeTruthy();
  });

  it('NoActiveFile_EditorStatusCleared', () => {
    render(<StatusBar />);
    act(() => {
      window.dispatchEvent(new CustomEvent(EDITOR_STATUS_EVENT, {
        detail: {
          language: 'Markdown',
          fileSizeLabel: '10 B',
          syntaxStatus: 'syntax on',
          wrapLabel: 'wrap off',
          locationLabel: 'Ln 1, Col 1',
          markdownFileActive: false,
          canSave: true,
        },
      }));
      useStore.setState({ activeFilePath: null });
    });

    expect(screen.queryByText('Markdown')).toBeNull();
  });

  it('MarkdownViewModeUpdated_PersistedThroughBridge', async () => {
    render(<StatusBar />);
    act(() => {
      window.dispatchEvent(new CustomEvent(EDITOR_STATUS_EVENT, {
        detail: {
          language: 'Markdown',
          fileSizeLabel: '10 B',
          syntaxStatus: 'syntax on',
          wrapLabel: 'wrap off',
          locationLabel: 'Ln 1, Col 1',
          markdownFileActive: true,
          canSave: true,
        },
      }));
    });

    fireEvent.click(screen.getByTitle(/markdown view mode/i));
    await act(async () => {
      fireEvent.click(screen.getByText('Preview'));
    });

    expect(useStore.getState().editorPreferences.markdownViewMode).toBe('preview');
    expect(setEditorPreferencesMock).toHaveBeenCalledWith(
      expect.objectContaining({ markdownViewMode: 'preview' }),
    );
  });

  it('ClickOutside_MarkdownModePopupClosed', () => {
    render(<StatusBar />);
    act(() => {
      window.dispatchEvent(new CustomEvent(EDITOR_STATUS_EVENT, {
        detail: {
          language: 'Markdown',
          fileSizeLabel: '10 B',
          syntaxStatus: 'syntax on',
          wrapLabel: 'wrap off',
          locationLabel: 'Ln 1, Col 1',
          markdownFileActive: true,
          canSave: true,
        },
      }));
    });

    fireEvent.click(screen.getByTitle(/markdown view mode/i));
    expect(screen.getByText('Preview')).toBeTruthy();
    fireEvent.mouseDown(document.body);
    expect(screen.queryByText('Preview')).toBeNull();
  });

  it('CurrentModeClickedAgain_MenuClosesWithoutChangingMode', async () => {
    render(<StatusBar />);
    act(() => {
      window.dispatchEvent(new CustomEvent(EDITOR_STATUS_EVENT, {
        detail: {
          language: 'Markdown',
          fileSizeLabel: '10 B',
          syntaxStatus: 'syntax on',
          wrapLabel: 'wrap off',
          locationLabel: 'Ln 1, Col 1',
          markdownFileActive: true,
          canSave: true,
        },
      }));
    });

    fireEvent.click(screen.getByTitle(/markdown view mode/i));
    await act(async () => {
      fireEvent.click(screen.getByText('Text'));
    });

    expect(useStore.getState().editorPreferences.markdownViewMode).toBe('text');
    expect(setEditorPreferencesMock).not.toHaveBeenCalled();
  });

  it('SaveButtonClicked_SaveEventDispatched', () => {
    render(<StatusBar />);
    act(() => {
      window.dispatchEvent(new CustomEvent(EDITOR_STATUS_EVENT, {
        detail: {
          language: 'Markdown',
          fileSizeLabel: '10 B',
          syntaxStatus: 'syntax on',
          wrapLabel: 'wrap off',
          locationLabel: 'Ln 1, Col 1',
          markdownFileActive: false,
          canSave: true,
        },
      }));
    });

    const saveEvents: Event[] = [];
    const handler = (e: Event) => saveEvents.push(e);
    window.addEventListener('engine:save-active-file', handler);
    fireEvent.click(screen.getByText('Save'));
    window.removeEventListener('engine:save-active-file', handler);

    expect(saveEvents).toHaveLength(1);
  });
});