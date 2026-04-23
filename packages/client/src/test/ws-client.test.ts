import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const bridgeMocks = vi.hoisted(() => ({
  getLocalServerToken: vi.fn().mockResolvedValue('desktop-token'),
  restartLocalServer: vi.fn().mockResolvedValue(true),
}));

vi.mock('../bridge.js', () => ({
  bridge: {
    getLocalServerToken: bridgeMocks.getLocalServerToken,
    restartLocalServer: bridgeMocks.restartLocalServer,
  },
}));

vi.mock('../connectionProfiles.js', () => ({
  loadActiveConnectionProfile: vi.fn().mockReturnValue(null),
}));

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
    bridgeMocks.getLocalServerToken.mockReset();
    bridgeMocks.restartLocalServer.mockReset();
    bridgeMocks.getLocalServerToken.mockResolvedValue('desktop-token');
    bridgeMocks.restartLocalServer.mockResolvedValue(true);
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
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: vi.fn().mockResolvedValue({ status: 'ok' }),
    });
    globalThis.fetch = fetchMock as typeof fetch;

    const client = new WSClient();
    client.connect();

    await vi.advanceTimersByTimeAsync(500);

    expect(fetchMock).toHaveBeenCalledWith(
      '/health',
      expect.objectContaining({
        method: 'GET',
        cache: 'no-store',
      }),
    );
    expect(bridgeMocks.restartLocalServer).toHaveBeenCalledTimes(1);
    expect(bridgeMocks.getLocalServerToken).toHaveBeenCalledTimes(1);
    expect(MockWebSocket.instances).toHaveLength(1);
    expect(MockWebSocket.instances[0]?.url).toBe('ws://localhost:3000/ws?token=desktop-token');
  });

  it('queues outbound messages until the websocket opens', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: vi.fn().mockResolvedValue({ status: 'ok' }),
    });
    globalThis.fetch = fetchMock as typeof fetch;

    const client = new WSClient();
    client.connect();

    await vi.advanceTimersByTimeAsync(500);

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
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: vi.fn().mockResolvedValue({ status: 'ok' }),
    });
    globalThis.fetch = fetchMock as typeof fetch;

    const client = new WSClient();
    client.connect();

    await vi.advanceTimersByTimeAsync(500);

    const socket = MockWebSocket.instances[0];
    expect(socket).toBeDefined();

    client.disconnect();
    client.connect();
    await vi.advanceTimersByTimeAsync(300);

    expect(socket?.close).not.toHaveBeenCalled();
    expect(MockWebSocket.instances).toHaveLength(1);
  });

  it('uses remote configuration when provided', async () => {
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc xyz' });

    await vi.advanceTimersByTimeAsync(1);

    expect(MockWebSocket.instances).toHaveLength(1);
    expect(MockWebSocket.instances[0]?.url).toBe('wss://engine.example.dev:7443/ws?token=abc%20xyz');
    expect(client.isRemote).toBe(true);
  });

  it('keeps only the most recent 100 queued messages while disconnected', async () => {
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);

    const socket = MockWebSocket.instances[0];
    expect(socket).toBeDefined();
    socket!.readyState = MockWebSocket.CONNECTING;

    for (let i = 0; i < 120; i += 1) {
      client.send({ type: 'session.read', id: String(i) });
    }

    socket!.readyState = MockWebSocket.OPEN;
    socket!.onopen?.();

    expect(socket?.send).toHaveBeenCalledTimes(100);
    expect(socket?.send).toHaveBeenNthCalledWith(1, JSON.stringify({ type: 'session.read', id: '20' }));
    expect(socket?.send).toHaveBeenNthCalledWith(100, JSON.stringify({ type: 'session.read', id: '119' }));
  });
});
