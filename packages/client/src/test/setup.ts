import { expect, afterEach, vi } from 'vitest';
import { cleanup } from '@testing-library/react';
import '@testing-library/jest-dom';

// Cleanup after each test
afterEach(() => {
  cleanup();
});

// Mock Tauri API
vi.mock('@tauri-apps/api/core', () => ({
  invoke: vi.fn(),
}));

// Mock window bridge (from FileTree dependencies)
Object.defineProperty(window, '__engineContextMenuHandler', {
  writable: true,
  value: vi.fn(),
});
