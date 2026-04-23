// Session types
export interface Session {
  id: string;
  projectPath: string;
  branchName: string;
  createdAt: string;
  updatedAt: string;
  projectDirection?: string;
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
  loaded?: boolean;
  hasChildren?: boolean;
  size?: number;
  modified?: string;
}

export interface FileContent {
  path: string;
  content: string;
  language: string;
  size: number;
}

export interface ApprovalRequest {
  id: string;
  sessionId: string;
  kind: 'shell' | 'git_commit';
  title: string;
  message: string;
  command: string;
}

export interface SearchResult {
  path: string;
  line: number;
  column?: number;
  preview: string;
}

export interface RuntimeConfig {
  githubToken?: string | null;
  githubOwner?: string | null;
  githubRepo?: string | null;
  anthropicKey?: string | null;
  openaiKey?: string | null;
  modelProvider?: string | null;
  ollamaBaseUrl?: string | null;
  model?: string | null;
}

// Discord control plane types (kept separate from RuntimeConfig because
// Discord config is persisted to `.engine/discord.json` rather than
// mirrored into env vars).
export interface DiscordConfig {
  enabled: boolean;
  /** Outgoing field only — when sending, null/'' means "keep current". */
  botToken: string;
  /** Read-only server-side mask for display. */
  botTokenMasked?: string;
  guildId: string;
  allowedUserIds: string[];
  commandPrefix: string;
  controlChannelName: string;
  hasToken?: boolean;
}

export interface DiscordValidationResult {
  ok: boolean;
  enabled: boolean;
  guildName?: string;
  botTag?: string;
  errors: string[];
  warnings: string[];
}

export interface DiscordMessageRecord {
  id: string;
  projectPath: string;
  channelId: string;
  threadId: string;
  sessionId: string;
  authorId: string;
  authorName: string;
  direction: 'in' | 'out';
  kind: string;
  content: string;
  createdAt: string;
}

export interface DiscordSearchHit extends DiscordMessageRecord {
  snippet: string;
}

// Git types
export interface GitStatus {
  branch: string;
  staged: string[];
  unstaged: string[];
  untracked: string[];
  ignored: string[];
  ahead: number;
  behind: number;
}

export interface GitCommit {
  hash: string;
  message: string;
  author: string;
  date: string;
}

export interface WorkspaceTask {
  id: string;
  label: string;
  command: string;
  kind: 'build' | 'run' | 'check' | 'test';
  source: 'package-json' | 'go' | 'cargo';
  description?: string;
}

// Terminal types
export interface TerminalInfo {
  id: string;
  cwd: string;
  pid: number;
}

// WebSocket protocol — Client → Server
export type ClientMessage =
  | { type: 'project.open'; path: string }
  | { type: 'chat'; sessionId: string; content: string }
  | { type: 'chat.stop'; sessionId: string }
  | { type: 'session.create'; projectPath: string }
  | { type: 'session.load'; sessionId: string }
  | { type: 'session.list' }
  | { type: 'file.read'; path: string }
  | { type: 'file.save'; path: string; content: string }
  | { type: 'file.create'; path: string }
  | { type: 'folder.create'; path: string }
  | { type: 'file.tree'; path: string }
  | { type: 'file.search'; query: string; root?: string; fileGlob?: string }
  | { type: 'git.status' }
  | { type: 'git.diff'; path?: string }
  | { type: 'git.log'; limit?: number }
  | { type: 'git.commit'; message: string }
  | { type: 'workspace.tasks'; path?: string }
  | { type: 'config.sync'; config: RuntimeConfig }
  | { type: 'github.user' }
  | { type: 'github.issues'; projectPath: string }
  | { type: 'approval.respond'; id: string; allow: boolean }
  | { type: 'terminal.create'; cwd: string }
  | { type: 'terminal.input'; terminalId: string; data: string }
  | { type: 'terminal.resize'; terminalId: string; cols: number; rows: number }
  | { type: 'terminal.close'; terminalId: string }
  | { type: 'editor.tabs.sync'; tabs: TabInfo[] }
  | { type: 'engine.config.get' }
  | { type: 'engine.team.set'; team: string; provider: string; model: string };

// WebSocket protocol — Server → Client
export type ServerMessage =
  | { type: 'chat.started'; sessionId: string }
  | { type: 'chat.chunk'; sessionId: string; content: string; done: boolean }
  | { type: 'chat.tool_call'; sessionId: string; name: string; input: unknown }
  | { type: 'chat.tool_result'; sessionId: string; name: string; result: unknown; isError: boolean }
  | { type: 'chat.error'; sessionId: string; error: string }
  | { type: 'session.list'; sessions: Session[] }
  | { type: 'session.created'; session: Session }
  | { type: 'session.updated'; session: Session }
  | { type: 'session.loaded'; session: Session; messages: Message[] }
  | { type: 'file.content'; path: string; content: string; language: string; size: number }
  | { type: 'file.saved'; path: string }
  | { type: 'file.tree'; tree: FileNode }
  | { type: 'search.results'; query: string; results: SearchResult[]; error?: string }
  | { type: 'git.status'; status: GitStatus }
  | { type: 'git.diff'; path?: string; diff: string }
  | { type: 'git.log'; commits: GitCommit[] }
  | { type: 'git.commit.result'; ok: boolean; hash?: string; message: string }
  | { type: 'workspace.tasks'; tasks: WorkspaceTask[]; defaultBuildTaskId?: string | null; defaultRunTaskId?: string | null }
  | { type: 'github.user'; user: GitHubUser | null; error?: string }
  | { type: 'github.issues'; issues: GitHubIssue[]; error?: string }
  | { type: 'approval.request'; request: ApprovalRequest }
  | { type: 'terminal.created'; terminalId: string; cwd: string }
  | { type: 'terminal.output'; terminalId: string; data: string }
  | { type: 'terminal.closed'; terminalId: string }
  | { type: 'editor.open'; path: string }
  | { type: 'editor.tab.close'; path: string }
  | { type: 'editor.tab.focus'; path: string }
  | { type: 'file.created'; path: string }
  | { type: 'folder.created'; path: string }
  | { type: 'error'; message: string; code?: string }
  | { type: 'engine.config'; yaml: string; error?: string }
  | { type: 'engine.team.updated'; team: string };

// Tab and system info types
export interface TabInfo {
  path: string;
  isActive: boolean;
  isDirty: boolean;
}

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

// Engine orchestration — team config types
export interface EngineAgentModel {
  model: string;
  modelDisplay: string;
}

export interface EngineTeamConfig {
  name: string;
  description: string;
  orchestrator: EngineAgentModel;
  architect: EngineAgentModel;
  implementer: EngineAgentModel;
  tester: EngineAgentModel;
  documenter: EngineAgentModel;
}

export interface EngineConfig {
  teams: Record<string, EngineTeamConfig>;
  activeTeam?: string;
}

// Remote connection types
export interface RemoteServerInfo {
  host: string;
  port: string;
  name?: string;
}

export interface PairingResult {
  ok: boolean;
  token?: string;
  deviceId?: string;
  error?: string;
}
