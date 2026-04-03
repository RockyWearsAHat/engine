import * as pty from 'node-pty';
import { randomUUID } from 'node:crypto';

export interface Terminal {
  id: string;
  cwd: string;
  pid: number;
  write(data: string): void;
  resize(cols: number, rows: number): void;
  kill(): void;
  onData(handler: (data: string) => void): void;
  onExit(handler: (code: number) => void): void;
}

export class TerminalManager {
  private terminals = new Map<string, pty.IPty>();
  private dataHandlers = new Map<string, Set<(data: string) => void>>();
  private exitHandlers = new Map<string, Set<(code: number) => void>>();

  create(cwd: string, cols = 80, rows = 24): Terminal {
    const id = randomUUID();
    const shell = process.env['SHELL'] ?? '/bin/bash';

    const proc = pty.spawn(shell, [], {
      name: 'xterm-256color',
      cols,
      rows,
      cwd,
      env: { ...process.env, TERM: 'xterm-256color' },
    });

    this.terminals.set(id, proc);
    this.dataHandlers.set(id, new Set());
    this.exitHandlers.set(id, new Set());

    proc.onData(data => {
      for (const handler of this.dataHandlers.get(id) ?? []) handler(data);
    });

    proc.onExit(({ exitCode }) => {
      for (const handler of this.exitHandlers.get(id) ?? []) handler(exitCode ?? 0);
      this.cleanup(id);
    });

    return {
      id,
      cwd,
      pid: proc.pid,
      write: (data) => proc.write(data),
      resize: (c, r) => proc.resize(c, r),
      kill: () => { try { proc.kill(); } catch { /* already dead */ } this.cleanup(id); },
      onData: (handler) => { this.dataHandlers.get(id)?.add(handler); },
      onExit: (handler) => { this.exitHandlers.get(id)?.add(handler); },
    };
  }

  get(id: string): pty.IPty | undefined {
    return this.terminals.get(id);
  }

  has(id: string): boolean {
    return this.terminals.has(id);
  }

  private cleanup(id: string): void {
    this.terminals.delete(id);
    this.dataHandlers.delete(id);
    this.exitHandlers.delete(id);
  }

  closeAll(): void {
    for (const [id, proc] of this.terminals) {
      try { proc.kill(); } catch { /* already dead */ }
      this.cleanup(id);
    }
  }
}
