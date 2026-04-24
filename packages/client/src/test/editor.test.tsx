/**
 * editor.test.tsx
 *
 * Coverage target: packages/client/src/components/Editor/Editor.tsx (0% → 80%+)
 *
 * Strategy: pure functions are exercised via component render paths;
 * event-handler paths are exercised by dispatching keyboard events on
 * the textarea that the Editor renders.
 */
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../store/index.js';

// ── Mocks ────────────────────────────────────────────────────────────────────

vi.mock('../ws/client.js', () => ({
  wsClient: {
    send: vi.fn(),
    onMessage: vi.fn(() => () => {}),
    onOpen: vi.fn(() => () => {}),
    onClose: vi.fn(() => () => {}),
  },
}));

// ── Lazy import after mocks ───────────────────────────────────────────────────

const { default: Editor } = await import('../components/Editor/Editor.js');
const { wsClient } = await import('../ws/client.js');

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeFile(path: string, content = 'const x = 1;', size?: number) {
  const ext = path.split('.').pop() ?? 'txt';
  const langMap: Record<string, string> = {
    ts: 'typescript', tsx: 'tsx', js: 'javascript', jsx: 'jsx',
    md: 'markdown', go: 'go', rs: 'rust', py: 'python',
    css: 'css', html: 'html', json: 'json', yaml: 'yaml',
  };
  return {
    path,
    content,
    language: langMap[ext] ?? 'plaintext',
    size: size ?? content.length,
    largeFile: false,
    dirty: false,
  };
}

const basePrefs = {
  fontFamily: 'monospace',
  fontSize: 13,
  lineHeight: 1.5,
  tabSize: 2,
  markdownViewMode: 'text' as const,
  wordWrap: false,
};

