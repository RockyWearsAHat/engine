import type { ClientMessage, ServerMessage } from '@engine/shared';
import { loadActiveConnectionProfile } from '../connectionProfiles.js';

type MessageHandler = (msg: ServerMessage) => void;

export interface RemoteConfig {
  host: string;
  port: string;
  token: string;
}

class WSClient {
  private ws: WebSocket | null = null;
  private handlers: Set<MessageHandler> = new Set();
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private reconnectDelay = 1000;
  private maxDelay = 16000;
  private shouldConnect = false;
  private remoteConfig: RemoteConfig | null = null;

  connect(remote?: RemoteConfig): void {
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
    this.doConnect();
  }

  private doConnect(): void {
    if (!this.shouldConnect) return;
    let url: string;
    if (this.remoteConfig) {
      const { host, port, token } = this.remoteConfig;
      url = `wss://${host}:${port}/ws?token=${encodeURIComponent(token)}`;
    } else {
      url = `${window.location.protocol === 'https:' ? 'wss' : 'ws'}://${window.location.host}/ws`;
    }
    const ws = new WebSocket(url);
    this.ws = ws;

    ws.onopen = () => {
      this.reconnectDelay = 1000;
      this.emit({ type: 'session.list' });
    };

    ws.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data as string) as ServerMessage;
        for (const handler of this.handlers) handler(msg);
      } catch { /* ignore malformed */ }
    };

    ws.onclose = () => {
      if (!this.shouldConnect) return;
      this.reconnectTimer = setTimeout(() => {
        this.reconnectDelay = Math.min(this.reconnectDelay * 2, this.maxDelay);
        this.doConnect();
      }, this.reconnectDelay);
    };

    ws.onerror = () => ws.close();
  }

  disconnect(): void {
    this.shouldConnect = false;
    this.remoteConfig = null;
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    this.ws?.close();
    this.ws = null;
  }

  get isRemote(): boolean {
    return this.remoteConfig !== null;
  }

  send(msg: ClientMessage): void {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(msg));
    }
  }

  emit(msg: ClientMessage): void {
    this.send(msg);
  }

  onMessage(handler: MessageHandler): () => void {
    this.handlers.add(handler);
    return () => this.handlers.delete(handler);
  }

  get isConnected(): boolean {
    return this.ws?.readyState === WebSocket.OPEN;
  }
}

export const wsClient = new WSClient();
