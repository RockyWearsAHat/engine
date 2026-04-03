/**
 * Platform bridge — abstracts Electron IPC, Tauri commands, and plain web.
 *
 * Import this module instead of calling window.electronAPI or Tauri invoke
 * directly, so the client code is platform-agnostic.
 */

// Tauri's invoke is injected at runtime via the Tauri webview bridge.
// We declare only the shape we need to avoid a hard dependency on the npm pkg.
declare global {
  interface Window {
    __TAURI__?: {
      core: {
        invoke<T = unknown>(cmd: string, args?: Record<string, unknown>): Promise<T>;
      };
      opener?: { openUrl(url: string): Promise<void> };
    };
    electronAPI?: {
      getProjectPath(): Promise<string>;
      getGithubToken(): Promise<string | null>;
      setGithubToken(token: string): Promise<boolean>;
      openExternal(url: string): Promise<void>;
      platform: string;
      isElectron: boolean;
    };
  }
}

function isTauri(): boolean {
  return typeof window !== 'undefined' && '__TAURI__' in window;
}

function isElectron(): boolean {
  return typeof window !== 'undefined' && !!window.electronAPI?.isElectron;
}

export const bridge = {
  async getProjectPath(): Promise<string> {
    if (isTauri()) {
      return window.__TAURI__!.core.invoke<string>('get_project_path');
    }
    if (isElectron()) {
      return window.electronAPI!.getProjectPath();
    }
    // Plain web / dev server — fall back to env var injected by Vite or '.'
    return (import.meta as { env?: Record<string, string> }).env?.VITE_PROJECT_PATH ?? '.';
  },

  async getGithubToken(): Promise<string | null> {
    if (isTauri()) {
      return window.__TAURI__!.core.invoke<string | null>('get_github_token');
    }
    if (isElectron()) {
      return window.electronAPI!.getGithubToken();
    }
    return null;
  },

  async setGithubToken(token: string): Promise<boolean> {
    if (isTauri()) {
      return window.__TAURI__!.core.invoke<boolean>('set_github_token', { token });
    }
    if (isElectron()) {
      return window.electronAPI!.setGithubToken(token);
    }
    return false;
  },

  async openExternal(url: string): Promise<void> {
    if (isTauri()) {
      if (window.__TAURI__?.opener) {
        await window.__TAURI__.opener.openUrl(url);
      } else {
        await window.__TAURI__!.core.invoke('open_external', { url });
      }
      return;
    }
    if (isElectron()) {
      return window.electronAPI!.openExternal(url);
    }
    window.open(url, '_blank', 'noopener,noreferrer');
  },
};
