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

const githubRepoOwnerStorageKey = 'engine.githubRepoOwner';
const githubRepoNameStorageKey = 'engine.githubRepoName';

function isTauri(): boolean {
  return typeof window !== 'undefined' && '__TAURI__' in window;
}

function isElectron(): boolean {
  return typeof window !== 'undefined' && !!window.electronAPI?.isElectron;
}

function getBrowserSetting(key: string): string | null {
  if (typeof window === 'undefined') {
    return null;
  }

  try {
    const value = window.localStorage.getItem(key)?.trim();
    return value ? value : null;
  } catch {
    return null;
  }
}

function setBrowserSetting(key: string, value: string): boolean {
  if (typeof window === 'undefined') {
    return false;
  }

  try {
    const nextValue = value.trim();
    if (nextValue) {
      window.localStorage.setItem(key, nextValue);
    } else {
      window.localStorage.removeItem(key);
    }
    return true;
  } catch {
    return false;
  }
}

export interface BackgroundServiceStatus {
  platform: string;
  installed: boolean;
  running: boolean;
  startupTarget: string;
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

  async getGithubRepoOwner(): Promise<string | null> {
    if (isTauri()) {
      return window.__TAURI__!.core.invoke<string | null>('get_github_owner');
    }
    return getBrowserSetting(githubRepoOwnerStorageKey);
  },

  async setGithubRepoOwner(owner: string): Promise<boolean> {
    if (isTauri()) {
      return window.__TAURI__!.core.invoke<boolean>('set_github_owner', { owner });
    }
    return setBrowserSetting(githubRepoOwnerStorageKey, owner);
  },

  async getGithubRepoName(): Promise<string | null> {
    if (isTauri()) {
      return window.__TAURI__!.core.invoke<string | null>('get_github_repo');
    }
    return getBrowserSetting(githubRepoNameStorageKey);
  },

  async setGithubRepoName(repo: string): Promise<boolean> {
    if (isTauri()) {
      return window.__TAURI__!.core.invoke<boolean>('set_github_repo', { repo });
    }
    return setBrowserSetting(githubRepoNameStorageKey, repo);
  },

  async openFolderDialog(): Promise<string | null> {
    if (isTauri()) {
      return window.__TAURI__!.core.invoke<string | null>('open_folder_dialog');
    }
    return null;
  },

  async setLastProjectPath(path: string): Promise<boolean> {
    if (isTauri()) {
      return window.__TAURI__!.core.invoke<boolean>('set_last_project_path', { path });
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

  async getAnthropicKey(): Promise<string | null> {
    if (isTauri()) return window.__TAURI__!.core.invoke<string | null>('get_anthropic_key');
    return null;
  },

  async setAnthropicKey(key: string): Promise<boolean> {
    if (isTauri()) return window.__TAURI__!.core.invoke<boolean>('set_anthropic_key', { key });
    return false;
  },

  async getOpenAiKey(): Promise<string | null> {
    if (isTauri()) return window.__TAURI__!.core.invoke<string | null>('get_openai_key');
    return null;
  },

  async setOpenAiKey(key: string): Promise<boolean> {
    if (isTauri()) return window.__TAURI__!.core.invoke<boolean>('set_openai_key', { key });
    return false;
  },

  async getModel(): Promise<string | null> {
    if (isTauri()) return window.__TAURI__!.core.invoke<string | null>('get_model');
    return null;
  },

  async setModel(model: string): Promise<boolean> {
    if (isTauri()) return window.__TAURI__!.core.invoke<boolean>('set_model', { model });
    return false;
  },

  async installAgentService(): Promise<string> {
    if (isTauri()) return window.__TAURI__!.core.invoke<string>('install_agent_service');
    return 'Not supported on this platform.';
  },

  async uninstallAgentService(): Promise<string> {
    if (isTauri()) return window.__TAURI__!.core.invoke<string>('uninstall_agent_service');
    return 'Not supported on this platform.';
  },

  async agentServiceStatus(): Promise<BackgroundServiceStatus> {
    if (isTauri()) {
      return window.__TAURI__!.core.invoke<BackgroundServiceStatus>('agent_service_status');
    }
    return {
      platform: typeof navigator !== 'undefined' ? navigator.platform : 'web',
      installed: false,
      running: false,
      startupTarget: 'not available in web mode',
    };
  },

  async minimizeWindow(): Promise<void> {
    if (isTauri()) {
      await window.__TAURI__!.core.invoke('window_minimize');
    }
  },

  async toggleMaximizeWindow(): Promise<void> {
    if (isTauri()) {
      await window.__TAURI__!.core.invoke('window_toggle_maximize');
    }
  },

  async closeWindow(): Promise<void> {
    if (isTauri()) {
      await window.__TAURI__!.core.invoke('window_close');
    }
  },

  async startWindowDrag(): Promise<void> {
    if (isTauri()) {
      await window.__TAURI__!.core.invoke('window_start_drag');
    }
  },
};
