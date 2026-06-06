/**
 * test-helpers.ts — Shared test utilities for reducing duplication across test suites.
 *
 * Provides:
 * - WS client mock factories with callback capture
 * - Bridge mock factories with sensible defaults
 * - Store setup utilities
 * - Common cleanup patterns
 */
import { act } from '@testing-library/react';
import { vi } from 'vitest';
import type { ServerMessage } from '@engine/shared';

// ─── WS Client Mocking ────────────────────────────────────────────────────────

/**
 * Create a WS client mock's internal structure for use in vi.hoisted().
 * Call this from within vi.hoisted to capture onMessage and onOpen callbacks.
 *
 * Usage in test files:
 * const { wsCallbacks, mockDef } = vi.hoisted(() => createWsClientMockFactory());
 * vi.mock('../ws/client.js', () => mockDef);
 */
export function createWsClientMockFactory() {
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

// ─── Mock Clearing ────────────────────────────────────────────────────────────

/**
 * Clear all vi.fn() mocks recursively from an object or array of targets.
 * Clears both mockClear() and mockReset() on all vitest mocks found.
 */
export function clearAllMocks(...targets: any[]) {
  targets.forEach((target) => {
    if (!target) return;
    if (target?.mockClear) {
      target.mockClear();
      if (target?.mockReset) target.mockReset();
      return;
    }
    if (typeof target === 'object') {
      Object.values(target).forEach((fn: any) => {
        if (fn?.mockClear) {
          fn.mockClear();
          if (fn?.mockReset) fn.mockReset();
        }
      });
    }
  });
}

/**
 * @deprecated Use clearAllMocks() instead. Provided for backward compatibility.
 */
export const clearBridgeMocks = clearAllMocks;

/**
 * @deprecated Use clearAllMocks() instead. Provided for backward compatibility.
 */
export const clearWsMocks = clearAllMocks;

/**
 * @deprecated Use clearAllMocks() instead. Provided for backward compatibility.
 */
export const clearMocks = clearAllMocks;

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

// ─── Store Setup for UI Component Tests ────────────────────────────────────────

import { useStore } from '../store/index.js';
import type { EditorPreferences, Session } from '@engine/shared';

/**
 * Setup store with defaults for UI component tests.
 * Returns a reset function for use in afterEach if needed.
 */
export function setupStoreForUITests(overrides?: {
  connected?: boolean;
  activeSession?: Session | null;
  editorPreferences?: EditorPreferences;
  gitStatus?: any;
  githubUser?: any;
  openFiles?: any[];
  activeFilePath?: string | null;
}): () => void {
  const defaults = {
    connected: false,
    activeSession: {
      id: 'sess-1',
      projectPath: '/project/root',
      branchName: 'main',
      createdAt: '',
      updatedAt: '',
      summary: '',
      messageCount: 0,
    },
    editorPreferences: {
      fontFamily: 'monospace',
      fontSize: 13,
      lineHeight: 1.5,
      tabSize: 2,
      markdownViewMode: 'text' as const,
      wordWrap: false,
    },
    gitStatus: {
      branch: 'main',
      staged: [],
      unstaged: [],
      untracked: [],
      ignored: [],
      ahead: 0,
      behind: 0,
    },
    githubUser: null,
    openFiles: [],
    activeFilePath: null,
  };

  useStore.setState({
    ...defaults,
    ...overrides,
  });

  // Return a reset function for optional use in afterEach
  return () => {
    useStore.setState(defaults);
  };
}

