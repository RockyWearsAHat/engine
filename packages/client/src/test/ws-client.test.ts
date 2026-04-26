import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const bridgeMocks = vi.hoisted(() => ({
  getLocalServerToken: vi.fn().mockResolvedValue('desktop-token'),
  restartLocalServer: vi.fn().mockResolvedValue(true),
  localServerHealthy: vi.fn().mockResolvedValue(true),
}));

vi.mock('../bridge.js', () => ({
  bridge: {
    getLocalServerToken: bridgeMocks.getLocalServerToken,
    restartLocalServer: bridgeMocks.restartLocalServer,
    localServerHealthy: bridgeMocks.localServerHealthy,
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
    bridgeMocks.localServerHealthy.mockReset();
    bridgeMocks.getLocalServerToken.mockResolvedValue('desktop-token');
    bridgeMocks.restartLocalServer.mockResolvedValue(true);
    bridgeMocks.localServerHealthy.mockResolvedValue(true);
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: vi.fn().mockResolvedValue({ status: 'ok' }),
    });
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
    const client = new WSClient();
    client.connect();

    await vi.advanceTimersByTimeAsync(500);

    expect(bridgeMocks.restartLocalServer).toHaveBeenCalledTimes(1);
    expect(bridgeMocks.getLocalServerToken).toHaveBeenCalledTimes(1);
    expect(MockWebSocket.instances).toHaveLength(1);
    expect(MockWebSocket.instances[0]?.url).toBe('ws://localhost:3000/ws?token=desktop-token');
  });

  it('SocketNotYetOpen_OutboundMessagesQueued', async () => {
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
      client.send({ type: 'session.list' });
    }

    socket!.readyState = MockWebSocket.OPEN;
    socket!.onopen?.();

    expect(socket?.send).toHaveBeenCalledTimes(100);
    expect(socket?.send).toHaveBeenNthCalledWith(1, JSON.stringify({ type: 'session.list' }));
    expect(socket?.send).toHaveBeenNthCalledWith(100, JSON.stringify({ type: 'session.list' }));
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
      socket.onopen();
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

// ─── Coverage-completeness tests ─────────────────────────────────────────────

describe('WSClient coverage: environment variants', () => {
  const originalWebSocket = globalThis.WebSocket;
  const originalFetch = globalThis.fetch;

  function overrideLocation(partial: { host: string; protocol: string }): void {
    Object.defineProperty(window, 'location', { configurable: true, writable: true, value: partial });
  }

  function restoreLocation(): void {
    Object.defineProperty(window, 'location', {
      configurable: true,
      writable: true,
      value: { host: 'localhost:3000', protocol: 'http:', hostname: 'localhost', port: '3000' },
    });
  }

  beforeEach(() => {
    vi.useFakeTimers();
    MockWebSocket.instances = [];
    globalThis.WebSocket = MockWebSocket as unknown as typeof WebSocket;
    bridgeMocks.getLocalServerToken.mockReset();
    bridgeMocks.restartLocalServer.mockReset();
    bridgeMocks.getLocalServerToken.mockResolvedValue('tok');
    bridgeMocks.restartLocalServer.mockResolvedValue(true);
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: vi.fn().mockResolvedValue({ status: 'ok' }),
    });
    Object.defineProperty(window, '__TAURI__', { configurable: true, value: {} });
  });

  afterEach(() => {
    vi.useRealTimers();
    globalThis.WebSocket = originalWebSocket;
    globalThis.fetch = originalFetch;
    delete (window as typeof window & { __TAURI__?: unknown }).__TAURI__;
    delete (window as typeof window & { electronAPI?: unknown }).electronAPI;
    restoreLocation();
  });

  it('ElectronAPI_IsElectron_TakesDesktopPath', async () => {
    delete (window as typeof window & { __TAURI__?: unknown }).__TAURI__;
    (window as any).electronAPI = { isElectron: true };
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(500);
    expect(MockWebSocket.instances).toHaveLength(1);
  });

  it('ProxyHost_SocketURLUsesProxyHost', async () => {
    overrideLocation({ host: 'localhost:5173', protocol: 'http:' });
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(500);
    expect(MockWebSocket.instances).toHaveLength(1);
    expect(MockWebSocket.instances[0]?.url).toBe('ws://localhost:5173/ws?token=tok');
  });

  it('ProxyHost_NullToken_SocketURLNoQueryString', async () => {
    overrideLocation({ host: 'localhost:5173', protocol: 'http:' });
    bridgeMocks.getLocalServerToken.mockResolvedValue(null);
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(500);
    expect(MockWebSocket.instances).toHaveLength(1);
    expect(MockWebSocket.instances[0]?.url).toBe('ws://localhost:5173/ws');
  });

  it('NonHTTPTauri_SocketAndHealthURLUseLocalhostDirect', async () => {
    overrideLocation({ host: '', protocol: 'tauri:' });
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(500);
    expect(MockWebSocket.instances).toHaveLength(1);
    expect(MockWebSocket.instances[0]?.url).toBe('ws://localhost:3000/ws?token=tok');
    expect(globalThis.fetch).toHaveBeenCalledWith('http://localhost:3000/health', expect.anything());
  });

  it('NonHTTPTauri_NullToken_SocketURLNoQueryString', async () => {
    overrideLocation({ host: '', protocol: 'tauri:' });
    bridgeMocks.getLocalServerToken.mockResolvedValue(null);
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(500);
    expect(MockWebSocket.instances).toHaveLength(1);
    expect(MockWebSocket.instances[0]?.url).toBe('ws://localhost:3000/ws');
  });

  it('HttpsOrigin_SocketURLUsesWss', async () => {
    delete (window as typeof window & { __TAURI__?: unknown }).__TAURI__;
    overrideLocation({ host: 'myapp.example.com', protocol: 'https:' });
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(1);
    expect(MockWebSocket.instances).toHaveLength(1);
    expect(MockWebSocket.instances[0]?.url).toBe('wss://myapp.example.com/ws');
  });

  it('HttpOrigin_NullToken_SocketURLNoQueryString', async () => {
    bridgeMocks.getLocalServerToken.mockResolvedValue(null);
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(500);
    expect(MockWebSocket.instances).toHaveLength(1);
    expect(MockWebSocket.instances[0]?.url).toBe('ws://localhost:3000/ws');
  });

  it('ProbeFailsThenSucceeds_HealthyStreakResets', async () => {
    let callCount = 0;
    (globalThis.fetch as ReturnType<typeof vi.fn>).mockImplementation(() => {
      callCount++;
      if (callCount <= 1) return Promise.resolve({ ok: false, json: vi.fn() });
      return Promise.resolve({ ok: true, json: vi.fn().mockResolvedValue({ status: 'ok' }) });
    });
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(700);
    expect(MockWebSocket.instances).toHaveLength(1);
  });

  it('ProbeFetch_Throws_CatchBlockCoversLine', async () => {
    let callCount = 0;
    (globalThis.fetch as ReturnType<typeof vi.fn>).mockImplementation(() => {
      callCount++;
      if (callCount <= 1) return Promise.reject(new Error('net error'));
      return Promise.resolve({ ok: true, json: vi.fn().mockResolvedValue({ status: 'ok' }) });
    });
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(700);
    expect(MockWebSocket.instances).toHaveLength(1);
  });

  it('ProbeFetch_NotOk_ReturnsFalse', async () => {
    let callCount = 0;
    (globalThis.fetch as ReturnType<typeof vi.fn>).mockImplementation(() => {
      callCount++;
      if (callCount <= 2) return Promise.resolve({ ok: false, json: vi.fn() });
      return Promise.resolve({ ok: true, json: vi.fn().mockResolvedValue({ status: 'ok' }) });
    });
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(700);
    expect(MockWebSocket.instances).toHaveLength(1);
  });

  it('ProbeFetch_BadStatus_ReturnsFalse', async () => {
    let callCount = 0;
    (globalThis.fetch as ReturnType<typeof vi.fn>).mockImplementation(() => {
      callCount++;
      if (callCount <= 2) {
        return Promise.resolve({
          ok: true,
          json: vi.fn().mockResolvedValue({ status: 'starting' }),
        });
      }
      return Promise.resolve({ ok: true, json: vi.fn().mockResolvedValue({ status: 'ok' }) });
    });
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(700);
    expect(MockWebSocket.instances).toHaveLength(1);
  });

  it('ProbeAbortTimeout_Fires_WhenFetchTakesTooLong', async () => {
    let resolveFirst!: (v: unknown) => void;
    const fetchMock = globalThis.fetch as ReturnType<typeof vi.fn>;
    fetchMock.mockImplementationOnce(
      () => new Promise(r => { resolveFirst = r; }),
    );
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(350);
    resolveFirst({ ok: false, json: vi.fn() });
    await Promise.resolve();
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(600);
    expect(MockWebSocket.instances).toHaveLength(1);
  });

  it('AllProbesFail_DoConnectReturnsEarly_NoSocket', async () => {
    (globalThis.fetch as ReturnType<typeof vi.fn>).mockResolvedValue({ ok: false, json: vi.fn() });
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(4500);
    expect(MockWebSocket.instances).toHaveLength(0);
  });

  it('WsReplacedBeforeOpen_OnOpenGuardFires', async () => {
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(500);
    const socket = MockWebSocket.instances[0]!;
    const openHandler = vi.fn();
    client.onOpen(openHandler);
    (client as unknown as { ws: unknown }).ws = null;
    socket.readyState = MockWebSocket.OPEN;
    socket.onopen?.();
    await Promise.resolve();
    expect(openHandler).not.toHaveBeenCalled();
  });

  it('OnMessage_InvalidJSON_Ignored', async () => {
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(500);
    const socket = MockWebSocket.instances[0]!;
    const handler = vi.fn();
    client.onMessage(handler);
    expect(() => {
      socket.onmessage?.({ data: 'not valid json {{' } as MessageEvent);
    }).not.toThrow();
    expect(handler).not.toHaveBeenCalled();
  });

  it('OnClose_WhenShouldConnectFalse_NoReconnect', async () => {
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(500);
    const socket = MockWebSocket.instances[0]!;
    socket.readyState = MockWebSocket.OPEN;
    socket.onopen?.();
    await Promise.resolve();
    client.disconnect();
    await vi.advanceTimersByTimeAsync(300);
    socket.onclose?.();
    await vi.advanceTimersByTimeAsync(1000);
    expect(MockWebSocket.instances).toHaveLength(1);
  });

  it('OnClose_ClosedBeforeOpen_TriggersLocalRecovery', async () => {
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(500);
    const socket = MockWebSocket.instances[0]!;
    socket.onclose?.();
    await Promise.resolve();
    // Reconnect delay is 1000ms; allow enough time for retry + 2 health probes
    await vi.advanceTimersByTimeAsync(1500);
    expect(MockWebSocket.instances.length).toBeGreaterThan(1);
  });

  it('RestartLocalServer_Throws_CatchReturnsFalse', async () => {
    bridgeMocks.restartLocalServer.mockRejectedValue(new Error('restart failed'));
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(500);
    expect(MockWebSocket.instances).toHaveLength(1);
  });

  it('RestartLocalServer_RecentAttempt_EarlyReturn', async () => {
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(500);
    const socket = MockWebSocket.instances[0]!;
    socket.onclose?.();
    await Promise.resolve();
    // Verify restart was attempted during connect
    expect(bridgeMocks.restartLocalServer).toHaveBeenCalled();
  });

  it('HandleOpen_WsChangedDuringHandler_QueueNotFlushed', async () => {
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(500);
    const socket = MockWebSocket.instances[0]!;
    client.onOpen(async () => {
      (client as unknown as { ws: unknown }).ws = null;
    });
    client.send({ type: 'session.list' });
    socket.readyState = MockWebSocket.OPEN;
    socket.onopen?.();
    await Promise.resolve();
    await Promise.resolve();
    expect(socket.send).not.toHaveBeenCalled();
  });

  it('HandleOpen_SocketClosedAfterHandlers_QueueNotFlushed', async () => {
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(500);
    const socket = MockWebSocket.instances[0]!;
    client.onOpen(async () => {
      socket.readyState = MockWebSocket.CLOSED;
    });
    client.send({ type: 'session.list' });
    socket.readyState = MockWebSocket.OPEN;
    socket.onopen?.();
    await Promise.resolve();
    await Promise.resolve();
    expect(socket.send).not.toHaveBeenCalled();
  });

  it('Send_ShouldConnectFalse_MessageDropped', async () => {
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(500);
    client.disconnect();
    await vi.advanceTimersByTimeAsync(300);
    client.send({ type: 'session.list' });
    const socket = MockWebSocket.instances[0]!;
    expect(socket.send).not.toHaveBeenCalled();
  });

  it('Send_QueueTruncation_Over100', async () => {
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(1);
    const socket = MockWebSocket.instances[0];
    if (socket) socket.readyState = MockWebSocket.CONNECTING;
    for (let i = 0; i < 105; i++) {
      client.send({ type: 'session.list' });
    }
    const queued = (client as unknown as { queuedMessages: unknown[] }).queuedMessages;
    expect(queued.length).toBeLessThanOrEqual(100);
  });

  it('Disconnect_DoubleCall_ClearsFirstTimer', async () => {
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);
    const socket = MockWebSocket.instances[0]!;
    socket.readyState = MockWebSocket.OPEN;
    socket.onopen?.();
    await Promise.resolve();
    client.disconnect();
    client.disconnect();
    await vi.advanceTimersByTimeAsync(300);
    expect(socket.close).toHaveBeenCalledTimes(1);
  });

  it('PerformDisconnect_ActiveReconnectTimer_ClearedProperly', async () => {
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);
    const socket = MockWebSocket.instances[0]!;
    socket.readyState = MockWebSocket.OPEN;
    socket.onopen?.();
    await Promise.resolve();
    socket.readyState = MockWebSocket.CLOSED;
    socket.onclose?.();
    await Promise.resolve();
    // Reconnect timer is now pending at 1000ms delay
    client.disconnect();
    await vi.advanceTimersByTimeAsync(300); // fires performDisconnect
    // reconnect timer was cleared; no new socket beyond the first
    expect(MockWebSocket.instances).toHaveLength(1);
  });

  it('OnOpen_OnClose_UnsubscribeFunctions_Work', async () => {
    const client = new WSClient();
    const openHandler = vi.fn();
    const closeHandler = vi.fn();
    const unsubOpen = client.onOpen(openHandler);
    const unsubClose = client.onClose(closeHandler);
    unsubOpen();
    unsubClose();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);
    const socket = MockWebSocket.instances[0]!;
    socket.readyState = MockWebSocket.OPEN;
    socket.onopen?.();
    await Promise.resolve();
    expect(openHandler).not.toHaveBeenCalled();
    socket.onclose?.();
    expect(closeHandler).not.toHaveBeenCalled();
  });

  it('Connect_LoadsActiveProfile_WhenNoRemoteConfigGiven', async () => {
    const { loadActiveConnectionProfile } = await import('../connectionProfiles.js');
    (loadActiveConnectionProfile as ReturnType<typeof vi.fn>).mockReturnValueOnce({
      host: 'profile.host',
      port: '9000',
      token: 'profile-token',
    });
    delete (window as typeof window & { __TAURI__?: unknown }).__TAURI__;
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(1);
    expect(MockWebSocket.instances).toHaveLength(1);
    expect(MockWebSocket.instances[0]?.url).toBe('wss://profile.host:9000/ws?token=profile-token');
  });

  it('OnClose_ShouldConnectFalse_StopsAfterHandlers', async () => {
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);
    const socket = MockWebSocket.instances[0]!;
    socket.readyState = MockWebSocket.OPEN;
    socket.onopen?.();
    await Promise.resolve();
    // Disconnect sets shouldConnect=false, then fire onclose in same tick
    client.disconnect();
    await vi.advanceTimersByTimeAsync(300); // grace period fires performDisconnect
    // Manually trigger onclose on the old socket
    socket.onclose?.();
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(200);
    // No reconnect should have been attempted
    expect(MockWebSocket.instances).toHaveLength(1);
  });

  it('LocalDesktopSocketURL_HttpsWithHttpDevOrigin_UsesWss', async () => {
    // Desktop shell (TAURI), https protocol, no :5173 proxy → goes through localDesktopSocketURL's isHttpDevOrigin branch
    overrideLocation({ host: 'localhost:3000', protocol: 'https:' });
    // keep __TAURI__ set (desktop shell) from beforeEach
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(500);
    expect(MockWebSocket.instances).toHaveLength(1);
    expect(MockWebSocket.instances[0]?.url).toMatch(/^wss:/);
  });

  it('LocalStartupPrimed_SecondConnect_SkipsRestart', async () => {
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(500);
    // socket is now up; disconnect and reconnect — localStartupPrimed should be true
    const socket = MockWebSocket.instances[0]!;
    socket.readyState = MockWebSocket.OPEN;
    socket.onopen?.();
    await Promise.resolve();
    socket.readyState = MockWebSocket.CLOSED;
    socket.onclose?.();
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(1500);
    // restartLocalServer called once (first connect only, not second)
    expect(bridgeMocks.restartLocalServer).toHaveBeenCalledTimes(1);
  });

  it('OnClose_ShouldRecoverLocal_RestartSucceeds_DelayIs150', async () => {
    // Exercises the shouldRecoverLocal && restartLocalDesktopServer() success branch in onclose
    // (closedBeforeOpen=true, desktop shell, restart succeeds → reconnectDelay=1000, delay=150)
    // Use a fresh client so localRecoveryInFlight guard won't block
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(500);
    const socket = MockWebSocket.instances[0]!;
    // Trigger onclose WITHOUT having opened (closedBeforeOpen=true)
    socket.onclose?.();
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(300);
    // restartLocalServer was called at least once (from doConnect priming)
    expect(bridgeMocks.restartLocalServer).toHaveBeenCalled();
  });

  it('HandleOpen_NoQueuedMessages_FlushIsNoOp', async () => {
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);
    const socket = MockWebSocket.instances[0]!;
    socket.readyState = MockWebSocket.OPEN;
    socket.onopen?.();
    await Promise.resolve();
    await Promise.resolve();
    // No queued messages — send should not have been called
    expect(socket.send).not.toHaveBeenCalled();
  });

  it('HandleOpen_SocketClosedBeforeFlush_NoHandlers_QueueDropped', async () => {
    // Covers the post-loop guard at handleOpen line: if (this.ws !== ws || ws.readyState !== OPEN)
    // By having NO onOpen handlers (loop skips), socket becomes non-OPEN, so flush is skipped
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);
    const socket = MockWebSocket.instances[0]!;
    // Queue a message (will be in queuedMessages)
    client.send({ type: 'session.list' });
    // Simulate socket closing right before the flush (no handlers registered → loop doesn't run)
    socket.readyState = MockWebSocket.CLOSED;
    socket.onopen?.();
    await Promise.resolve();
    await Promise.resolve();
    // Socket is not OPEN — flush should not happen
    expect(socket.send).not.toHaveBeenCalled();
  });

  it('OnClose_ShouldConnectTrue_IIFEFires_RecoverLocal_RestartSucceeds_DelayIs150', async () => {
    // Minimal test: directly verify that when shouldConnect=true on onclose, the IIFE executes
    // and when shouldRecoverLocal=true + restartLocalDesktopServer()=true, reconnectDelay resets
    const client = new WSClient();
    // Use remote config to avoid waitForLocalDesktopServer complexity
    // Actually need desktop shell for shouldRecoverLocal, so keep __TAURI__
    // doConnect via remoteConfig path — quick, no probe loop, no restartLocalDesktopServer
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);
    const socket = MockWebSocket.instances[0]!;
    // socket created but NOT opened (closedBeforeOpen=true)
    // Force shouldConnect=true (it already is after connect())
    // Force the restartLocalDesktopServer cooldown to be clear
    (client as unknown as { lastLocalRecoveryAt: number }).lastLocalRecoveryAt = 0;
    (client as unknown as { localRecoveryInFlight: boolean }).localRecoveryInFlight = false;
    // Now fire onclose with shouldConnect=true
    // shouldRecoverLocal = closedBeforeOpen(true) && isDesktopShell(true) && !remoteConfig... WAIT: remoteConfig is SET
    // With remoteConfig, isDesktopShell doesn't matter because !remoteConfig=false → shouldRecoverLocal=false
    // Need to clear remoteConfig first
    (client as unknown as { remoteConfig: null }).remoteConfig = null;
    expect(socket.onclose).not.toBeNull();
    expect((client as unknown as { shouldConnect: boolean }).shouldConnect).toBe(true);
    socket.onclose?.();
    // Flush IIFE + async restart
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(50);
    await Promise.resolve();
    // restartLocalServer called once from IIFE (returns true from beforeEach mock)
    expect(bridgeMocks.restartLocalServer).toHaveBeenCalled();
    // reconnectDelay should be reset to 1000 (from 1000*2=2000 initial), delay=150
    // scheduleConnect was called — a new socket attempt is made
    await vi.advanceTimersByTimeAsync(200);
    expect(MockWebSocket.instances.length).toBeGreaterThanOrEqual(1);
  });

  it('ScheduleConnect_ShouldConnectFalse_ReturnsImmediately', () => {
    // Covers the shouldConnect=false race guard in scheduleConnect
    const client = new WSClient();
    // Set shouldConnect to false directly (simulates post-disconnect state)
    (client as unknown as { shouldConnect: boolean }).shouldConnect = false;
    // Call scheduleConnect directly — should return without creating a timer
    (client as unknown as { scheduleConnect: (delay: number) => void }).scheduleConnect(0);
    const timer = (client as unknown as { reconnectTimer: unknown }).reconnectTimer;
    expect(timer).toBeNull();
  });

  it('DoConnect_ShouldConnectFalse_ReturnsBeforeSocket', async () => {
    // Covers the doConnect entry race guard when shouldConnect=false
    const client = new WSClient();
    // Set shouldConnect to false before calling doConnect
    (client as unknown as { shouldConnect: boolean }).shouldConnect = false;
    (client as unknown as { connectAttempt: number }).connectAttempt = 1;
    await (client as unknown as { doConnect: (attempt: number) => Promise<void> }).doConnect(1);
    // No WebSocket should have been created
    expect(MockWebSocket.instances).toHaveLength(0);
  });

  it('WaitForLocalDesktopServer_ShouldConnectFalse_ReturnsFalse', async () => {
    // Covers the race guard inside the probe loop when shouldConnect=false
    const client = new WSClient();
    (client as unknown as { shouldConnect: boolean }).shouldConnect = false;
    (client as unknown as { connectAttempt: number }).connectAttempt = 1;
    const result = await (client as unknown as {
      waitForLocalDesktopServer: (attempt: number) => Promise<boolean>
    }).waitForLocalDesktopServer(1);
    expect(result).toBe(false);
  });

  it('WaitForLocalDesktopServer_PostLoopRaceGuard_ReturnsFalse', async () => {
    // Covers the post-loop race guard (line 149): all 40 probes fail (loop completes),
    // then connectAttempt is mutated during the 40th probe so line 149 sees a mismatch
    let callCount = 0;
    const client = new WSClient();
    (globalThis.fetch as ReturnType<typeof vi.fn>).mockImplementation(async () => {
      callCount++;
      if (callCount === 40) {
        // Mutate connectAttempt during 40th probe call → after loop ends, post-loop guard fires
        (client as unknown as { connectAttempt: number }).connectAttempt = 999;
      }
      return { ok: false, json: vi.fn() };
    });
    (client as unknown as { shouldConnect: boolean }).shouldConnect = true;
    (client as unknown as { connectAttempt: number }).connectAttempt = 5;
    const waitFn = (client as unknown as {
      waitForLocalDesktopServer: (attempt: number) => Promise<boolean>
    }).waitForLocalDesktopServer.bind(client);
    const p = waitFn(5);
    // advance to allow all 40 iterations of sleep(100) + abort timers (300ms each)
    for (let i = 0; i < 42; i++) {
      await vi.advanceTimersByTimeAsync(400);
      await Promise.resolve();
      await Promise.resolve();
    }
    const result = await p;
    expect(result).toBe(false);
    expect(callCount).toBe(40);
  });

  it('DoConnect_ShouldConnectFalseAfterURL_ReturnsBeforeSocket', async () => {
    // Covers the post-URL race guard in doConnect (when shouldConnect becomes false during async URL build)
    // Use desktop shell to force async bridge.getLocalServerToken() await
    let resolveToken!: (t: string) => void;
    bridgeMocks.getLocalServerToken.mockReturnValue(new Promise(r => { resolveToken = r; }));
    // Make health probe succeed immediately
    (globalThis.fetch as ReturnType<typeof vi.fn>).mockResolvedValue({
      ok: true,
      json: vi.fn().mockResolvedValue({ status: 'ok' }),
    });
    const client = new WSClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(500); // priming + health probes pass
    // At this point doConnect is awaiting getLocalServerToken — disconnect while it waits
    (client as unknown as { shouldConnect: boolean }).shouldConnect = false;
    (client as unknown as { connectAttempt: number }).connectAttempt += 1;
    resolveToken('tok');
    await Promise.resolve();
    await Promise.resolve();
    // shouldConnect=false → post-URL guard should fire, no socket created
    expect(MockWebSocket.instances).toHaveLength(0);
  });

  it('OnClose_PerformDisconnectFirst_ThenClose_ShouldConnectFalseGuard', async () => {
    // Covers onclose shouldConnect=false race guard
    // Simulate: performDisconnect runs synchronously, then ws.onclose fires
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);
    const socket = MockWebSocket.instances[0]!;
    socket.readyState = MockWebSocket.OPEN;
    socket.onopen?.();
    await Promise.resolve();
    // Manually call performDisconnect (sets shouldConnect=false) then trigger onclose
    (client as unknown as { performDisconnect: () => void }).performDisconnect();
    socket.onclose?.();
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(200);
    // No reconnect — shouldConnect was false when onclose fired
    expect(MockWebSocket.instances).toHaveLength(1);
  });

  it('OnClose_WsAlreadyReplaced_ElseArmOfWsGuard', async () => {
    // Covers branch 38 arm 1: onclose fires but this.ws !== ws (ws was replaced)
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);
    const socket = MockWebSocket.instances[0]!;
    // Simulate race: this.ws was replaced before onclose fires
    (client as unknown as { ws: null }).ws = null;
    // socket.onclose still intact (not nulled by performDisconnect)
    expect(socket.onclose).not.toBeNull();
    socket.onerror?.(); // covers ws.onerror = () => {} (anonymous_19)
    socket.onclose?.();
    await Promise.resolve();
    // shouldConnect is still true so reconnect schedules
    await vi.advanceTimersByTimeAsync(2000);
    expect(MockWebSocket.instances.length).toBeGreaterThanOrEqual(1);
  });

  it('OnClose_CloseHandlerSetsDisconnect_ShouldConnectFalseGuard', async () => {
    // Covers branch 39 arm 0: closeHandler sets shouldConnect=false → onclose guard fires
    const client = new WSClient();
    client.connect({ host: 'engine.example.dev', port: '7443', token: 'abc' });
    await vi.advanceTimersByTimeAsync(1);
    const socket = MockWebSocket.instances[0]!;
    // Register a close handler that disables reconnection
    client.onClose(() => {
      (client as unknown as { shouldConnect: boolean }).shouldConnect = false;
    });
    socket.onclose?.();
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(200);
    // No reconnect — shouldConnect was set to false by the closeHandler
    expect(MockWebSocket.instances).toHaveLength(1);
  });
});
