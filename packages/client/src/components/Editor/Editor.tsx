import MonacoEditor from '@monaco-editor/react';
import { useStore } from '../../store/index.js';
import { wsClient } from '../../ws/client.js';
import { X, FileText } from 'lucide-react';
import { basename } from '../../utils.js';

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

export default function Editor() {
  const { openFiles, activeFilePath, setActiveFile, closeFile, markFileDirty } = useStore();
  const activeFile = openFiles.find(f => f.path === activeFilePath);

  const handleSave = (path: string, content: string) => {
    wsClient.send({ type: 'file.save', path, content });
  };

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
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', background: 'var(--bg)' }}>
      {/* Tab bar */}
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
              {/* accent top bar for active tab */}
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
                onClick={e => { e.stopPropagation(); closeFile(file.path); }}
                title="Close"
              >
                {file.dirty ? <span style={{ width: 7, height: 7, borderRadius: '50%', background: 'var(--accent-2)', display: 'block' }} />
                             : <X size={11} />}
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
            theme="engine-dark"
            path={activeFile.path}
            language={activeFile.language}
            value={activeFile.content}
            onChange={value => { if (value !== undefined) markFileDirty(activeFile.path, value); }}
            onMount={(editor, monaco) => {
              editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => {
                handleSave(activeFile.path, editor.getValue());
              });
              monaco.editor.defineTheme('engine-dark', {
                base: 'vs-dark',
                inherit: true,
                rules: [
                  { token: 'comment', foreground: '4a4a6a', fontStyle: 'italic' },
                  { token: 'keyword', foreground: '818cf8' },
                  { token: 'string', foreground: '86efac' },
                  { token: 'number', foreground: 'fb923c' },
                  { token: 'type', foreground: '67e8f9' },
                ],
                colors: {
                  'editor.background': '#0c0c10',
                  'editor.lineHighlightBackground': '#111116',
                  'editorGutter.background': '#0c0c10',
                  'editor.selectionBackground': '#1e1e3a',
                  'editor.inactiveSelectionBackground': '#1e1e3a88',
                  'editorLineNumber.foreground': '#2a2a40',
                  'editorLineNumber.activeForeground': '#4a4a6a',
                  'editorIndentGuide.background1': '#1f1f2a',
                  'editorIndentGuide.activeBackground1': '#2a2a40',
                  'editor.findMatchBackground': '#1e1e3a',
                  'editor.findMatchHighlightBackground': '#1a1a30',
                  'scrollbarSlider.background': '#1e1e2e66',
                  'scrollbarSlider.hoverBackground': '#2a2a38aa',
                },
              });
              monaco.editor.setTheme('engine-dark');
              editor.focus();
            }}
            options={{
              fontSize: 13,
              lineHeight: 22,
              fontFamily: '"JetBrains Mono", Menlo, monospace',
              fontLigatures: true,
              lineNumbers: 'on',
              minimap: { enabled: false },
              scrollBeyondLastLine: false,
              wordWrap: 'off',
              tabSize: 2,
              insertSpaces: true,
              automaticLayout: true,
              cursorBlinking: 'smooth',
              cursorSmoothCaretAnimation: 'on',
              smoothScrolling: true,
              renderLineHighlight: 'line',
              bracketPairColorization: { enabled: true },
              guides: { bracketPairs: true, indentation: true },
              suggest: { showIcons: true },
              padding: { top: 16, bottom: 16 },
              scrollbar: { verticalScrollbarSize: 5, horizontalScrollbarSize: 5 },
              overviewRulerLanes: 0,
              hideCursorInOverviewRuler: true,
              glyphMargin: false,
              folding: true,
              renderWhitespace: 'none',
            }}
          />
        </div>
      )}
    </div>
  );
}