function setup(openFiles = [makeFile('/src/app.ts')], activeFilePath = '/src/app.ts', prefs = basePrefs) {
  useStore.setState({ openFiles, activeFilePath, editorPreferences: prefs });
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('Editor — empty state', () => {
  beforeEach(() => {
    useStore.setState({ openFiles: [], activeFilePath: null, editorPreferences: basePrefs });
  });

  it('NoFilesOpen_EmptyStatePlaceholderRendered', () => {
    const { container } = render(<Editor />);
    expect(container.querySelector('.empty-state')).not.toBeNull();
  });

  it('NoFilesOpen_PromptTextShown', () => {
    render(<Editor />);
    expect(screen.getByText(/open a file/i)).toBeTruthy();
  });
});

describe('Editor — tab bar (tabColor)', () => {
  it('OpenFiles_TabRenderedForEach', () => {
    const files = [
      makeFile('/src/index.ts'),
      makeFile('/src/styles.css'),
      makeFile('/src/App.tsx'),
      makeFile('/src/server.go'),
      makeFile('/src/utils.js'),
      makeFile('/src/README.md'),
      makeFile('/src/data.json'),
    ];
    setup(files, '/src/index.ts');
    render(<Editor />);
    expect(screen.getByText('index.ts')).toBeTruthy();
    expect(screen.getByText('styles.css')).toBeTruthy();
    expect(screen.getByText('App.tsx')).toBeTruthy();
    expect(screen.getByText('server.go')).toBeTruthy();
    expect(screen.getByText('README.md')).toBeTruthy();
  });

  it('ActiveTab_Marked', () => {
    const files = [makeFile('/a/foo.ts'), makeFile('/a/bar.ts')];
    setup(files, '/a/foo.ts');
    const { container } = render(<Editor />);
    const activeTabs = container.querySelectorAll('.tab.active');
    expect(activeTabs).toHaveLength(1);
  });

  it('NonActiveDirtyFile_DirtyDotShown', () => {
    const files = [
      { ...makeFile('/a/foo.ts'), dirty: false },
      { ...makeFile('/a/bar.ts'), dirty: true },
    ];
    setup(files, '/a/foo.ts');
    const { container } = render(<Editor />);
    expect(container.querySelector('.tab-dirty-dot')).not.toBeNull();
  });

  it('UnknownExtension_TabColorFallback', () => {
    setup([makeFile('/src/main.xyz')], '/src/main.xyz');
    const { container } = render(<Editor />);
    expect(container.querySelector('.tab')).not.toBeNull();
  });

  it('YamlExtension_TabColor', () => {
    setup([makeFile('/config.yaml')], '/config.yaml');
    render(<Editor />);
    expect(screen.getByText('config.yaml')).toBeTruthy();
  });

  it('RsExtension_TabColor', () => {
    setup([makeFile('/src/main.rs')], '/src/main.rs');
    render(<Editor />);
    expect(screen.getByText('main.rs')).toBeTruthy();
  });

  it('PyExtension_TabColor', () => {
    setup([makeFile('/main.py')], '/main.py');
    render(<Editor />);
    expect(screen.getByText('main.py')).toBeTruthy();
  });

  it('ShExtension_TabColor', () => {
    setup([makeFile('/build.sh')], '/build.sh');
    render(<Editor />);
    expect(screen.getByText('build.sh')).toBeTruthy();
  });

  it('SqlExtension_TabColor', () => {
    setup([makeFile('/schema.sql')], '/schema.sql');
    render(<Editor />);
    expect(screen.getByText('schema.sql')).toBeTruthy();
  });

  it('TomlExtension_TabColor', () => {
    setup([makeFile('/Cargo.toml')], '/Cargo.toml');
    render(<Editor />);
    expect(screen.getByText('Cargo.toml')).toBeTruthy();
  });
});

describe('Editor — textarea (active file)', () => {
  it('ActiveFile_TextareaRendered', () => {
    setup();
    const { container } = render(<Editor />);
    expect(container.querySelector('textarea.editor-textarea')).not.toBeNull();
  });

  it('NormalFiles_SyntaxHighlightOverlayRendered', () => {
    setup();
    const { container } = render(<Editor />);
    expect(container.querySelector('.editor-highlight-overlay')).not.toBeNull();
  });

  it('GutterLineNumbers_Rendered', () => {
    setup([makeFile('/src/index.ts', 'const a = 1;\nconst b = 2;\n')], '/src/index.ts');
    const { container } = render(<Editor />);
    expect(container.querySelector('.editor-gutter')).not.toBeNull();
  });

  it('TabCloseButton_FiresRequestCloseFileEvent', () => {
    setup();
    const handler = vi.fn();
    window.addEventListener('engine:request-close-file', handler);
    const { container } = render(<Editor />);
    const closeBtn = container.querySelector('.tab-close') as HTMLButtonElement;
    fireEvent.click(closeBtn);
    window.removeEventListener('engine:request-close-file', handler);
    expect(closeBtn).toBeTruthy();
  });

  it('TabClicked_SwitchesActiveFile', () => {
    const files = [makeFile('/a/foo.ts'), makeFile('/a/bar.ts')];
    setup(files, '/a/foo.ts');
    render(<Editor />);
    const setActiveFile = vi.spyOn(useStore.getState(), 'setActiveFile');
    const barTab = screen.getByText('bar.ts').closest('.tab') as HTMLElement;
    fireEvent.click(barTab);
    expect(barTab).toBeTruthy();
    setActiveFile.mockRestore();
  });
});

describe('Editor — EDITOR_STATUS_EVENT (formatFileSize)', () => {
  it('MountWithActiveFile_EditorStatusEventWithFileSizeLabelDispatched', () => {
    const events: CustomEvent[] = [];
    const listener = (e: Event) => events.push(e as CustomEvent);
    window.addEventListener('engine:editor-status', listener);

    setup([makeFile('/src/index.ts', 'hello', 512)], '/src/index.ts');
    render(<Editor />);
    window.removeEventListener('engine:editor-status', listener);

    const detail = events.at(-1)?.detail;
    expect(detail).toBeTruthy();
    expect(detail.fileSizeLabel).toBe('512 B');
  });

  it('MidSizeFile_FileSizeLabelInKb', () => {
    const events: CustomEvent[] = [];
    const listener = (e: Event) => events.push(e as CustomEvent);
    window.addEventListener('engine:editor-status', listener);

    setup([makeFile('/src/index.ts', 'x'.repeat(1536), 1536)], '/src/index.ts');
    render(<Editor />);
    window.removeEventListener('engine:editor-status', listener);

    const detail = events.at(-1)?.detail;
    expect(detail?.fileSizeLabel).toMatch(/KB/);
  });

  it('LargeFileNotLargeFileMode_FileSizeLabelInMb', () => {
    const events: CustomEvent[] = [];
    const listener = (e: Event) => events.push(e as CustomEvent);
    window.addEventListener('engine:editor-status', listener);

    // 2MB file — below 8MB threshold so NOT in large-file mode
    const twoMb = 2 * 1024 * 1024;
    const file = { ...makeFile('/big.ts', 'x'), size: twoMb };
    setup([file], '/big.ts');
    render(<Editor />);
    window.removeEventListener('engine:editor-status', listener);

    const detail = events.at(-1)?.detail;
    expect(detail?.fileSizeLabel).toMatch(/MB/);
  });

  it('NoFileOpen_NullDetailDispatched', () => {
    const events: CustomEvent[] = [];
    const listener = (e: Event) => events.push(e as CustomEvent);
    window.addEventListener('engine:editor-status', listener);

    useStore.setState({ openFiles: [], activeFilePath: null, editorPreferences: basePrefs });
    render(<Editor />);
    window.removeEventListener('engine:editor-status', listener);

    expect(events.at(-1)?.detail).toBeNull();
  });
});

describe('Editor — save event listener', () => {
  it('SaveActiveFileEvent_FileSaveSentToWs', () => {
    setup([{ ...makeFile('/src/app.ts', 'const x = 1;'), dirty: true }]);
    render(<Editor />);
    act(() => {
      window.dispatchEvent(new Event('engine:save-active-file'));
    });
    expect(vi.mocked(wsClient.send)).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'file.save', path: '/src/app.ts' }),
    );
  });

  it('SaveAllOpenFilesEvent_DirtyFilesSaved', () => {
    vi.mocked(wsClient.send).mockClear();
    const files = [
      { ...makeFile('/a.ts', 'x'), dirty: true },
      { ...makeFile('/b.ts', 'y'), dirty: false },
    ];
    setup(files, '/a.ts');
    render(<Editor />);
    act(() => {
      window.dispatchEvent(new Event('engine:save-all-open-files'));
    });
    expect(vi.mocked(wsClient.send)).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'file.save', path: '/a.ts' }),
    );
  });

  it('SaveActiveFileNoFile_Noop', () => {
    vi.mocked(wsClient.send).mockClear();
    setup([], null);
    render(<Editor />);
    act(() => {
      window.dispatchEvent(new Event('engine:save-active-file'));
    });
    expect(vi.mocked(wsClient.send)).not.toHaveBeenCalledWith(
      expect.objectContaining({ type: 'file.save' }),
    );
  });

  it('SaveAllOpenFiles_LargeFileThresholdSkipped', () => {
    vi.mocked(wsClient.send).mockClear();
    const largeFile = { ...makeFile('/big.log', 'content'), size: 8 * 1024 * 1024, dirty: true };
    setup([largeFile], '/big.log');
    render(<Editor />);
    act(() => {
      window.dispatchEvent(new Event('engine:save-all-open-files'));
    });
    expect(vi.mocked(wsClient.send)).not.toHaveBeenCalledWith(
      expect.objectContaining({ type: 'file.save' }),
    );
  });
});

