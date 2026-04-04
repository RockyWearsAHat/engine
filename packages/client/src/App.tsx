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
  Minus, Square, X,
} from 'lucide-react';

type ActivityTab = 'explorer' | 'git' | 'issues' | 'search' | 'settings';
type RightTab = 'chat' | 'agent';
type NoticeTone = 'info' | 'error';
type WindowAction = 'minimize' | 'toggle-maximize' | 'close';

function normalizeProjectPath(path: string): string {
  if (!path) {
    return '';
  }
  if (!path.startsWith('file://')) {
    return path;
  }

  try {
    let normalized = decodeURIComponent(new URL(path).pathname);
    if (/^\/[A-Za-z]:/.test(normalized)) {
      normalized = normalized.slice(1);
    }
    return normalized || path;
  } catch {
    return path;
  }
}

function projectLabel(path: string): string {
  return path.split(/[\\/]/).pop() ?? path;
}

function isDesktopShell(): boolean {
  return typeof window !== 'undefined' && ('__TAURI__' in window || !!window.electronAPI?.isElectron);
}

function isMacPlatform(): boolean {
  return typeof navigator !== 'undefined' && /(Mac|iPhone|iPad|iPod)/i.test(navigator.userAgent);
}

export default function App() {
  const {
    connected, setConnected,
    sessions, setSessions,
    activeSession, setActiveSession,
    chatMessages, addUserMessage, startAssistantMessage,
    appendChunk, finalizeMessage, addToolCall, resolveToolCall, setMessages,
    fileTree, setFileTree,
    openFiles, openFile, closeFile, setActiveFile, markFileSaved,
    gitStatus, setGitStatus,
    setGithubToken, setGithubUser,
    githubIssues, setGithubIssues, setGithubIssuesLoading, setGithubIssuesError,
    agentSessions, updateAgentSession, addLiveToolCall, resolveLiveToolCall,
    setSearchResults,
  } = useStore();

  const [activityTab, setActivityTab] = useState<ActivityTab>('explorer');
  const [showSidebar, setShowSidebar] = useState(true);
  const [rightTab, setRightTab] = useState<RightTab>('chat');
  const [showTerminal, setShowTerminal] = useState(false);
  const [projectName, setProjectName] = useState('');
  const [terminalHeight, setTerminalHeight] = useState(220);
  const [appNotice, setAppNotice] = useState<{ message: string; tone: NoticeTone } | null>(null);

  const streamingRef = useRef<{ sessionId: string; msgId: string } | null>(null);
  const pendingToolCallsRef = useRef<Record<string, Array<{
    id: string;
    msgId: string;
    name: string;
    startedAt: number;
  }>>>({});
  const noticeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const desktopShell = isDesktopShell();
  const macPlatform = isMacPlatform();

  const showNotice = useCallback((message: string, tone: NoticeTone = 'info') => {
    if (noticeTimerRef.current) {
      clearTimeout(noticeTimerRef.current);
    }
    setAppNotice({ message, tone });
    noticeTimerRef.current = setTimeout(() => {
      setAppNotice(null);
      noticeTimerRef.current = null;
    }, 4200);
  }, []);

  useEffect(() => () => {
    if (noticeTimerRef.current) {
      clearTimeout(noticeTimerRef.current);
    }
  }, []);

  const handleWindowAction = useCallback(async (action: WindowAction) => {
    try {
      switch (action) {
        case 'minimize':
          await bridge.minimizeWindow();
          break;
        case 'toggle-maximize':
          await bridge.toggleMaximizeWindow();
          break;
        case 'close':
          await bridge.closeWindow();
          break;
      }
    } catch {
      showNotice('Window controls are unavailable right now.', 'error');
    }
  }, [showNotice]);

  const startWindowDrag = useCallback(() => {
    if (!desktopShell) {
      return;
    }
    void bridge.startWindowDrag().catch(() => {
      showNotice('Window dragging is unavailable right now.', 'error');
    });
  }, [desktopShell, showNotice]);

  const syncRuntimeConfig = useCallback(async () => {
    const [savedGithubToken, githubOwner, githubRepo, anthropicKey, openaiKey, model] = await Promise.all([
      bridge.getGithubToken().catch(() => null),
      bridge.getGithubRepoOwner().catch(() => null),
      bridge.getGithubRepoName().catch(() => null),
      bridge.getAnthropicKey().catch(() => null),
      bridge.getOpenAiKey().catch(() => null),
      bridge.getModel().catch(() => null),
    ]);

    setGithubToken(savedGithubToken);
    wsClient.send({
      type: 'config.sync',
      config: {
        githubToken: savedGithubToken,
        githubOwner,
        githubRepo,
        anthropicKey,
        openaiKey,
        model,
      },
    });

    if (savedGithubToken) {
      wsClient.send({ type: 'github.user' });
    } else {
      setGithubUser(null);
    }
  }, [setGithubToken, setGithubUser]);

  // Open folder helper
  const openFolder = useCallback(async (path?: string) => {
    let folderPath = path ? normalizeProjectPath(path) : undefined;
    try {
      if (!folderPath) {
        const pickedPath = await bridge.openFolderDialog();
        folderPath = pickedPath ? normalizeProjectPath(pickedPath) : undefined;
      }
    } catch {
      showNotice('The desktop folder picker could not be opened.', 'error');
      return;
    }
    if (!folderPath) return;
    setProjectName(projectLabel(folderPath));
    setShowSidebar(true);
    setActivityTab('explorer');
    void bridge.setLastProjectPath(folderPath);
    wsClient.send({ type: 'project.open', path: folderPath });
  }, [showNotice]);

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
          setMessages([]);
          pendingToolCallsRef.current[msg.session.id] = [];
          setSessions(prev => {
            const exists = prev.find(s => s.id === msg.session.id);
            return exists ? prev.map(s => s.id === msg.session.id ? msg.session : s)
                          : [msg.session, ...prev];
          });
          break;

        case 'session.loaded':
          setActiveSession(msg.session);
          setMessages(msg.messages);
          pendingToolCallsRef.current[msg.session.id] = [];
          break;

        case 'chat.chunk': {
          const sid = msg.sessionId;
          if (!streamingRef.current || streamingRef.current.sessionId !== sid) {
            const msgId = randomUUID();
            streamingRef.current = { sessionId: sid, msgId };
            startAssistantMessage(msgId);
          }
          appendChunk(streamingRef.current.msgId, msg.content);
          updateAgentSession(sid, {
            isStreaming: !msg.done,
            currentActivity: msg.done ? '' : 'Responding...',
          });
          if (msg.done) {
            finalizeMessage(streamingRef.current.msgId);
            streamingRef.current = null;
          }
          break;
        }

        case 'chat.tool_call': {
          if (!streamingRef.current || streamingRef.current.sessionId !== msg.sessionId) {
            const msgId = randomUUID();
            streamingRef.current = { sessionId: msg.sessionId, msgId };
            startAssistantMessage(msgId);
          }
          const toolId = randomUUID();
          const startedAt = Date.now();
          addToolCall(streamingRef.current.msgId, {
            id: toolId,
            name: msg.name,
            input: msg.input,
            pending: true,
          });
          const queue = pendingToolCallsRef.current[msg.sessionId] ?? [];
          queue.push({ id: toolId, msgId: streamingRef.current.msgId, name: msg.name, startedAt });
          pendingToolCallsRef.current[msg.sessionId] = queue;
          addLiveToolCall(msg.sessionId, {
            id: toolId,
            name: msg.name,
            input: msg.input,
            pending: true,
            startedAt,
          });
          updateAgentSession(msg.sessionId, {
            isStreaming: true,
            currentActivity: msg.name,
          });
          break;
        }

        case 'chat.tool_result': {
          const queue = pendingToolCallsRef.current[msg.sessionId] ?? [];
          const nextToolIndex = queue.findIndex(toolCall => toolCall.name === msg.name);
          if (nextToolIndex !== -1) {
            const [toolCall] = queue.splice(nextToolIndex, 1);
            const durationMs = Date.now() - toolCall.startedAt;
            resolveToolCall(toolCall.msgId, toolCall.id, msg.result, msg.isError, durationMs);
            resolveLiveToolCall(msg.sessionId, toolCall.id, msg.result, msg.isError, durationMs);
            pendingToolCallsRef.current[msg.sessionId] = queue;
          }
          updateAgentSession(msg.sessionId, {
            isStreaming: true,
            currentActivity: 'Responding...',
          });
          break;
        }

        case 'chat.error':
          if (streamingRef.current?.sessionId === msg.sessionId) {
            finalizeMessage(streamingRef.current.msgId);
            streamingRef.current = null;
          }
          pendingToolCallsRef.current[msg.sessionId] = [];
          updateAgentSession(msg.sessionId, {
            isStreaming: false,
            currentActivity: '',
          });
          break;

        case 'file.content':
          openFile(msg.path, msg.content, msg.language);
          break;

        case 'file.saved':
          markFileSaved(msg.path);
          break;

        case 'file.tree':
          setFileTree(msg.tree);
          break;

        case 'search.results':
          setSearchResults(msg.query, msg.results, msg.error ?? null);
          break;

        case 'git.status':
          setGitStatus(msg.status);
          break;

        case 'github.issues':
          setGithubIssues(msg.issues);
          setGithubIssuesError(msg.error ?? null);
          setGithubIssuesLoading(false);
          break;

        case 'github.user':
          setGithubUser(msg.user);
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
      await syncRuntimeConfig();

      const initialPath = normalizeProjectPath(await bridge.getProjectPath().catch(() => ''));
      if (initialPath && initialPath !== '.') {
        setProjectName(projectLabel(initialPath));
        wsClient.send({ type: 'project.open', path: initialPath });
      } else {
        wsClient.send({ type: 'session.list' });
      }
    }, 120);

    return () => { off(); clearInterval(interval); wsClient.disconnect(); };
  }, [setConnected, syncRuntimeConfig, setGithubIssues, setGithubIssuesError, setGithubIssuesLoading, setGitStatus, setFileTree, setSearchResults, setGithubUser, closeFile, openFile, markFileSaved, setActiveFile, finalizeMessage, appendChunk, startAssistantMessage, updateAgentSession, addToolCall, addLiveToolCall, resolveToolCall, resolveLiveToolCall, setActiveSession, setMessages, setSessions]); // eslint-disable-line react-hooks/exhaustive-deps

  const hasProject = !!fileTree;
  const toggleActivity = (tab: ActivityTab) => {
    if (activityTab === tab && showSidebar) { setShowSidebar(false); }
    else { setActivityTab(tab); setShowSidebar(true); }
  };

    return (
      <div className="app">
        {/* Titlebar */}
        <div className="titlebar">
          <div className="titlebar-leading">
            {desktopShell && macPlatform && (
              <WindowControls
                macStyle
                onAction={handleWindowAction}
              />
            )}
            <div className="titlebar-brand">
              <span className="titlebar-brand-mark">E</span>
              <div className="titlebar-brand-copy">
                <span className="titlebar-brand-name">Engine</span>
                <span className="titlebar-brand-subtitle">Desktop shell</span>
              </div>
            </div>
          </div>

          <div
            className="titlebar-drag-zone"
            onMouseDown={(event) => {
              if (event.button !== 0) return;
              startWindowDrag();
            }}
            onDoubleClick={() => {
              if (!desktopShell) return;
              void handleWindowAction('toggle-maximize');
            }}
          >
            <div className="titlebar-chip">
              <span className={`titlebar-chip-dot ${connected ? 'online' : 'offline'}`} />
              <span className="titlebar-chip-name">{projectName || 'No workspace open'}</span>
              {gitStatus?.branch && (
                <span className="titlebar-chip-branch">{gitStatus.branch}</span>
              )}
            </div>
          </div>

          <div className="titlebar-actions">
            <button className="shell-action primary" onClick={() => openFolder()}>
              <FolderOpen size={14} />
              Open Folder
            </button>
            <FileMenu onOpenFolder={() => openFolder()} />
            <button
              className={`shell-action ${showTerminal ? 'active' : ''}`}
              onClick={() => setShowTerminal(v => !v)}
            >
              Terminal
            </button>
            <button
              className="shell-icon-btn"
              onClick={() => setShowSidebar(v => !v)}
              title={showSidebar ? 'Hide sidebar' : 'Show sidebar'}
            >
              {showSidebar ? <ChevronLeft size={14} /> : <ChevronRight size={14} />}
            </button>
            {desktopShell && !macPlatform && (
              <WindowControls onAction={handleWindowAction} />
            )}
          </div>
        </div>

        {appNotice && (
          <ShellNotice message={appNotice.message} tone={appNotice.tone} />
        )}

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

function WindowControls({
  macStyle = false,
  onAction,
}: {
  macStyle?: boolean;
  onAction: (action: WindowAction) => void;
}) {
  if (macStyle) {
    return (
      <div className="window-controls mac">
        <button className="traffic-light close" aria-label="Close window" title="Close" onClick={() => onAction('close')} />
        <button className="traffic-light minimize" aria-label="Minimize window" title="Minimize" onClick={() => onAction('minimize')} />
        <button className="traffic-light maximize" aria-label="Zoom window" title="Zoom" onClick={() => onAction('toggle-maximize')} />
      </div>
    );
  }

  return (
    <div className="window-controls inline">
      <button className="window-control-btn" aria-label="Minimize window" title="Minimize" onClick={() => onAction('minimize')}>
        <Minus size={12} />
      </button>
      <button className="window-control-btn" aria-label="Maximize window" title="Maximize" onClick={() => onAction('toggle-maximize')}>
        <Square size={11} />
      </button>
      <button className="window-control-btn danger" aria-label="Close window" title="Close" onClick={() => onAction('close')}>
        <X size={12} />
      </button>
    </div>
  );
}

function ShellNotice({ message, tone }: { message: string; tone: NoticeTone }) {
  return (
    <div className={`shell-notice ${tone}`}>
      {message}
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
    <div className="shell-menu-root">
      <button
        onClick={() => setOpen(v => !v)}
        className={`shell-action ${open ? 'active' : ''}`}
      >
        File <ChevronDown size={10} />
      </button>

      {open && (
        <>
          <div
            style={{ position: 'fixed', inset: 0, zIndex: 99 }}
            onClick={() => setOpen(false)}
          />
          <div className="shell-menu">
            <MenuItem icon={<FolderOpen size={13} />} label="Open Folder…" onClick={() => { setOpen(false); onOpenFolder(); }} />
            <div className="shell-menu-divider" />
            <MenuItem icon={<ServerCog size={13} />} label="Install Agent Service" onClick={handleInstall} />
            <MenuItem icon={<ServerCog size={13} />} label="Remove Agent Service" onClick={handleUninstall} />
          </div>
        </>
      )}

      {serviceMsg && (
        <div className="shell-menu-toast">
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
      className="shell-menu-item"
    >
      <span className="shell-menu-item-icon">{icon}</span>
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
      <div className="welcome-grid animate-appear">
        <div className="welcome-hero">
          <div className="welcome-kicker">Engine shell</div>
          <div className="welcome-logo">E</div>
          <div className="welcome-title">Open a workspace and keep the editor, shell, and AI in one coherent flow.</div>
          <div className="welcome-subtitle">
            Persistent sessions, live tool feedback, and a desktop shell that behaves like a real app instead of a prototype.
          </div>

          <div className="welcome-actions">
            <button className="btn-primary" onClick={() => onOpenFolder()}>
              <FolderOpen size={15} />
              Open Folder
            </button>
            {recent[0] && (
              <button className="btn-secondary" onClick={() => onOpenFolder(recent[0].path)}>
                <ChevronRight size={15} />
                Reopen {projectLabel(recent[0].path)}
              </button>
            )}
          </div>

          <div className="welcome-pill-row">
            <span className="welcome-pill">Native desktop shell</span>
            <span className="welcome-pill">Persistent project context</span>
            <span className="welcome-pill">Live terminal + AI tooling</span>
          </div>
        </div>

        <div className="welcome-recent-card">
          <div className="welcome-recent-title">Recent workspaces</div>
          {recent.length > 0 ? recent.map(r => (
            <div key={r.path} className="welcome-recent-item" onClick={() => onOpenFolder(r.path)}>
              <FolderOpen size={13} style={{ flexShrink: 0, color: 'var(--accent-2)' }} />
              <div style={{ flex: 1, overflow: 'hidden' }}>
                <div className="welcome-recent-name">{projectLabel(r.path)}</div>
                <div className="welcome-recent-path">{r.path}</div>
              </div>
            </div>
          )) : (
            <div className="welcome-empty-state">
              Your recent workspaces will appear here after you open one.
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
