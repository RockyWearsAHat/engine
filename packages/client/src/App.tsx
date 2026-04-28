import { useEffect, useRef, useState, useCallback, useMemo, type MouseEvent as ReactMouseEvent } from 'react';
import { useStore } from './store/index.js';
import { wsClient } from './ws/client.js';
import { bridge } from './bridge.js';
import type { ApprovalRequest, FileNode, ServerMessage, WorkspaceTask } from '@engine/shared';
import { randomUUID } from './utils.js';
import FileTree from './components/FileTree/FileTree.js';
import Editor from './components/Editor/Editor.js';
import Terminal from './components/Terminal/Terminal.js';
import AIChat from './components/AI/AIChat.js';
import AgentPanel from './components/AgentPanel/AgentPanel.js';
import StatusBar from './components/StatusBar/StatusBar.js';
import PreferencesPanel from './components/Preferences/PreferencesPanel.js';
import MachineConnectionsPanel from './components/Connections/MachineConnectionsPanel.js';
import UsageDashboard from './components/Usage/UsageDashboard.js';
import CommandPalette, {
  type CommandPaletteItem,
  type CommandPaletteMode,
} from './components/CommandPalette/CommandPalette.js';
import {
  PERFORM_CLOSE_FILE_EVENT,
  REQUEST_CLOSE_FILE_EVENT,
  SAVE_FILES_EVENT,
  type CloseFileEventDetail,
  type SaveFilesEventDetail,
} from './editorEvents.js';
import {
  FolderOpen, GitBranch, AlertCircle, Settings2, Activity,
  Search, ServerCog, ChartColumn,
  Minus, Square, X, FileText, Hammer, Play, Terminal as TerminalIcon, Menu, FileStack, RotateCcw,
} from 'lucide-react';

type ActivityTab = 'explorer' | 'open-editors' | 'git' | 'search' | 'issues' | 'usage';
type RightTab = 'chat' | 'agent';
type NoticeTone = 'info' | 'error';
type WindowAction = 'minimize' | 'toggle-maximize' | 'toggle-fullscreen' | 'close';
type PanelResizeTarget = 'sidebar' | 'right-panel' | 'terminal';
type WorkspaceOpenTarget = {
  workspacePath: string;
  initialFilePath?: string;
  label: string;
};
type PendingUnsavedAction =
  | { kind: 'file-close'; path: string }
  | { kind: 'window-close' };
type TaskLaunchRequest = {
  id: string;
  command: string;
  cwd: string;
  label: string;
};

const DEFAULT_SIDEBAR_WIDTH = 240;
const DEFAULT_RIGHT_PANEL_WIDTH = 300;
const DEFAULT_TERMINAL_HEIGHT = 220;
const ACTIVITY_BAR_WIDTH = 42;

const SIDEBAR_MIN_WIDTH = 200;
const RIGHT_PANEL_MIN_WIDTH = 260;
const TERMINAL_MIN_HEIGHT = 160;

const clamp = (value: number, min: number, max: number) => Math.min(max, Math.max(min, value));

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

function relativeWorkspacePath(root: string, path: string): string {
  if (!root) {
    return path;
  }
  const normalizedRoot = root.endsWith('/') ? root : `${root}/`;
  return path.startsWith(normalizedRoot) ? path.slice(normalizedRoot.length) : path;
}