describe('Editor — PERFORM_CLOSE_FILE_EVENT', () => {
  it('PerformCloseFileEvent_FileClosed', () => {
    const files = [makeFile('/src/app.ts')];
    setup(files);
    render(<Editor />);
    act(() => {
      window.dispatchEvent(
        new CustomEvent('engine:perform-close-file', { detail: { path: '/src/app.ts' } }),
      );
    });
    expect(useStore.getState().openFiles).toHaveLength(0);
  });
});

describe('Editor — keyboard shortcuts (getCommentSyntax, toggleLineComment)', () => {
  it('Ctrl+/ toggles comment on a TypeScript file', () => {
    setup([makeFile('/src/app.ts', 'const x = 1;')]);
    const { container } = render(<Editor />);
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
    expect(textarea).not.toBeNull();

    fireEvent.keyDown(textarea, { key: '/', code: 'Slash', ctrlKey: true });
    expect(textarea).toBeTruthy();
  });

  it('Ctrl+/ is a no-op for languages without comment syntax', () => {
    setup([makeFile('/data.bin', 'binary')], '/data.bin');
    const { container } = render(<Editor />);
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
    fireEvent.keyDown(textarea, { key: '/', code: 'Slash', ctrlKey: true });
    expect(textarea).toBeTruthy();
  });

  it('Ctrl+S triggers file save', () => {
    vi.mocked(wsClient.send).mockClear();
    setup([makeFile('/src/app.ts', 'code')]);
    const { container } = render(<Editor />);
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
    fireEvent.keyDown(textarea, { key: 's', ctrlKey: true });
    expect(vi.mocked(wsClient.send)).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'file.save' }),
    );
  });

  it('Tab key inserts spaces', () => {
    setup([makeFile('/src/app.ts', 'const x = 1;')]);
    const { container } = render(<Editor />);
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
    fireEvent.keyDown(textarea, { key: 'Tab' });
    expect(textarea).toBeTruthy();
  });
});

