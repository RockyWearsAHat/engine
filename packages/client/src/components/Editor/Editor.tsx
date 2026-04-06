import { memo, useCallback, useEffect, useRef, useState } from 'react';
import { useShallow } from 'zustand/react/shallow';
import { useStore } from '../../store/index.js';
import { wsClient } from '../../ws/client.js';
import { X, FileText, Gauge } from 'lucide-react';
import { basename } from '../../utils.js';
import { highlightCode, resolveSyntaxLanguage } from './editorSyntax.js';
import {
  buildLineBreaks,
  getHighlightDelayMs,
  lineColumnFromOffset,
  updateLineBreaksForEdit,
} from './editorBuffer.js';
import MarkdownPreview from './MarkdownPreview.js';
import SyntacticalPreview from './SyntacticalPreview.js';
import {
  EDITOR_STATUS_EVENT,
  PERFORM_CLOSE_FILE_EVENT,
  REQUEST_CLOSE_FILE_EVENT,
  SAVE_FILES_EVENT,
  type CloseFileEventDetail,
  type EditorStatusDetail,
  type SaveFilesEventDetail,
} from '../../editorEvents.js';

const SYNTAX_HIGHLIGHT_MAX_BYTES = 768 * 1024;
const LARGE_FILE_OPTIMIZATION_THRESHOLD = 8 * 1024 * 1024;
const LARGE_FILE_INDEX_CHUNK = 1_200_000;
const LARGE_FILE_OVERSCAN = 80;
const EDITOR_SURFACE_PADDING = 16;
const largeFileIndexCache = new Map<string, { size: number; lineStarts: number[] }>();

function tabColor(path: string): string {
  const ext = path.split('.').pop()?.toLowerCase() ?? '';
  const map: Record<string, string> = {
    ts: '#6366f1', tsx: '#6366f1', js: '#f59e0b', jsx: '#f59e0b',
    css: '#a78bfa', scss: '#a78bfa', less: '#a78bfa',
    html: '#fb923c', json: '#f59e0b', yaml: '#f43f5e', yml: '#f43f5e',
    md: '#888', mdx: '#888', py: '#22c55e', go: '#22d3ee',
    rs: '#fb923c', sh: '#22c55e', sql: '#f59e0b', toml: '#fb923c',
  };
  return map[ext] ?? '#555';
}

function formatFileSize(bytes: number): string {
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)} KB`;
  }
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function getCommentSyntax(language: string): { start: string; end?: string } | null {
  const map: Record<string, { start: string; end?: string }> = {
    js: { start: '//' }, jsx: { start: '//' }, ts: { start: '//' }, tsx: { start: '//' },
    py: { start: '#' }, go: { start: '//' }, rs: { start: '//' }, sh: { start: '#' }, bash: { start: '#' },
    java: { start: '//' }, c: { start: '//' }, cpp: { start: '//' }, cs: { start: '//' },
    css: { start: '/*', end: '*/' }, scss: { start: '//' }, less: { start: '//' },
    html: { start: '<!--', end: '-->' }, xml: { start: '<!--', end: '-->' },
    sql: { start: '--' }, yaml: { start: '#' }, json: { start: '//' },
  };
  return map[language] ?? null;
}

function toggleLineComment(text: string, selStart: number, selEnd: number, commentSyntax: { start: string; end?: string }): { text: string; newStart: number; newEnd: number } {
  const lines = text.split('\n');
  let lineStart = 0;
  let startLine = 0;
  let endLine = 0;
  
  // Find which lines are selected
  for (let i = 0; i < lines.length; i++) {
    const lineEnd = lineStart + lines[i].length + 1;
    if (lineStart <= selStart && selStart < lineEnd) startLine = i;
    if (lineStart <= selEnd && selEnd <= lineEnd) endLine = i;
    lineStart = lineEnd;
  }
  
  // Check if all selected lines are commented
  const commentPrefix = commentSyntax.start + ' ';
  const allCommented = lines.slice(startLine, endLine + 1).every(line => line.trim().startsWith(commentSyntax.start));
  
  // Toggle comments
  const modified = [...lines];
  if (allCommented) {
    // Uncomment
    for (let i = startLine; i <= endLine; i++) {
      const match = modified[i].match(new RegExp(`^(\\s*)${commentSyntax.start.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')} ?`));
      if (match) {
        modified[i] = modified[i].slice(match[0].length);
      }
    }
  } else {
    // Comment
    for (let i = startLine; i <= endLine; i++) {
      modified[i] = (modified[i].match(/^\s*/) ? modified[i].match(/^\s*/)?.[0] : '') + commentPrefix + modified[i].trimStart();
    }
  }
  
  const newText = modified.join('\n');
  return { text: newText, newStart: selStart, newEnd: Math.min(selEnd, newText.length) };
}

