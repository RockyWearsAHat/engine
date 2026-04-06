import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { WSClient } from '../ws/client.js';

class MockWebSocket {
  static instances: MockWebSocket[] = [];
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;

  readonly url: string;
  readyState = MockWebSocket.CONNECTING;
  onopen: (() => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  send = vi.fn();
  close = vi.fn();

  constructor(url: string) {
    this.url = url;
    MockWebSocket.instances.push(this);
  }
}

describe('WSClient desktop startup behavior', () => {
  const originalFetch = globalThis.fetch;
  const originalWebSocket = globalThis.WebSocket;

  beforeEach(() => {
    vi.useFakeTimers();
    MockWebSocket.instances = [];
    globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket;
    Object.defineProperty(window, '__TAURI__', {
      configurable: true,
      value: {},
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    globalThis.WebSocket = originalWebSocket;
    globalThis.fetch = originalFetch;
    delete (window as typeof window & { __TAURI__?: unknown }).__TAURI__;
  });

  it('waits for the local desktop server health check before opening a websocket', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true });
    globalThis.fetch = fetchMock as typeof fetch;

    const client = new WSClient();
    client.connect();

    await vi.runAllTimersAsync();

    expect(fetchMock).toHaveBeenCalledWith(
      'http://127.0.0.1:3000/health',
      expect.objectContaining({
        method: 'GET',
        mode: 'no-cors',
        cache: 'no-store',
      }),
    );
    expect(MockWebSocket.instances).toHaveLength(1);
    expect(MockWebSocket.instances[0]?.url).toBe('ws://127.0.0.1:3000/ws');
  });

  it('queues outbound messages until the websocket opens', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true });
    globalThis.fetch = fetchMock as typeof fetch;

    const client = new WSClient();
    client.connect();

    await vi.runAllTimersAsync();

    const socket = MockWebSocket.instances[0];
    expect(socket).toBeDefined();

    client.send({ type: 'session.list' });
    expect(socket?.send).not.toHaveBeenCalled();

    socket!.readyState = MockWebSocket.OPEN;
    socket!.onopen?.();
    await Promise.resolve();

    expect(socket?.send).toHaveBeenCalledWith(JSON.stringify({ type: 'session.list' }));
  });

  it('cancels a pending disconnect when the app reconnects immediately', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true });
    globalThis.fetch = fetchMock as typeof fetch;

    const client = new WSClient();
    client.connect();

    await vi.runAllTimersAsync();

    const socket = MockWebSocket.instances[0];
    expect(socket).toBeDefined();

    client.disconnect();
    client.connect();
    await vi.advanceTimersByTimeAsync(300);

    expect(socket?.close).not.toHaveBeenCalled();
    expect(MockWebSocket.instances).toHaveLength(1);
  });
});
