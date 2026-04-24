import { create } from 'zustand';
import type { Session, Message, FileNode, GitStatus, GitHubUser, GitHubIssue, AgentSession, LiveToolCall, TabInfo, SearchResult } from '@engine/shared';
import { wsClient } from '../ws/client';
import {
  DEFAULT_EDITOR_PREFERENCES,
  type EditorPreferences,
} from '../editorPreferences.js';

export interface ToolCallDisplay {
  id: string;
  name: string;
  input: unknown;
  result?: unknown;
  isError?: boolean;
  pending: boolean;
  durationMs?: number;
}

export interface ChatMessage {
  id: string;
  role: 'user' | 'assistant';
  content: string;
  toolCalls: ToolCallDisplay[];
  streaming: boolean;
  failed?: boolean;
}

export interface OpenFile {
  path: string;
  content: string;
  language: string;
  size: number;
  largeFile: boolean;
  dirty: boolean;
}

interface EditorStore {
  // Connection
  connected: boolean;
  setConnected: (v: boolean) => void;

  // Sessions
  sessions: Session[];
  activeSession: Session | null;
  setSessions: (s: Session[] | ((prev: Session[]) => Session[])) => void;
  setActiveSession: (s: Session | null) => void;

  // Chat
  chatMessages: ChatMessage[];
  streamingMessageId: string | null;
  addUserMessage: (id: string, content: string) => void;
  startAssistantMessage: (id: string) => void;
  appendChunk: (id: string, text: string) => void;
  finalizeMessage: (id: string) => void;
  addToolCall: (msgId: string, tc: ToolCallDisplay) => void;
  resolveToolCall: (msgId: string, toolId: string, result: unknown, isError: boolean, durationMs?: number) => void;
  markMessageFailed: (id: string) => void;
  setMessages: (msgs: Message[]) => void;

  // File tree
  fileTree: FileNode | null;
  setFileTree: (tree: FileNode | null) => void;
  mergeFileTree: (tree: FileNode) => void;

  // Open files
  openFiles: OpenFile[];
  activeFilePath: string | null;
  openFile: (path: string, content: string, language: string, size: number) => void;
  closeFile: (path: string) => void;
  clearOpenFiles: () => void;
  setActiveFile: (path: string) => void;
  syncFileContent: (path: string, content: string) => void;
  markFileDirty: (path: string) => void;
  markFileSaved: (path: string) => void;

  // Editor preferences
  editorPreferences: EditorPreferences;
  setEditorPreferences: (settings: EditorPreferences) => void;

  // Git
  gitStatus: GitStatus | null;
  setGitStatus: (s: GitStatus | null) => void;

  // GitHub
  githubToken: string | null;
  githubUser: GitHubUser | null;
  setGithubToken: (t: string | null) => void;
  setGithubUser: (u: GitHubUser | null) => void;

  // GitHub Issues
  githubIssues: GitHubIssue[];
  githubIssuesLoading: boolean;
  githubIssuesError: string | null;
  setGithubIssues: (issues: GitHubIssue[], loading?: boolean) => void;
  setGithubIssuesLoading: (loading: boolean) => void;
  setGithubIssuesError: (error: string | null) => void;

  // Search
  searchQuery: string;
  searchResults: SearchResult[];
  searchLoading: boolean;
  searchError: string | null;
  setSearchQuery: (query: string) => void;
  setSearchLoading: (loading: boolean) => void;
  setSearchResults: (query: string, results: SearchResult[], error?: string | null) => void;
  clearSearch: () => void;

  // Agent monitor
  agentSessions: AgentSession[];
  activeAgentSessionId: string | null;
  updateAgentSession: (id: string, patch: Partial<AgentSession>) => void;
  addLiveToolCall: (sessionId: string, tc: LiveToolCall) => void;
  resolveLiveToolCall: (sessionId: string, toolId: string, result: unknown, isError: boolean, durationMs: number) => void;
  setActiveAgentSession: (id: string | null) => void;

