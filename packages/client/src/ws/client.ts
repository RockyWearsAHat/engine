import type { ClientMessage, ServerMessage } from '@engine/shared';
import { loadActiveConnectionProfile } from '../connectionProfiles.js';

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

function localDesktopSocketURL(): string {
  return 'ws://127.0.0.1:3000/ws';
}

function localDesktopHealthURL(): string {
  return 'http://127.0.0.1:3000/health';
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
  // Track whether we have ever had a successful connection. On first-boot the
  // health probe can pass slightly before the OS TCP stack is ready for a WS
  // upgrade — if the initial WS attempt fails we retry immediately (0 ms) once
  // rather than waiting the full 1 s reconnect back-off.
  private hasConnectedOnce = false;

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
      if (activeProfile?.host && activeProfile.port && activeProfile.token) {
        this.remoteConfig = {
          host: activeProfile.host,
          port: activeProfile.port,
          token: activeProfile.token,
        };
      }
    }
    if (this.ws && (this.ws.readyState === WebSocket.CONNECTING || this.ws.readyState === WebSocket.OPEN)) {
      return;
    }
    this.scheduleConnect(0);
  }

  private scheduleConnect(delay: number): void {
    if (!this.shouldConnect) return;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
    }
    const attempt = ++this.connectAttempt;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      void this.doConnect(attempt);
    }, delay);
  }

  private async waitForLocalDesktopServer(attempt: number): Promise<boolean> {
    if (!this.shouldConnect || attempt !== this.connectAttempt) return false;

    let resolved = false;
    let resolveFn!: () => void;
    const gate = new Promise<void>((r) => { resolveFn = r; });
    const finish = () => { if (!resolved) { resolved = true; resolveFn(); } };

    // Primary: Tauri emits `engine:server-ready` only after the Go server's
    // WebSocket endpoint has accepted a real upgrade handshake from Rust.
    // This means when we receive the event, `new WebSocket(url)` is guaranteed
    // to succeed — no timing guesses or settle delays needed.
    try {
      const { once } = await import('@tauri-apps/api/event');
      once('engine:server-ready', finish).catch(() => { /* ignore */ });
    } catch {
      // Tauri event API unavailable — HTTP poll fallback will drive resolution
    }

    // Fallback: HTTP health poll handles the edge case where the server was
    // already running when the app launched and the event fired before our
    // listener was registered. We add a settle after HTTP success because
    // unlike the event path, HTTP-ready ≠ WS-ready on WKWebView.
    void (async () => {
      for (let i = 0; i < 80; i++) {
        if (resolved || !this.shouldConnect || attempt !== this.connectAttempt) return;
        if (await this.probeLocalDesktopServer()) {
          // WKWebView can accept HTTP before its WS stack is ready; give it
          // enough time. Only needed when we didn't get the Rust WS-confirmed
          // event (which means we fell through to this HTTP path).
          if (!this.hasConnectedOnce) await sleep(800);
          finish();
          return;
        }
        await sleep(100);
      }
      // Polling exhausted — schedule a backoff reconnect
      if (!resolved) {
        const delay = this.reconnectDelay;
        this.reconnectDelay = Math.min(this.reconnectDelay * 2, this.maxDelay);
        this.scheduleConnect(delay);
        finish();
      }
    })();

    // Safety timeout so we never hang the UI
    const timeoutId = setTimeout(finish, 15_000);
    await gate;
    clearTimeout(timeoutId);

    return this.shouldConnect && attempt === this.connectAttempt;
  }

  private async probeLocalDesktopServer(): Promise<boolean> {
    if (typeof fetch !== 'function') {
      return false;
    }
    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), 300);
    try {
      // Use cors mode so we can read the response status — no-cors returns an
      // opaque response that looks like success even when the server is not up.
      const res = await fetch(localDesktopHealthURL(), {
        method: 'GET',
        cache: 'no-store',
        signal: controller.signal,
      });
      return res.ok;
    } catch {
      return false;
    } finally {
      clearTimeout(timeoutId);
    }
  }

  private async doConnect(attempt: number): Promise<void> {
    if (!this.shouldConnect || attempt !== this.connectAttempt) return;
    let url: string;
    if (this.remoteConfig) {
      const { host, port, token } = this.remoteConfig;
      url = `wss://${host}:${port}/ws?token=${encodeURIComponent(token)}`;
    } else if (isDesktopShell()) {
      if (!(await this.waitForLocalDesktopServer(attempt))) {
        return;
      }
      url = localDesktopSocketURL();
    } else {
      url = `${window.location.protocol === 'https:' ? 'wss' : 'ws'}://${window.location.host}/ws`;
    }

    if (!this.shouldConnect || attempt !== this.connectAttempt) {
      return;
    }

    const ws = new WebSocket(url);
    this.ws = ws;

    ws.onopen = () => {
      if (this.ws !== ws) {
        return;
      }
      void this.handleOpen(ws);
    };

    ws.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data as string) as ServerMessage;
        for (const handler of this.handlers) handler(msg);
      } catch { /* ignore malformed */ }
    };

    ws.onclose = () => {
      if (this.ws === ws) {
        this.ws = null;
      }
      for (const handler of this.closeHandlers) {
        handler();
      }
      if (!this.shouldConnect) return;
      // On first-boot the initial WS attempt can fail before we have ever
      // connected (race between health probe and WS readiness). Retry
      // immediately so the user sees no disconnected gap at startup.
      const delay = this.hasConnectedOnce ? this.reconnectDelay : 0;
      this.reconnectDelay = Math.min(this.reconnectDelay * 2, this.maxDelay);
      this.scheduleConnect(delay);
    };

    ws.onerror = () => {};
  }

  private async handleOpen(ws: WebSocket): Promise<void> {
    this.reconnectDelay = 1000;
    this.hasConnectedOnce = true;
    for (const handler of this.openHandlers) {
      await handler();
      if (this.ws !== ws || ws.readyState !== WebSocket.OPEN) {
        return;
      }
    }
    if (this.ws !== ws || ws.readyState !== WebSocket.OPEN) {
      return;
    }
    const queued = this.queuedMessages.splice(0);
    for (const message of queued) {
      ws.send(JSON.stringify(message));
    }
  }

  disconnect(): void {
    if (this.disconnectTimer) {
      clearTimeout(this.disconnectTimer);
    }
    this.disconnectTimer = setTimeout(() => {
      this.disconnectTimer = null;
      this.performDisconnect();
    }, this.disconnectGraceMs);
  }

  private performDisconnect(): void {
    this.shouldConnect = false;
    this.connectAttempt += 1;
    this.remoteConfig = null;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }

    const ws = this.ws;
    this.ws = null;
    if (!ws) {
      return;
    }

    if (ws.readyState === WebSocket.CONNECTING) {
      ws.onmessage = null;
      ws.onerror = null;
      ws.onclose = null;
      ws.onopen = () => {
        ws.onopen = null;
        ws.close();
      };
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
    return () => this.openHandlers.delete(handler);
  }

  onClose(handler: CloseHandler): () => void {
    this.closeHandlers.add(handler);
    return () => this.closeHandlers.delete(handler);
  }

  get isConnected(): boolean {
    return this.ws?.readyState === WebSocket.OPEN;
  }
}

export const wsClient = new WSClient();
