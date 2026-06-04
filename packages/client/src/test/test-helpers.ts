/**
 * test-helpers.ts — Shared test utilities for reducing duplication across test suites.
 *
 * Provides:
 * - WS client mock factories with callback capture
 * - Bridge mock factories with sensible defaults
 * - Store setup utilities
 * - Common cleanup patterns
 */
import { act, render, RenderOptions, RenderResult } from '@testing-library/react';
import { ReactElement } from 'react';
import { vi } from 'vitest';
import type { ServerMessage } from '@engine/shared';

// ─── WS Client Mocking ────────────────────────────────────────────────────────

/**
 * Create a WS client mock with callback capture for onMessage and onOpen.
 * Returns both the mock definition (for vi.mock) and a namespace with captured callbacks.
 */
export function createWsClientMock() {
  let capturedWsCallback: ((data: unknown) => void) | null = null;
  let capturedOnOpenCallback: (() => void) | null = null;

  const wsCallbacks = {
    capturedWsCallback,
    capturedOnOpenCallback,
    setCapturedWsCallback(cb: ((data: unknown) => void) | null) {
      capturedWsCallback = cb;
      wsCallbacks.capturedWsCallback = cb;
    },
    setCapturedOnOpenCallback(cb: (() => void) | null) {
      capturedOnOpenCallback = cb;
      wsCallbacks.capturedOnOpenCallback = cb;
    },
    get getCapturedWsCallback() {
      return capturedWsCallback;
    },
    get getCapturedOnOpenCallback() {
      return capturedOnOpenCallback;
    },
  };

  const mockDef = {
    wsClient: {
      send: vi.fn(),
      connect: vi.fn(),
      disconnect: vi.fn(),
      onMessage: vi.fn((cb: (data: unknown) => void) => {
        wsCallbacks.setCapturedWsCallback(cb);
        return () => { wsCallbacks.setCapturedWsCallback(null); };
      }),
      onOpen: vi.fn((cb: () => void) => {
        wsCallbacks.setCapturedOnOpenCallback(cb);
        return () => { wsCallbacks.setCapturedOnOpenCallback(null); };
      }),
      onClose: vi.fn(() => () => {}),
    },
  };

  return { mockDef, wsCallbacks };
}

/**
 * Send a WS message through the captured callback, wrapped in act().
 */
export function sendWsMessage(
  capturedCallback: ((data: unknown) => void) | null | undefined,
  msg: unknown
) {
  act(() => {
    capturedCallback?.(msg);
  });
}

/**
 * Emit a message to all registered WS handlers (for multi-handler setups).
 */
export function emitWsMessage(handlers: Set<(msg: ServerMessage) => void>, msg: ServerMessage) {
  handlers.forEach((handler) => handler(msg));
}

// ─── Bridge Mocking ───────────────────────────────────────────────────────────

/**
 * Create a default bridge mock with sensible fallbacks.
 * Pass overrides to customize specific methods.
 */