  // File visibility
  showDotfiles: boolean;
  toggleDotfiles: () => void;
}

// Pushes current open tab state to the Go server so the agent can introspect it.
function syncTabs(get: () => EditorStore): void {
  const { openFiles, activeFilePath } = get();
  const tabs: TabInfo[] = openFiles.map(f => ({
    path: f.path,
    isActive: f.path === activeFilePath,
    isDirty: f.dirty,
  }));
  wsClient.send({ type: 'editor.tabs.sync', tabs });
}

function mergeAgentSessions(sessions: Session[], previous: AgentSession[]): AgentSession[] {
  return sessions.map(session => {
    const existing = previous.find(agentSession => agentSession.id === session.id);
    if (!existing) {
      return {
        ...session,
        isActive: false,
        isStreaming: false,
        currentActivity: '',
        recentToolCalls: [],
      };
    }
    return {
      ...existing,
      ...session,
    };
  });
}

function mergeFileTreeNode(current: FileNode | null, next: FileNode): FileNode {
  /* istanbul ignore start */
  if (!current) return next;
  /* istanbul ignore stop */

  // Same node — preserve previously-loaded children that the shallow refresh lost
  if (current.path === next.path) {
    if (
      current.type === 'directory' &&
      next.type === 'directory' &&
      current.children?.length &&
      next.children?.length
    ) {
      const nextByPath = new Map(next.children.map(c => [c.path, c]));
      const preserved: FileNode[] = [];
      for (const child of next.children) {
        const prev = current.children?.find(c => c.path === child.path);
        // If the previous child was loaded (has deep children), keep that data
        if (prev && prev.type === 'directory' && prev.loaded && prev.children?.length && !child.loaded) {
          preserved.push(prev);
        } else {
          preserved.push(child);
        }
      }
      // Keep children from current that are not in next (shouldn't happen, but safe)
      return { ...next, children: preserved };
    }
    return next;
  }

  if (current.type !== 'directory' || !current.children?.length) {
    return current;
  }

  let replaced = false;
  const children = current.children.map((child) => {
    const merged = mergeFileTreeNode(child, next);
    if (merged !== child) {
      replaced = true;
    }
    return merged;
  });

  if (!replaced) {
    return current;
  }

  return {
    ...current,
    children,
  };
}

function isPathWithinTree(rootPath: string, nextPath: string): boolean {
  return nextPath === rootPath
    || nextPath.startsWith(`${rootPath}/`)
    || nextPath.startsWith(`${rootPath}\\`);
}

function parentPathForTreeNode(path: string): string {
  const separatorIndex = Math.max(path.lastIndexOf('/'), path.lastIndexOf('\\'));
  return separatorIndex === -1 ? '' : path.slice(0, separatorIndex);
}

function compareTreeNodes(a: FileNode, b: FileNode): number {
  if (a.type !== b.type) {
    return a.type === 'directory' ? -1 : 1;
  }
  return a.name.localeCompare(b.name);
}

function attachFileTreeNode(current: FileNode, next: FileNode): FileNode {
  if (current.type !== 'directory') {
    return current;
  }

  const parentPath = parentPathForTreeNode(next.path);
  if (current.path === parentPath) {
    const existingChildren = current.children ?? [];
    const nextChildren = existingChildren
      .filter((child) => child.path !== next.path)
      .concat(next)
      .sort(compareTreeNodes);

    return {
      ...current,
      children: nextChildren,
    };
  }

  if (!current.children?.length) {
    return current;
  }

  let changed = false;
  const children = current.children.map((child) => {
    const attached = attachFileTreeNode(child, next);
    if (attached !== child) {
      changed = true;
    }
    return attached;
  });

  if (!changed) {
    return current;
  }

  return {
    ...current,
    children,
  };
}

