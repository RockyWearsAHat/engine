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

  it('DesktopStartup_HealthCheckAwaited', async () => {
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

  it('SocketNotYetOpen_OutboundMessagesQueued', async () => {
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

  it('ImmediateReconnect_PendingDisconnectCancelled', async () => {
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

  it('RemoteConfigProvided_RemoteConfigUsed', async () => {
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc xyz' });

    await vi.advanceTimersByTimeAsync(1);

    expect(MockWebSocket.instances).toHaveLength(1);
    expect(MockWebSocket.instances[0]?.url).toBe('wss://engine.example.dev:7443/ws?token=abc%20xyz');
    expect(client.isRemote).toBe(true);
  });

  it('Disconnected_OnlyMostRecent100MessagesKept', async () => {
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

  it('SubscribedHandler_MessageDeliveredAndUnsubscribedCleanly', async () => {
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);

    const socket = MockWebSocket.instances[0]!;
    const handler = vi.fn();
    const dispose = client.onMessage(handler);

    socket.onmessage?.({ data: JSON.stringify({ type: 'session.list', sessions: [] }) } as MessageEvent);
    expect(handler).toHaveBeenCalledTimes(1);

    dispose();
    socket.onmessage?.({ data: JSON.stringify({ type: 'session.list', sessions: [] }) } as MessageEvent);
    expect(handler).toHaveBeenCalledTimes(1);
  });

  it('OpenHandlersPending_AwaitsBeforeFlushingQueue', async () => {
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);

    const socket = MockWebSocket.instances[0]!;
    const openHandler = vi.fn().mockImplementation(async () => {
      await Promise.resolve();
    });
    client.onOpen(openHandler);
    client.send({ type: 'session.list' });

    socket.readyState = MockWebSocket.OPEN;
    socket.onopen?.();
    await Promise.resolve();
    await Promise.resolve();

    expect(openHandler).toHaveBeenCalledTimes(1);
    expect(socket.send).toHaveBeenCalledWith(JSON.stringify({ type: 'session.list' }));
    expect(client.isConnected).toBe(true);
  });

  it('SocketCloses_CloseHandlersNotified', async () => {
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);

    const socket = MockWebSocket.instances[0]!;
    const onClose = vi.fn();
    client.onClose(onClose);

    socket.onclose?.();
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('DeferredDisconnect_ConnectingSocketClosed', async () => {
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);

    const socket = MockWebSocket.instances[0]!;
    client.disconnect();
    await vi.advanceTimersByTimeAsync(300);
    socket.onopen?.();

    expect(socket.close).toHaveBeenCalledTimes(1);
  });

  it('SocketAlreadyOpen_SendsImmediately', async () => {
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);

    const socket = MockWebSocket.instances[0]!;
    socket.readyState = MockWebSocket.OPEN;
    socket.onopen?.();
    await Promise.resolve();

    client.send({ type: 'session.list' });
    expect(socket.send).toHaveBeenCalledWith(JSON.stringify({ type: 'session.list' }));
  });

  it('ShouldConnectFalseAfterDisconnect_QueueDropped', async () => {
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);

    client.disconnect();
    await vi.advanceTimersByTimeAsync(300);

    client.send({ type: 'session.list' });
    const socket = MockWebSocket.instances[0]!;
    expect(socket.send).not.toHaveBeenCalled();
  });

  it('EmitDelegatesToSend', async () => {
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);

    const socket = MockWebSocket.instances[0]!;
    socket.readyState = MockWebSocket.OPEN;
    socket.onopen?.();
    await Promise.resolve();

    client.emit({ type: 'session.list' });
    expect(socket.send).toHaveBeenCalledWith(JSON.stringify({ type: 'session.list' }));
  });

  it('SendToClosedSocket_ScheduleConnectCalled', async () => {
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);

    const socket = MockWebSocket.instances[0]!;
    socket.readyState = MockWebSocket.OPEN;
    socket.onopen?.();
    await Promise.resolve();

    socket.readyState = MockWebSocket.CLOSED;
    socket.onclose?.();

    MockWebSocket.instances.length = 1;
    client.send({ type: 'session.list' });

    await vi.advanceTimersByTimeAsync(2000);
    expect(socket.send).not.toHaveBeenCalledWith(JSON.stringify({ type: 'session.list' }));
  });

  it('WsAlreadyNull_PerformDisconnectReturnsEarly', async () => {
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);

    const socket = MockWebSocket.instances[0]!;
    socket.readyState = MockWebSocket.OPEN;
    socket.onopen?.();
    await Promise.resolve();

    // First disconnect sets ws to null
    client.disconnect();
    await vi.advanceTimersByTimeAsync(500);
    // Second disconnect — ws is null, should early-return without errors
    client.disconnect();
    await vi.advanceTimersByTimeAsync(500);
    expect(client).toBeTruthy();
  });

  it('ConnectingSocket_ClosedViaOnopen', async () => {
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);

    const socket = MockWebSocket.instances[0]!;
    // Still CONNECTING (default readyState)
    expect(socket.readyState).toBe(MockWebSocket.CONNECTING);

    client.disconnect();
    await vi.advanceTimersByTimeAsync(500);

    // The onopen callback was installed; call it to verify it closes
    if (socket.onopen) {
      socket.onopen(new Event('open'));
    }
    expect(socket.close).toHaveBeenCalled();
  });

  it('NotDesktopShell_PlainBrowserWsUrlUsed', async () => {
    delete (window as typeof window & { __TAURI__?: unknown }).__TAURI__;

    const client = new WSClient();
    client.connect();

    await vi.advanceTimersByTimeAsync(1);

    expect(MockWebSocket.instances).toHaveLength(1);
    const url = MockWebSocket.instances[0]?.url ?? '';
    expect(url).toMatch(/^wss?:\/\/.+\/ws$/);
  });

  it('AlreadyOpen_SecondConnectNoOp', async () => {
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);

    const socket = MockWebSocket.instances[0]!;
    socket.readyState = MockWebSocket.OPEN;
    socket.onopen?.();
    await Promise.resolve();

    // Second connect with no remote — remoteConfig is already set (FALSE branch of else-if)
    // and socket is OPEN (TRUE branch of readyState === OPEN guard)
    client.connect();
    await vi.advanceTimersByTimeAsync(1);

    // No new socket should be created
    expect(MockWebSocket.instances).toHaveLength(1);
  });

  it('PerformDisconnect_SocketInClosingState_CloseNotCalledAgain', async () => {
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);

    const socket = MockWebSocket.instances[0]!;
    socket.readyState = MockWebSocket.OPEN;
    socket.onopen?.();
    await Promise.resolve();

    // Simulate socket already transitioning to CLOSING before performDisconnect fires
    socket.readyState = MockWebSocket.CLOSING;

    client.disconnect();
    await vi.advanceTimersByTimeAsync(300);

    // ws.close() should NOT be called because readyState is not OPEN
    expect(socket.close).not.toHaveBeenCalled();
  });
});
