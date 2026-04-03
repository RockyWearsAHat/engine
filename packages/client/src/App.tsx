import { useEffect, useRef } from 'react';
import { useStore } from './store/index.js';
import { wsClient } from './ws/client.js';
import type { ServerMessage } from '@myeditor/shared';
import { randomUUID } from './utils.js';
import FileTree from './components/FileTree/FileTree.js';
import Editor from './components/Editor/Editor.js';
import Terminal from './components/Terminal/Terminal.js';
import AIChat from './components/AI/AIChat.js';
import StatusBar from './components/StatusBar/StatusBar.js';
import { FolderTree, MessageSquare, SquareTerminal } from 'lucide-react';

export default function App() {
  const store = useStore();
  const streamingIdRef = useRef<string | null>(null);

  useEffect(() => {
    wsClient.connect();

    const off = wsClient.onMessage((msg: ServerMessage) => {
      const { setConnected, setSessions, setActiveSession, setMessages, openFile,
              setFileTree, setGitStatus, addToolCall, resolveToolCall,
              appendChunk, finalizeMessage, startAssistantMessage } = useStore.getState();

      switch (msg.type) {
        case 'session.list':
          setSessions(msg.sessions);
          break;
        case 'session.created':
          setActiveSession(msg.session);
          setSessions([...useStore.getState().sessions, msg.session]);
          break;
        case 'session.loaded':
          setActiveSession(msg.session);
          setMessages(msg.messages);
          break;
        case 'file.content':
          openFile(msg.path, msg.content, msg.language);
          break;
        case 'file.tree':
          setFileTree(msg.tree);
          break;
        case 'git.status':
          setGitStatus(msg.status);
          break;
        case 'editor.open':
          wsClient.send({ type: 'file.read', path: msg.path });
          break;
        case 'chat.chunk': {
          if (!streamingIdRef.current) {
            const newId = randomUUID();
            streamingIdRef.current = newId;
            startAssistantMessage(newId);
          }
          if (msg.done) {
            finalizeMessage(streamingIdRef.current!);
            streamingIdRef.current = null;
          } else if (msg.content) {
            appendChunk(streamingIdRef.current!, msg.content);
          }
          break;
        }
        case 'chat.tool_call': {
          if (streamingIdRef.current) {
            addToolCall(streamingIdRef.current, {
              id: randomUUID(),
              name: msg.name,
              input: msg.input,
              pending: true,
            });
          }
          break;
        }
        case 'chat.tool_result': {
          if (streamingIdRef.current) {
            resolveToolCall(streamingIdRef.current, msg.name, msg.result, msg.isError);
          }
          break;
        }
      }
    });

    // Auto-create a session for the current project
    wsClient.send({
      type: 'session.create',
      projectPath: window.location.pathname !== '/' ? window.location.pathname : '.',
    });

    return () => {
      off();
      wsClient.disconnect();
    };
  }, []);

  // Monitor WS connection state
  useEffect(() => {
    const check = setInterval(() => {
      store.setConnected(wsClient.isConnected);
    }, 500);
    return () => clearInterval(check);
  }, [store]);

  const { showFileTree, bottomPanel, toggleFileTree, setBottomPanel } = store;

  return (
    <div className="flex flex-col h-screen bg-editor-bg text-gray-200 overflow-hidden">
      {/* Top bar */}
      <div className="flex items-center justify-between px-3 py-1.5 bg-editor-surface border-b border-editor-border shrink-0">
        <div className="flex items-center gap-1">
          <span className="text-sm font-semibold text-blue-400 mr-2">MyEditor</span>
          <button
            onClick={toggleFileTree}
            className={`p-1 rounded hover:bg-editor-hover ${showFileTree ? 'text-blue-400' : 'text-gray-500'}`}
            title="Toggle file tree"
          >
            <FolderTree size={16} />
          </button>
        </div>
        <div className="flex items-center gap-1">
          <button
            onClick={() => setBottomPanel('chat')}
            className={`p-1 rounded hover:bg-editor-hover ${bottomPanel === 'chat' ? 'text-blue-400' : 'text-gray-500'}`}
            title="AI Chat"
          >
            <MessageSquare size={16} />
          </button>
          <button
            onClick={() => setBottomPanel('terminal')}
            className={`p-1 rounded hover:bg-editor-hover ${bottomPanel === 'terminal' ? 'text-blue-400' : 'text-gray-500'}`}
            title="Terminal"
          >
            <SquareTerminal size={16} />
          </button>
        </div>
      </div>

      {/* Main content */}
      <div className="flex flex-1 overflow-hidden">
        {/* File tree */}
        {showFileTree && (
          <div className="w-56 shrink-0 border-r border-editor-border overflow-y-auto">
            <FileTree />
          </div>
        )}

        {/* Editor + bottom panel */}
        <div className="flex flex-col flex-1 overflow-hidden">
          {/* Monaco editor */}
          <div className="flex-1 overflow-hidden">
            <Editor />
          </div>

          {/* Bottom panel: AI chat or terminal */}
          <div className="h-72 shrink-0 border-t border-editor-border overflow-hidden">
            {bottomPanel === 'chat' ? <AIChat /> : <Terminal />}
          </div>
        </div>
      </div>

      {/* Status bar */}
      <StatusBar />
    </div>
  );
}