function collectWorkspaceFiles(node: FileNode | null, files: FileNode[] = []): FileNode[] {
  if (!node) {
    return files;
  }
  if (node.type === 'file') {
    files.push(node);
    return files;
  }
  node.children?.forEach((child) => collectWorkspaceFiles(child, files));
  return files;
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
    appendChunk, finalizeMessage, markMessageFailed, addToolCall, resolveToolCall, setMessages,
    fileTree, setFileTree, mergeFileTree,
    openFiles, activeFilePath, openFile, clearOpenFiles, setActiveFile, markFileSaved,
    gitStatus, setGitStatus,
    setGithubToken, setGithubUser, setGithubAuthFlow,
    githubIssues, setGithubIssues, setGithubIssuesLoading, setGithubIssuesError,
    agentSessions, updateAgentSession, addLiveToolCall, resolveLiveToolCall,
    setSearchResults, setEditorPreferences,
  } = useStore();

  const [activityTab, setActivityTab] = useState<ActivityTab>('explorer');
  const [showSidebar, setShowSidebar] = useState(true);
  const [rightTab, setRightTab] = useState<RightTab>('chat');
  const [showTerminal, setShowTerminal] = useState(false);
  const [sidebarWidth, setSidebarWidth] = useState(DEFAULT_SIDEBAR_WIDTH);
  const [rightPanelWidth, setRightPanelWidth] = useState(DEFAULT_RIGHT_PANEL_WIDTH);
  const [projectName, setProjectName] = useState('');
  const [terminalHeight, setTerminalHeight] = useState(DEFAULT_TERMINAL_HEIGHT);
  const [appNotice, setAppNotice] = useState<{ message: string; tone: NoticeTone } | null>(null);
  const [showPreferences, setShowPreferences] = useState(false);
  const [dropTargetActive, setDropTargetActive] = useState(false);
  const [pendingWorkspaceSwitch, setPendingWorkspaceSwitch] = useState<WorkspaceOpenTarget | null>(null);
  const [resolvingWorkspaceSwitch, setResolvingWorkspaceSwitch] = useState(false);
  const [pendingUnsavedAction, setPendingUnsavedAction] = useState<PendingUnsavedAction | null>(null);
  const [resolvingUnsavedAction, setResolvingUnsavedAction] = useState(false);
  const [workspaceTasks, setWorkspaceTasks] = useState<WorkspaceTask[]>([]);
  const [defaultBuildTaskId, setDefaultBuildTaskId] = useState<string | null>(null);
  const [defaultRunTaskId, setDefaultRunTaskId] = useState<string | null>(null);
  const [taskRequest, setTaskRequest] = useState<TaskLaunchRequest | null>(null);
  const [showCommandPalette, setShowCommandPalette] = useState(false);
  const [commandPaletteMode, setCommandPaletteMode] = useState<CommandPaletteMode>('commands');
  const [pendingApproval, setPendingApproval] = useState<ApprovalRequest | null>(null);
  const [resizingPanel, setResizingPanel] = useState<PanelResizeTarget | null>(null);

  const streamingRef = useRef<{ sessionId: string; msgId: string } | null>(null);
  const pendingToolCallsRef = useRef<Record<string, Array<{
    id: string;
    msgId: string;
    name: string;
    startedAt: number;
  }>>>({});
  const noticeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const pendingSaveRequestRef = useRef<{ remaining: Set<string>; resolve: () => void } | null>(null);
  const pendingSaveRequestTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const editorAreaRef = useRef<HTMLDivElement | null>(null);
  const dirtyOpenFilesRef = useRef(openFiles.filter(file => file.dirty));
  const allowWindowCloseRef = useRef(false);
  const syncRuntimeConfigRef = useRef<() => Promise<void>>(async () => {});
  const requestWorkspaceTasksRef = useRef<(path?: string) => void>(() => {});
  const requestFileCloseRef = useRef<(path: string) => void>(() => {});
  const ensureStreamingAssistantMessageRef = useRef<(sessionId: string) => string>(() => '');
  const workspaceRootRef = useRef('');
  const resizeOriginRef = useRef({
    sidebarWidth: DEFAULT_SIDEBAR_WIDTH,
    rightPanelWidth: DEFAULT_RIGHT_PANEL_WIDTH,
    terminalHeight: DEFAULT_TERMINAL_HEIGHT,
    editorBottom: 0,
  });
  const desktopShell = isDesktopShell();
  const macPlatform = isMacPlatform();
  const dirtyOpenFiles = openFiles.filter(file => file.dirty);
  const buildTask = workspaceTasks.find(task => task.id === defaultBuildTaskId)
    ?? workspaceTasks.find(task => task.kind === 'build')
    ?? null;
  const runTask = workspaceTasks.find(task => task.id === defaultRunTaskId)
    ?? workspaceTasks.find(task => task.kind === 'run')
    ?? buildTask
    ?? null;
  const workspaceRoot = normalizeProjectPath(activeSession?.projectPath ?? '');
  const missionProjectName = projectName || projectLabel(workspaceRoot);

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

  const ensureStreamingAssistantMessage = useCallback((sessionId: string) => {
    const nextStreamingMessage = streamingRef.current;
    if (
      nextStreamingMessage
      && nextStreamingMessage.sessionId === sessionId
      && useStore.getState().chatMessages.some((message) => message.id === nextStreamingMessage.msgId)
    ) {
      return nextStreamingMessage.msgId;
    }

    const msgId = randomUUID();
    streamingRef.current = { sessionId, msgId };
    startAssistantMessage(msgId);
    return msgId;
  }, [startAssistantMessage]);

  useEffect(() => {
    dirtyOpenFilesRef.current = dirtyOpenFiles;
  }, [dirtyOpenFiles]);

  useEffect(() => {
    workspaceRootRef.current = workspaceRoot;
  }, [workspaceRoot]);

  useEffect(() => () => {
    if (noticeTimerRef.current) {
      clearTimeout(noticeTimerRef.current);
    }
    if (pendingSaveRequestTimerRef.current) {
      clearTimeout(pendingSaveRequestTimerRef.current);
    }
  }, []);

  // Prevent context menu everywhere except FileTree (sidebar-body and tree-context-menu)
  useEffect(() => {
    const handleContextMenu = (event: MouseEvent) => {
      const target = event.target as HTMLElement | null;
      // Allow context menu only on FileTree sidebar-body and tree nodes
      if (target?.closest('.sidebar-body, .tree-node, .tree-context-menu')) {
        return;
      }
      event.preventDefault();
    };

    document.addEventListener('contextmenu', handleContextMenu, true);
    return () => document.removeEventListener('contextmenu', handleContextMenu, true);
  }, []);

  const finishPendingSaveRequest = useCallback(() => {
    if (pendingSaveRequestTimerRef.current) {
      clearTimeout(pendingSaveRequestTimerRef.current);
      pendingSaveRequestTimerRef.current = null;
    }
    const pendingSaveRequest = pendingSaveRequestRef.current;
    if (!pendingSaveRequest) {
      return;
    }
    pendingSaveRequestRef.current = null;
    pendingSaveRequest.resolve();
  }, []);

  const beginWindowDrag = useCallback((event: ReactMouseEvent<HTMLElement>) => {
    if (!desktopShell || event.button !== 0) {
      return;
    }

    const target = event.target instanceof HTMLElement ? event.target : null;
    if (target?.closest('button, input, textarea, select, a, [role="button"], [data-no-window-drag]')) {
      return;
    }

    void bridge.startWindowDrag().catch(() => {
      showNotice('Window dragging is unavailable right now.', 'error');
    });
  }, [desktopShell, showNotice]);

  const beginPanelResize = useCallback((target: PanelResizeTarget) => (event: React.PointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) {
      return;
    }

    event.preventDefault();
    event.currentTarget.setPointerCapture(event.pointerId);
    resizeOriginRef.current = {
      sidebarWidth,
      rightPanelWidth,
      terminalHeight,
      editorBottom: editorAreaRef.current?.getBoundingClientRect().bottom ?? window.innerHeight,
    };
    setResizingPanel(target);
  }, [rightPanelWidth, sidebarWidth, terminalHeight]);

  useEffect(() => {
    if (!resizingPanel) {
      return;
    }

    const onPointerMove = (event: PointerEvent) => {
      if (resizingPanel === 'sidebar') {
        const nextWidth = clamp(event.clientX - ACTIVITY_BAR_WIDTH, SIDEBAR_MIN_WIDTH, Math.max(SIDEBAR_MIN_WIDTH, window.innerWidth * 0.4));
        setSidebarWidth(nextWidth);
        return;
      }

      if (resizingPanel === 'right-panel') {
        const nextWidth = clamp(window.innerWidth - event.clientX, RIGHT_PANEL_MIN_WIDTH, Math.max(RIGHT_PANEL_MIN_WIDTH, window.innerWidth * 0.42));
        setRightPanelWidth(nextWidth);
        return;
      }

      const editorBottom = resizeOriginRef.current.editorBottom || window.innerHeight;
      const nextHeight = clamp(editorBottom - event.clientY, TERMINAL_MIN_HEIGHT, Math.max(TERMINAL_MIN_HEIGHT, window.innerHeight * 0.6));
      setTerminalHeight(nextHeight);
    };

    const onPointerUp = () => {
      setResizingPanel(null);
    };

    document.body.classList.add('panel-resizing');
    document.body.style.cursor = resizingPanel === 'terminal' ? 'row-resize' : 'col-resize';
    document.body.style.userSelect = 'none';
    window.addEventListener('pointermove', onPointerMove);
    window.addEventListener('pointerup', onPointerUp);

    return () => {
      document.body.classList.remove('panel-resizing');
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
      window.removeEventListener('pointermove', onPointerMove);
      window.removeEventListener('pointerup', onPointerUp);
    };
  }, [resizingPanel]);

  const syncRuntimeConfig = useCallback(async () => {
    const [savedGithubToken, githubOwner, githubRepo, anthropicKey, openaiKey, savedModelProvider, ollamaBaseUrl, model, editorPreferences, clonesDir] = await Promise.all([
      bridge.getGithubToken().catch(() => null),
      bridge.getGithubRepoOwner().catch(() => null),
      bridge.getGithubRepoName().catch(() => null),
      bridge.getAnthropicKey().catch(() => null),
      bridge.getOpenAiKey().catch(() => null),
      bridge.getModelProvider().catch(() => null),
      bridge.getOllamaBaseUrl().catch(() => null),
      bridge.getModel().catch(() => null),
      bridge.getEditorPreferences().catch(() => null),
      bridge.getClonesDir().catch(() => null),
    ]);
    const modelProvider = savedModelProvider || 'ollama';

    setGithubToken(savedGithubToken);
    if (editorPreferences) {
      setEditorPreferences(editorPreferences);
    }
    wsClient.send({
      type: 'config.sync',
      config: {
        githubToken: savedGithubToken,
        githubOwner,
        githubRepo,
        anthropicKey,
        openaiKey,
        modelProvider,
        ollamaBaseUrl,
        model,
        clonesDir,
      },
    });

    if (savedGithubToken) {
      wsClient.send({ type: 'github.user' });
    } else {
      setGithubUser(null);
    }
  }, [setEditorPreferences, setGithubToken, setGithubUser]);

  const completeWorkspaceOpen = useCallback((target: WorkspaceOpenTarget) => {
    const workspacePath = normalizeProjectPath(target.workspacePath);
    const initialFilePath = target.initialFilePath
      ? normalizeProjectPath(target.initialFilePath)
      : undefined;

    clearOpenFiles();
    setFileTree(null);
    setGitStatus(null);
    setWorkspaceTasks([]);
    setDefaultBuildTaskId(null);
    setDefaultRunTaskId(null);
    setPendingWorkspaceSwitch(null);
    setProjectName(projectLabel(workspacePath));
    setShowSidebar(true);
    setActivityTab('explorer');
    void bridge.setLastProjectPath(workspacePath);
    wsClient.send({ type: 'project.open', path: workspacePath });
    if (initialFilePath) {
      window.setTimeout(() => {
        wsClient.send({ type: 'file.read', path: initialFilePath });
      }, 48);
    }
  }, [clearOpenFiles, setFileTree, setGitStatus]);

  const requestWorkspaceTasks = useCallback((path?: string) => {
    wsClient.send({ type: 'workspace.tasks', path: path ? normalizeProjectPath(path) : undefined });
  }, []);

  const launchWorkspaceTask = useCallback((task: WorkspaceTask | null) => {
    if (!task) {
      showNotice('No workspace task is available for this project yet.', 'error');
      return;
    }
    const cwd = normalizeProjectPath(activeSession?.projectPath ?? '');
    if (!cwd) {
      showNotice('Open a workspace before running project tasks.', 'error');
      return;
    }

    setShowTerminal(true);
    setTaskRequest({
      id: randomUUID(),
      command: task.command,
      cwd,
      label: task.label,
    });
    showNotice(`Running ${task.label.toLowerCase()} in the terminal.`);
  }, [activeSession?.projectPath, showNotice]);

  const requestFileSaves = useCallback(async (paths: string[]) => {
    const targetPaths = Array.from(new Set(paths.filter(Boolean)));
    if (targetPaths.length === 0) {
      return;
    }

    await new Promise<void>((resolve) => {
      finishPendingSaveRequest();
      pendingSaveRequestRef.current = {
        remaining: new Set(targetPaths),
        resolve,
      };
      if (pendingSaveRequestTimerRef.current) {
        clearTimeout(pendingSaveRequestTimerRef.current);
      }
      pendingSaveRequestTimerRef.current = window.setTimeout(() => {
        finishPendingSaveRequest();
      }, 2000);
      window.dispatchEvent(new CustomEvent<SaveFilesEventDetail>(SAVE_FILES_EVENT, {
        detail: { paths: targetPaths },
      }));
    });
  }, [finishPendingSaveRequest]);

  const saveAllOpenFiles = useCallback(async () => {
    await requestFileSaves(openFiles.filter(file => file.dirty).map(file => file.path));
  }, [openFiles, requestFileSaves]);

  const saveOpenFile = useCallback(async (path: string) => {
    await requestFileSaves([path]);
  }, [requestFileSaves]);

  const performEditorClose = useCallback((path: string) => {
    window.dispatchEvent(new CustomEvent<CloseFileEventDetail>(PERFORM_CLOSE_FILE_EVENT, {
      detail: { path },
    }));
  }, []);

  const requestFileClose = useCallback((path: string) => {
    const file = openFiles.find((openFile) => openFile.path === path);
    if (!file) {
      return;
    }
    if (file.dirty) {
      setPendingUnsavedAction((current) => current ?? { kind: 'file-close', path });
      return;
    }
    performEditorClose(path);
  }, [openFiles, performEditorClose]);

  const performWindowClose = useCallback(async () => {
    allowWindowCloseRef.current = true;
    try {
      await bridge.closeWindow();
    } catch (error) {
      allowWindowCloseRef.current = false;
      throw error;
    }
  }, []);

  const requestWindowClose = useCallback(async () => {
    if (dirtyOpenFilesRef.current.length > 0) {
      setPendingUnsavedAction((current) => current ?? { kind: 'window-close' });
      return;
    }
    await performWindowClose();
  }, [performWindowClose]);

  const handleWindowAction = useCallback(async (action: WindowAction) => {
    try {
      switch (action) {
        case 'minimize':
          await bridge.minimizeWindow();
          break;
        case 'toggle-maximize':
          await bridge.toggleMaximizeWindow();
          break;
        case 'toggle-fullscreen':
          await bridge.toggleFullscreenWindow();
          break;
        case 'close':
          await requestWindowClose();
          break;
      }
    } catch {
      showNotice('Window controls are unavailable right now.', 'error');
    }
  }, [requestWindowClose, showNotice]);

  const resolvePendingUnsavedAction = useCallback(async (mode: 'discard' | 'save') => {
    if (!pendingUnsavedAction) {
      return;
    }

    setResolvingUnsavedAction(true);
    try {
      if (mode === 'save') {
        if (pendingUnsavedAction.kind === 'file-close') {
          await saveOpenFile(pendingUnsavedAction.path);
        } else {
          await saveAllOpenFiles();
        }
      }

      if (pendingUnsavedAction.kind === 'file-close') {
        performEditorClose(pendingUnsavedAction.path);
        setPendingUnsavedAction(null);
        return;
      }

      await performWindowClose();
    } catch {
      showNotice('Engine could not complete that close action right now.', 'error');
    } finally {
      setResolvingUnsavedAction(false);
    }
  }, [pendingUnsavedAction, performEditorClose, performWindowClose, saveAllOpenFiles, saveOpenFile, showNotice]);

  const requestWorkspaceOpen = useCallback((target: WorkspaceOpenTarget) => {
    const normalizedWorkspacePath = normalizeProjectPath(target.workspacePath);
    const normalizedInitialFilePath = target.initialFilePath
      ? normalizeProjectPath(target.initialFilePath)
      : undefined;
    const currentWorkspacePath = normalizeProjectPath(activeSession?.projectPath ?? '');
    const currentWorkspacePrefix = currentWorkspacePath.endsWith('/')
      ? currentWorkspacePath
      : `${currentWorkspacePath}/`;

    if (currentWorkspacePath && currentWorkspacePath === normalizedWorkspacePath) {
      if (normalizedInitialFilePath) {
        wsClient.send({ type: 'file.read', path: normalizedInitialFilePath });
      }
      return;
    }

    if (
      normalizedInitialFilePath
      && currentWorkspacePath
      && normalizedInitialFilePath.startsWith(currentWorkspacePrefix)
    ) {
      wsClient.send({ type: 'file.read', path: normalizedInitialFilePath });
      return;
    }

    const nextTarget = {
      ...target,
      workspacePath: normalizedWorkspacePath,
      initialFilePath: normalizedInitialFilePath,
    };

    if (dirtyOpenFiles.length > 0) {
      setPendingWorkspaceSwitch(nextTarget);
      return;
    }

    completeWorkspaceOpen(nextTarget);
  }, [activeSession?.projectPath, completeWorkspaceOpen, dirtyOpenFiles.length]);

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
    requestWorkspaceOpen({
      workspacePath: folderPath,
      label: projectLabel(folderPath),
    });
  }, [requestWorkspaceOpen, showNotice]);

  const openFileFromPath = useCallback(async (path?: string) => {
    let filePath = path ? normalizeProjectPath(path) : undefined;
    try {
      if (!filePath) {
        const pickedPath = await bridge.openFileDialog();
        filePath = pickedPath ? normalizeProjectPath(pickedPath) : undefined;
      }
    } catch {
      showNotice('The desktop file picker could not be opened.', 'error');
      return;
    }

    if (!filePath) {
      return;
    }

    try {
      const inspected = await bridge.inspectPath(filePath);
      if (inspected.kind === 'directory') {
        requestWorkspaceOpen({
          workspacePath: inspected.path,
          label: inspected.name,
        });
        return;
      }

      if (!inspected.parentPath) {
        showNotice('That file needs a parent folder before Engine can open it.', 'error');
        return;
      }

      requestWorkspaceOpen({
        workspacePath: inspected.parentPath,
        initialFilePath: inspected.path,
        label: inspected.name,
      });
    } catch {
      showNotice('Engine could not open that file.', 'error');
    }
  }, [requestWorkspaceOpen, showNotice]);

  const openDroppedPath = useCallback(async (path: string) => {
    try {
      const inspected = await bridge.inspectPath(normalizeProjectPath(path));
      if (inspected.kind === 'directory') {
        await openFolder(inspected.path);
        return;
      }

      if (!inspected.parentPath) {
        showNotice('Dropped files need a parent folder before Engine can open them.', 'error');
        return;
      }

      requestWorkspaceOpen({
        workspacePath: inspected.parentPath,
        initialFilePath: inspected.path,
        label: inspected.name,
      });
    } catch {
      showNotice('Engine could not open the dropped item.', 'error');
    }
  }, [openFolder, requestWorkspaceOpen, showNotice]);

  const desktopHandlersRef = useRef({
    openDroppedPath,
    openFolder,
    openFileFromPath,
    launchWorkspaceTask,
    buildTask,
    runTask,
  });

  useEffect(() => {
    desktopHandlersRef.current = {
      openDroppedPath,
      openFolder,
      openFileFromPath,
      launchWorkspaceTask,
      buildTask,
      runTask,
    };
  }, [buildTask, launchWorkspaceTask, openDroppedPath, openFileFromPath, openFolder, runTask]);

  const openCommandPalette = useCallback((mode: CommandPaletteMode) => {
    setCommandPaletteMode(mode);
    setShowCommandPalette(true);
  }, []);

  const paletteCommandItems = useMemo<CommandPaletteItem[]>(() => {
    const commands: CommandPaletteItem[] = [
      {
        id: 'palette:open-folder',
        kind: 'command',
        title: 'Open Folder…',
        subtitle: 'Open or switch the current workspace folder',
        keywords: 'folder workspace project picker',
        badge: 'Workspace',
        action: () => openFolder(),
      },
      {
        id: 'palette:open-file',
        kind: 'command',
        title: 'Open File…',
        subtitle: 'Pick a file and open its parent workspace automatically',
        keywords: 'file picker workspace',
        badge: 'Workspace',
        action: () => openFileFromPath(),
      },
      {
        id: 'palette:save-file',
        kind: 'command',
        title: 'Save Active File',
        subtitle: activeFilePath ? `Write ${projectLabel(activeFilePath)} to disk` : 'No active file is open right now',
        keywords: 'save file editor',
        badge: 'Editor',
        disabled: !activeFilePath,
        action: () => window.dispatchEvent(new Event('engine:save-active-file')),
      },
      {
        id: 'palette:save-all',
        kind: 'command',
        title: 'Save All Open Files',
        subtitle: dirtyOpenFiles.length > 0
          ? `Persist ${dirtyOpenFiles.length} unsaved ${dirtyOpenFiles.length === 1 ? 'editor' : 'editors'}`
          : 'No open editors have unsaved changes',
        keywords: 'save all workspace dirty editors',
        badge: 'Editor',
        disabled: dirtyOpenFiles.length === 0,
        action: () => { void saveAllOpenFiles(); },
      },
      {
        id: 'palette:preferences',
        kind: 'command',
        title: 'Open Settings',
        subtitle: 'Tune the editor, extensions, providers, and GitHub wiring',
        keywords: 'settings preferences config',
        badge: 'Settings',
        action: () => setShowPreferences(true),
      },
      {
        id: 'palette:build',
        kind: 'command',
        title: 'Build Workspace',
        subtitle: buildTask ? buildTask.command : 'No build task detected for this workspace yet',
        keywords: 'build compile task workspace',
        badge: 'Task',
        disabled: !buildTask,
        action: () => launchWorkspaceTask(buildTask),
      },
      {
        id: 'palette:run',
        kind: 'command',
        title: 'Run Workspace',
        subtitle: runTask ? runTask.command : 'No run task detected for this workspace yet',
        keywords: 'run dev task workspace terminal',
        badge: 'Task',
        disabled: !runTask,
        action: () => launchWorkspaceTask(runTask),
      },
      {
        id: 'palette:toggle-sidebar',
        kind: 'command',
        title: showSidebar ? 'Hide Sidebar' : 'Show Sidebar',
        subtitle: 'Toggle the left workspace sidebar',
        keywords: 'sidebar explorer toggle',
        badge: 'Layout',
        action: () => setShowSidebar((current) => !current),
      },
      {
        id: 'palette:toggle-terminal',
        kind: 'command',
        title: showTerminal ? 'Hide Terminal' : 'Show Terminal',
        subtitle: 'Toggle the integrated terminal panel',
        keywords: 'terminal panel toggle',
        badge: 'Layout',
        action: () => setShowTerminal((current) => !current),
      },
      {
        id: 'palette:focus-chat',
        kind: 'command',
        title: 'Focus Chat Panel',
        subtitle: 'Bring the assistant conversation back into view',
        keywords: 'chat assistant panel focus',
        badge: 'Panel',
        action: () => setRightTab('chat'),
      },
      {
        id: 'palette:focus-agent',
        kind: 'command',
        title: 'Focus Agent Monitor',
        subtitle: 'Inspect live agent activity and tool execution',
        keywords: 'agent monitor panel focus',
        badge: 'Panel',
        action: () => setRightTab('agent'),
      },
      {
        id: 'palette:show-explorer',
        kind: 'command',
        title: 'Show Explorer',
        subtitle: 'Focus the workspace file tree',
        keywords: 'explorer files sidebar',
        badge: 'Sidebar',
        action: () => {
          setActivityTab('explorer');
          setShowSidebar(true);
        },
      },
      {
        id: 'palette:show-open-editors',
        kind: 'command',
        title: 'Show Open Editors',
        subtitle: 'Focus the currently open editor list',
        keywords: 'open editors tabs sidebar',
        badge: 'Sidebar',
        action: () => {
          setActivityTab('open-editors');
          setShowSidebar(true);
        },
      },
      {
        id: 'palette:show-search',
        kind: 'command',
        title: 'Show Search',
        subtitle: 'Focus ripgrep workspace search',
        keywords: 'search grep sidebar',
        badge: 'Sidebar',
        action: () => {
          setActivityTab('search');
          setShowSidebar(true);
        },
      },
      {
        id: 'palette:show-git',
        kind: 'command',
        title: 'Show Source Control',
        subtitle: 'Focus staged, unstaged, diff, and commit tools',
        keywords: 'git source control sidebar',
        badge: 'Sidebar',
        action: () => {
          setActivityTab('git');
          setShowSidebar(true);
        },
      },
      {
        id: 'palette:show-issues',
        kind: 'command',
        title: 'Show Issues',
        subtitle: 'Focus GitHub issue tracking for this workspace',
        keywords: 'issues github sidebar',
        badge: 'Sidebar',
        action: () => {
          setActivityTab('issues');
          setShowSidebar(true);
        },
      },
      {
        id: 'palette:show-usage',
        kind: 'command',
        title: 'Show Usage Dashboard',
        subtitle: 'View project or user-wide API spend, tokens, and compute time',
        keywords: 'usage dashboard spend tokens model filter analytics charts',
        badge: 'Workspace',
        action: () => {
          setActivityTab('usage');
          setShowSidebar(false);
        },
      },
    ];

    if (desktopShell) {
      commands.push({
        id: 'palette:toggle-fullscreen',
        kind: 'command',
        title: macPlatform ? 'Toggle Fullscreen' : 'Toggle Window Maximize',
        subtitle: macPlatform ? 'Use the native desktop fullscreen behavior' : 'Use the desktop shell window controls',
        keywords: 'window fullscreen maximize shell',
        badge: 'Window',
        action: () => { void handleWindowAction(macPlatform ? 'toggle-fullscreen' : 'toggle-maximize'); },
      });
    }

    return commands;
  }, [
    activeFilePath,
    buildTask,
    desktopShell,
    dirtyOpenFiles.length,
    handleWindowAction,
    launchWorkspaceTask,
    macPlatform,
    openFileFromPath,
    openFolder,
    runTask,
    saveAllOpenFiles,
    showSidebar,
    showTerminal,
  ]);

  const paletteFileItems = useMemo<CommandPaletteItem[]>(() => {
    const openFileSet = new Set(openFiles.map((file) => file.path));
    return collectWorkspaceFiles(fileTree)
      .map((node) => {
        const relativePath = relativeWorkspacePath(workspaceRoot, node.path);
        const isOpen = openFileSet.has(node.path);
        return {
          id: `palette:file:${node.path}`,
          kind: 'file' as const,
          title: node.name,
          subtitle: relativePath === node.name ? node.path : relativePath,
          keywords: `${node.name} ${relativePath} ${node.path}`,
          badge: isOpen ? 'Open' : 'File',
          action: () => wsClient.send({ type: 'file.read', path: node.path }),
        };
      })
      .sort((left, right) => left.subtitle.localeCompare(right.subtitle, undefined, { sensitivity: 'base' }));
  }, [fileTree, openFiles, workspaceRoot]);

  const paletteItems = commandPaletteMode === 'commands' ? paletteCommandItems : paletteFileItems;

  useEffect(() => {
    syncRuntimeConfigRef.current = syncRuntimeConfig;
  }, [syncRuntimeConfig]);

  useEffect(() => {
    requestWorkspaceTasksRef.current = requestWorkspaceTasks;
  }, [requestWorkspaceTasks]);

  useEffect(() => {
    requestFileCloseRef.current = requestFileClose;
  }, [requestFileClose]);

  useEffect(() => {
    ensureStreamingAssistantMessageRef.current = ensureStreamingAssistantMessage;
  }, [ensureStreamingAssistantMessage]);

  // WebSocket handler
  useEffect(() => {
    const initializeConnection = async () => {
      setConnected(true);
      await syncRuntimeConfigRef.current();

      const state = useStore.getState();
      const currentSession = state.activeSession;
      const tabs = state.openFiles.map((file) => ({
        path: file.path,
        isActive: file.path === state.activeFilePath,
        isDirty: file.dirty,
      }));
      if (tabs.length > 0) {
        wsClient.send({ type: 'editor.tabs.sync', tabs });
      }

      if (currentSession?.id) {
        wsClient.send({ type: 'session.load', sessionId: currentSession.id });
        requestWorkspaceTasksRef.current(currentSession.projectPath);
        return;
      }

      const initialPath = normalizeProjectPath(await bridge.getProjectPath().catch(() => ''));
      if (initialPath && initialPath !== '.') {
        setProjectName(projectLabel(initialPath));
        wsClient.send({ type: 'project.open', path: initialPath });
        requestWorkspaceTasksRef.current(initialPath);
      } else {
        wsClient.send({ type: 'session.list' });
      }
    };

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

        case 'session.updated':
          setSessions(prev => {
            const exists = prev.find(session => session.id === msg.session.id);
            return exists
              ? prev.map(session => session.id === msg.session.id ? msg.session : session)
              : [msg.session, ...prev];
          });
          if (useStore.getState().activeSession?.id === msg.session.id) {
            setActiveSession(msg.session);
          }
          break;

        case 'session.loaded':
          setActiveSession(msg.session);
          setMessages(msg.messages);
          pendingToolCallsRef.current[msg.session.id] = [];
          if (streamingRef.current?.sessionId === msg.session.id) {
            ensureStreamingAssistantMessageRef.current(msg.session.id);
            updateAgentSession(msg.session.id, {
              isStreaming: true,
              currentActivity: 'Thinking...',
            });
          }
          break;

        case 'chat.started':
          ensureStreamingAssistantMessageRef.current(msg.sessionId);
          updateAgentSession(msg.sessionId, {
            isStreaming: true,
            currentActivity: 'Thinking...',
          });
          break;

        case 'chat.chunk': {
          const sid = msg.sessionId;
          const msgId = ensureStreamingAssistantMessageRef.current(sid);
          if (msg.content) {
            appendChunk(msgId, msg.content);
          }
          updateAgentSession(sid, {
            isStreaming: !msg.done,
            currentActivity: msg.done ? '' : 'Responding...',
          });
          if (msg.done) {
            finalizeMessage(msgId);
            streamingRef.current = null;
          }
          break;
        }

        case 'chat.tool_call': {
          const msgId = ensureStreamingAssistantMessageRef.current(msg.sessionId);
          const toolId = randomUUID();
          const startedAt = Date.now();
          addToolCall(msgId, {
            id: toolId,
            name: msg.name,
            input: msg.input,
            pending: true,
          });
          const queue = pendingToolCallsRef.current[msg.sessionId] ?? [];
          queue.push({ id: toolId, msgId, name: msg.name, startedAt });
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
          {
            const msgId = ensureStreamingAssistantMessageRef.current(msg.sessionId);
            const currentMessage = useStore.getState().chatMessages.find(chatMessage => chatMessage.id === msgId);
            if (currentMessage?.content) {
              appendChunk(msgId, '\n\n⚠ ' + msg.error);
            }
            markMessageFailed(msgId);
            streamingRef.current = null;
          }
          pendingToolCallsRef.current[msg.sessionId] = [];
          updateAgentSession(msg.sessionId, {
            isStreaming: false,
            currentActivity: '',
          });
          break;

        case 'file.content':
          openFile(msg.path, msg.content, msg.language, msg.size);
          break;

        case 'file.saved':
          markFileSaved(msg.path);
          if (pendingSaveRequestRef.current) {
            pendingSaveRequestRef.current.remaining.delete(msg.path);
            if (pendingSaveRequestRef.current.remaining.size === 0) {
              finishPendingSaveRequest();
            }
          }
          break;

        case 'file.tree':
          mergeFileTree(msg.tree);
          break;

        case 'file.created':
        case 'folder.created':
          if (workspaceRootRef.current) {
            wsClient.send({ type: 'file.tree', path: workspaceRootRef.current });
          }
          break;

        case 'search.results':
          setSearchResults(msg.query, msg.results, msg.error ?? null);
          break;

        case 'git.status':
          setGitStatus(msg.status);
          break;

        case 'workspace.tasks':
          setWorkspaceTasks(msg.tasks);
          setDefaultBuildTaskId(msg.defaultBuildTaskId ?? null);
          setDefaultRunTaskId(msg.defaultRunTaskId ?? null);
          break;

        case 'github.issues':
          setGithubIssues(msg.issues);
          setGithubIssuesError(msg.error ?? null);
          setGithubIssuesLoading(false);
          break;

        case 'github.user':
          setGithubUser(msg.user);
          break;

        case 'github.auth.code':
          setGithubAuthFlow({
            userCode: (msg as any).userCode,
            verificationUri: (msg as any).verificationUri,
            expiresIn: (msg as any).expiresIn,
          });
          break;

        case 'github.auth.done': {
          const token = (msg as any).token as string;
          setGithubAuthFlow(null);
          void bridge.setGithubToken(token).then(() => {
            setGithubToken(token);
            void syncRuntimeConfigRef.current();
          });
          break;
        }

        case 'github.auth.error':
          setGithubAuthFlow(null);
          showNotice((msg as any).error ?? 'GitHub login failed', 'error');
          break;

        case 'github.auth.status':
          break;

        case 'approval.request':
          setPendingApproval(msg.request);
          break;

        case 'editor.open':
          wsClient.send({ type: 'file.read', path: msg.path });
          break;

        case 'editor.tab.close':
          requestFileCloseRef.current(msg.path);
          break;

        case 'editor.tab.focus':
          setActiveFile(msg.path);
          break;

        case 'test.summary': {
          const errCount = msg.summary.errors.length;
          updateAgentSession(msg.sessionId, {
            currentActivity: errCount > 0 ? `${errCount} error${errCount !== 1 ? 's' : ''} detected` : '',
          });
          break;
        }
      }
    });
    const offOpen = wsClient.onOpen(initializeConnection);
    const offClose = wsClient.onClose(() => {
      setConnected(false);
    });
    const runningViaDevServer = typeof window !== 'undefined' && window.location.host.includes(':5173');
    const connectTimer = window.setTimeout(
      () => wsClient.connect(),
      runningViaDevServer ? 900 : 0,
    );

    return () => {
      window.clearTimeout(connectTimer);
      off();
      offOpen();
      offClose();
      if (!runningViaDevServer) {
        wsClient.disconnect();
      }
    };
  }, []); // store actions are stable; refs provide fresh UI state without reconnect churn

  useEffect(() => {
    if (!activeSession?.projectPath) {
      setWorkspaceTasks([]);
      setDefaultBuildTaskId(null);
      setDefaultRunTaskId(null);
      return;
    }
    requestWorkspaceTasks(activeSession.projectPath);
  }, [activeSession?.projectPath, requestWorkspaceTasks]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.altKey) {
        return;
      }
      const key = event.key.toLowerCase();
      if (key !== 'p') {
        return;
      }
      event.preventDefault();
      openCommandPalette(event.shiftKey ? 'commands' : 'files');
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [openCommandPalette]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || !event.shiftKey || event.key.toLowerCase() !== 'b') {
        return;
      }
      const target = event.target as HTMLElement | null;
      if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) {
        return;
      }
      event.preventDefault();
      launchWorkspaceTask(runTask);
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [launchWorkspaceTask, runTask]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.shiftKey || event.altKey || event.key !== ',') {
        return;
      }
      const target = event.target as HTMLElement | null;
      if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) {
        return;
      }
      event.preventDefault();
      setShowPreferences(true);
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, []);

  // Cmd+. toggles .git visibility in the file tree
  useEffect(() => {
    const onToggleDotfiles = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key === '.') {
        event.preventDefault();
        useStore.getState().toggleDotfiles();
      }
    };
    window.addEventListener('keydown', onToggleDotfiles);
    return () => window.removeEventListener('keydown', onToggleDotfiles);
  }, []);

  useEffect(() => {
    const onRequestCloseFile = (event: Event) => {
      const detail = (event as CustomEvent<CloseFileEventDetail>).detail;
      if (detail?.path) {
        requestFileClose(detail.path);
      }
    };
    window.addEventListener(REQUEST_CLOSE_FILE_EVENT, onRequestCloseFile as EventListener);
    return () => window.removeEventListener(REQUEST_CLOSE_FILE_EVENT, onRequestCloseFile as EventListener);
  }, [requestFileClose]);

  useEffect(() => {
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
      if (dirtyOpenFiles.length === 0) {
        return;
      }
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', onBeforeUnload);
    return () => window.removeEventListener('beforeunload', onBeforeUnload);
  }, [dirtyOpenFiles.length]);

  useEffect(() => {
    if (!desktopShell || typeof window === 'undefined' || !('__TAURI__' in window)) {
      return;
    }

    let disposed = false;
    let cleanup: Array<() => void> = [];

    void Promise.all([
      import('@tauri-apps/api/window'),
      import('@tauri-apps/api/event'),
    ]).then(async ([{ getCurrentWindow }, { listen }]) => {
      const currentWindow = getCurrentWindow();
      const unlistenClose = await currentWindow.onCloseRequested((event) => {
        if (allowWindowCloseRef.current || dirtyOpenFilesRef.current.length === 0) {
          return;
        }
        event.preventDefault();
        setPendingUnsavedAction((current) => current ?? { kind: 'window-close' });
      });
      const unlistenDrop = 'onDragDropEvent' in currentWindow
        ? await currentWindow.onDragDropEvent((event) => {
            if (event.payload.type === 'enter' || event.payload.type === 'over') {
              setDropTargetActive(true);
              return;
            }
            if (event.payload.type === 'leave') {
              setDropTargetActive(false);
              return;
            }
            setDropTargetActive(false);
            const [firstPath] = event.payload.paths;
            if (firstPath) {
              void desktopHandlersRef.current.openDroppedPath(firstPath);
            }
          })
        : () => {};
      const unlistenMenu = await listen<string>('engine-shell-menu', (event) => {
        const handlers = desktopHandlersRef.current;
        switch (event.payload) {
          case 'open-folder':
            void handlers.openFolder();
            break;
          case 'open-file':
            void handlers.openFileFromPath();
            break;
          case 'save-file':
            window.dispatchEvent(new Event('engine:save-active-file'));
            break;
          case 'save-all-files':
            window.dispatchEvent(new Event('engine:save-all-open-files'));
            break;
          case 'open-preferences':
            setShowPreferences(true);
            break;
          case 'build-workspace':
            handlers.launchWorkspaceTask(handlers.buildTask);
            break;
          case 'run-workspace':
            handlers.launchWorkspaceTask(handlers.runTask);
            break;
          case 'toggle-sidebar':
            setShowSidebar((current) => !current);
            break;
          case 'toggle-terminal':
            setShowTerminal((current) => !current);
            break;
          case 'focus-agent':
            setRightTab('agent');
            break;
          case 'show-explorer':
            setActivityTab('explorer');
            setShowSidebar(true);
            break;
          case 'show-open-editors':
            setActivityTab('open-editors');
            setShowSidebar(true);
            break;
          case 'show-search':
            setActivityTab('search');
            setShowSidebar(true);
            break;
          case 'show-git':
            setActivityTab('git');
            setShowSidebar(true);
            break;
          case 'show-issues':
            setActivityTab('issues');
            setShowSidebar(true);
            break;
          case 'show-usage':
            setActivityTab('usage');
            setShowSidebar(false);
            break;
          case 'open-command-palette':
            openCommandPalette('commands');
            break;
          case 'open-file-palette':
            openCommandPalette('files');
            break;
          case 'reload-ui':
            window.location.reload();
            break;
          case 'focus-chat':
            setRightTab('chat');
            break;
          case 'open-project-page':
            void bridge.openExternal('https://github.com/RockyWearsAHat/engine');
            break;
        }
      });

      if (disposed) {
        unlistenClose();
        unlistenDrop();
        unlistenMenu();
        return;
      }

      cleanup = [unlistenClose, unlistenDrop, unlistenMenu];
    }).catch((error) => {
      console.warn('Desktop shell events are unavailable.', error);
    });

    return () => {
      disposed = true;
      cleanup.forEach((unlisten) => unlisten());
    };
  }, [desktopShell]);

  const hasProject = !!fileTree;
  const toggleActivity = (tab: ActivityTab) => {
    if (tab === 'usage') {
      setActivityTab('usage');
      setShowSidebar(false);
      return;
    }
    if (activityTab === tab && showSidebar) { setShowSidebar(false); }
    else { setActivityTab(tab); setShowSidebar(true); }
  };

    return (
      <div className="app">
        {/* Titlebar — native traffic lights via overlay, our content fills the rest */}
        {desktopShell && (
        <div className={`titlebar ${macPlatform ? 'titlebar-mac-overlay' : ''}`}>
          <div className="titlebar-leading">
            {/* macOS: native traffic lights handle close/min/fullscreen via titleBarStyle overlay */}
            {/* Add padding-left so our content doesn't overlap the native buttons */}
            <div
              className="titlebar-brand"
              data-tauri-drag-region
              onMouseDown={beginWindowDrag}
            >
              <span className="titlebar-brand-name" data-tauri-drag-region>Engine</span>
            </div>
          </div>

          <div
            className="titlebar-drag-zone"
            data-tauri-drag-region
            onMouseDown={beginWindowDrag}
            onDoubleClick={() => {
              if (!desktopShell) return;
              void handleWindowAction(macPlatform ? 'toggle-fullscreen' : 'toggle-maximize');
            }}
          >
            <div className="titlebar-chip" data-tauri-drag-region>
              <span className={`titlebar-chip-dot ${connected ? 'online' : 'offline'}`} data-tauri-drag-region />
              <span className="titlebar-chip-name" data-tauri-drag-region>{projectName || 'Drop a folder or open a workspace'}</span>
              {gitStatus?.branch && (
                <span className="titlebar-chip-branch" data-tauri-drag-region>{gitStatus.branch}</span>
              )}
            </div>
          </div>

          <div className="titlebar-actions">
            <FileMenu
              desktopShell={desktopShell}
              buildTaskAvailable={!!buildTask}
              runTaskAvailable={!!runTask}
              showSidebar={showSidebar}
              showTerminal={showTerminal}
              activityTab={activityTab}
              rightTab={rightTab}
              onOpenFolder={openFolder}
              onOpenFile={openFileFromPath}
              onOpenPreferences={() => setShowPreferences(true)}
              onOpenCommands={() => openCommandPalette('commands')}
              onOpenFilesPalette={() => openCommandPalette('files')}
              onToggleTerminal={() => setShowTerminal(v => !v)}
              onToggleSidebar={() => setShowSidebar(v => !v)}
              onShowExplorer={() => {
                setActivityTab('explorer');
                setShowSidebar(true);
              }}
              onShowOpenEditors={() => {
                setActivityTab('open-editors');
                setShowSidebar(true);
              }}
              onShowSearch={() => {
                setActivityTab('search');
                setShowSidebar(true);
              }}
              onShowGit={() => {
                setActivityTab('git');
                setShowSidebar(true);
              }}
              onShowIssues={() => {
                setActivityTab('issues');
                setShowSidebar(true);
              }}
              onShowUsage={() => {
                setActivityTab('usage');
                setShowSidebar(false);
              }}
              onFocusChat={() => setRightTab('chat')}
              onFocusAgent={() => setRightTab('agent')}
              onBuildWorkspace={() => launchWorkspaceTask(buildTask)}
              onRunWorkspace={() => launchWorkspaceTask(runTask)}
              onOpenProjectPage={() => {
                void bridge.openExternal('https://github.com/RockyWearsAHat/engine');
              }}
              onReloadUi={() => window.location.reload()}
            />
            {desktopShell && !macPlatform && (
              <WindowControls onAction={handleWindowAction} />
            )}
          </div>
        </div>
        )}

        {appNotice && (
          <ShellNotice message={appNotice.message} tone={appNotice.tone} />
        )}

        {dropTargetActive && (
          <div className="workspace-drop-overlay">
            <div className="workspace-drop-card animate-appear">
              <div className="workspace-drop-kicker">Drop to open</div>
              <div className="workspace-drop-title">Drop a folder or file anywhere in the window.</div>
              <div className="workspace-drop-copy">
                Folders become workspaces instantly. Files open inside their parent workspace without leaving the shell.
              </div>
            </div>
          </div>
        )}

        <div className="workspace">
          <div className="activity-bar">
            {([
              ['explorer', FolderOpen],
              ['open-editors', FileStack],
              ['git', GitBranch],
              ['search', Search],
              ['issues', AlertCircle],
              ['usage', ChartColumn],
            ] as [ActivityTab, React.ComponentType<{ size?: number }>][]).map(([id, Icon]) => (
              <button
                key={id}
                className={`activity-btn ${activityTab === id && (showSidebar || id === 'usage') ? 'active' : ''}`}
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

          {showSidebar && (
            <>
              <div className="sidebar animate-slide" style={{ width: sidebarWidth }}>
                <FileTree
                  activityTab={activityTab}
                  onOpenFolder={() => openFolder()}
                  onOpenFile={() => openFileFromPath()}
                  openFiles={openFiles}
                  activeFilePath={activeFilePath}
                  onSetActiveFile={setActiveFile}
                />
              </div>
              <div
                className="panel-resize-handle vertical"
                onPointerDown={beginPanelResize('sidebar')}
                aria-hidden="true"
              />
            </>
          )}

          <div className="main-column">
            <div className="editor-area" ref={editorAreaRef}>
              {hasProject ? (
                <>
                  {activityTab === 'usage' ? (
                    <UsageDashboard
                      projectPath={activeSession?.projectPath ?? null}
                      mode="workspace"
                    />
                  ) : (
                    <>
                      <MissionControlStrip
                        projectName={missionProjectName}
                        branchName={activeSession?.branchName ?? ''}
                        projectDirection={activeSession?.projectDirection ?? ''}
                        summary={activeSession?.summary ?? ''}
                        onFocusAssistant={() => setRightTab('chat')}
                        onFocusAgent={() => setRightTab('agent')}
                      />
                      <Editor />
                    </>
                  )}
                </>
              ) : (
                <WelcomeScreen
                  sessions={sessions}
                  desktopShell={desktopShell}
                  onOpenFolder={openFolder}
                  onOpenFile={openFileFromPath}
                  onOpenPreferences={() => setShowPreferences(true)}
                  onOpenCommands={() => openCommandPalette('commands')}
                  onOpenTerminal={() => setShowTerminal(true)}
                />
              )}

              {showTerminal && hasProject && (
                <>
                  <div
                    className="panel-resize-handle horizontal"
                    onPointerDown={beginPanelResize('terminal')}
                    aria-hidden="true"
                  />
                  <div className="bottom-panel" style={{ height: terminalHeight }}>
                    <Terminal
                      commandRequest={taskRequest}
                      onCommandHandled={(id) => {
                        setTaskRequest((current) => current?.id === id ? null : current);
                      }}
                    />
                  </div>
                </>
              )}
            </div>
          </div>

          <div
            className="panel-resize-handle vertical right"
            onPointerDown={beginPanelResize('right-panel')}
            aria-hidden="true"
          />

          <div className="right-panel" style={{ width: rightPanelWidth }}>
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

      {showPreferences && (
        <div className="preferences-overlay">
          <button
            className="preferences-backdrop"
            aria-label="Close preferences"
            onClick={() => setShowPreferences(false)}
          />
          <div className="preferences-drawer animate-appear">
            <div className="preferences-drawer-header">
              <div>
                <div className="preferences-drawer-kicker">Engine settings</div>
                <div className="preferences-drawer-title">Settings</div>
              </div>
              <button
                className="shell-icon-btn"
                onClick={() => setShowPreferences(false)}
                title="Close preferences"
              >
                <X size={14} />
              </button>
            </div>
            <PreferencesPanel />
          </div>
        </div>
      )}

      <CommandPalette
        open={showCommandPalette}
        mode={commandPaletteMode}
        workspaceName={projectName || undefined}
        items={paletteItems}
        onClose={() => setShowCommandPalette(false)}
        onModeChange={setCommandPaletteMode}
      />

      {pendingApproval && (
        <ApprovalModal
          request={pendingApproval}
          onDeny={() => {
            wsClient.send({ type: 'approval.respond', id: pendingApproval.id, allow: false });
            setPendingApproval(null);
          }}
          onApprove={() => {
            wsClient.send({ type: 'approval.respond', id: pendingApproval.id, allow: true });
            setPendingApproval(null);
          }}
        />
      )}

      {pendingWorkspaceSwitch && (
        <WorkspaceSwitchModal
          target={pendingWorkspaceSwitch}
          dirtyFiles={dirtyOpenFiles}
          busy={resolvingWorkspaceSwitch}
          onCancel={() => {
            if (resolvingWorkspaceSwitch) {
              return;
            }
            setPendingWorkspaceSwitch(null);
          }}
          onDiscard={async () => {
            if (resolvingWorkspaceSwitch) {
              return;
            }
            completeWorkspaceOpen(pendingWorkspaceSwitch);
          }}
          onSaveAll={async () => {
            if (resolvingWorkspaceSwitch) {
              return;
            }
            setResolvingWorkspaceSwitch(true);
            try {
              await saveAllOpenFiles();
              completeWorkspaceOpen(pendingWorkspaceSwitch);
            } finally {
              setResolvingWorkspaceSwitch(false);
            }
          }}
        />
      )}

      {pendingUnsavedAction && (
        <UnsavedChangesModal
          action={pendingUnsavedAction}
          dirtyFiles={dirtyOpenFiles}
          busy={resolvingUnsavedAction}
          onCancel={() => {
            if (resolvingUnsavedAction) {
              return;
            }
            setPendingUnsavedAction(null);
          }}
          onDiscard={async () => {
            if (resolvingUnsavedAction) {
              return;
            }
            if (pendingUnsavedAction.kind === 'file-close') {
              performEditorClose(pendingUnsavedAction.path);
              setPendingUnsavedAction(null);
              return;
            }
            await resolvePendingUnsavedAction('discard');
          }}
          onSave={async () => {
            if (resolvingUnsavedAction) {
              return;
            }
            await resolvePendingUnsavedAction('save');
          }}
        />
      )}

      <StatusBar />
    </div>
  );
}

function ApprovalModal({
  request,
  onApprove,
  onDeny,
}: {
  request: ApprovalRequest;
  onApprove: () => void;
  onDeny: () => void;
}) {
  return (
    <div className="approval-overlay">
      <button
        className="approval-backdrop"
        aria-label="Deny requested action"
        onClick={onDeny}
      />
      <div className="approval-card animate-appear" role="dialog" aria-modal="true" aria-labelledby="approval-title">
        <div className="approval-kicker">Approval required</div>
        <div id="approval-title" className="approval-title">
          {request.title}
        </div>
        <div className="approval-copy">{request.message}</div>
        <div className="approval-meta">
          <span className="editor-meta-chip">
            {request.kind === 'git_commit' ? 'Git commit' : 'Shell command'}
          </span>
          <span>Session {request.sessionId.slice(-6)}</span>
        </div>
        <pre className="approval-command">{request.command}</pre>
        <div className="approval-actions">
          <button className="btn-secondary" onClick={onDeny}>
            Deny
          </button>
          <button className="btn-primary" onClick={onApprove}>
            Allow
          </button>
        </div>
      </div>
    </div>
  );
}

function MissionControlStrip({
  projectName,
  branchName,
  projectDirection,
  summary,
  onFocusAssistant,
  onFocusAgent,
}: {
  projectName: string;
  branchName: string;
  projectDirection: string;
  summary: string;
  onFocusAssistant: () => void;
  onFocusAgent: () => void;
}) {
  const items = [...parseMissionSummary(projectDirection), ...parseMissionSummary(summary)].slice(0, 4);
  if (!projectName && items.length === 0) {
    return null;
  }

  return (
    <section className="mission-strip" aria-label="Mission control">
      <div className="mission-strip-header">
        <div className="mission-strip-copy">
          <div className="mission-strip-kicker">Mission control</div>
          <div className="mission-strip-title-row">
            <div className="mission-strip-title">{projectName || 'Active workspace'}</div>
            {branchName && <span className="editor-meta-chip">{branchName}</span>}
          </div>
          <div className="mission-strip-subtitle">
            AI context stays visible in the editor instead of hiding in the side panel.
          </div>
        </div>
        <div className="mission-strip-actions">
          <button className="mission-strip-action" onClick={onFocusAssistant} type="button">
            Assistant
          </button>
          <button className="mission-strip-action" onClick={onFocusAgent} type="button">
            Agent activity
          </button>
        </div>
      </div>

      {items.length > 0 && (
        <div className="mission-strip-grid">
          {items.map((item) => (
            <article key={item.label} className="mission-strip-card">
              <div className="mission-strip-card-label">{item.label}</div>
              <div className="mission-strip-card-value">{item.value}</div>
            </article>
          ))}
        </div>
      )}
    </section>
  );
}

function parseMissionSummary(summary: string): Array<{ label: string; value: string }> {
  return summary
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const separatorIndex = line.indexOf(':');
      if (separatorIndex === -1) {
        return { label: 'Context', value: line };
      }
      const rawLabel = line.slice(0, separatorIndex).trim();
      const rawValue = line.slice(separatorIndex + 1).trim();
      return {
        label: rawLabel || 'Context',
        value: rawValue || line,
      };
    })
    .slice(0, 4);
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
        <button className="traffic-light maximize" aria-label="Toggle fullscreen" title="Toggle fullscreen" onClick={() => onAction('toggle-fullscreen')} />
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
function FileMenu({
  desktopShell,
  buildTaskAvailable,
  runTaskAvailable,
  showSidebar,
  showTerminal,
  activityTab,
  rightTab,
  onOpenFolder,
  onOpenFile,
  onOpenPreferences,
  onOpenCommands,
  onOpenFilesPalette,
  onToggleTerminal,
  onToggleSidebar,
  onShowExplorer,
  onShowOpenEditors,
  onShowSearch,
  onShowGit,
  onShowIssues,
  onShowUsage,
  onFocusChat,
  onFocusAgent,
  onBuildWorkspace,
  onRunWorkspace,
  onOpenProjectPage,
  onReloadUi,
}: {
  desktopShell: boolean;
  buildTaskAvailable: boolean;
  runTaskAvailable: boolean;
  showSidebar: boolean;
  showTerminal: boolean;
  activityTab: ActivityTab;
  rightTab: RightTab;
  onOpenFolder: () => void;
  onOpenFile: () => void;
  onOpenPreferences: () => void;
  onOpenCommands: () => void;
  onOpenFilesPalette: () => void;
  onToggleTerminal: () => void;
  onToggleSidebar: () => void;
  onShowExplorer: () => void;
  onShowOpenEditors: () => void;
  onShowSearch: () => void;
  onShowGit: () => void;
  onShowIssues: () => void;
  onShowUsage: () => void;
  onFocusChat: () => void;
  onFocusAgent: () => void;
  onBuildWorkspace: () => void;
  onRunWorkspace: () => void;
  onOpenProjectPage: () => void;
  onReloadUi: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [serviceMsg, setServiceMsg] = useState('');

  useEffect(() => {
    if (!open) {
      return;
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setOpen(false);
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [open]);

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
        className={`shell-action icon-only ${open ? 'active' : ''}`}
        title="Menu"
      >
        <Menu size={13} />
      </button>

      {open && (
        <>
          <div
            style={{ position: 'fixed', inset: 0, zIndex: 99 }}
            onClick={() => setOpen(false)}
          />
          <div className="shell-menu">
            <div className="shell-menu-group-label">Workspace</div>
            <MenuItem icon={<Search size={13} />} label="Command Palette…" shortcut="Cmd/Ctrl+Shift+P" onClick={() => { setOpen(false); onOpenCommands(); }} />
            <MenuItem icon={<FileText size={13} />} label="Quick Open File…" shortcut="Cmd/Ctrl+P" onClick={() => { setOpen(false); onOpenFilesPalette(); }} />
            {desktopShell && <MenuItem icon={<FolderOpen size={13} />} label="Open Folder…" shortcut="Cmd/Ctrl+O" onClick={() => { setOpen(false); onOpenFolder(); }} />}
            {desktopShell && <MenuItem icon={<FileText size={13} />} label="Open File…" shortcut="Cmd/Ctrl+Shift+O" onClick={() => { setOpen(false); onOpenFile(); }} />}
            <MenuItem icon={<FileText size={13} />} label="Save Active File" shortcut="Cmd/Ctrl+S" onClick={() => { setOpen(false); window.dispatchEvent(new Event('engine:save-active-file')); }} />
            <MenuItem icon={<FileText size={13} />} label="Save All Open Files" shortcut="Cmd/Ctrl+Shift+S" onClick={() => { setOpen(false); window.dispatchEvent(new Event('engine:save-all-open-files')); }} />
            <MenuItem icon={<Settings2 size={13} />} label="Settings…" shortcut="Cmd/Ctrl+," onClick={() => { setOpen(false); onOpenPreferences(); }} />

            <div className="shell-menu-divider" />
            <div className="shell-menu-group-label">View</div>
            <MenuItem icon={<Menu size={13} />} label={showSidebar ? 'Hide Sidebar' : 'Show Sidebar'} onClick={() => { setOpen(false); onToggleSidebar(); }} />
            <MenuItem icon={<TerminalIcon size={13} />} label={showTerminal ? 'Hide Terminal' : 'Show Terminal'} onClick={() => { setOpen(false); onToggleTerminal(); }} />
            <MenuItem icon={<FolderOpen size={13} />} label={`${activityTab === 'explorer' ? '✓ ' : ''}Explorer`} onClick={() => { setOpen(false); onShowExplorer(); }} />
            <MenuItem icon={<FileStack size={13} />} label={`${activityTab === 'open-editors' ? '✓ ' : ''}Open Editors`} onClick={() => { setOpen(false); onShowOpenEditors(); }} />
            <MenuItem icon={<Search size={13} />} label={`${activityTab === 'search' ? '✓ ' : ''}Search`} onClick={() => { setOpen(false); onShowSearch(); }} />
            <MenuItem icon={<GitBranch size={13} />} label={`${activityTab === 'git' ? '✓ ' : ''}Source Control`} onClick={() => { setOpen(false); onShowGit(); }} />
            <MenuItem icon={<AlertCircle size={13} />} label={`${activityTab === 'issues' ? '✓ ' : ''}Issues`} onClick={() => { setOpen(false); onShowIssues(); }} />
            <MenuItem icon={<ChartColumn size={13} />} label={`${activityTab === 'usage' ? '✓ ' : ''}Usage Dashboard`} onClick={() => { setOpen(false); onShowUsage(); }} />

            <div className="shell-menu-divider" />
            <div className="shell-menu-group-label">Panels</div>
            <MenuItem icon={<Settings2 size={13} />} label={`${rightTab === 'chat' ? '✓ ' : ''}Focus Chat`} onClick={() => { setOpen(false); onFocusChat(); }} />
            <MenuItem icon={<Activity size={13} />} label={`${rightTab === 'agent' ? '✓ ' : ''}Focus Agent Monitor`} onClick={() => { setOpen(false); onFocusAgent(); }} />

            {(buildTaskAvailable || runTaskAvailable) && (
              <>
                <div className="shell-menu-divider" />
                <div className="shell-menu-group-label">Tasks</div>
              </>
            )}
            {buildTaskAvailable && <MenuItem icon={<Hammer size={13} />} label="Build Workspace" shortcut="Cmd/Ctrl+Shift+B" onClick={() => { setOpen(false); onBuildWorkspace(); }} />}
            {runTaskAvailable && <MenuItem icon={<Play size={13} />} label="Run Workspace" onClick={() => { setOpen(false); onRunWorkspace(); }} />}

            <div className="shell-menu-divider" />
            <div className="shell-menu-group-label">App</div>
            <MenuItem icon={<FolderOpen size={13} />} label="Project Home" onClick={() => { setOpen(false); onOpenProjectPage(); }} />
            <MenuItem icon={<RotateCcw size={13} />} label="Reload UI" onClick={() => { setOpen(false); onReloadUi(); }} />
            {desktopShell && <MenuItem icon={<ServerCog size={13} />} label="Install Agent Service" onClick={handleInstall} />}
            {desktopShell && <MenuItem icon={<ServerCog size={13} />} label="Remove Agent Service" onClick={handleUninstall} />}
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

function WorkspaceSwitchModal({
  target,
  dirtyFiles,
  busy,
  onCancel,
  onDiscard,
  onSaveAll,
}: {
  target: WorkspaceOpenTarget;
  dirtyFiles: { path: string }[];
  busy: boolean;
  onCancel: () => void;
  onDiscard: () => void;
  onSaveAll: () => void;
}) {
  return (
    <div className="workspace-switch-overlay">
      <button
        className="workspace-switch-backdrop"
        aria-label="Close workspace switch dialog"
        onClick={onCancel}
        disabled={busy}
      />
      <div className="workspace-switch-card animate-appear" role="dialog" aria-modal="true" aria-labelledby="workspace-switch-title">
        <div className="workspace-switch-kicker">Unsaved editors</div>
        <div id="workspace-switch-title" className="workspace-switch-title">
          Save your open files before switching to {projectLabel(target.workspacePath)}?
        </div>
        <div className="workspace-switch-copy">
          Engine found unsaved changes in the current workspace. Save them now, discard them, or stay where you are.
        </div>
        <div className="workspace-switch-list">
          {dirtyFiles.map((file) => (
            <div key={file.path} className="workspace-switch-item">
              <FileText size={13} />
              <span>{projectLabel(file.path)}</span>
            </div>
          ))}
        </div>
        <div className="workspace-switch-actions">
          <button className="btn-secondary" onClick={onCancel} disabled={busy}>
            Cancel
          </button>
          <button className="shell-action" onClick={onDiscard} disabled={busy}>
            Don&apos;t Save
          </button>
          <button className="btn-primary" onClick={onSaveAll} disabled={busy}>
            {busy ? 'Saving…' : 'Save All & Open'}
          </button>
        </div>
      </div>
    </div>
  );
}

function UnsavedChangesModal({
  action,
  dirtyFiles,
  busy,
  onCancel,
  onDiscard,
  onSave,
}: {
  action: PendingUnsavedAction;
  dirtyFiles: { path: string }[];
  busy: boolean;
  onCancel: () => void;
  onDiscard: () => void;
  onSave: () => void;
}) {
  const closingFile = action.kind === 'file-close';
  const files = closingFile ? [{ path: action.path }] : dirtyFiles;

  return (
    <div className="workspace-switch-overlay">
      <button
        className="workspace-switch-backdrop"
        aria-label="Close unsaved changes dialog"
        onClick={onCancel}
        disabled={busy}
      />
      <div className="workspace-switch-card animate-appear" role="dialog" aria-modal="true" aria-labelledby="unsaved-changes-title">
        <div className="workspace-switch-kicker">Unsaved changes</div>
        <div id="unsaved-changes-title" className="workspace-switch-title">
          {closingFile
            ? `Save changes to ${projectLabel(action.path)} before closing?`
            : 'Save your open files before closing Engine?'}
        </div>
        <div className="workspace-switch-copy">
          {closingFile
            ? 'This editor has unsaved changes. Save it now, discard it, or keep editing.'
            : 'Engine found unsaved changes in open editors. Save them now, discard them, or keep the window open.'}
        </div>
        <div className="workspace-switch-list">
          {files.map((file) => (
            <div key={file.path} className="workspace-switch-item">
              <FileText size={13} />
              <span>{projectLabel(file.path)}</span>
            </div>
          ))}
        </div>
        <div className="workspace-switch-actions">
          <button className="btn-secondary" onClick={onCancel} disabled={busy}>
            Cancel
          </button>
          <button className="shell-action" onClick={onDiscard} disabled={busy}>
            Don&apos;t Save
          </button>
          <button className="btn-primary" onClick={onSave} disabled={busy}>
            {busy ? 'Saving…' : closingFile ? 'Save & Close' : 'Save All & Close'}
          </button>
        </div>
      </div>
    </div>
  );
}

function MenuItem({
  icon,
  label,
  shortcut,
  disabled = false,
  onClick,
}: {
  icon: React.ReactNode;
  label: string;
  shortcut?: string;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="shell-menu-item"
      disabled={disabled}
    >
      <span className="shell-menu-item-icon">{icon}</span>
      <span>{label}</span>
      {shortcut && <span className="shell-menu-item-shortcut">{shortcut}</span>}
    </button>
  );
}

// Welcome screen
function WelcomeScreen({
  sessions,
  desktopShell,
  onOpenFolder,
  onOpenFile,
  onOpenPreferences,
  onOpenCommands,
  onOpenTerminal,
}: {
  sessions: { id: string; projectPath: string; updatedAt: string }[];
  desktopShell: boolean;
  onOpenFolder: (path?: string) => void;
  onOpenFile: (path?: string) => void;
  onOpenPreferences: () => void;
  onOpenCommands: () => void;
  onOpenTerminal: () => void;
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
          <div className="welcome-kicker">Start</div>
          <div className="welcome-title">Open a workspace.</div>
          <div className="welcome-subtitle">
            Keep the chrome out of the way. Open content and work.
          </div>

          <div className="welcome-command-list">
            {desktopShell ? (
              <>
                <WelcomeCommand label="Open folder…" hint="Open" onClick={() => onOpenFolder()} />
                <WelcomeCommand label="Open file…" hint="Open" onClick={() => onOpenFile()} />
                {recent[0] && (
                  <WelcomeCommand label={`Reopen ${projectLabel(recent[0].path)}`} hint="Recent" onClick={() => onOpenFolder(recent[0].path)} />
                )}
              </>
            ) : (
              <WelcomeCommand label="Manage machines…" hint="Remote" onClick={onOpenPreferences} />
            )}
            <WelcomeCommand label="Command palette…" hint="Cmd/Ctrl+Shift+P" onClick={onOpenCommands} />
            <WelcomeCommand label="Settings…" hint="Cmd/Ctrl+," onClick={onOpenPreferences} />
            <WelcomeCommand label="Toggle terminal" hint="Panel" onClick={onOpenTerminal} />
          </div>

          <div className="welcome-note-list">
            <div className="welcome-note">Machine links and recent workspaces stay on the right.</div>
            <div className="welcome-note">Use the activity strip for explorer, git, search, and issues.</div>
            <div className="welcome-note">The editor stays central; the shell stays quiet.</div>
          </div>
        </div>

        <div className="welcome-aside">
          <MachineConnectionsPanel compact />

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
    </div>
  );
}

function WelcomeCommand({ label, hint, onClick }: { label: string; hint: string; onClick: () => void }) {
  return (
    <button className="welcome-command" onClick={onClick}>
      <span>{label}</span>
      <span className="welcome-command-hint">{hint}</span>
    </button>
  );
}
