import { useEffect, useRef, useState } from 'react';
import { useStore } from './store/index.js';
import { wsClient } from './ws/client.js';
import type { ServerMessage } from '@myeditor/shared';
import { randomUUID } from './utils.js';
import FileTree from './components/FileTree/FileTree.js';
import Editor from './components/Editor/Editor.js';
import Terminal from './components/Terminal/Terminal.js';
import AIChat from './components/AI/AIChat.js';
import AgentPanel from './components/AgentPanel/AgentPanel.js';
import StatusBar from './components/StatusBar/StatusBar.js';
import { FolderTree, SquareTerminal, MessageSquare, GitBranch, Settings, Activity } from 'lucide-react';

// Electron API type
declare global {
  interface Window {
    electronAPI?: {
      getProjectPath(): Promise<string>;
      getGithubToken(): Promise<string | null>;
      setGithubToken(token: string): Promise<boolean>;
      openExternal(url: string): Promise<void>;
      platform: string;
      isElectron: boolean;
    };
  }
}

type ActivityTab = 'explorer' | 'git' | 'settings';
type BottomPanel = 'chat' | 'terminal';

export default function App() {
  const store = useStore();
  const streamingIdRef = useRef<string | null>(null);
  const [activityTab, setActivityTab] = useState<ActivityTab>('explorer');
  const [bottomPanel, setBottomPanel] = useState<BottomPanel>('chat');
  const [showAgentPanel, setShowAgentPanel] = useState(true);
  const currentSessionIdRef = useRef<string | null>(null);

  useEffect(() => {
    wsClient.connect();

    const off = wsClient.onMessage((msg: ServerMessage) => {
      const {
        setSessions, setActiveSession, setMessages, openFile,
        setFileTree, setGitStatus, addToolCall, resolveToolCall,
        appendChunk, finalizeMessage, startAssistantMessage,
        addLiveToolCall, resolveLiveToolCall, updateAgentSession,
      } = useStore.getState();

      switch (msg.type) {
        case 'session.list':
          setSessions(msg.sessions);
          break;

        case 'session.created':
          setActiveSession(msg.session);
          setSessions([...useStore.getState().sessions, msg.session]);
          currentSessionIdRef.current = msg.session.id;
          break;

        case 'session.loaded':
          setActiveSession(msg.session);
          setMessages(msg.messages);
          currentSessionIdRef.current = msg.session.id;
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
            if (currentSessionIdRef.current) {
              updateAgentSession(currentSessionIdRef.current, { isStreaming: true, currentActivity: 'thinking...' });
            }
          }
          if (msg.done) {
            finalizeMessage(streamingIdRef.current!);
            streamingIdRef.current = null;
            if (currentSessionIdRef.current) {
              updateAgentSession(currentSessionIdRef.current, { isStreaming: false, currentActivity: '' });
            }
          } else if (msg.content) {
            appendChunk(streamingIdRef.current!, msg.content);
          }
          break;
        }

        case 'chat.tool_call': {
          const sid = currentSessionIdRef.current;
          if (streamingIdRef.current) {
            const tcId = randomUUID();
            addToolCall(streamingIdRef.current, {
              id: tcId,
              name: msg.name,
              input: msg.input,
              pending: true,
            });
            if (sid) {
              addLiveToolCall(sid, {
                id: tcId,
                name: msg.name,
                input: msg.input,
                pending: true,
                startedAt: Date.now(),
              });
            }
          }
          break;
        }

        case 'chat.tool_result': {
          const sid = currentSessionIdRef.current;
          if (streamingIdRef.current) {
            resolveToolCall(streamingIdRef.current, msg.name, msg.result, msg.isError);
            if (sid) {
              const agentSessions = useStore.getState().agentSessions;
              const agentSession = agentSessions.find(s => s.id === sid);
              const tc = agentSession?.recentToolCalls.slice().reverse().find(t => t.name === msg.name && t.pending);
              if (tc) {
                resolveLiveToolCall(sid, tc.id, msg.result, msg.isError, Date.now() - tc.startedAt);
              }
            }
          }
          break;
        }
      }
    });

    // Get project path — from Electron IPC or fallback
    async function initProject() {
      let projectPath = '.';
      if (window.electronAPI?.isElectron) {
        try {
          projectPath = await window.electronAPI.getProjectPath();
        } catch { /* fallback */ }
      }
      wsClient.send({ type: 'session.create', projectPath: projectPath || '.' });
      wsClient.send({ type: 'session.list' });
    }

    // Wait for WS to connect then init
    const connectInterval = setInterval(() => {
      if (wsClient.isConnected) {
        clearInterval(connectInterval);
        initProject();
      }
    }, 200);

    return () => {
      off();
      clearInterval(connectInterval);
      wsClient.disconnect();
    };
  }, []);

  // Monitor connection
  useEffect(() => {
    const check = setInterval(() => store.setConnected(wsClient.isConnected), 500);
    return () => clearInterval(check);
  }, [store]);

  const { showFileTree, toggleFileTree } = store;

  const activityButtons = [
    { id: 'explorer' as ActivityTab, icon: <FolderTree size={18} />, title: 'Explorer' },
    { id: 'git' as ActivityTab, icon: <GitBranch size={18} />, title: 'Source Control' },
    { id: 'settings' as ActivityTab, icon: <Settings size={18} />, title: 'Settings' },
  ];

  return (
    <div className="flex flex-col h-screen bg-editor-bg text-gray-200 overflow-hidden select-none">
      {/* Main content */}
      <div className="flex flex-1 overflow-hidden">

        {/* Activity bar (far left) */}
        <div className="w-10 shrink-0 flex flex-col items-center py-2 gap-1 bg-[#0a0a0a] border-r border-editor-border">
          {activityButtons.map(btn => (
            <button
              key={btn.id}
              onClick={() => {
                if (activityTab === btn.id) {
                  toggleFileTree();
                } else {
                  setActivityTab(btn.id);
                  if (!showFileTree) toggleFileTree();
                }
              }}
              className={`w-8 h-8 flex items-center justify-center rounded transition-colors ${
                activityTab === btn.id && showFileTree
                  ? 'text-white bg-editor-hover border-l-2 border-blue-400'
                  : 'text-gray-600 hover:text-gray-300'
              }`}
              title={btn.title}
            >
              {btn.icon}
            </button>
          ))}
          <div className="flex-1" />
          {/* Agent panel toggle */}
          <button
            onClick={() => setShowAgentPanel(v => !v)}
            className={`w-8 h-8 flex items-center justify-center rounded transition-colors ${
              showAgentPanel ? 'text-blue-400' : 'text-gray-600 hover:text-gray-300'
            }`}
            title="Agent Monitor"
          >
            <Activity size={18} />
          </button>
        </div>

        {/* File tree / sidebar */}
        {showFileTree && (
          <div className="w-52 shrink-0 border-r border-editor-border overflow-hidden flex flex-col">
            <FileTree activeTab={activityTab} />
          </div>
        )}

        {/* Center: editor + bottom panel */}
        <div className="flex flex-col flex-1 overflow-hidden">
          {/* Monaco editor */}
          <div className="flex-1 overflow-hidden">
            <Editor />
          </div>

          {/* Bottom panel */}
          <div className="h-64 shrink-0 border-t border-editor-border flex flex-col overflow-hidden">
            {/* Panel tabs */}
            <div className="flex items-center bg-editor-surface border-b border-editor-border shrink-0">
              {([
                { id: 'chat' as BottomPanel, label: 'AI Chat', icon: <MessageSquare size={12} /> },
                { id: 'terminal' as BottomPanel, label: 'Terminal', icon: <SquareTerminal size={12} /> },
              ] as const).map(tab => (
                <button
                  key={tab.id}
                  onClick={() => setBottomPanel(tab.id)}
                  className={`flex items-center gap-1.5 px-3 py-1.5 text-xs transition-colors ${
                    bottomPanel === tab.id
                      ? 'text-white border-b-2 border-blue-400'
                      : 'text-gray-500 hover:text-gray-300'
                  }`}
                >
                  {tab.icon} {tab.label}
                </button>
              ))}
            </div>
            <div className="flex-1 overflow-hidden">
              {bottomPanel === 'chat' ? <AIChat /> : <Terminal />}
            </div>
          </div>
        </div>

        {/* Right: Agent monitor panel */}
        {showAgentPanel && (
          <div className="w-64 shrink-0 overflow-hidden">
            <AgentPanel />
          </div>
        )}
      </div>

      {/* Status bar */}
      <StatusBar />
    </div>
  );
}
