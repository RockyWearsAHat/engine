// Session types
export interface Session {
  id: string;
  projectPath: string;
  branchName: string;
  createdAt: string;
  updatedAt: string;
  summary: string;
  messageCount: number;
}

export interface Message {
  id: string;
  sessionId: string;
  role: 'user' | 'assistant';
  content: string;
  toolCalls?: ToolCall[];
  createdAt: string;
}

export interface ToolCall {
  id: string;
  name: string;
  input: unknown;
  result?: unknown;
  isError?: boolean;
}

// File system types
export interface FileNode {
  name: string;
  path: string;
  type: 'file' | 'directory';
  children?: FileNode[];
  size?: number;
  modified?: string;
}

export interface FileContent {
  path: string;
  content: string;
  language: string;
  size: number;
}

// Git types
export interface GitStatus {
  branch: string;
  staged: string[];
  unstaged: string[];
  untracked: string[];
  ahead: number;
  behind: number;
}

export interface GitCommit {
  hash: string;
  message: string;
  author: string;
  date: string;
}

// Terminal types
export interface TerminalInfo {
  id: string;
  cwd: string;
  pid: number;
}

// WebSocket protocol — Client → Server
export type ClientMessage =
  | { type: 'chat'; sessionId: string; content: string }
  | { type: 'chat.stop'; sessionId: string }
  | { type: 'session.create'; projectPath: string }
  | { type: 'session.load'; sessionId: string }
  | { type: 'session.list' }
  | { type: 'file.read'; path: string }
  | { type: 'file.save'; path: string; content: string }
  | { type: 'file.tree'; path: string }
  | { type: 'git.status' }
  | { type: 'git.diff'; path?: string }
  | { type: 'git.log'; limit?: number }
  | { type: 'github.issues'; projectPath: string }
  | { type: 'terminal.create'; cwd: string }
  | { type: 'terminal.input'; terminalId: string; data: string }
  | { type: 'terminal.resize'; terminalId: string; cols: number; rows: number }
  | { type: 'terminal.close'; terminalId: string };

// WebSocket protocol — Server → Client
export type ServerMessage =
  | { type: 'chat.chunk'; sessionId: string; content: string; done: boolean }
  | { type: 'chat.tool_call'; sessionId: string; name: string; input: unknown }
  | { type: 'chat.tool_result'; sessionId: string; name: string; result: unknown; isError: boolean }
  | { type: 'chat.error'; sessionId: string; error: string }
  | { type: 'session.list'; sessions: Session[] }
  | { type: 'session.created'; session: Session }
  | { type: 'session.loaded'; session: Session; messages: Message[] }
  | { type: 'file.content'; path: string; content: string; language: string }
  | { type: 'file.saved'; path: string }
  | { type: 'file.tree'; tree: FileNode }
  | { type: 'git.status'; status: GitStatus }
  | { type: 'git.diff'; path?: string; diff: string }
  | { type: 'git.log'; commits: GitCommit[] }
  | { type: 'github.issues'; issues: GitHubIssue[]; error?: string }
  | { type: 'terminal.created'; terminalId: string; cwd: string }
  | { type: 'terminal.output'; terminalId: string; data: string }
  | { type: 'terminal.closed'; terminalId: string }
  | { type: 'editor.open'; path: string }
  | { type: 'error'; message: string; code?: string };

// GitHub types
export interface GitHubUser {
  login: string;
  name: string;
  avatarUrl: string;
}

export interface GitHubIssue {
  number: number;
  title: string;
  body: string;
  htmlUrl: string;
  state: 'open' | 'closed';
  author: string;
  labels: { name: string; color: string }[];
  createdAt: string;
  updatedAt: string;
}

export interface GitHubRepo {
  name: string;
  fullName: string;
  htmlUrl: string;
  cloneUrl: string;
  defaultBranch: string;
  private: boolean;
}

// Agent monitor types
export interface AgentSession {
  id: string;
  projectPath: string;
  branchName: string;
  summary: string;
  messageCount: number;
  createdAt: string;
  updatedAt: string;
  // Live state (not persisted)
  isActive: boolean;
  isStreaming: boolean;
  currentActivity: string;
  recentToolCalls: LiveToolCall[];
}

export interface LiveToolCall {
  id: string;
  name: string;
  input: unknown;
  result?: unknown;
  isError?: boolean;
  pending: boolean;
  startedAt: number;
  durationMs?: number;
}
