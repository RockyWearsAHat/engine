import { randomUUID } from 'node:crypto';
import type { WebSocket } from '@fastify/websocket';
import type { ClientMessage, ServerMessage } from '@myeditor/shared';
import * as db from '../db/index.js';
import * as fsOps from '../fs/index.js';
import * as gitOps from '../git/index.js';
import { TerminalManager } from '../terminal/manager.js';
import { chat } from '../ai/context.js';

const terminalManager = new TerminalManager();

// Per-connection state
interface ConnectionState {
  projectPath: string | null;
  sessionId: string | null;
  terminalIds: Set<string>;
}

export function handleConnection(ws: WebSocket, defaultProjectPath: string): void {
  const state: ConnectionState = {
    projectPath: defaultProjectPath,
    sessionId: null,
    terminalIds: new Set(),
  };

  function send(msg: ServerMessage): void {
    if (ws.readyState === ws.OPEN) {
      ws.send(JSON.stringify(msg));
    }
  }

  ws.on('message', async (raw: Buffer) => {
    let msg: ClientMessage;
    try {
      msg = JSON.parse(raw.toString()) as ClientMessage;
    } catch {
      send({ type: 'error', message: 'Invalid JSON', code: 'INVALID_JSON' });
      return;
    }

    const projectPath = state.projectPath ?? defaultProjectPath;

    try {
      switch (msg.type) {

        case 'session.list': {
          const sessions = db.listSessions(projectPath);
          send({ type: 'session.list', sessions });
          break;
        }

        case 'session.create': {
          const id = randomUUID();
          const branch = await gitOps.getCurrentBranch(msg.projectPath);
          db.createSession(id, msg.projectPath, branch);
          state.projectPath = msg.projectPath;
          state.sessionId = id;
          const session = db.getSession(id)!;
          send({ type: 'session.created', session });
          break;
        }

        case 'session.load': {
          const session = db.getSession(msg.sessionId);
          if (!session) { send({ type: 'error', message: 'Session not found' }); break; }
          state.sessionId = msg.sessionId;
          state.projectPath = session.projectPath;
          const messages = db.getMessages(msg.sessionId);
          send({ type: 'session.loaded', session, messages });
          break;
        }

        case 'chat': {
          if (!state.sessionId) {
            send({ type: 'chat.error', sessionId: msg.sessionId, error: 'No active session' });
            break;
          }
          await chat({
            projectPath,
            sessionId: state.sessionId,
            onChunk: (content, done) => send({ type: 'chat.chunk', sessionId: state.sessionId!, content, done }),
            onToolCall: (name, input) => send({ type: 'chat.tool_call', sessionId: state.sessionId!, name, input }),
            onToolResult: (name, result, isError) => {
              send({ type: 'chat.tool_result', sessionId: state.sessionId!, name, result, isError });
              // If AI called open_file, forward to editor
              if (name === 'open_file' && typeof result === 'string') {
                const match = result.match(/Opening (.+)/);
                if (match?.[1]) send({ type: 'editor.open', path: match[1] });
              }
            },
            onError: (error) => send({ type: 'chat.error', sessionId: state.sessionId!, error }),
          }, msg.content);
          break;
        }

        case 'file.read': {
          const fc = await fsOps.readFile(msg.path);
          send({ type: 'file.content', path: msg.path, content: fc.content, language: fc.language });
          break;
        }

        case 'file.save': {
          await fsOps.writeFile(msg.path, msg.content);
          send({ type: 'file.saved', path: msg.path });
          break;
        }

        case 'file.tree': {
          const tree = await fsOps.getTree(msg.path);
          send({ type: 'file.tree', tree });
          break;
        }

        case 'git.status': {
          const status = await gitOps.getStatus(projectPath);
          send({ type: 'git.status', status });
          break;
        }

        case 'git.diff': {
          const diff = await gitOps.getDiff(projectPath, msg.path);
          send({ type: 'git.diff', path: msg.path, diff });
          break;
        }

        case 'git.log': {
          const commits = await gitOps.getLog(projectPath, msg.limit ?? 20);
          send({ type: 'git.log', commits });
          break;
        }

        case 'terminal.create': {
          const term = terminalManager.create(msg.cwd);
          state.terminalIds.add(term.id);
          send({ type: 'terminal.created', terminalId: term.id, cwd: msg.cwd });
          term.onData(data => send({ type: 'terminal.output', terminalId: term.id, data }));
          term.onExit(() => {
            state.terminalIds.delete(term.id);
            send({ type: 'terminal.closed', terminalId: term.id });
          });
          break;
        }

        case 'terminal.input': {
          terminalManager.get(msg.terminalId)?.write(msg.data);
          break;
        }

        case 'terminal.resize': {
          terminalManager.get(msg.terminalId)?.resize(msg.cols, msg.rows);
          break;
        }

        case 'terminal.close': {
          terminalManager.get(msg.terminalId)?.kill();
          state.terminalIds.delete(msg.terminalId);
          break;
        }

        default:
          send({ type: 'error', message: 'Unknown message type', code: 'UNKNOWN_TYPE' });
      }
    } catch (err: unknown) {
      send({ type: 'error', message: String(err), code: 'HANDLER_ERROR' });
    }
  });

  ws.on('close', () => {
    for (const id of state.terminalIds) {
      terminalManager.get(id)?.kill();
    }
  });
}
