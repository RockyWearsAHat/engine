import { vi } from 'vitest';
import { useStore } from '../store/index.js';

/**
 * Reset store to a clean state for tests.
 * Clears all sessions, messages, files, and UI state.
 */
export function resetStoreForTests() {
  useStore.setState({
    connected: false,
    sessions: [],
    activeSession: null,
    chatMessages: [],
    streamingMessageId: null,
    fileTree: null,
    openFiles: [],
    activeFilePath: null,
    gitStatus: null,
    githubToken: null,
    githubUser: null,
    githubAuthFlow: null,
    githubIssues: [],
    githubIssuesLoading: false,
    githubIssuesError: null,
    searchQuery: '',
    searchResults: [],
    searchLoading: false,
    searchError: null,
    agentSessions: [],
    activeAgentSessionId: null,
    showDotfiles: false,
  });
}

/**
 * Deprecated: use resetStoreForTests() instead.
 */
export function resetStoreForProjectAndAgentTests() {
  resetStoreForTests();
}

/**
 * Mock the ws/client module for tests.
 * Returns a module with mocked wsClient.
 */
export function mockWsClientModule() {
  return {
    wsClient: {
      send: vi.fn(),
      connect: vi.fn(),
      disconnect: vi.fn(),
      onMessage: vi.fn(),
      onOpen: vi.fn(),
      onClose: vi.fn(),
    },
  };
}