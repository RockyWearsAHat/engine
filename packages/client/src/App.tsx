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
import { FolderOpen, Terminal as TermIcon, GitBranch, Settings2, Activity, AlertCircle } from 'lucide-react';

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

type ActivityTab = 'explorer' | 'git' | 'issues' | 'settings';
type BottomPanel = 'chat' | 'terminal';

export default function App() {
  const store = useStore();
  const streamingIdRef = useRef<string | null>(null);
  const currentSessionIdRef = useRef<string | null>(null);
  const [activityTab, setActivityTab] = useState<ActivityTab>('explorer');
  const [bottomPanel, setBottomPanel] = useState<BottomPanel>('chat');
  const [showSidebar, setShowSidebar] = useState(true);
  const [showAgentPanel, setShowAgentPanel] = useState(true);
  const [projectName, setProjectName] = useState('MyEditor');

  useEffect(() => {
    wsClient.connect();

    const off = wsClient.onMessage((msg: ServerMessage) => {
      const {
        setSessions, setActiveSession, setMessages, openFile,
        setFileTree, setGitStatus, addToolCall, resolveToolCall,
        appendChunk, finalizeMessage, startAssistantMessage,
        addLiveToolCall, resolveLiveToolCall, updateAgentSession,
        setGithubIssues,
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
        case 'github.issues':
          setGithubIssues(msg.issues);
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
            addToolCall(streamingIdRef.current, { id: tcId, name: msg.name, input: msg.input, pending: true });
            if (sid) {
              addLiveToolCall(sid, { id: tcId, name: msg.name, input: msg.input, pending: true, startedAt: Date.now() });
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
              const tc = agentSession?.recentToolCalls.slice().reverse().find(
                (t: { name: string; pending: boolean }) => t.name === msg.name && t.pending
              );
              if (tc) resolveLiveToolCall(sid, tc.id, msg.result, msg.isError, Date.now() - tc.startedAt);
            }
          }
          break;
        }
      }
    });

    async function initProject() {
      let projectPath = '.';
      if (window.electronAPI?.isElectron) {
        try { projectPath = await window.electronAPI.getProjectPath(); } catch { /* fallback */ }
      }
      const name = projectPath.split('/').pop() ?? 'Project';
      setProjectName(name);
      wsClient.send({ type: 'session.create', projectPath: projectPath || '.' });
      wsClient.send({ type: 'session.list' });
    }

    const connectInterval = setInterval(() => {
      if (wsClient.isConnected) {
        clearInterval(connectInterval);
        initProject();
      }
    }, 150);

    return () => { off(); clearInterval(connectInterval); wsClient.disconnect(); };
  }, []);

  useEffect(() => {
    const check = setInterval(() => store.setConnected(wsClient.isConnected), 500);
    return () => clearInterval(check);
  }, [store]);

  const activityItems: { id: ActivityTab; icon: React.ReactNode; label: string }[] = [
    { id: 'explorer', icon: <FolderOpen size={17} />, label: 'Explorer' },
    { id: 'git', icon: <GitBranch size={17} />, label: 'Source Control' },
    { id: 'issues', icon: <AlertCircle size={17} />, label: 'Issues' },
    { id: 'settings', icon: <Settings2 size={17} />, label: 'Settings' },
  ];

  const handleActivityClick = (id: ActivityTab) => {
    if (activityTab === id && showSidebar) {
      setShowSidebar(false);
    } else {
      setActivityTab(id);
      setShowSidebar(true);
    }
  };

  return (
    <div className="flex flex-col h-screen overflow-hidden" style={{ background: '#0c0c0e', color: '#f0f0f2' }}>

      {/* macOS titlebar drag region — clears traffic lights */}
      <div
        className="titlebar-drag shrink-0 flex items-center"
        style={{ height: 36, paddingLeft: 80, background: '#0c0c0e', borderBottom: '1px solid rgba(255,255,255,0.06)' }}
      >
        <span
          className="titlebar-no-drag text-sm font-medium"
          style={{ color: '#9898a6', fontSize: 12, userSelect: 'none' }}
        >
          {projectName}
        </span>
      </div>

      {/* Main workspace */}
      <div className="flex flex-1 overflow-hidden">

        {/* Activity bar */}
        <div
          className="shrink-0 flex flex-col items-center py-2 gap-0.5"
          style={{ width: 44, background: '#0c0c0e', borderRight: '1px solid rgba(255,255,255,0.06)' }}
        >
          {activityItems.map(item => (
            <button
              key={item.id}
              onClick={() => handleActivityClick(item.id)}
              title={item.label}
              style={{
                width: 32, height: 32,
                display: 'flex', alignItems: 'center', justifyContent: 'center',
                borderRadius: 6,
                color: activityTab === item.id && showSidebar ? '#5b8def' : '#55555f',
                background: activityTab === item.id && showSidebar ? 'rgba(91,141,239,0.1)' : 'transparent',
                border: 'none', cursor: 'pointer',
                transition: 'all 120ms ease',
              }}
              onMouseEnter={e => {
                if (!(activityTab === item.id && showSidebar)) {
                  (e.currentTarget as HTMLButtonElement).style.color = '#9898a6';
                  (e.currentTarget as HTMLButtonElement).style.background = 'rgba(255,255,255,0.05)';
                }
              }}
              onMouseLeave={e => {
                if (!(activityTab === item.id && showSidebar)) {
                  (e.currentTarget as HTMLButtonElement).style.color = '#55555f';
                  (e.currentTarget as HTMLButtonElement).style.background = 'transparent';
                }
              }}
            >
              {item.icon}
            </button>
          ))}

          <div style={{ flex: 1 }} />

          {/* Agent panel toggle */}
          <button
            onClick={() => setShowAgentPanel(v => !v)}
            title="Agent Monitor"
            style={{
              width: 32, height: 32,
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              borderRadius: 6,
              color: showAgentPanel ? '#5b8def' : '#55555f',
              background: showAgentPanel ? 'rgba(91,141,239,0.1)' : 'transparent',
              border: 'none', cursor: 'pointer',
              transition: 'all 120ms ease',
            }}
          >
            <Activity size={17} />
          </button>
        </div>

        {/* Sidebar */}
        {showSidebar && (
          <div
            className="shrink-0 overflow-hidden flex flex-col fade-in"
            style={{ width: 220, borderRight: '1px solid rgba(255,255,255,0.06)', background: '#111113' }}
          >
            <FileTree activeTab={activityTab} />
          </div>
        )}

        {/* Center: editor + bottom panel */}
        <div className="flex flex-col flex-1 overflow-hidden">
          <div className="flex-1 overflow-hidden">
            <Editor />
          </div>

          {/* Bottom panel with tabs */}
          <div
            className="shrink-0 flex flex-col overflow-hidden"
            style={{ height: 260, borderTop: '1px solid rgba(255,255,255,0.06)' }}
          >
            {/* Tab strip */}
            <div
              className="shrink-0 flex items-center gap-1 px-2"
              style={{ height: 30, background: '#111113', borderBottom: '1px solid rgba(255,255,255,0.06)' }}
            >
              {([
                { id: 'chat' as BottomPanel, label: 'AI Chat', icon: null },
                { id: 'terminal' as BottomPanel, label: 'Terminal', icon: <TermIcon size={11} /> },
              ] as const).map(tab => (
                <button
                  key={tab.id}
                  onClick={() => setBottomPanel(tab.id)}
                  className="flex items-center gap-1.5 px-2 py-0.5 rounded transition-all"
                  style={{
                    fontSize: 11,
                    color: bottomPanel === tab.id ? '#f0f0f2' : '#55555f',
                    background: bottomPanel === tab.id ? 'rgba(255,255,255,0.07)' : 'transparent',
                    border: 'none', cursor: 'pointer',
                  }}
                >
                  {tab.icon}
                  {tab.label}
                </button>
              ))}
            </div>
            <div className="flex-1 overflow-hidden">
              {bottomPanel === 'chat' ? <AIChat /> : <Terminal />}
            </div>
          </div>
        </div>

        {/* Right: Agent monitor */}
        {showAgentPanel && (
          <div
            className="shrink-0 overflow-hidden fade-in"
            style={{ width: 260, borderLeft: '1px solid rgba(255,255,255,0.06)' }}
          >
            <AgentPanel />
          </div>
        )}
      </div>

      <StatusBar />
    </div>
  );
}