function trimRenderedLine(rawLine: string): string {
  let nextLine = rawLine;
  if (nextLine.endsWith('\n')) {
    nextLine = nextLine.slice(0, -1);
  }
  if (nextLine.endsWith('\r')) {
    nextLine = nextLine.slice(0, -1);
  }
  return nextLine;
}

function readLargeFileLine(text: string, lineStarts: number[], lineIndex: number): string {
  const start = lineStarts[lineIndex] ?? 0;
  const end = lineIndex + 1 < lineStarts.length ? lineStarts[lineIndex + 1] : text.length;
  return trimRenderedLine(text.slice(start, end));
}

function buildLargeFileIndex(
  text: string,
  onProgress: (progress: number) => void,
  signal: AbortSignal,
): Promise<number[]> {
  return new Promise((resolve, reject) => {
    const lineStarts = [0];
    let offset = 0;

    const step = () => {
      if (signal.aborted) {
        reject(new DOMException('Large file indexing aborted', 'AbortError'));
        return;
      }

      const nextLimit = Math.min(text.length, offset + LARGE_FILE_INDEX_CHUNK);
      for (let index = offset; index < nextLimit; index += 1) {
        if (text.charCodeAt(index) === 10) {
          lineStarts.push(index + 1);
        }
      }

      offset = nextLimit;
      onProgress(text.length === 0 ? 1 : nextLimit / text.length);

      if (offset < text.length) {
        requestAnimationFrame(step);
        return;
      }

      if (lineStarts.length > 1 && lineStarts[lineStarts.length - 1] === text.length) {
        lineStarts.pop();
      }
      resolve(lineStarts);
    };

    step();
  });
}

