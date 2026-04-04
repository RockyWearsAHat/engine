import { useEffect, useRef, useState, useCallback } from 'react';
import { useStore } from './store/index.js';
import { wsClient } from './ws/client.js';
import { bridge } from './bridge.js';
import type { ServerMessage } from '@engine/shared';
import { randomUUID } from './utils.js';
import FileTree from './components/FileTree/FileTree.js';
import Editor from './components/Editor/Editor.js';
import Terminal from './components/Terminal/Terminal.js';
import AIChat from './components/AI/AIChat.js';
import AgentPanel from './components/AgentPanel/AgentPanel.js';
import StatusBar from './components/StatusBar/StatusBar.js';
import {
  FolderOpen, GitBranch, AlertCircle, Settings2, Activity,
  Search, ChevronRight, ChevronLeft, ServerCog, ChevronDown,
} from 'lucide-react';

type ActivityTab = 'explorer' | 'git' | 'issues' | 'search' | 'settings';
type RightTab = 'chat' | 'agent';

export default function App() {
  const {
    connected, setConnected,
    sessions, setSessions,
    activeSession, setActiveSession,
    chatMessages, addUserMessage, startAssistantMessage,
    appendChunk, addToolCall, resolveToolCall,
    fileTree, setFileTree,
    openFiles, openFile, closeFile, setActiveFile,
    gitStatus, setGitStatus,
    githubIssues, setGithubIssues, setGithubIssuesLoading,
    agentSessions, updateAgentSession,
  } = useStore();

  const [activityTab, setActivityTab] = useState<ActivityTab>('explorer');
  const [showSidebar, setShowSidebar] = useState(true);
  const [rightTab, setRightTab] = useState<RightTab>('chat');
  const [showTerminal, setShowTerminal] = useState(false);
  const [projectName, setProjectName] = useState('');
  const [terminalHeight, setTerminalHeight] = useState(220);

  const streamingRef = useRef<{ sessionId: string; msgId: string } | null>(null);

  // Open folder helper
  const openFolder = useCallback(async (path?: string) => {
    let folderPath = path;
    if (!folderPath) {
      folderPath = await bridge.openFolderDialog() ?? undefined;
    }
    if (!folderPath) return;
    setProjectName(folderPath.split('/').pop() ?? folderPath);
    wsClient.send({ type: 'project.open', path: folderPath });
  }, []);

  // WebSocket handler
  useEffect(() => {
    wsClient.connect();

    const off = wsClient.onMessage((msg: ServerMessage) => {
      switch (msg.type) {

        case 'session.list':
          setSessions(msg.sessions);
          break;

        case 'session.created':
          setActiveSession(msg.session);
          setSessions(prev => {
            const exists = prev.find(s => s.id === msg.session.id);
            return exists ? prev.map(s => s.id === msg.session.id ? msg.session : s)
                          : [msg.session, ...prev];
          });
          break;

        case 'session.loaded':
          setActiveSession(msg.session);
          msg.messages.forEach(m => {
            if (m.role === 'user') addUserMessage(m.id, m.content);
            else {
              startAssistantMessage(m.id);
              appendChunk(m.id, m.content);
            }
          });
          break;

        case 'chat.chunk': {
          const sid = msg.sessionId;
          if (!streamingRef.current || streamingRef.current.sessionId !== sid) {
            const msgId = randomUUID();
            streamingRef.current = { sessionId: sid, msgId };
            startAssistantMessage(msgId);
          }
          appendChunk(streamingRef.current.msgId, msg.content);
          if (msg.done) streamingRef.current = null;
          break;
        }

        case 'chat.tool_call': {
          if (!streamingRef.current) {
            const msgId = randomUUID();
            streamingRef.current = { sessionId: msg.sessionId, msgId };
            startAssistantMessage(msgId);
          }
          addToolCall(streamingRef.current.msgId, {
            id: randomUUID(),
            name: msg.name,
            input: msg.input,
            pending: true,
          });
          break;
        }

        case 'chat.tool_result': {
          if (streamingRef.current) {
            resolveToolCall(streamingRef.current.msgId, msg.name, msg.result, msg.isError);
          }
          break;
        }

        case 'chat.error':
          streamingRef.current = null;
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

        case 'github.issues':
          setGithubIssues(msg.issues);
          setGithubIssuesLoading(false);
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
      }
    });

    // Wait for WS then init
    const interval = setInterval(async () => {
      if (!wsClient.isConnected) return;
      clearInterval(interval);
      setConnected(true);

      const initialPath = await bridge.getProjectPath().catch(() => '');
      if (initialPath && initialPath !== '.') {
        setProjectName(initialPath.split('/').pop() ?? initialPath);
        wsClient.send({ type: 'project.open', path: initialPath });
      } else {
        wsClient.send({ type: 'session.list' });
      }
    }, 120);

    return () => { off(); clearInterval(interval); wsClient.disconnect(); };
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const hasProject = !!fileTree;
  const toggleActivity = (tab: ActivityTab) => {
    if (activityTab === tab && showSidebar) { setShowSidebar(false); }
    else { setActivityTab(tab); setShowSidebar(true); }
  };

  return (
    <div className="app">
      {/* Titlebar */}
      <div className="titlebar titlebar-drag">
        <span className="titlebar-no-drag" style={{ display: 'flex', alignItems: 'center', gap: 6, color: 'var(--tx-2)', fontSize: 12 }}>
          <span style={{ fontWeight: 700, color: 'var(--accent-2)', letterSpacing: '-0.01em' }}>E</span>
          <span style={{ color: 'var(--tx-3)', fontSize: 11 }}>Engine</span>
          {projectName && (
            <>
              <span style={{ color: 'var(--tx-3)' }}>{' · '}</span>
              <span style={{ color: 'var(--tx-2)', fontWeight: 500 }}>{projectName}</span>
            </>
          )}
        </span>
        {/* File menu */}
        <div className="titlebar-no-drag" style={{ marginLeft: 8 }}>
          <FileMenu onOpenFolder={openFolder} />
        </div>
        <div className="titlebar-no-drag" style={{ marginLeft: 'auto', display: 'flex', gap: 4 }}>
          <button
            onClick={() => setShowTerminal(v => !v)}
            style={{
              background: showTerminal ? 'var(--surface-3)' : 'transparent',
              border: 'none', borderRadius: 4, padding: '3px 7px',
              cursor: 'pointer', color: showTerminal ? 'var(--tx-2)' : 'var(--tx-3)',
              fontSize: 11, fontFamily: 'inherit', transition: 'all 100ms',
            }}
          >Terminal</button>
          <button
            onClick={() => setShowSidebar(v => !v)}
            style={{
              background: 'transparent', border: 'none', borderRadius: 4,
              padding: '3px 5px', cursor: 'pointer', color: 'var(--tx-3)',
              display: 'flex', alignItems: 'center', transition: 'color 100ms',
            }}
          >
            {showSidebar ? <ChevronLeft size={13} /> : <ChevronRight size={13} />}
          </button>
        </div>
      </div>

      <div className="workspace">
        {/* Activity bar */}
        <div className="activity-bar">
          {([
            ['explorer', FolderOpen],
            ['git', GitBranch],
            ['search', Search],
            ['issues', AlertCircle],
            ['settings', Settings2],
          ] as [ActivityTab, React.ComponentType<{ size?: number }>][]).map(([id, Icon]) => (
            <button
              key={id}
              className={`activity-btn ${activityTab === id && showSidebar ? 'active' : ''}`}
              onClick={() => toggleActivity(id)}
              title={id.charAt(0).toUpperCase() + id.slice(1)}
            >
              <Icon size={17} />
            </button>
          ))}
          <div style={{ flex: 1 }} />
          <button
            className={`activity-btn ${rightTab === 'agent' ? 'active' : ''}`}
            onClick={() => setRightTab(r => r === 'agent' ? 'chat' : 'agent')}
            title="Agent Monitor"
          >
            <Activity size={17} />
          </button>
        </div>

        {/* Sidebar */}
        {showSidebar && (
          <div className="sidebar animate-slide">
            <FileTree
              activityTab={activityTab}
              onOpenFolder={() => openFolder()}
            />
          </div>
        )}

        {/* Main content */}
        <div style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
          {hasProject ? (
            <div className="editor-area">
              <Editor />
            </div>
          ) : (
            <WelcomeScreen
              sessions={sessions}
              onOpenFolder={openFolder}
            />
          )}

          {/* Terminal panel */}
          {showTerminal && hasProject && (
            <div className="bottom-panel" style={{ height: terminalHeight }}>
              <Terminal />
            </div>
          )}
        </div>

        {/* Right panel */}
        <div className="right-panel">
          <div className="panel-tab-bar">
            <button className={`panel-tab ${rightTab === 'chat' ? 'active' : ''}`} onClick={() => setRightTab('chat')}>
              Chat
            </button>
            <button className={`panel-tab ${rightTab === 'agent' ? 'active' : ''}`} onClick={() => setRightTab('agent')}>
              <Activity size={12} />
              Agent
            </button>
          </div>
          {rightTab === 'chat'  ? <AIChat /> : <AgentPanel />}
        </div>
      </div>

      <StatusBar />
    </div>
  );
}

// File menu dropdown
function FileMenu({ onOpenFolder }: { onOpenFolder: () => void }) {
  const [open, setOpen] = useState(false);
  const [serviceMsg, setServiceMsg] = useState('');

  const handleInstall = async () => {
    setOpen(false);
    const msg = await bridge.installAgentService();
    setServiceMsg(msg);
    setTimeout(() => setServiceMsg(''), 4000);
  };

  const handleUninstall = async () => {
    setOpen(false);
    const msg = await bridge.uninstallAgentService();
    setServiceMsg(msg);
    setTimeout(() => setServiceMsg(''), 4000);
  };

  return (
    <div style={{ position: 'relative' }}>
      <button
        onClick={() => setOpen(v => !v)}
        style={{
          background: open ? 'var(--surface-3)' : 'transparent',
          border: 'none', borderRadius: 4, padding: '3px 6px',
          cursor: 'pointer', color: 'var(--tx-3)',
          fontSize: 11, fontFamily: 'inherit',
          display: 'flex', alignItems: 'center', gap: 2,
          transition: 'all 100ms',
        }}
      >
        File <ChevronDown size={10} />
      </button>

      {open && (
        <>
          <div
            style={{ position: 'fixed', inset: 0, zIndex: 99 }}
            onClick={() => setOpen(false)}
          />
          <div style={{
            position: 'absolute', top: '100%', left: 0, marginTop: 2,
            background: 'var(--surface)', border: '1px solid var(--border-2)',
            borderRadius: 'var(--radius)', boxShadow: '0 8px 24px rgba(0,0,0,0.4)',
            minWidth: 200, zIndex: 100, overflow: 'hidden',
          }}>
            <MenuItem icon={<FolderOpen size={13} />} label="Open Folder…" onClick={() => { setOpen(false); onOpenFolder(); }} />
            <div style={{ height: 1, background: 'var(--border)', margin: '2px 0' }} />
            <MenuItem icon={<ServerCog size={13} />} label="Install Agent Service" onClick={handleInstall} />
            <MenuItem icon={<ServerCog size={13} />} label="Remove Agent Service" onClick={handleUninstall} />
          </div>
        </>
      )}

      {serviceMsg && (
        <div style={{
          position: 'fixed', bottom: 32, left: '50%', transform: 'translateX(-50%)',
          background: 'var(--surface)', border: '1px solid var(--border-2)',
          borderRadius: 'var(--radius)', padding: '8px 16px', fontSize: 12,
          color: 'var(--tx-2)', zIndex: 200, boxShadow: '0 4px 16px rgba(0,0,0,0.3)',
          maxWidth: 360, textAlign: 'center',
        }}>
          {serviceMsg}
        </div>
      )}
    </div>
  );
}

function MenuItem({ icon, label, onClick }: { icon: React.ReactNode; label: string; onClick: () => void }) {
  return (
    <div
      onClick={onClick}
      style={{
        display: 'flex', alignItems: 'center', gap: 8,
        padding: '7px 12px', cursor: 'pointer', fontSize: 12,
        color: 'var(--tx-2)', transition: 'background 80ms',
      }}
      onMouseEnter={e => (e.currentTarget.style.background = 'var(--surface-2)')}
      onMouseLeave={e => (e.currentTarget.style.background = '')}
    >
      <span style={{ color: 'var(--tx-3)', display: 'flex' }}>{icon}</span>
      {label}
    </div>
  );
}

// Welcome screen
function WelcomeScreen({
  sessions,
  onOpenFolder,
}: {
  sessions: { id: string; projectPath: string; updatedAt: string }[];
  onOpenFolder: (path?: string) => void;
}) {
  const recent = sessions
    .filter(s => s.projectPath && s.projectPath !== '.')
    .reduce<{ path: string; updatedAt: string }[]>((acc, s) => {
      if (!acc.find(a => a.path === s.projectPath)) {
        acc.push({ path: s.projectPath, updatedAt: s.updatedAt });
      }
      return acc;
    }, [])
    .slice(0, 5);

  return (
    <div className="welcome">
      <div className="welcome-content animate-appear">
        <div className="welcome-logo">E</div>
        <div className="welcome-title">Engine</div>
        <div className="welcome-subtitle">AI-native code editor</div>

        <div className="welcome-actions">
          <button className="btn-primary" onClick={() => onOpenFolder()}>
            <FolderOpen size={15} />
            Open Folder
          </button>
        </div>

        {recent.length > 0 && (
          <div className="welcome-recent">
            <div className="welcome-recent-title">Recent</div>
            {recent.map(r => {
              const name = r.path.split('/').pop() ?? r.path;
              return (
                <div key={r.path} className="welcome-recent-item" onClick={() => onOpenFolder(r.path)}>
                  <FolderOpen size={13} style={{ flexShrink: 0, color: 'var(--accent-2)' }} />
                  <div style={{ flex: 1, overflow: 'hidden' }}>
                    <div className="welcome-recent-name">{name}</div>
                    <div className="welcome-recent-path">{r.path}</div>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
