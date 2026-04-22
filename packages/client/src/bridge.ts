/**
 * Platform bridge — abstracts Electron IPC, Tauri commands, and plain web.
 *
 * Import this module instead of calling window.electronAPI or Tauri invoke
 * directly, so the client code is platform-agnostic.
 */

import {
  DEFAULT_EDITOR_PREFERENCES,
  normalizeEditorPreferences,
  type EditorPreferences,
} from './editorPreferences.js';
import { loadActiveConnectionProfile } from './connectionProfiles.js';

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
      getLocalServerToken?(): Promise<string | null>;
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
const githubTokenStorageKey = 'engine.githubToken';
const anthropicKeyStorageKey = 'engine.anthropicKey';
const openAiKeyStorageKey = 'engine.openaiKey';
const modelProviderStorageKey = 'engine.modelProvider';
const ollamaBaseUrlStorageKey = 'engine.ollamaBaseUrl';
const modelStorageKey = 'engine.model';
const lastProjectPathStorageKey = 'engine.lastProjectPath';
const editorPreferencesStorageKey = 'engine.editorPreferences';

export interface InspectedPath {
  path: string;
  name: string;
  kind: 'file' | 'directory';
  parentPath: string | null;
}

function isTauri(): boolean {
  return typeof window !== 'undefined' && '__TAURI__' in window;
}

function isElectron(): boolean {
  return typeof window !== 'undefined' && !!window.electronAPI?.isElectron;
}