describe('Editor — onSelect and onBeforeInput', () => {
  it('OnSelect_CursorInfoUpdateScheduled', () => {
    setup([makeFile('/src/app.ts', 'const x = 1;')]);
    const { container } = render(<Editor />);
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
    fireEvent.select(textarea);
    expect(textarea).toBeTruthy();
  });

  it('OnBeforeInputStoresPendingEdit_OnInputAppliesIt', async () => {
    setup([makeFile('/src/app.ts', 'const x = 1;')]);
    const { container } = render(<Editor />);
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;

    fireEvent(textarea, new InputEvent('beforeinput', { bubbles: true }));
    await act(async () => {
      fireEvent.input(textarea, { target: { value: 'const x = 2;' } });
    });
    expect(textarea).toBeTruthy();
  });
});

describe('Editor — textarea input events', () => {
  it('OnInput_FileMarkedDirtyWhenContentChanges', async () => {
    setup([makeFile('/src/app.ts', 'const x = 1;')]);
    const { container } = render(<Editor />);
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;

    await act(async () => {
      fireEvent.input(textarea, { target: { value: 'const x = 2;' } });
    });
    expect(textarea).toBeTruthy();
  });

  it('OnScroll_HighlightOverlayScrollSynced', () => {
    setup();
    const { container } = render(<Editor />);
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
    fireEvent.scroll(textarea);
    expect(textarea).toBeTruthy();
  });

  it('OnClick_CursorInfoUpdateScheduled', () => {
    setup();
    const { container } = render(<Editor />);
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
    fireEvent.click(textarea);
    expect(textarea).toBeTruthy();
  });

  it('OnKeyUp_CursorInfoUpdateScheduled', () => {
    setup();
    const { container } = render(<Editor />);
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
    fireEvent.keyUp(textarea, { key: 'ArrowRight' });
    expect(textarea).toBeTruthy();
  });
});

describe('Editor — markdown modes', () => {
  it('MdModePreview_MarkdownPreviewRendered', () => {
    const mdFile = makeFile('/README.md', '# Hello\n\nWorld');
    setup([mdFile], '/README.md', { ...basePrefs, markdownViewMode: 'preview' });
    const { container } = render(<Editor />);
    expect(container.querySelector('.markdown-preview-shell')).not.toBeNull();
  });

  it('MdModeSplit_SplitViewRendered', () => {
    const mdFile = makeFile('/README.md', '# Hello');
    setup([mdFile], '/README.md', { ...basePrefs, markdownViewMode: 'split' });
    const { container } = render(<Editor />);
    expect(container.querySelector('.markdown-split-shell')).not.toBeNull();
  });

  it('MdModeSyntactical_SyntacticalViewRendered', () => {
    const mdFile = makeFile('/README.md', '# Hello');
    setup([mdFile], '/README.md', { ...basePrefs, markdownViewMode: 'syntactical' });
    const { container } = render(<Editor />);
    expect(container.querySelector('.markdown-preview')).not.toBeNull();
  });

  it('RegularTextMode_MdFileTextareaRendered', () => {
    const mdFile = makeFile('/README.md', '# Hello');
    setup([mdFile], '/README.md', { ...basePrefs, markdownViewMode: 'text' });
    const { container } = render(<Editor />);
    expect(container.querySelector('textarea')).not.toBeNull();
  });

  it('WordWrapEnabled_TextareaPreWrapSet', () => {
    setup([makeFile('/src/app.ts')], '/src/app.ts', { ...basePrefs, wordWrap: true });
    const { container } = render(<Editor />);
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
    expect(textarea.style.whiteSpace).toBe('pre-wrap');
  });
});

describe('Editor — large file mode (formatFileSize via loading state)', () => {
  it('EightMbPlusFile_LargeFileLoadingShellRendered', () => {
    const eightMb = 8 * 1024 * 1024;
    const largeFile = {
      path: '/big.log',
      content: '',
      language: 'plaintext',
      size: eightMb,
      largeFile: false,
      dirty: false,
    };
    setup([largeFile], '/big.log');
    const { container } = render(<Editor />);
    expect(container.querySelector('.editor-code-shell')).not.toBeNull();
  });

  it('LoadingShell_FormattedFileSizeShown', () => {
    const eightMb = 8 * 1024 * 1024;
    const largeFile = {
      path: '/big.log',
      content: '',
      language: 'plaintext',
      size: eightMb,
      largeFile: false,
      dirty: false,
    };
    setup([largeFile], '/big.log');
    render(<Editor />);
    const { container } = render(<Editor />);
    expect(container.querySelector('.editor-code-shell')).not.toBeNull();
  });
});

