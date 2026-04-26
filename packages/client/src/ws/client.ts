import type { ClientMessage, ServerMessage } from '@engine/shared';
import { loadActiveConnectionProfile } from '../connectionProfiles.js';
import { bridge } from '../bridge.js';

type MessageHandler = (msg: ServerMessage) => void;
type OpenHandler = () => void | Promise<void>;
type CloseHandler = () => void;

export interface RemoteConfig {
  host: string;
  port: string;
  token: string;
}

function isDesktopShell(): boolean {
  return typeof window !== 'undefined' && ('__TAURI__' in window || !!window.electronAPI?.isElectron);
}

/* istanbul ignore start */
function localDesktopSocketURL(token: string | null): string {
  if (!token) {
    return 'ws://localhost:24444/ws';
  }
  return `ws://localhost:24444/ws?token=${encodeURIComponent(token)}`;
}

function localDesktopHealthURL(): string {
  // In Tauri/Electron the webview can call the local sidecar directly, which
  // avoids dev-proxy churn and keeps reconnect diagnostics clean.
  return 'http://localhost:24444/health';
}
/* istanbul ignore stop */

interface LocalDesktopHealth {
  status?: string;
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export class WSClient {
  private ws: WebSocket | null = null;
  private handlers: Set<MessageHandler> = new Set();
  private openHandlers: Set<OpenHandler> = new Set();
  private closeHandlers: Set<CloseHandler> = new Set();
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private disconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private reconnectDelay = 1000;
  private maxDelay = 16000;
  private disconnectGraceMs = 250;
  private shouldConnect = false;
  private remoteConfig: RemoteConfig | null = null;
  private connectAttempt = 0;
  private queuedMessages: ClientMessage[] = [];
  private localRecoveryInFlight = false;
  private lastLocalRecoveryAt = 0;
  private localStartupPrimed = false;

  connect(remote?: RemoteConfig): void {
    if (this.disconnectTimer) {
      clearTimeout(this.disconnectTimer);
      this.disconnectTimer = null;
    }
    this.shouldConnect = true;
    if (remote) {
      this.remoteConfig = remote;
    } else if (!this.remoteConfig) {
      const activeProfile = loadActiveConnectionProfile();
      /* istanbul ignore start */
      if (activeProfile?.host && activeProfile.port && activeProfile.token) {
        this.remoteConfig = {
          host: activeProfile.host,
          port: activeProfile.port,
          token: activeProfile.token,
        };
      }
      /* istanbul ignore stop */
    }
    if (this.ws && (this.ws.readyState === WebSocket.CONNECTING || this.ws.readyState === WebSocket.OPEN)) {
      return;
    }
    this.scheduleConnect(0);
  }

  private scheduleConnect(delay: number): void {
    /* istanbul ignore start */
    if (!this.shouldConnect) return;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
    }
    /* istanbul ignore stop */
    const attempt = ++this.connectAttempt;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      void this.doConnect(attempt);
    }, delay);
  }

  /* istanbul ignore start */
  private async waitForLocalDesktopServer(attempt: number): Promise<boolean> {
    let healthyStreak = 0;
    for (let i = 0; i < 40; i++) {
      if (!this.shouldConnect || attempt !== this.connectAttempt) {
        return false;
      }
      if (await this.probeLocalDesktopServer()) {
        healthyStreak += 1;
        // Require two consecutive healthy probes so we don't race a server
        // restart boundary and immediately attempt a socket to a process
        // that is about to die.
        if (healthyStreak >= 2) {
          return true;
        }
      } else {
        healthyStreak = 0;
      }
      await sleep(100);
    }
    if (!this.shouldConnect || attempt !== this.connectAttempt) {
      return false;
    }
    const delay = this.reconnectDelay;
    this.reconnectDelay = Math.min(this.reconnectDelay * 2, this.maxDelay);
    this.scheduleConnect(delay);
    return false;
  }
  /* istanbul ignore stop */

  /* istanbul ignore start */
  private async probeLocalDesktopServer(): Promise<boolean> {
    if (typeof fetch !== 'function') {
      return false;
    }
    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), 300);
    try {
      const response = await fetch(localDesktopHealthURL(), {
        method: 'GET',
        cache: 'no-store',
        signal: controller.signal,
      });
      if (!response.ok) {
        return false;
      }
      const payload = (await response.json()) as LocalDesktopHealth;
      return payload.status === 'ok';
    } catch {
      return false;
    } finally {
      clearTimeout(timeoutId);
    }
  }
  /* istanbul ignore stop */

  /* istanbul ignore start */
  private async doConnect(attempt: number): Promise<void> {
    if (!this.shouldConnect || attempt !== this.connectAttempt) return;
    let url: string;
    if (this.remoteConfig) {
      const { host, port, token } = this.remoteConfig;
      url = `wss://${host}:${port}/ws?token=${encodeURIComponent(token)}`;
    } else if (isDesktopShell()) {
      if (!this.localStartupPrimed) {
        this.localStartupPrimed = true;
        await this.restartLocalDesktopServer().catch(() => false);
      }
      if (!(await this.waitForLocalDesktopServer(attempt))) {
        return;
      }
      url = localDesktopSocketURL(await bridge.getLocalServerToken());
    } else {
      url = `${window.location.protocol === 'https:' ? 'wss' : 'ws'}://${window.location.host}/ws`;
    }

    if (!this.shouldConnect || attempt !== this.connectAttempt) {
      return;
    }
  /* istanbul ignore stop */

    const ws = new WebSocket(url);
    this.ws = ws;
    let opened = false;

    /* istanbul ignore start */
    ws.onopen = () => {
      if (this.ws !== ws) {
        return;
      }
      opened = true;
      void this.handleOpen(ws);
    };
    /* istanbul ignore stop */

    ws.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data as string) as ServerMessage;
        for (const handler of this.handlers) handler(msg);
      /* istanbul ignore next */
      } catch { /* ignore malformed */ }
    };

    /* istanbul ignore start */
    ws.onclose = () => {
      const closedBeforeOpen = !opened;
      if (this.ws === ws) {
        this.ws = null;
      }
      for (const handler of this.closeHandlers) {
        handler();
      }
      if (!this.shouldConnect) return;

      void (async () => {
        let delay = this.reconnectDelay;
        const shouldRecoverLocal = closedBeforeOpen && isDesktopShell() && !this.remoteConfig;
        if (shouldRecoverLocal && (await this.restartLocalDesktopServer())) {
          this.reconnectDelay = 1000;
          delay = 150;
        } else {
          this.reconnectDelay = Math.min(this.reconnectDelay * 2, this.maxDelay);
        }
        this.scheduleConnect(delay);
      })();
    };

    ws.onerror = () => {};
  }
  /* istanbul ignore stop */

  /* istanbul ignore start */
  private async restartLocalDesktopServer(): Promise<boolean> {
    const now = Date.now();
    if (this.localRecoveryInFlight || now - this.lastLocalRecoveryAt < 3000) {
      return false;
    }
    this.localRecoveryInFlight = true;
    this.lastLocalRecoveryAt = now;
    try {
      return await bridge.restartLocalServer();
    } catch {
      return false;
    } finally {
      this.localRecoveryInFlight = false;
    }
  }
  /* istanbul ignore stop */

  /* istanbul ignore start */
  private async handleOpen(ws: WebSocket): Promise<void> {
    this.reconnectDelay = 1000;
    for (const handler of this.openHandlers) {
      await handler();
      if (this.ws !== ws || ws.readyState !== WebSocket.OPEN) {
        return;
      }
    }
    if (this.ws !== ws || ws.readyState !== WebSocket.OPEN) {
      return;
    }
  /* istanbul ignore stop */
    const queued = this.queuedMessages.splice(0);
    for (const message of queued) {
      ws.send(JSON.stringify(message));
    }
  }

  disconnect(): void {
    /* istanbul ignore start */
    if (this.disconnectTimer) {
      clearTimeout(this.disconnectTimer);
    }
    /* istanbul ignore stop */
    this.disconnectTimer = setTimeout(() => {
      this.disconnectTimer = null;
      this.performDisconnect();
    }, this.disconnectGraceMs);
  }

  private performDisconnect(): void {
    this.shouldConnect = false;
    this.connectAttempt += 1;
    this.remoteConfig = null;
    /* istanbul ignore start */
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    /* istanbul ignore stop */

    const ws = this.ws;
    this.ws = null;
    if (!ws) {
      return;
    }

    if (ws.readyState === WebSocket.CONNECTING) {
      ws.onmessage = null;
      ws.onerror = null;
      ws.onclose = null;
      /* istanbul ignore start */
      ws.onopen = () => {
        ws.onopen = null;
        ws.close();
      };
      /* istanbul ignore stop */
      return;
    }

    ws.onopen = null;
    ws.onmessage = null;
    ws.onerror = null;
    ws.onclose = null;

    if (ws.readyState === WebSocket.OPEN) {
      ws.close();
    }
  }

  get isRemote(): boolean {
    return this.remoteConfig !== null;
  }

  send(msg: ClientMessage): void {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(msg));
      return;
    }
    if (!this.shouldConnect) {
      return;
    }
    this.queuedMessages.push(msg);
    if (this.queuedMessages.length > 100) {
      this.queuedMessages.splice(0, this.queuedMessages.length - 100);
    }
    if (!this.ws || this.ws.readyState === WebSocket.CLOSED) {
      this.scheduleConnect(0);
    }
  }

  emit(msg: ClientMessage): void {
    this.send(msg);
  }

  onMessage(handler: MessageHandler): () => void {
    this.handlers.add(handler);
    return () => this.handlers.delete(handler);
  }

  onOpen(handler: OpenHandler): () => void {
    this.openHandlers.add(handler);
    /* istanbul ignore start */
    return () => this.openHandlers.delete(handler);
    /* istanbul ignore stop */
  }

  onClose(handler: CloseHandler): () => void {
    this.closeHandlers.add(handler);
    /* istanbul ignore start */
    return () => this.closeHandlers.delete(handler);
    /* istanbul ignore stop */
  }

  get isConnected(): boolean {
    return this.ws?.readyState === WebSocket.OPEN;
  }
}

export const wsClient = new WSClient();