export const useStore = create<EditorStore>((set, get) => ({
  connected: false,
  setConnected: (v) => set({ connected: v }),

  sessions: [],
  activeSession: null,
  setSessions: (sessions) => set(s => {
    const nextSessions = typeof sessions === 'function' ? sessions(s.sessions) : sessions;
    return {
      sessions: nextSessions,
      agentSessions: mergeAgentSessions(nextSessions, s.agentSessions),
    };
  }),
  setActiveSession: (s) => set({ activeSession: s }),

  chatMessages: [],
  streamingMessageId: null,

  addUserMessage: (id, content) => set(s => ({
    chatMessages: [...s.chatMessages, { id, role: 'user', content, toolCalls: [], streaming: false }],
  })),

  startAssistantMessage: (id) => set(s => ({
    chatMessages: [...s.chatMessages, { id, role: 'assistant', content: '', toolCalls: [], streaming: true }],
    streamingMessageId: id,
  })),

  appendChunk: (id, text) => set(s => ({
    chatMessages: s.chatMessages.map(m =>
      m.id === id ? { ...m, content: m.content + text } : m
    ),
  })),

  finalizeMessage: (id) => set(s => ({
    chatMessages: s.chatMessages.map(m => m.id === id ? { ...m, streaming: false } : m),
    streamingMessageId: null,
  })),

  markMessageFailed: (id) => set(s => ({
    chatMessages: s.chatMessages.map(m => m.id === id ? { ...m, streaming: false, failed: true } : m),
    streamingMessageId: null,
  })),

  addToolCall: (msgId, tc) => set(s => ({
    chatMessages: s.chatMessages.map(m =>
      m.id === msgId ? { ...m, toolCalls: [...m.toolCalls, tc] } : m
    ),
  })),

  resolveToolCall: (msgId, toolId, result, isError, durationMs) => set(s => ({
    chatMessages: s.chatMessages.map(m =>
      m.id === msgId
        ? {
            ...m,
            toolCalls: m.toolCalls.map(tc =>
              tc.id === toolId ? { ...tc, result, isError, pending: false, durationMs } : tc
            ),
          }
        : m
    ),
  })),

  setMessages: (msgs) => {
    const chatMessages: ChatMessage[] = msgs.map(m => ({
      id: m.id,
      role: m.role,
      content: m.content,
      toolCalls: (m.toolCalls ?? []).map(tc => ({
        id: tc.id,
        name: tc.name,
        input: tc.input,
        result: tc.result,
        isError: tc.isError,
        pending: false,
      })),
      streaming: false,
    }));
    set({ chatMessages, streamingMessageId: null });
  },

  fileTree: null,
  setFileTree: (tree) => set({ fileTree: tree }),
  mergeFileTree: (tree) => set((state) => {
    if (!state.fileTree) {
      return { fileTree: tree };
    }

    const merged = mergeFileTreeNode(state.fileTree, tree);
    if (merged !== state.fileTree) {
      return { fileTree: merged };
    }

    if (!isPathWithinTree(state.fileTree.path, tree.path)) {
      return { fileTree: tree };
    }

    return {
      fileTree: attachFileTreeNode(state.fileTree, tree),
    };
  }),

  openFiles: [],
  activeFilePath: null,

  openFile: (path, content, language, size) => {
    const largeFile = size >= 2 * 1024 * 1024;
    const existing = get().openFiles.find(f => f.path === path);
    if (!existing) {
      set(s => ({ openFiles: [...s.openFiles, { path, content, language, size, largeFile, dirty: false }], activeFilePath: path }));
    } else {
      set(s => ({
        openFiles: s.openFiles.map(f => (
          f.path === path ? { ...f, content, language, size, largeFile } : f
        )),
        activeFilePath: path,
      }));
    }
    syncTabs(get);
  },

  closeFile: (path) => set(s => {
    const files = s.openFiles.filter(f => f.path !== path);
    const active = s.activeFilePath === path
      ? (files[files.length - 1]?.path ?? null)
      : s.activeFilePath;
    setTimeout(() => syncTabs(get), 0);
    return { openFiles: files, activeFilePath: active };
  }),

  clearOpenFiles: () => {
    set({ openFiles: [], activeFilePath: null });
    syncTabs(get);
  },

  setActiveFile: (path) => {
    set({ activeFilePath: path });
    syncTabs(get);
  },

  syncFileContent: (path, content) => set(s => ({
    openFiles: s.openFiles.map(f => f.path === path ? { ...f, content } : f),
  })),

  markFileDirty: (path) => {
    const state = get();
    const target = state.openFiles.find((file) => file.path === path);
    if (!target || target.dirty) {
      return;
    }
    set({
      openFiles: state.openFiles.map((file) => (
        file.path === path ? { ...file, dirty: true } : file
      )),
    });
    syncTabs(get);
  },

  markFileSaved: (path) => {
    const state = get();
    const target = state.openFiles.find((file) => file.path === path);
    if (!target || !target.dirty) {
      return;
    }
    set({
      openFiles: state.openFiles.map((file) => (
        file.path === path ? { ...file, dirty: false } : file
      )),
    });
    syncTabs(get);
  },

  editorPreferences: DEFAULT_EDITOR_PREFERENCES,
  setEditorPreferences: (settings) => set({ editorPreferences: settings }),

  gitStatus: null,
  setGitStatus: (s) => set({ gitStatus: s }),

  githubToken: null,
  githubUser: null,
  setGithubToken: (t) => set({ githubToken: t }),
  setGithubUser: (u) => set({ githubUser: u }),

  githubIssues: [],
  githubIssuesLoading: false,
  githubIssuesError: null,
  setGithubIssues: (issues, loading = false) => set({ githubIssues: issues, githubIssuesLoading: loading }),
  setGithubIssuesLoading: (loading) => set({ githubIssuesLoading: loading }),
  setGithubIssuesError: (error) => set({ githubIssuesError: error }),

  searchQuery: '',
  searchResults: [],
  searchLoading: false,
  searchError: null,
  setSearchQuery: (query) => set({ searchQuery: query }),
  setSearchLoading: (loading) => set({ searchLoading: loading }),
  setSearchResults: (query, results, error = null) => set({
    searchQuery: query,
    searchResults: results,
    searchLoading: false,
    searchError: error,
  }),
  clearSearch: () => set({
    searchQuery: '',
    searchResults: [],
    searchLoading: false,
    searchError: null,
  }),

  agentSessions: [],
  activeAgentSessionId: null,
  setActiveAgentSession: (id) => set({ activeAgentSessionId: id }),

  updateAgentSession: (id, patch) => set(s => {
    const existing = s.agentSessions.find(agentSession => agentSession.id === id);
    if (existing) {
      return {
        agentSessions: s.agentSessions.map(agentSession =>
          agentSession.id === id ? { ...agentSession, ...patch } : agentSession
        ),
      };
    }
    const session = s.sessions.find(sessionItem => sessionItem.id === id);
    if (!session) {
      return {};
    }
    return {
      agentSessions: [
        ...s.agentSessions,
        {
          ...session,
          isActive: false,
          isStreaming: false,
          currentActivity: '',
          recentToolCalls: [],
          ...patch,
        },
      ],
    };
  }),

  addLiveToolCall: (sessionId, tc) => set(s => ({
    agentSessions: s.agentSessions.map(a =>
      a.id === sessionId
        ? { ...a, recentToolCalls: [...a.recentToolCalls.slice(-19), tc], currentActivity: `${tc.name}...`, isStreaming: true }
        : a
    ),
  })),

  resolveLiveToolCall: (sessionId, toolId, result, isError, durationMs) => set(s => ({
    agentSessions: s.agentSessions.map(a =>
      a.id === sessionId
        ? {
            ...a,
            recentToolCalls: a.recentToolCalls.map(tc =>
              tc.id === toolId ? { ...tc, result, isError, pending: false, durationMs } : tc
            ),
          }
        : a
    ),
  })),

  showDotfiles: false,
  toggleDotfiles: () => set(s => ({ showDotfiles: !s.showDotfiles })),

}));
