import MonacoEditor from '@monaco-editor/react';
import { useStore } from '../../store/index.js';
import { wsClient } from '../../ws/client.js';
import { X } from 'lucide-react';
import { basename } from '../../utils.js';

export default function Editor() {
  const { openFiles, activeFilePath, setActiveFile, closeFile, markFileDirty, markFileSaved } = useStore();

  const activeFile = openFiles.find(f => f.path === activeFilePath);

  const handleSave = (path: string, content: string) => {
    wsClient.send({ type: 'file.save', path, content });
    markFileSaved(path);
  };

  if (openFiles.length === 0) {
    return (
      <div className="flex items-center justify-center h-full text-gray-600 text-sm select-none">
        <div className="text-center">
          <div className="text-4xl mb-3">✦</div>
          <div className="text-gray-500 font-medium">MyEditor</div>
          <div className="text-gray-600 text-xs mt-1">Open a file or ask the AI to get started</div>
        </div>
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full">
      {/* Tab bar */}
      <div className="flex items-center bg-editor-surface border-b border-editor-border overflow-x-auto shrink-0">
        {openFiles.map(file => (
          <div
            key={file.path}
            onClick={() => setActiveFile(file.path)}
            className={`flex items-center gap-1.5 px-3 py-1.5 text-xs cursor-pointer border-r border-editor-border whitespace-nowrap shrink-0 ${
              file.path === activeFilePath
                ? 'bg-editor-bg text-gray-200 border-b-2 border-b-blue-400'
                : 'text-gray-500 hover:bg-editor-hover hover:text-gray-300'
            }`}
          >
            <span className={file.dirty ? 'after:content-["●"] after:ml-1 after:text-blue-400' : ''}>
              {basename(file.path)}
            </span>
            <button
              onClick={(e) => { e.stopPropagation(); closeFile(file.path); }}
              className="ml-1 hover:text-red-400 rounded"
            >
              <X size={10} />
            </button>
          </div>
        ))}
      </div>

      {/* Monaco */}
      {activeFile && (
        <div className="flex-1 overflow-hidden">
          <MonacoEditor
            height="100%"
            theme="vs-dark"
            path={activeFile.path}
            language={activeFile.language}
            value={activeFile.content}
            onChange={(value) => {
              if (value !== undefined) markFileDirty(activeFile.path, value);
            }}
            onMount={(editor, monaco) => {
              editor.addCommand(
                monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS,
                () => handleSave(activeFile.path, editor.getValue())
              );
            }}
            options={{
              fontSize: 13,
              fontFamily: "'JetBrains Mono', 'Fira Code', Menlo, monospace",
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
              padding: { top: 8, bottom: 8 },
            }}
          />
        </div>
      )}
    </div>
  );
}
