import { useEffect, useRef, useState } from 'react';
import { useStore } from './store/index.js';
import { wsClient } from './ws/client.js';
import { bridge } from './bridge.js';
import type { ServerMessage } from '@myeditor/shared';
import { randomUUID } from './utils.js';
import FileTree from './components/FileTree/FileTree.js';
import Editor from './components/Editor/Editor.js';
import Terminal from './components/Terminal/Terminal.js';
import AIChat from './components/AI/AIChat.js';
import AgentPanel from './components/AgentPanel/AgentPanel.js';
import StatusBar from './components/StatusBar/StatusBar.js';
import { FolderOpen, Terminal as TermIcon, GitBranch, Settings2, Activity, AlertCircle } from 'lucide-react';


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
        setSessions, setActiveSession, setMessages, openFile, closeFile, setActiveFile,
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
        case 'editor.tab.close':
          closeFile(msg.path);
          break;
        case 'editor.tab.focus':
          setActiveFile(msg.path);
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
      try { projectPath = await bridge.getProjectPath(); } catch { /* fallback */ }
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
    <div className="flex flex-col h-screen overflow-hidden" style={{ background: 'var(--bg)', color: 'var(--tx)' }}>

      {/* macOS titlebar drag region */}
      <div
        className="titlebar-drag shrink-0 flex items-center"
        style={{ height: 36, paddingLeft: 80, background: 'var(--surface)', borderBottom: '1px solid var(--border)' }}
      >
        <div className="titlebar-no-drag" style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--tx-2)', userSelect: 'none' }}>
            MyEditor
          </span>
          {store.activeSession && (
            <span style={{ fontSize: 11, color: 'var(--tx-3)', fontFamily: 'monospace', userSelect: 'none' }}>
              {store.activeSession.projectPath.split('/').pop()}
            </span>
          )}
        </div>
      </div>

      {/* Main workspace */}
      <div className="flex flex-1 overflow-hidden">

        {/* Activity bar */}
        <div
          className="shrink-0 flex flex-col items-center py-2 gap-0.5"
          style={{ width: 40, background: 'var(--surface)', borderRight: '1px solid var(--border)' }}
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
                color: activityTab === item.id && showSidebar ? 'var(--tx)' : 'var(--tx-3)',
                background: activityTab === item.id && showSidebar ? 'var(--surface-3)' : 'transparent',
                borderLeft: `2px solid ${activityTab === item.id && showSidebar ? 'var(--accent)' : 'transparent'}`,
                border: 'none',
                borderLeftWidth: 2,
                borderLeftStyle: 'solid',
                borderLeftColor: activityTab === item.id && showSidebar ? 'var(--accent)' : 'transparent',
                cursor: 'pointer',
                transition: 'all 150ms ease',
              }}
              onMouseEnter={e => {
                if (!(activityTab === item.id && showSidebar)) {
                  (e.currentTarget as HTMLButtonElement).style.color = 'var(--tx-2)';
                  (e.currentTarget as HTMLButtonElement).style.background = 'var(--surface-3)';
                }
              }}
              onMouseLeave={e => {
                if (!(activityTab === item.id && showSidebar)) {
                  (e.currentTarget as HTMLButtonElement).style.color = 'var(--tx-3)';
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
              color: showAgentPanel ? 'var(--accent)' : 'var(--tx-3)',
              background: showAgentPanel ? 'var(--accent-dim)' : 'transparent',
              border: 'none', cursor: 'pointer',
              transition: 'all 150ms ease',
            }}
          >
            <Activity size={16} />
          </button>
        </div>

        {/* Sidebar */}
        {showSidebar && (
          <div
            className="shrink-0 overflow-hidden flex flex-col fade-in"
            style={{ width: 220, borderRight: '1px solid var(--border)', background: 'var(--surface)' }}
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
            style={{ height: 260, borderTop: '1px solid var(--border)' }}
          >
            {/* Tab strip */}
            <div
              className="shrink-0 flex items-center"
              style={{ background: 'var(--surface)', borderBottom: '1px solid var(--border)', minHeight: 32 }}
            >
              {([
                { id: 'chat' as BottomPanel, label: 'AI Chat', icon: null },
                { id: 'terminal' as BottomPanel, label: 'Terminal', icon: <TermIcon size={11} /> },
              ] as const).map(tab => (
                <button
                  key={tab.id}
                  onClick={() => setBottomPanel(tab.id)}
                  style={{
                    display: 'flex', alignItems: 'center', gap: 6,
                    padding: '0 14px', height: 32,
                    fontSize: '11px', fontFamily: 'Outfit, sans-serif',
                    color: bottomPanel === tab.id ? 'var(--tx)' : 'var(--tx-3)',
                    borderBottom: bottomPanel === tab.id ? '2px solid var(--accent)' : '2px solid transparent',
                    background: 'transparent', border: 'none',
                    borderBottomWidth: 2, borderBottomStyle: 'solid',
                    borderBottomColor: bottomPanel === tab.id ? 'var(--accent)' : 'transparent',
                    cursor: 'pointer',
                    transition: 'color 150ms',
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
            style={{ width: 260, borderLeft: '1px solid var(--border)' }}
          >
            <AgentPanel />
          </div>
        )}
      </div>

      <StatusBar />
    </div>
  );
}