function LargeFileViewport({
  path,
  text,
  size,
  fontFamily,
  fontSize,
  lineHeight,
  onVisibleLineChange,
}: {
  path: string;
  text: string;
  size: number;
  fontFamily: string;
  fontSize: number;
  lineHeight: number;
  onVisibleLineChange: (line: number) => void;
}) {
  const cachedIndex = largeFileIndexCache.get(path);
  const [lineStarts, setLineStarts] = useState<number[]>(() =>
    cachedIndex?.size === size ? cachedIndex.lineStarts : [],
  );
  const [indexProgress, setIndexProgress] = useState(() => (cachedIndex?.size === size ? 1 : 0));
  const [viewportHeight, setViewportHeight] = useState(0);
  const [scrollTop, setScrollTop] = useState(0);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const lineHeightPx = Math.max(18, fontSize * lineHeight);

  useEffect(() => {
    const cached = largeFileIndexCache.get(path);
    if (cached?.size === size) {
      setLineStarts(cached.lineStarts);
      setIndexProgress(1);
      return;
    }

    const controller = new AbortController();
    setLineStarts([]);
    setIndexProgress(0);
    void buildLargeFileIndex(text, setIndexProgress, controller.signal)
      .then((computedLineStarts) => {
        largeFileIndexCache.set(path, { size, lineStarts: computedLineStarts });
        setLineStarts(computedLineStarts);
        setIndexProgress(1);
      })
      .catch((error: unknown) => {
        if (error instanceof DOMException && error.name === 'AbortError') {
          return;
        }
        setLineStarts([0]);
        setIndexProgress(1);
      });

    return () => controller.abort();
  }, [path, size, text]);

  useEffect(() => {
    const node = scrollRef.current;
    if (!node || typeof ResizeObserver === 'undefined') {
      return;
    }
    const updateViewportHeight = () => setViewportHeight(node.clientHeight);
    updateViewportHeight();
    const observer = new ResizeObserver(updateViewportHeight);
    observer.observe(node);
    return () => observer.disconnect();
  }, [lineStarts.length]);

  useEffect(() => {
    onVisibleLineChange(Math.max(1, Math.floor(scrollTop / lineHeightPx) + 1));
  }, [lineHeightPx, onVisibleLineChange, scrollTop]);

  if (lineStarts.length === 0) {
    return (
      <div className="large-file-loading-shell">
        <div className="large-file-loading-card">
          <div className="large-file-loading-kicker">Opening large file</div>
          <div className="large-file-loading-title">Preparing {formatFileSize(size)} for smooth scrolling.</div>
          <div className="large-file-loading-copy">
            Engine is indexing line boundaries once so this file stays responsive in the same editor.
          </div>
          <div className="large-file-loading-progress">
            <span style={{ width: `${Math.max(indexProgress * 100, 6)}%` }} />
          </div>
          <div className="large-file-loading-meta">
            <Gauge size={14} />
            {(indexProgress * 100).toFixed(indexProgress === 1 ? 0 : 1)}% ready
          </div>
        </div>
      </div>
    );
  }

  const totalLines = Math.max(lineStarts.length, 1);
  const visibleLineCount = Math.max(1, Math.ceil((viewportHeight || 640) / lineHeightPx) + LARGE_FILE_OVERSCAN * 2);
  const startLine = Math.max(0, Math.floor(scrollTop / lineHeightPx) - LARGE_FILE_OVERSCAN);
  const endLine = Math.min(totalLines, startLine + visibleLineCount);
  const renderedLines = [];

  for (let index = startLine; index < endLine; index += 1) {
    renderedLines.push({
      lineNumber: index + 1,
      text: readLargeFileLine(text, lineStarts, index),
    });
  }

  return (
    <div className="large-file-shell">
      <div className="large-file-banner">
        <span className="editor-meta-chip">Large-file optimization</span>
        <span>
          Engine is keeping this file responsive in the same editor.
          Live editing and syntax coloring stay paused only while the file is this large.
        </span>
      </div>

      <div
        ref={scrollRef}
        className="large-file-scroll"
        onScroll={(event) => setScrollTop(event.currentTarget.scrollTop)}
      >
        <div
          className="large-file-inner"
          style={{ height: totalLines * lineHeightPx }}
        >
          <div
            className="large-file-window"
            style={{ transform: `translateY(${startLine * lineHeightPx}px)` }}
          >
            {renderedLines.map((line) => (
              <div
                key={line.lineNumber}
                className="large-file-line"
                style={{
                  minHeight: lineHeightPx,
                  fontFamily,
                  fontSize,
                  lineHeight,
                }}
              >
                <span className="large-file-line-number">{line.lineNumber}</span>
                <span className="large-file-line-text">{line.text || ' '}</span>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

function Editor() {
  const {
    openFiles,
    activeFilePath,
    setActiveFile,
    closeFile,
    syncFileContent,
    markFileDirty,
    markFileSaved,
    editorPreferences,
  } = useStore(useShallow((state) => ({
    openFiles: state.openFiles,
    activeFilePath: state.activeFilePath,
    setActiveFile: state.setActiveFile,
    closeFile: state.closeFile,
    syncFileContent: state.syncFileContent,
    markFileDirty: state.markFileDirty,
    markFileSaved: state.markFileSaved,
    editorPreferences: state.editorPreferences,
  })));
  const activeFile = openFiles.find(f => f.path === activeFilePath);
  const [selectionInfo, setSelectionInfo] = useState({ line: 1, column: 1, endLine: 1 });
  const buffersRef = useRef<Record<string, string>>({});
  const lineBreaksRef = useRef<Record<string, number[]>>({});
  const pendingEditRef = useRef<{ path: string; start: number; end: number } | null>(null);
  const cursorFrameRef = useRef<number | null>(null);
  const highlightFrameRef = useRef<number | null>(null);
  const highlightTimeoutRef = useRef<number | null>(null);
  const scrollFrameRef = useRef<number | null>(null);
  const pendingScrollTopRef = useRef(0);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const highlightOverlayRef = useRef<HTMLDivElement | null>(null);
  const highlightContentRef = useRef<HTMLPreElement | null>(null);
  const contentAreaRef = useRef<HTMLDivElement | null>(null);
  const gutterRef = useRef<HTMLDivElement | null>(null);
  const [editorScrollTop, setEditorScrollTop] = useState(0);
  const [editorViewportHeight, setEditorViewportHeight] = useState(600);

  const largeFileOptimizationActive = Boolean(activeFile && activeFile.size >= LARGE_FILE_OPTIMIZATION_THRESHOLD);
  const activeSyntaxLanguage = activeFile
    ? resolveSyntaxLanguage(activeFile.path, activeFile.language)
    : 'plaintext';
  const markdownFileActive = Boolean(activeFile && activeSyntaxLanguage === 'markdown');
  const mdMode = editorPreferences.markdownViewMode;
  const markdownPreviewActive = Boolean(markdownFileActive && mdMode === 'preview' && !largeFileOptimizationActive);
  const markdownSplitActive = Boolean(markdownFileActive && mdMode === 'split' && !largeFileOptimizationActive);
  const markdownSyntacticalActive = Boolean(markdownFileActive && mdMode === 'syntactical' && !largeFileOptimizationActive);
  const currentValue = activeFile
    ? largeFileOptimizationActive
      ? activeFile.content
      : (buffersRef.current[activeFile.path] ?? activeFile.content)
    : '';

  const ensureLineBreaks = (path: string, text: string): number[] => {
    const cached = lineBreaksRef.current[path];
    if (cached) {
      return cached;
    }

    const computed = buildLineBreaks(text);
    lineBreaksRef.current[path] = computed;
    return computed;
  };

  const commitSelectionInfo = useCallback((nextSelection: { line: number; column: number; endLine: number }) => {
    setSelectionInfo((currentSelection) => (
      currentSelection.line === nextSelection.line
      && currentSelection.column === nextSelection.column
      && currentSelection.endLine === nextSelection.endLine
        ? currentSelection
        : nextSelection
    ));
  }, []);

  const updateCursorInfo = (textarea: HTMLTextAreaElement, path: string) => {
    const lineBreaks = lineBreaksRef.current[path] ?? buildLineBreaks(textarea.value);
    lineBreaksRef.current[path] = lineBreaks;
    const start = lineColumnFromOffset(lineBreaks, textarea.selectionStart ?? 0);
    const end = lineColumnFromOffset(lineBreaks, textarea.selectionEnd ?? 0);
    commitSelectionInfo({ line: start.line, column: start.column, endLine: end.line });
  };

  const scheduleCursorInfo = (textarea: HTMLTextAreaElement, path: string) => {
    if (cursorFrameRef.current !== null) {
      cancelAnimationFrame(cursorFrameRef.current);
    }

    cursorFrameRef.current = requestAnimationFrame(() => {
      cursorFrameRef.current = null;
      updateCursorInfo(textarea, path);
    });
  };

  const syncHighlightScroll = (textarea: HTMLTextAreaElement) => {
    if (!highlightOverlayRef.current) {
      return;
    }

    highlightOverlayRef.current.scrollTop = textarea.scrollTop;
    highlightOverlayRef.current.scrollLeft = textarea.scrollLeft;
  };

  const syncGutterScroll = (textarea: HTMLTextAreaElement) => {
    if (!gutterRef.current) {
      return;
    }
    gutterRef.current.scrollTop = textarea.scrollTop;
  };

  const reconcileDirtyState = useCallback((path: string, nextValue: string, baselineValue: string) => {
    buffersRef.current[path] = nextValue;
    if (nextValue === baselineValue) {
      markFileSaved(path);
      return;
    }
    markFileDirty(path);
  }, [markFileDirty, markFileSaved]);

  const cancelPendingHighlightWork = useCallback(() => {
    if (highlightTimeoutRef.current !== null) {
      window.clearTimeout(highlightTimeoutRef.current);
      highlightTimeoutRef.current = null;
    }
    if (highlightFrameRef.current !== null) {
      cancelAnimationFrame(highlightFrameRef.current);
      highlightFrameRef.current = null;
    }
  }, []);

  const renderHighlightedBuffer = useCallback((path: string, text: string) => {
    if (!activeFile || activeFile.path !== path || !highlightContentRef.current) {
      return;
    }

    const syntaxEnabled = activeFile.size < LARGE_FILE_OPTIMIZATION_THRESHOLD
      && !activeFile.largeFile
      && activeFile.size <= SYNTAX_HIGHLIGHT_MAX_BYTES;
    if (!syntaxEnabled) {
      highlightContentRef.current.className = 'editor-highlight-code';
      highlightContentRef.current.textContent = text;
      return;
    }

    const syntaxLanguage = resolveSyntaxLanguage(activeFile.path, activeFile.language);
    highlightContentRef.current.innerHTML = highlightCode(text, syntaxLanguage);
    highlightContentRef.current.className = `editor-highlight-code language-${syntaxLanguage}`;
  }, [activeFile]);

  const scheduleHighlightedBuffer = useCallback((path: string, text: string, mode: 'input' | 'sync' = 'sync') => {
    cancelPendingHighlightWork();
    const runHighlight = () => {
      highlightFrameRef.current = requestAnimationFrame(() => {
        highlightFrameRef.current = null;
        renderHighlightedBuffer(path, text);
      });
    };

    const delay = mode === 'input' ? getHighlightDelayMs(text.length) : 0;
    if (delay === 0) {
      runHighlight();
      return;
    }

    highlightTimeoutRef.current = window.setTimeout(() => {
      highlightTimeoutRef.current = null;
      runHighlight();
    }, delay);
  }, [cancelPendingHighlightWork, renderHighlightedBuffer]);

  const scheduleEditorScrollTop = useCallback((scrollTop: number) => {
    pendingScrollTopRef.current = scrollTop;
    if (scrollFrameRef.current !== null) {
      return;
    }

    scrollFrameRef.current = requestAnimationFrame(() => {
      scrollFrameRef.current = null;
      setEditorScrollTop((currentScrollTop) => (
        currentScrollTop === pendingScrollTopRef.current
          ? currentScrollTop
          : pendingScrollTopRef.current
      ));
    });
  }, []);

  useEffect(() => {
    if (!activeFile) {
      setSelectionInfo({ line: 1, column: 1, endLine: 1 });
      return;
    }

    if (buffersRef.current[activeFile.path] === undefined || !activeFile.dirty) {
      buffersRef.current[activeFile.path] = activeFile.content;
      if (activeFile.size < LARGE_FILE_OPTIMIZATION_THRESHOLD) {
        lineBreaksRef.current[activeFile.path] = buildLineBreaks(activeFile.content);
      } else {
        delete lineBreaksRef.current[activeFile.path];
      }
    }

    if (activeFile.size >= LARGE_FILE_OPTIMIZATION_THRESHOLD) {
      setSelectionInfo({ line: 1, column: 1, endLine: 1 });
      return;
    }

    const textarea = textareaRef.current;
    if (textarea?.dataset.path === activeFile.path) {
      const nextValue = buffersRef.current[activeFile.path] ?? activeFile.content;
      if (textarea.value !== nextValue) {
        const nextCursor = Math.min(textarea.selectionStart ?? 0, nextValue.length);
        textarea.value = nextValue;
        textarea.selectionStart = nextCursor;
        textarea.selectionEnd = nextCursor;
      }
      syncHighlightScroll(textarea);
      scheduleCursorInfo(textarea, activeFile.path);
    }
    scheduleHighlightedBuffer(activeFile.path, buffersRef.current[activeFile.path] ?? activeFile.content, 'sync');
  }, [activeFile?.path, activeFile?.content, activeFile?.dirty, activeFile?.size, scheduleHighlightedBuffer]);

  useEffect(() => () => {
    cancelPendingHighlightWork();
    if (cursorFrameRef.current !== null) {
      cancelAnimationFrame(cursorFrameRef.current);
    }
    if (scrollFrameRef.current !== null) {
      cancelAnimationFrame(scrollFrameRef.current);
    }
  }, [cancelPendingHighlightWork]);

  useEffect(() => {
    const node = contentAreaRef.current;
    if (!node || typeof ResizeObserver === 'undefined') {
      return;
    }
    const update = () => setEditorViewportHeight(node.clientHeight);
    update();
    const observer = new ResizeObserver(update);
    observer.observe(node);
    return () => observer.disconnect();
  }, [activeFile?.path]);

  useEffect(() => {
    pendingScrollTopRef.current = 0;
    if (scrollFrameRef.current !== null) {
      cancelAnimationFrame(scrollFrameRef.current);
      scrollFrameRef.current = null;
    }
    setEditorScrollTop(0);
  }, [activeFile?.path]);

  const saveFileByPath = useCallback((path: string) => {
    const file = openFiles.find((openFile) => openFile.path === path);
    if (!file || file.size >= LARGE_FILE_OPTIMIZATION_THRESHOLD) {
      return;
    }

    const content = file.path === activeFile?.path
      ? (textareaRef.current?.value ?? buffersRef.current[file.path] ?? file.content)
      : (buffersRef.current[file.path] ?? file.content);
    buffersRef.current[file.path] = content;
    syncFileContent(file.path, content);
    wsClient.send({ type: 'file.save', path: file.path, content });
  }, [activeFile?.path, openFiles, syncFileContent]);

  const handleSave = useCallback(() => {
    if (!activeFile) {
      return;
    }
    saveFileByPath(activeFile.path);
  }, [activeFile, saveFileByPath]);

  const saveDirtyFiles = useCallback((paths?: string[]) => {
    const targetPaths = paths && paths.length > 0
      ? paths
      : openFiles.filter((file) => file.dirty).map((file) => file.path);
    targetPaths.forEach((path) => saveFileByPath(path));
  }, [openFiles, saveFileByPath]);

  const performCloseFile = useCallback((path: string) => {
    delete buffersRef.current[path];
    delete lineBreaksRef.current[path];
    if (pendingEditRef.current?.path === path) {
      pendingEditRef.current = null;
    }
    closeFile(path);
  }, [closeFile]);

  const requestCloseFile = useCallback((path: string) => {
    window.dispatchEvent(new CustomEvent<CloseFileEventDetail>(REQUEST_CLOSE_FILE_EVENT, {
      detail: { path },
    }));
  }, []);

  useEffect(() => {
    const saveListener = () => handleSave();
    const saveAllListener = () => saveDirtyFiles();
    const saveFilesListener = (event: Event) => {
      const detail = (event as CustomEvent<SaveFilesEventDetail>).detail;
      saveDirtyFiles(detail?.paths);
    };
    const closeFileListener = (event: Event) => {
      const detail = (event as CustomEvent<CloseFileEventDetail>).detail;
      if (detail?.path) {
        performCloseFile(detail.path);
      }
    };
    window.addEventListener('engine:save-active-file', saveListener);
    window.addEventListener('engine:save-all-open-files', saveAllListener);
    window.addEventListener(SAVE_FILES_EVENT, saveFilesListener as EventListener);
    window.addEventListener(PERFORM_CLOSE_FILE_EVENT, closeFileListener as EventListener);
    return () => {
      window.removeEventListener('engine:save-active-file', saveListener);
      window.removeEventListener('engine:save-all-open-files', saveAllListener);
      window.removeEventListener(SAVE_FILES_EVENT, saveFilesListener as EventListener);
      window.removeEventListener(PERFORM_CLOSE_FILE_EVENT, closeFileListener as EventListener);
    };
  }, [handleSave, performCloseFile, saveDirtyFiles]);

  const syntaxHighlightingEnabled = Boolean(
    activeFile
    && activeFile.size < LARGE_FILE_OPTIMIZATION_THRESHOLD
    && !activeFile.largeFile
    && activeFile.size <= SYNTAX_HIGHLIGHT_MAX_BYTES,
  );
  const markdownWrapForced = markdownFileActive && !largeFileOptimizationActive;
  const wrapEnabled = editorPreferences.wordWrap || markdownWrapForced;
  const editorFontSize = activeFile?.largeFile
    ? Math.min(editorPreferences.fontSize, 12)
    : editorPreferences.fontSize;
  const editorLineHeight = activeFile?.largeFile
    ? Math.max(1.45, editorPreferences.lineHeight - 0.05)
    : editorPreferences.lineHeight;
  const tabInsertion = ' '.repeat(editorPreferences.tabSize);
  const editorWhiteSpace = wrapEnabled ? 'pre-wrap' : 'pre';

  // Round to integer to eliminate sub-pixel drift between textarea text layout and gutter divs
  const lineHeightPx = Math.round(editorFontSize * editorLineHeight);
  const lineCount = activeFile && !largeFileOptimizationActive
    ? (lineBreaksRef.current[activeFile.path]?.length ?? 0) + 1
    : 0;
  const GUTTER_OVERSCAN = 20;
  const gutterStartLine = Math.max(1, Math.floor(editorScrollTop / lineHeightPx) + 1 - GUTTER_OVERSCAN);
  const gutterVisibleCount = Math.ceil(editorViewportHeight / lineHeightPx) + GUTTER_OVERSCAN * 2;
  const gutterEndLine = Math.min(lineCount, gutterStartLine + gutterVisibleCount);
  const gutterLines: number[] = [];
  for (let i = gutterStartLine; i <= gutterEndLine; i += 1) {
    gutterLines.push(i);
  }

  const syntaxStatus = largeFileOptimizationActive
    ? 'large-file mode'
    : markdownPreviewActive
      ? 'preview'
      : markdownSplitActive
        ? 'split preview'
        : markdownSyntacticalActive
          ? 'syntactical'
          : activeFile?.largeFile
            ? 'optimized text'
            : syntaxHighlightingEnabled
              ? 'syntax on'
              : 'plain text';
  const locationLabel = largeFileOptimizationActive
    ? `Top line ${selectionInfo.line}`
    : `Ln ${selectionInfo.line}, Col ${selectionInfo.column}`;
  const wrapLabel = wrapEnabled ? (markdownWrapForced ? 'markdown wrap' : 'wrap on') : 'wrap off';
  const gutterDigits = String(Math.max(lineCount, 1)).length;
  const gutterWidth = Math.max(52, 22 + gutterDigits * Math.max(8, editorFontSize * 0.68));
  const gutterContentHeight = lineCount * lineHeightPx;

  useEffect(() => {
    if (!activeFile) {
      window.dispatchEvent(new CustomEvent<EditorStatusDetail | null>(EDITOR_STATUS_EVENT, { detail: null }));
      return;
    }

    const detail: EditorStatusDetail = {
      path: activeFile.path,
      language: activeFile.language,
      fileSizeLabel: formatFileSize(activeFile.size),
      locationLabel,
      syntaxStatus,
      wrapLabel,
      markdownFileActive: markdownFileActive && !largeFileOptimizationActive,
      markdownViewMode: editorPreferences.markdownViewMode,
      canSave: !largeFileOptimizationActive,
      dirty: activeFile.dirty,
    };
    window.dispatchEvent(new CustomEvent<EditorStatusDetail | null>(EDITOR_STATUS_EVENT, { detail }));
  }, [
    activeFile,
    editorPreferences.markdownViewMode,
    largeFileOptimizationActive,
    locationLabel,
    markdownFileActive,
    syntaxStatus,
    wrapLabel,
  ]);

  useEffect(() => () => {
    window.dispatchEvent(new CustomEvent<EditorStatusDetail | null>(EDITOR_STATUS_EVENT, { detail: null }));
  }, []);

  if (openFiles.length === 0) {
    return (
      <div className="empty-state animate-appear" style={{ background: 'var(--bg)', height: '100%' }}>
        <FileText size={36} style={{ opacity: 0.12 }} />
        <span style={{ color: 'var(--tx-3)' }}>Open a file from the explorer</span>
        <span style={{ fontSize: 11, color: 'var(--tx-3)' }}>or ask the AI to create one</span>
      </div>
    );
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', minHeight: 0, background: 'var(--bg)' }}>
      <div className="tab-bar">
        {openFiles.map(file => {
          const isActive = file.path === activeFilePath;
          const color = tabColor(file.path);
          const name = basename(file.path);
          return (
            <div
              key={file.path}
              className={`tab ${isActive ? 'active' : ''}`}
              onClick={() => setActiveFile(file.path)}
              title={file.path}
            >
              {isActive && (
                <span style={{
                  position: 'absolute', top: 0, left: 0, right: 0, height: 2,
                  background: color, borderRadius: '0 0 2px 2px',
                }} />
              )}
              <FileText size={11} style={{ color, flexShrink: 0 }} />
              <span className="tab-name">{name}</span>
              {file.dirty && !isActive && <span className="tab-dirty-dot" />}
              <button
                className="tab-close"
                onClick={e => { e.stopPropagation(); requestCloseFile(file.path); }}
                title="Close"
              >
                {file.dirty ? <span style={{ width: 7, height: 7, borderRadius: '50%', background: 'var(--accent-2)', display: 'block' }} />
                             : <X size={11} />}
              </button>
            </div>
          );
        })}
      </div>

      {activeFile && (
        <div style={{ display: 'flex', flexDirection: 'column', height: '100%', minHeight: 0, overflow: 'hidden', background: '#0c0c10' }}>
          <div className={`editor-code-shell ${syntaxHighlightingEnabled ? 'syntax-enabled' : ''}`}>
            {largeFileOptimizationActive ? (
              <LargeFileViewport
                path={activeFile.path}
                text={activeFile.content}
                size={activeFile.size}
                fontFamily={editorPreferences.fontFamily}
                fontSize={editorFontSize}
                lineHeight={editorLineHeight}
                onVisibleLineChange={(line) => commitSelectionInfo({ line, column: 1, endLine: line })}
              />
            ) : (
              <>
                {!markdownPreviewActive && !markdownSplitActive && !markdownSyntacticalActive && (
                <>
                <div className="editor-gutter" ref={gutterRef} style={{ width: gutterWidth }}>
                  <div className="editor-gutter-inner">
                    <div style={{ height: (gutterStartLine - 1) * lineHeightPx, flexShrink: 0 }} />
                    {gutterLines.map(n => (
                      <div
                        key={n}
                        className={`line-num${n >= selectionInfo.line && n <= selectionInfo.endLine ? ' active' : ''}`}
                        style={{
                          height: lineHeightPx,
                          lineHeight: `${lineHeightPx}px`,
                          fontSize: editorFontSize,
                          fontFamily: editorPreferences.fontFamily,
                        }}
                      >
                        {n}
                      </div>
                    ))}
                    <div style={{ height: Math.max(0, lineCount - gutterEndLine) * lineHeightPx, flexShrink: 0 }} />
                  </div>
                </div>
                <div className="editor-content-area" ref={contentAreaRef}>
                  {syntaxHighlightingEnabled && (
                    <div ref={highlightOverlayRef} className="editor-highlight-overlay" aria-hidden="true">
                      <pre
                        ref={highlightContentRef}
                        className="editor-highlight-code"
                        style={{
                          fontFamily: editorPreferences.fontFamily,
                          fontSize: editorFontSize,
                          lineHeight: `${lineHeightPx}px`,
                          whiteSpace: editorWhiteSpace,
                          overflowWrap: wrapEnabled ? 'break-word' : 'normal',
                          tabSize: editorPreferences.tabSize,
                        }}
                      />
                    </div>
                  )}
                  <textarea
                    key={activeFile.path}
                    ref={node => {
                      textareaRef.current = node;
                      if (node) {
                        syncHighlightScroll(node);
                        syncGutterScroll(node);
                        pendingScrollTopRef.current = node.scrollTop;
                        setEditorScrollTop(node.scrollTop);
                        scheduleCursorInfo(node, activeFile.path);
                      }
                    }}
                    className={`editor-textarea ${syntaxHighlightingEnabled ? 'syntax-enabled' : ''}`}
                    data-path={activeFile.path}
                    defaultValue={currentValue}
                    spellCheck={false}
                    wrap={wrapEnabled ? 'soft' : 'off'}
                    onBeforeInput={event => {
                      pendingEditRef.current = {
                        path: activeFile.path,
                        start: event.currentTarget.selectionStart ?? 0,
                        end: event.currentTarget.selectionEnd ?? 0,
                      };
                    }}
                    onInput={event => {
                      const previousValue = buffersRef.current[activeFile.path] ?? activeFile.content;
                      const nextValue = event.currentTarget.value;
                      const pendingEdit = pendingEditRef.current;
                      const lineBreaks = ensureLineBreaks(activeFile.path, previousValue);

                      if (pendingEdit?.path === activeFile.path) {
                        const insertedLength = nextValue.length - (previousValue.length - (pendingEdit.end - pendingEdit.start));
                        const insertedText = nextValue.slice(pendingEdit.start, pendingEdit.start + Math.max(insertedLength, 0));
                        lineBreaksRef.current[activeFile.path] = updateLineBreaksForEdit(
                          lineBreaks,
                          pendingEdit.start,
                          pendingEdit.end,
                          insertedText,
                        );
                      } else {
                        lineBreaksRef.current[activeFile.path] = buildLineBreaks(nextValue);
                      }

                      pendingEditRef.current = null;
                      reconcileDirtyState(activeFile.path, nextValue, activeFile.content);
                      syncHighlightScroll(event.currentTarget);
                      syncGutterScroll(event.currentTarget);
                      scheduleCursorInfo(event.currentTarget, activeFile.path);
                      scheduleHighlightedBuffer(activeFile.path, nextValue, 'input');
                    }}
                    onClick={event => scheduleCursorInfo(event.currentTarget, activeFile.path)}
                    onKeyUp={event => scheduleCursorInfo(event.currentTarget, activeFile.path)}
                    onSelect={event => scheduleCursorInfo(event.currentTarget, activeFile.path)}
                    onScroll={event => {
                      syncHighlightScroll(event.currentTarget);
                      syncGutterScroll(event.currentTarget);
                      scheduleEditorScrollTop(event.currentTarget.scrollTop);
                    }}
                    onKeyDown={event => {
                      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 's') {
                        event.preventDefault();
                        handleSave();
                        return;
                      }

                      if ((event.metaKey || event.ctrlKey) && (event.key === '/' || event.code === 'Slash')) {
                        event.preventDefault();
                        const textarea = event.currentTarget;
                        const syntax = getCommentSyntax(activeFile?.language ?? '');
                        if (!syntax) return;
                        
                        const start = textarea.selectionStart;
                        const end = textarea.selectionEnd;
                        const { text: nextValue, newStart, newEnd } = toggleLineComment(textarea.value, start, end, syntax);
                        
                        textarea.value = nextValue;
                        reconcileDirtyState(activeFile.path, nextValue, activeFile.content);
                        textarea.selectionStart = newStart;
                        textarea.selectionEnd = newEnd;
                        
                        const lineBreaks = buildLineBreaks(nextValue);
                        lineBreaksRef.current[activeFile.path] = lineBreaks;
                        syncHighlightScroll(textarea);
                        syncGutterScroll(textarea);
                        scheduleCursorInfo(textarea, activeFile.path);
                        scheduleHighlightedBuffer(activeFile.path, nextValue, 'input');
                        return;
                      }

                      if (event.key === 'Tab') {
                        event.preventDefault();
                        const textarea = event.currentTarget;
                        const start = textarea.selectionStart;
                        const end = textarea.selectionEnd;
                        const nextValue = `${textarea.value.slice(0, start)}${tabInsertion}${textarea.value.slice(end)}`;
                        const lineBreaks = ensureLineBreaks(activeFile.path, textarea.value);
                        lineBreaksRef.current[activeFile.path] = updateLineBreaksForEdit(lineBreaks, start, end, tabInsertion);
                        textarea.value = nextValue;
                        reconcileDirtyState(activeFile.path, nextValue, activeFile.content);
                        textarea.selectionStart = start + tabInsertion.length;
                        textarea.selectionEnd = start + tabInsertion.length;
                        syncHighlightScroll(textarea);
                        syncGutterScroll(textarea);
                        scheduleCursorInfo(textarea, activeFile.path);
                        scheduleHighlightedBuffer(activeFile.path, nextValue, 'input');
                      }
                    }}
                    onContextMenu={(event) => event.preventDefault()}
                    style={{
                      fontSize: editorFontSize,
                      lineHeight: `${lineHeightPx}px`,
                      fontFamily: editorPreferences.fontFamily,
                      whiteSpace: editorWhiteSpace,
                      overflowWrap: wrapEnabled ? 'break-word' : 'normal',
                      tabSize: editorPreferences.tabSize,
                    }}
                  />
                </div>
                </>
                )}
              </>
            )}
            {markdownPreviewActive && (
              <div className="markdown-preview-shell">
                <MarkdownPreview
                  value={currentValue}
                  className="editor-markdown-preview"
                />
              </div>
            )}
            {markdownSplitActive && (
              <div className="markdown-split-shell">
                <div className="markdown-split-raw">
                  <textarea
                    className="editor-textarea markdown-split-textarea"
                    defaultValue={currentValue}
                    spellCheck={false}
                    wrap="soft"
                    onChange={event => {
                      if (activeFile) {
                        const nextValue = event.currentTarget.value;
                        buffersRef.current[activeFile.path] = nextValue;
                        reconcileDirtyState(activeFile.path, nextValue, activeFile.content);
                      }
                    }}
                    style={{
                      fontSize: editorFontSize,
                      lineHeight: `${lineHeightPx}px`,
                      fontFamily: editorPreferences.fontFamily,
                      tabSize: editorPreferences.tabSize,
                    }}
                  />
                </div>
                <div className="markdown-split-divider" />
                <div className="markdown-split-preview">
                  <MarkdownPreview
                    value={currentValue}
                    className="editor-markdown-preview"
                  />
                </div>
              </div>
            )}
            {markdownSyntacticalActive && (
              <div className="markdown-preview-shell">
                <SyntacticalPreview value={currentValue} />
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

export default memo(Editor);
