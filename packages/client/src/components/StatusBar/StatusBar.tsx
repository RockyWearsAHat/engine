import { useCallback, useEffect, useState } from 'react';
import { useStore } from '../../store/index.js';
import { GitBranch, Wifi, WifiOff, Github, Circle } from 'lucide-react';
import { bridge } from '../../bridge.js';
import type { MarkdownViewMode } from '../../editorPreferences.js';
import {
  EDITOR_STATUS_EVENT,
  type EditorStatusDetail,
} from '../../editorEvents.js';

const isDesktop = typeof window !== 'undefined'
  && ('__TAURI__' in window || !!(window as unknown as Record<string, unknown>).electronAPI);

export default function StatusBar() {
  const {
    gitStatus,
    activeFilePath,
    openFiles,
    connected,
    githubUser,
    editorPreferences,
    setEditorPreferences,
  } = useStore();
  const activeFile = openFiles.find(f => f.path === activeFilePath);
  const staged = gitStatus?.staged.length ?? 0;
  const unstaged = gitStatus?.unstaged.length ?? 0;
  const untracked = gitStatus?.untracked.length ?? 0;
  const [editorStatus, setEditorStatus] = useState<EditorStatusDetail | null>(null);

  useEffect(() => {
    const handleEditorStatus = (event: Event) => {
      setEditorStatus((event as CustomEvent<EditorStatusDetail | null>).detail ?? null);
    };
    window.addEventListener(EDITOR_STATUS_EVENT, handleEditorStatus as EventListener);
    return () => window.removeEventListener(EDITOR_STATUS_EVENT, handleEditorStatus as EventListener);
  }, []);

  useEffect(() => {
    if (!activeFilePath) {
      setEditorStatus(null);
    }
  }, [activeFilePath]);

  const updateMarkdownViewMode = useCallback((mode: MarkdownViewMode) => {
    if (editorPreferences.markdownViewMode === mode) {
      return;
    }
    const nextSettings = {
      ...editorPreferences,
      markdownViewMode: mode,
    };
    setEditorPreferences(nextSettings);
    void bridge.setEditorPreferences(nextSettings);
  }, [editorPreferences, setEditorPreferences]);

  return (
    <div className={`status-bar ${connected ? '' : 'disconnected'}`}>
      <div className="status-group">
        <span className={`status-item ${connected ? 'connected' : 'disconnected'}`}>
          {connected ? <Wifi size={10} /> : <WifiOff size={10} />}
          <span>{connected ? 'connected' : 'offline'}</span>
        </span>

        {gitStatus && (
          <span className="status-item">
            <GitBranch size={10} style={{ color: 'var(--accent)' }} />
            <span style={{ color: 'var(--tx-2)' }}>{gitStatus.branch}</span>
            {staged > 0 && <span style={{ color: '#22c55e' }}>+{staged}</span>}
            {unstaged > 0 && <span style={{ color: '#f59e0b' }}>~{unstaged}</span>}
            {untracked > 0 && <span style={{ color: 'var(--tx-3)' }}>?{untracked}</span>}
            {gitStatus.ahead > 0 && <span style={{ color: 'var(--accent)' }}>{'\u2191'}{gitStatus.ahead}</span>}
            {gitStatus.behind > 0 && <span style={{ color: '#f97316' }}>{'\u2193'}{gitStatus.behind}</span>}
          </span>
        )}

        {githubUser && (
          <span className="status-item">
            <Github size={10} />
            <span>{githubUser.login}</span>
          </span>
        )}
      </div>

      <div className="status-group">
        {activeFile && editorStatus && (
          <>
            <span className="status-item">{editorStatus.language}</span>
            <span className="status-item">{editorStatus.fileSizeLabel}</span>
            <span className="status-item">{editorStatus.syntaxStatus}</span>
            <span className="status-item">{editorStatus.wrapLabel}</span>
            <span className="status-item">{editorStatus.locationLabel}</span>
            {activeFile.dirty && (
              <span className="status-item status-item-accent">
                <Circle size={5} fill="currentColor" />
                <span>unsaved</span>
              </span>
            )}
            {editorStatus.markdownFileActive && (
              <span className="status-toggle" aria-label="Markdown view mode">
                {(['text', 'preview'] as MarkdownViewMode[]).map((mode) => (
                  <button
                    key={mode}
                    className={`status-toggle-btn ${editorStatus.markdownViewMode === mode ? 'active' : ''}`}
                    onClick={() => updateMarkdownViewMode(mode)}
                  >
                    {mode === 'text' ? 'Text' : 'Preview'}
                  </button>
                ))}
              </span>
            )}
            {!isDesktop && (
              <button
                className="status-action"
                onClick={() => window.dispatchEvent(new Event('engine:save-active-file'))}
                disabled={!editorStatus.canSave}
                title={editorStatus.canSave ? 'Save active file' : 'Editing is paused while large-file mode is active.'}
              >
                Save
              </button>
            )}
          </>
        )}
        <span className="status-spacer" />
        <span className="status-item">Engine</span>
      </div>
    </div>
  );
}