async function getCurrentTauriWindow() {
  const { getCurrentWindow } = await import('@tauri-apps/api/window');
  return getCurrentWindow();
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

function getBrowserJson<T>(key: string): T | null {
  if (typeof window === 'undefined') {
    return null;
  }

  try {
    const raw = window.localStorage.getItem(key);
    if (!raw) {
      return null;
    }
    return JSON.parse(raw) as T;
  } catch {
    return null;
  }
}

function setBrowserJson(key: string, value: unknown): boolean {
  if (typeof window === 'undefined') {
    return false;
  }

  try {
    window.localStorage.setItem(key, JSON.stringify(value));
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
    const remoteWorkspacePath = loadActiveConnectionProfile()?.workspacePath?.trim();
    if (remoteWorkspacePath) {
      return remoteWorkspacePath;
    }
    const lastProjectPath = getBrowserSetting(lastProjectPathStorageKey);
    if (lastProjectPath) {
      return lastProjectPath;
    }
    // Plain web / dev server — fall back to env var injected by Vite or '.'
    return (import.meta as { env?: Record<string, string> }).env?.VITE_PROJECT_PATH ?? '.';
  },

  async getLocalServerToken(): Promise<string | null> {
    if (isTauri()) {
      return window.__TAURI__!.core.invoke<string | null>('get_local_server_token');
    }
    if (isElectron()) {
      return window.electronAPI!.getLocalServerToken?.() ?? null;
    }
    return null;
  },

  async getGithubToken(): Promise<string | null> {
    if (isTauri()) {
      return window.__TAURI__!.core.invoke<string | null>('get_github_token');
    }
    if (isElectron()) {
      return window.electronAPI!.getGithubToken();
    }
    return getBrowserSetting(githubTokenStorageKey);
  },

  async setGithubToken(token: string): Promise<boolean> {
    if (isTauri()) {
      return window.__TAURI__!.core.invoke<boolean>('set_github_token', { token });
    }
    if (isElectron()) {
      return window.electronAPI!.setGithubToken(token);
    }
    return setBrowserSetting(githubTokenStorageKey, token);
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

  async openFileDialog(): Promise<string | null> {
    if (isTauri()) {
      return window.__TAURI__!.core.invoke<string | null>('open_file_dialog');
    }
    return null;
  },

  async setLastProjectPath(path: string): Promise<boolean> {
    if (isTauri()) {
      return window.__TAURI__!.core.invoke<boolean>('set_last_project_path', { path });
    }
    return setBrowserSetting(lastProjectPathStorageKey, path);
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
    return getBrowserSetting(anthropicKeyStorageKey);
  },

  async setAnthropicKey(key: string): Promise<boolean> {
    if (isTauri()) return window.__TAURI__!.core.invoke<boolean>('set_anthropic_key', { key });
    return setBrowserSetting(anthropicKeyStorageKey, key);
  },

  async getOpenAiKey(): Promise<string | null> {
    if (isTauri()) return window.__TAURI__!.core.invoke<string | null>('get_openai_key');
    return getBrowserSetting(openAiKeyStorageKey);
  },

  async setOpenAiKey(key: string): Promise<boolean> {
    if (isTauri()) return window.__TAURI__!.core.invoke<boolean>('set_openai_key', { key });
    return setBrowserSetting(openAiKeyStorageKey, key);
  },

  async getModelProvider(): Promise<string | null> {
    if (isTauri()) return window.__TAURI__!.core.invoke<string | null>('get_model_provider');
    return getBrowserSetting(modelProviderStorageKey);
  },

  async setModelProvider(provider: string): Promise<boolean> {
    if (isTauri()) return window.__TAURI__!.core.invoke<boolean>('set_model_provider', { provider });
    return setBrowserSetting(modelProviderStorageKey, provider);
  },

  async getOllamaBaseUrl(): Promise<string | null> {
    if (isTauri()) return window.__TAURI__!.core.invoke<string | null>('get_ollama_base_url');
    return getBrowserSetting(ollamaBaseUrlStorageKey);
  },

  async setOllamaBaseUrl(baseUrl: string): Promise<boolean> {
    if (isTauri()) return window.__TAURI__!.core.invoke<boolean>('set_ollama_base_url', { baseUrl });
    return setBrowserSetting(ollamaBaseUrlStorageKey, baseUrl);
  },

  async getModel(): Promise<string | null> {
    if (isTauri()) return window.__TAURI__!.core.invoke<string | null>('get_model');
    return getBrowserSetting(modelStorageKey);
  },

  async setModel(model: string): Promise<boolean> {
    if (isTauri()) return window.__TAURI__!.core.invoke<boolean>('set_model', { model });
    return setBrowserSetting(modelStorageKey, model);
  },

  async getEditorPreferences(): Promise<EditorPreferences> {
    if (isTauri()) {
      const stored = await window.__TAURI__!.core.invoke<Partial<EditorPreferences>>('get_editor_preferences');
      return normalizeEditorPreferences(stored);
    }

    return normalizeEditorPreferences(getBrowserJson<EditorPreferences>(editorPreferencesStorageKey) ?? DEFAULT_EDITOR_PREFERENCES);
  },

  async setEditorPreferences(settings: EditorPreferences): Promise<boolean> {
    const nextSettings = normalizeEditorPreferences(settings);
    if (isTauri()) {
      return window.__TAURI__!.core.invoke<boolean>('set_editor_preferences', { settings: nextSettings });
    }
    return setBrowserJson(editorPreferencesStorageKey, nextSettings);
  },

  async inspectPath(path: string): Promise<InspectedPath> {
    if (isTauri()) {
      return window.__TAURI__!.core.invoke<InspectedPath>('inspect_path', { path });
    }

    const normalizedPath = path.trim();
    const name = normalizedPath.split(/[\\/]/).pop() || normalizedPath;
    const parentPath = normalizedPath.includes('/')
      ? normalizedPath.replace(/[/\\][^/\\]+$/, '')
      : null;
    return {
      path: normalizedPath,
      name,
      kind: 'file',
      parentPath,
    };
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

  async toggleFullscreenWindow(): Promise<void> {
    if (isTauri()) {
      await window.__TAURI__!.core.invoke('window_toggle_fullscreen');
    }
  },

  async isWindowFullscreen(): Promise<boolean> {
    if (isTauri()) {
      try {
        const currentWindow = await getCurrentTauriWindow();
        return await currentWindow.isFullscreen();
      } catch {
        return false;
      }
    }
    return false;
  },

  async closeWindow(): Promise<void> {
    if (isTauri()) {
      await window.__TAURI__!.core.invoke('window_close');
    }
  },

  async startWindowDrag(): Promise<void> {
    if (isTauri()) {
      const currentWindow = await getCurrentTauriWindow();
      if ('startDragging' in currentWindow && typeof currentWindow.startDragging === 'function') {
        try {
          await currentWindow.startDragging();
          return;
        } catch (windowApiError) {
          try {
            await window.__TAURI__!.core.invoke('window_start_drag');
            return;
          } catch (commandError) {
            const primaryMessage = windowApiError instanceof Error ? windowApiError.message : String(windowApiError);
            const fallbackMessage = commandError instanceof Error ? commandError.message : String(commandError);
            throw new Error(`Window dragging failed via the Tauri window API (${primaryMessage}) and the command fallback (${fallbackMessage}).`);
          }
        }
      }
      await window.__TAURI__!.core.invoke('window_start_drag');
    }
  },
};
