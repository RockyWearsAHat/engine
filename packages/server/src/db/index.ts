import Database from 'better-sqlite3';
import path from 'node:path';
import fs from 'node:fs';
import type { Session, Message, ToolCall } from '@myeditor/shared';

let _db: Database.Database | null = null;

export function initDb(projectPath: string): void {
  const dbDir = path.join(projectPath, '.myeditor');
  fs.mkdirSync(dbDir, { recursive: true });
  const dbPath = path.join(dbDir, 'state.db');

  _db = new Database(dbPath);
  _db.pragma('journal_mode = WAL');
  _db.pragma('foreign_keys = ON');

  _db.exec(`
    CREATE TABLE IF NOT EXISTS sessions (
      id TEXT PRIMARY KEY,
      project_path TEXT NOT NULL,
      branch_name TEXT NOT NULL DEFAULT 'main',
      summary TEXT NOT NULL DEFAULT '',
      created_at TEXT NOT NULL,
      updated_at TEXT NOT NULL
    );

    CREATE TABLE IF NOT EXISTS messages (
      id TEXT PRIMARY KEY,
      session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
      role TEXT NOT NULL CHECK(role IN ('user', 'assistant')),
      content TEXT NOT NULL,
      tool_calls TEXT,
      created_at TEXT NOT NULL
    );

    CREATE TABLE IF NOT EXISTS tool_log (
      id TEXT PRIMARY KEY,
      session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
      name TEXT NOT NULL,
      input TEXT NOT NULL,
      result TEXT,
      is_error INTEGER NOT NULL DEFAULT 0,
      duration_ms INTEGER NOT NULL DEFAULT 0,
      created_at TEXT NOT NULL
    );

    CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, created_at);
    CREATE INDEX IF NOT EXISTS idx_tool_log_session ON tool_log(session_id, created_at);
    CREATE INDEX IF NOT EXISTS idx_sessions_project ON sessions(project_path, updated_at);
  `);
}

function db(): Database.Database {
  if (!_db) throw new Error('Database not initialized — call initDb() first');
  return _db;
}

export function createSession(id: string, projectPath: string, branchName: string): void {
  const now = new Date().toISOString();
  db().prepare(`
    INSERT INTO sessions (id, project_path, branch_name, summary, created_at, updated_at)
    VALUES (?, ?, ?, '', ?, ?)
  `).run(id, projectPath, branchName, now, now);
}

export function getSession(id: string): Session | undefined {
  const row = db().prepare(`
    SELECT s.*, COUNT(m.id) as message_count
    FROM sessions s
    LEFT JOIN messages m ON m.session_id = s.id
    WHERE s.id = ?
    GROUP BY s.id
  `).get(id) as RawSession | undefined;
  return row ? toSession(row) : undefined;
}

export function listSessions(projectPath: string): Session[] {
  const rows = db().prepare(`
    SELECT s.*, COUNT(m.id) as message_count
    FROM sessions s
    LEFT JOIN messages m ON m.session_id = s.id
    WHERE s.project_path = ?
    GROUP BY s.id
    ORDER BY s.updated_at DESC
  `).all(projectPath) as RawSession[];
  return rows.map(toSession);
}

export function updateSessionSummary(id: string, summary: string): void {
  db().prepare('UPDATE sessions SET summary = ?, updated_at = ? WHERE id = ?')
    .run(summary, new Date().toISOString(), id);
}

export function saveMessage(
  id: string,
  sessionId: string,
  role: 'user' | 'assistant',
  content: string,
  toolCalls?: ToolCall[]
): void {
  const now = new Date().toISOString();
  db().prepare(`
    INSERT INTO messages (id, session_id, role, content, tool_calls, created_at)
    VALUES (?, ?, ?, ?, ?, ?)
  `).run(id, sessionId, role, content, toolCalls ? JSON.stringify(toolCalls) : null, now);
  db().prepare('UPDATE sessions SET updated_at = ? WHERE id = ?').run(now, sessionId);
}

export function getMessages(sessionId: string): Message[] {
  const rows = db().prepare(
    'SELECT * FROM messages WHERE session_id = ? ORDER BY created_at ASC'
  ).all(sessionId) as RawMessage[];
  return rows.map(toMessage);
}

export function logToolCall(
  id: string,
  sessionId: string,
  name: string,
  input: unknown,
  result: unknown,
  isError: boolean,
  durationMs: number
): void {
  db().prepare(`
    INSERT INTO tool_log (id, session_id, name, input, result, is_error, duration_ms, created_at)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?)
  `).run(
    id, sessionId, name,
    JSON.stringify(input), JSON.stringify(result),
    isError ? 1 : 0, durationMs,
    new Date().toISOString()
  );
}

// ── Raw DB row types ──────────────────────────────────────────────────────────

interface RawSession {
  id: string;
  project_path: string;
  branch_name: string;
  summary: string;
  created_at: string;
  updated_at: string;
  message_count: number;
}

interface RawMessage {
  id: string;
  session_id: string;
  role: string;
  content: string;
  tool_calls: string | null;
  created_at: string;
}

function toSession(r: RawSession): Session {
  return {
    id: r.id,
    projectPath: r.project_path,
    branchName: r.branch_name,
    summary: r.summary,
    createdAt: r.created_at,
    updatedAt: r.updated_at,
    messageCount: r.message_count,
  };
}

function toMessage(r: RawMessage): Message {
  return {
    id: r.id,
    sessionId: r.session_id,
    role: r.role as 'user' | 'assistant',
    content: r.content,
    toolCalls: r.tool_calls ? (JSON.parse(r.tool_calls) as ToolCall[]) : undefined,
    createdAt: r.created_at,
  };
}