export function createBridgeMock(
  overrides: Record<string, any> = {}
) {
  return {
    bridge: {
      // Filesystem
      openFolderDialog: vi.fn().mockResolvedValue(null),
      setLastProjectPath: vi.fn().mockResolvedValue(undefined),

      // Editor preferences
      getEditorPreferences: vi.fn().mockResolvedValue({
        fontFamily: 'monospace',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      }),
      setEditorPreferences: vi.fn().mockResolvedValue(true),

      // GitHub
      getGithubToken: vi.fn().mockResolvedValue(null),
      getGithubRepoOwner: vi.fn().mockResolvedValue(null),
      getGithubRepoName: vi.fn().mockResolvedValue(null),
      setGithubToken: vi.fn().mockResolvedValue(true),
      setGithubRepoOwner: vi.fn().mockResolvedValue(true),
      setGithubRepoName: vi.fn().mockResolvedValue(true),

      // Model provider
      getModelProvider: vi.fn().mockResolvedValue(null),
      getModel: vi.fn().mockResolvedValue(null),
      setModelProvider: vi.fn().mockResolvedValue(true),
      setModel: vi.fn().mockResolvedValue(true),

      // LLM API keys
      getAnthropicKey: vi.fn().mockResolvedValue(null),
      getOpenAiKey: vi.fn().mockResolvedValue(null),
      setAnthropicKey: vi.fn().mockResolvedValue(true),
      setOpenAiKey: vi.fn().mockResolvedValue(true),

      // Ollama
      getOllamaBaseUrl: vi.fn().mockResolvedValue(null),
      setOllamaBaseUrl: vi.fn().mockResolvedValue(true),

      // Agent service
      agentServiceStatus: vi.fn().mockResolvedValue({ installed: false, running: false }),
      installAgentService: vi.fn().mockResolvedValue(''),
      uninstallAgentService: vi.fn().mockResolvedValue(''),

      // Local server
      getLocalServerToken: vi.fn().mockResolvedValue(null),

      // Teams
      setActiveTeam: vi.fn().mockResolvedValue(undefined),
      getActiveTeam: vi.fn().mockResolvedValue(null),

      // External
      openExternal: vi.fn(),

      // Clones directory
      getClonesDir: vi.fn().mockResolvedValue(null),
      setClonesDir: vi.fn().mockResolvedValue(true),

      ...overrides,
    },
  };
}

// ─── Component Rendering with Setup ───────────────────────────────────────────

/**
 * Render a component with store/WS setup.
 * Handles common patterns: clear mocks, render, return mocks for assertions.
 */
export async function renderWithWsSetup<T extends ReactElement>(
  component: T,
  options?: { beforeRender?: () => void; clearMocks?: string[] } & RenderOptions
): Promise<{ result: RenderResult; wsCallback: ((data: unknown) => void) | null }> {
  if (options?.beforeRender) {
    options.beforeRender();
  }

  // This is a placeholder — in practice the test will have already set up vi.mock
  // and imported wsClient. We just render the component.
  const result = render(component, options);
  return { result, wsCallback: null };
}

// ─── Mock Clearing ────────────────────────────────────────────────────────────

/**
 * Clear all mocks from a set of mock objects (e.g., bridge methods).
 */
export function clearMocks(...targets: any[]) {
  targets.forEach((target) => {
    if (target?.mockClear) target.mockClear();
    else if (typeof target === 'object') {
      Object.values(target).forEach((fn: any) => {
        if (fn?.mockClear) fn.mockClear();
      });
    }
  });
}

/**
 * Reset/clear all mocks from a bridge mock.
 */
export function clearBridgeMocks(bridge: any) {
  Object.values(bridge).forEach((fn: any) => {
    if (fn?.mockClear) fn.mockClear();
    if (fn?.mockReset) fn.mockReset();
  });
}

/**
 * Reset/clear all mocks from a ws mock.
 */
export function clearWsMocks(wsClient: any) {
  ['send', 'connect', 'disconnect', 'onMessage', 'onOpen', 'onClose'].forEach((key) => {
    if (wsClient[key]?.mockClear) wsClient[key].mockClear();
    if (wsClient[key]?.mockReset) wsClient[key].mockReset();
  });
}

// ─── Common Test Setup Patterns ────────────────────────────────────────────────

/**
 * Setup to capture WS handler in a multi-handler set (common pattern in usage-dashboard).
 */
export function createWsHandlerSet() {
  const handlers = new Set<(msg: ServerMessage) => void>();
  return {
    handlers,
    mockDefinition: {
      onMessage: (handler: (msg: ServerMessage) => void) => {
        handlers.add(handler);
        return () => handlers.delete(handler);
      },
    },
    emitToAllHandlers: (msg: ServerMessage) => {
      handlers.forEach((handler) => handler(msg));
    },
  };
}
