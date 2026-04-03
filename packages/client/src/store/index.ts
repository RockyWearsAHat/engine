import { create } from 'zustand';
import type { Session, Message, FileNode, GitStatus } from '@myeditor/shared';

export interface ToolCallDisplay {
  id: string;
  name: string;
  input: unknown;
  result?: unknown;
  isError?: boolean;
  pending: boolean;
}

export interface ChatMessage {
  id: string;
  role: 'user' | 'assistant';
  content: string;
  toolCalls: ToolCallDisplay[];
  streaming: boolean;
}

export interface OpenFile {
  path: string;
  content: string;
  language: string;
  dirty: boolean;
}

interface EditorStore {
  // Connection
  connected: boolean;
  setConnected: (v: boolean) => void;

  // Sessions
  sessions: Session[];
  activeSession: Session | null;
  setSessions: (s: Session[]) => void;
  setActiveSession: (s: Session | null) => void;

  // Chat
  chatMessages: ChatMessage[];
  streamingMessageId: string | null;
  addUserMessage: (id: string, content: string) => void;
  startAssistantMessage: (id: string) => void;
  appendChunk: (id: string, text: string) => void;
  finalizeMessage: (id: string) => void;
  addToolCall: (msgId: string, tc: ToolCallDisplay) => void;
  resolveToolCall: (msgId: string, name: string, result: unknown, isError: boolean) => void;
  setMessages: (msgs: Message[]) => void;

  // File tree
  fileTree: FileNode | null;
  setFileTree: (tree: FileNode | null) => void;

  // Open files
  openFiles: OpenFile[];
  activeFilePath: string | null;
  openFile: (path: string, content: string, language: string) => void;
  closeFile: (path: string) => void;
  setActiveFile: (path: string) => void;
  markFileDirty: (path: string, content: string) => void;
  markFileSaved: (path: string) => void;

  // Git
  gitStatus: GitStatus | null;
  setGitStatus: (s: GitStatus | null) => void;

  // Layout
  showFileTree: boolean;
  showTerminal: boolean;
  bottomPanel: 'chat' | 'terminal';
  toggleFileTree: () => void;
  toggleTerminal: () => void;
  setBottomPanel: (p: 'chat' | 'terminal') => void;
}

export const useStore = create<EditorStore>((set, get) => ({
  connected: false,
  setConnected: (v) => set({ connected: v }),

  sessions: [],
  activeSession: null,
  setSessions: (sessions) => set({ sessions }),
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

  addToolCall: (msgId, tc) => set(s => ({
    chatMessages: s.chatMessages.map(m =>
      m.id === msgId ? { ...m, toolCalls: [...m.toolCalls, tc] } : m
    ),
  })),

  resolveToolCall: (msgId, name, result, isError) => set(s => ({
    chatMessages: s.chatMessages.map(m =>
      m.id === msgId
        ? {
            ...m,
            toolCalls: m.toolCalls.map(tc =>
              tc.name === name && tc.pending ? { ...tc, result, isError, pending: false } : tc
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
    set({ chatMessages });
  },

  fileTree: null,
  setFileTree: (tree) => set({ fileTree: tree }),

  openFiles: [],
  activeFilePath: null,

  openFile: (path, content, language) => {
    const existing = get().openFiles.find(f => f.path === path);
    if (!existing) {
      set(s => ({ openFiles: [...s.openFiles, { path, content, language, dirty: false }], activeFilePath: path }));
    } else {
      set({ activeFilePath: path });
    }
  },

  closeFile: (path) => set(s => {
    const files = s.openFiles.filter(f => f.path !== path);
    const active = s.activeFilePath === path
      ? (files[files.length - 1]?.path ?? null)
      : s.activeFilePath;
    return { openFiles: files, activeFilePath: active };
  }),

  setActiveFile: (path) => set({ activeFilePath: path }),

  markFileDirty: (path, content) => set(s => ({
    openFiles: s.openFiles.map(f => f.path === path ? { ...f, content, dirty: true } : f),
  })),

  markFileSaved: (path) => set(s => ({
    openFiles: s.openFiles.map(f => f.path === path ? { ...f, dirty: false } : f),
  })),

  gitStatus: null,
  setGitStatus: (s) => set({ gitStatus: s }),

  showFileTree: true,
  showTerminal: true,
  bottomPanel: 'chat',
  toggleFileTree: () => set(s => ({ showFileTree: !s.showFileTree })),
  toggleTerminal: () => set(s => ({ showTerminal: !s.showTerminal })),
  setBottomPanel: (p) => set({ bottomPanel: p }),
}));
