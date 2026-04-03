import MonacoEditor from '@monaco-editor/react';
import { useStore } from '../../store/index.js';
import { wsClient } from '../../ws/client.js';
import { X } from 'lucide-react';
import { basename } from '../../utils.js';

function fileExt(path: string): string {
  return path.split('.').pop()?.toLowerCase() ?? '';
}

function tabColor(path: string): string {
  const ext = fileExt(path);
  const map: Record<string, string> = {
    ts: '#4d7fff', tsx: '#4d7fff', js: '#f59e0b', jsx: '#f59e0b',
    css: '#a78bfa', scss: '#a78bfa', html: '#f97316',
    json: '#f59e0b', md: '#999', py: '#22c55e', go: '#22d3ee',
    rs: '#f97316', sh: '#22c55e',
  };
  return map[ext] ?? '#555';
}

export default function Editor() {
  const { openFiles, activeFilePath, setActiveFile, closeFile, markFileDirty, markFileSaved } = useStore();
  const activeFile = openFiles.find(f => f.path === activeFilePath);

  const handleSave = (path: string, content: string) => {
    wsClient.send({ type: 'file.save', path, content });
    markFileSaved(path);
  };

  if (openFiles.length === 0) {
    return (
      <div className="fade-in" style={{
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        height: '100%', background: 'var(--bg)',
      }}>
        <div style={{ textAlign: 'center', userSelect: 'none' }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', marginBottom: 16 }}>
            <svg width="40" height="40" viewBox="0 0 40 40" fill="none">
              <rect x="1" y="1" width="38" height="38" rx="8" stroke="#2a2a2a" strokeWidth="1.5" />
              <path d="M12 20h16M20 12v16" stroke="#4d7fff" strokeWidth="1.5" strokeLinecap="round" opacity="0.6" />
            </svg>
          </div>
          <p style={{ fontSize: '12px', fontWeight: 500, color: 'var(--tx-2)' }}>MyEditor</p>
          <p style={{ fontSize: '10px', color: 'var(--tx-3)', marginTop: 4 }}>Open a file or tell the AI what to build</p>
        </div>
      </div>
    );
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', background: 'var(--bg)' }}>
      {/* Tab bar */}
      <div style={{
        display: 'flex', alignItems: 'center', overflowX: 'auto',
        flexShrink: 0, background: 'var(--surface)',
        borderBottom: '1px solid var(--border)', minHeight: '34px',
      }}>
        {openFiles.map(file => {
          const isActive = file.path === activeFilePath;
          const color = tabColor(file.path);
          return (
            <div
              key={file.path}
              onClick={() => setActiveFile(file.path)}
              className="group"
              style={{
                display: 'flex', alignItems: 'center', gap: 8,
                padding: '0 12px', height: '100%', minHeight: 34,
                cursor: 'pointer', fontSize: '12px', whiteSpace: 'nowrap',
                flexShrink: 0, position: 'relative',
                borderRight: '1px solid var(--border)',
                background: isActive ? 'var(--bg)' : 'transparent',
                color: isActive ? 'var(--tx)' : 'var(--tx-3)',
                transition: 'color 150ms, background 150ms',
              }}
              onMouseEnter={e => {
                if (!isActive) (e.currentTarget as HTMLDivElement).style.color = 'var(--tx-2)';
              }}
              onMouseLeave={e => {
                if (!isActive) (e.currentTarget as HTMLDivElement).style.color = 'var(--tx-3)';
              }}
            >
              {isActive && (
                <span style={{
                  position: 'absolute', top: 0, left: 0, right: 0, height: 1,
                  background: color,
                }} />
              )}
              <span style={{ color, fontSize: '9px', fontFamily: 'monospace', fontWeight: 600 }}>
                {file.path.split('.').pop()?.toUpperCase().slice(0, 2) ?? '??'}
              </span>
              <span style={{ fontSize: '12px' }}>{basename(file.path)}</span>
              {file.dirty && (
                <span style={{ width: 6, height: 6, borderRadius: '50%', background: 'var(--accent)', flexShrink: 0 }} />
              )}
              <button
                onClick={(e) => { e.stopPropagation(); closeFile(file.path); }}
                style={{
                  marginLeft: 2, background: 'none', border: 'none', cursor: 'pointer',
                  color: 'var(--tx-3)', padding: 1, borderRadius: 2,
                  display: 'flex', alignItems: 'center', transition: 'color 150ms',
                  opacity: isActive ? 1 : 0,
                }}
                onMouseEnter={e => {
                  (e.currentTarget as HTMLButtonElement).style.color = 'var(--tx)';
                  (e.currentTarget as HTMLButtonElement).style.opacity = '1';
                }}
                onMouseLeave={e => {
                  (e.currentTarget as HTMLButtonElement).style.color = 'var(--tx-3)';
                  (e.currentTarget as HTMLButtonElement).style.opacity = isActive ? '1' : '0';
                }}
              >
                <X size={11} />
              </button>
            </div>
          );
        })}
      </div>

      {/* Monaco */}
      {activeFile && (
        <div style={{ flex: 1, overflow: 'hidden' }}>
          <MonacoEditor
            height="100%"
            theme="vs-dark"
            path={activeFile.path}
            language={activeFile.language}
            value={activeFile.content}
            onChange={(value) => { if (value !== undefined) markFileDirty(activeFile.path, value); }}
            onMount={(editor, monaco) => {
              editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => {
                handleSave(activeFile.path, editor.getValue());
              });
              monaco.editor.defineTheme('myeditor-dark', {
                base: 'vs-dark',
                inherit: true,
                rules: [],
                colors: {
                  'editor.background': '#080808',
                  'editor.lineHighlightBackground': '#0d0d0d',
                  'editorGutter.background': '#080808',
                  'editor.selectionBackground': '#1a2d5a',
                  'editor.inactiveSelectionBackground': '#1a2d5a88',
                  'editorLineNumber.foreground': '#333333',
                  'editorLineNumber.activeForeground': '#555555',
                },
              });
              monaco.editor.setTheme('myeditor-dark');
            }}
            options={{
              fontSize: 13,
              fontFamily: '"JetBrains Mono", Menlo, monospace',
              fontLigatures: true,
              lineNumbers: 'on',
              minimap: { enabled: false },
              scrollBeyondLastLine: false,
              wordWrap: 'off',
              tabSize: 2,
              insertSpaces: true,
              cursorBlinking: 'smooth',
              smoothScrolling: true,
              renderWhitespace: 'selection',
              padding: { top: 12, bottom: 12 },
              lineHeight: 1.7,
              letterSpacing: 0.3,
              scrollbar: { verticalScrollbarSize: 4, horizontalScrollbarSize: 4 },
              overviewRulerBorder: false,
              hideCursorInOverviewRuler: true,
              renderLineHighlight: 'gutter',
            }}
          />
        </div>
      )}
    </div>
  );
}