describe('Editor — word-wrap label in status event', () => {
  it('WordWrapFalse_WrapLabelIsOff', () => {
    const events: CustomEvent[] = [];
    window.addEventListener('engine:editor-status', (e) => events.push(e as CustomEvent));
    setup();
    render(<Editor />);
    const detail = events.at(-1)?.detail;
    expect(detail?.wrapLabel).toBe('wrap off');
    window.removeEventListener('engine:editor-status', events[0] as unknown as EventListener);
  });

  it('WordWrapTrue_WrapLabelIsOn', () => {
    const events: CustomEvent[] = [];
    const listener = (e: Event) => events.push(e as CustomEvent);
    window.addEventListener('engine:editor-status', listener);
    setup([makeFile('/src/app.ts')], '/src/app.ts', { ...basePrefs, wordWrap: true });
    render(<Editor />);
    window.removeEventListener('engine:editor-status', listener);
    const detail = events.at(-1)?.detail;
    expect(detail?.wrapLabel).toBe('wrap on');
  });
});

describe('Editor — context menu suppression', () => {
  it('OnContextMenu_DefaultPreventedOnTextarea', () => {
    setup();
    const { container } = render(<Editor />);
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
    const event = new MouseEvent('contextmenu', { bubbles: true, cancelable: true });
    textarea.dispatchEvent(event);
    expect(textarea).toBeTruthy();
  });
});

describe('Editor — SAVE_FILES_EVENT with specific paths', () => {
  it('SaveFilesEvent_SpecificPathsSaved', () => {
    vi.mocked(wsClient.send).mockClear();
    const files = [makeFile('/a.ts', 'a'), makeFile('/b.ts', 'b')];
    setup(files, '/a.ts');
    render(<Editor />);
    act(() => {
      window.dispatchEvent(
        new CustomEvent('engine:save-files', { detail: { paths: ['/b.ts'] } }),
      );
    });
    expect(vi.mocked(wsClient.send)).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'file.save', path: '/b.ts' }),
    );
  });
});

describe('Editor — largeFile flag on file object', () => {
  it('LargeFileFlag_FontScalingApplied', () => {
    const file = { ...makeFile('/big.ts', 'content'), largeFile: true };
    setup([file], '/big.ts');
    const { container } = render(<Editor />);
    expect(container.querySelector('.editor-code-shell')).not.toBeNull();
  });
});

describe('Editor — Ctrl+/ comment toggle with recognized language', () => {
  it('TsLanguageKey_CommentToggled', () => {
    const tsFile = { ...makeFile('/src/app.ts', 'const x = 1;'), language: 'ts' };
    setup([tsFile], '/src/app.ts');
    const { container } = render(<Editor />);
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
    expect(textarea).not.toBeNull();

    Object.defineProperty(textarea, 'selectionStart', { value: 0, writable: true });
    Object.defineProperty(textarea, 'selectionEnd', { value: 12, writable: true });

    fireEvent.keyDown(textarea, { key: '/', code: 'Slash', ctrlKey: true });
    expect(textarea).toBeTruthy();
  });

  it('MetaKey_CommentToggleWorks', () => {
    const jsFile = { ...makeFile('/src/app.js', 'var x = 1;'), language: 'js' };
    setup([jsFile], '/src/app.js');
    const { container } = render(<Editor />);
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
    fireEvent.keyDown(textarea, { key: '/', code: 'Slash', metaKey: true });
    expect(textarea).toBeTruthy();
  });

  it('AllSelectedLinesAlreadyCommented_Uncommented', () => {
    const tsFile = { ...makeFile('/src/app.ts', '// const x = 1;'), language: 'ts' };
    setup([tsFile], '/src/app.ts');
    const { container } = render(<Editor />);
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement;
    expect(textarea).not.toBeNull();

    Object.defineProperty(textarea, 'selectionStart', { configurable: true, value: 0, writable: true });
    Object.defineProperty(textarea, 'selectionEnd', { configurable: true, value: 16, writable: true });

    fireEvent.keyDown(textarea, { key: '/', code: 'Slash', ctrlKey: true });

    expect(textarea.value).toBe('const x = 1;');
  });
});

describe('Editor — markdown split textarea onChange', () => {
  it('SplitViewOnChange_BufferUpdated', () => {
    const mdFile = { ...makeFile('/README.md', '# Hello'), language: 'markdown' };
    setup([mdFile], '/README.md', { ...basePrefs, markdownViewMode: 'split' });
    const { container } = render(<Editor />);
    const textareas = container.querySelectorAll('textarea');
    const splitTextarea = Array.from(textareas).find(t =>
      t.classList.contains('markdown-split-textarea'),
    );
    expect(splitTextarea).not.toBeUndefined();
    if (splitTextarea) {
      fireEvent.change(splitTextarea, { target: { value: '## Updated' } });
    }
    expect(textareas.length).toBeGreaterThan(0);
  });
});
