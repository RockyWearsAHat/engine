/**
 * bridge.test.ts — per-method call-site verification for all three platform paths:
 *   1. Tauri (window.__TAURI__.core.invoke)
 *   2. Electron (window.electronAPI)
 *   3. Web / localStorage browser fallback
 *
 * Each describe block verifies that the bridge dispatches to the RIGHT target and
 * passes the CORRECT command name / args / storage keys.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { bridge } from '../bridge.js';

// ─── Helpers ────────────────────────────────────────────────────────────────

function mockTauri(invoke: ReturnType<typeof vi.fn>, opener?: { openUrl: ReturnType<typeof vi.fn> }) {
  const tauri: Record<string, unknown> = { core: { invoke } };
  if (opener) tauri['opener'] = opener;
  Object.defineProperty(window, '__TAURI__', { configurable: true, writable: true, value: tauri });
}

function mockElectron(api: Record<string, unknown>) {
  Object.defineProperty(window, 'electronAPI', {
    configurable: true,
    writable: true,
    value: { isElectron: true, ...api },
  });
}

function clearPlatform() {
  // Must DELETE (not set to undefined) — bridge.ts uses `'__TAURI__' in window`
  // which returns true for any property that exists, even with undefined value.
  const win = window as Record<string, unknown>;
  delete win['__TAURI__'];
  delete win['electronAPI'];
  window.localStorage.clear();
}

// ─── Test Setup ──────────────────────────────────────────────────────────────

beforeEach(() => {
  clearPlatform();
});

afterEach(() => {
  vi.restoreAllMocks();
  clearPlatform();
});

// ─── getProjectPath ──────────────────────────────────────────────────────────

describe('bridge.getProjectPath', () => {
  it('Tauri: calls invoke("get_project_path") and returns the result', async () => {
    const invoke = vi.fn().mockResolvedValue('/tauri/project');
    mockTauri(invoke);
    const result = await bridge.getProjectPath();
    expect(invoke).toHaveBeenCalledWith('get_project_path');
    expect(result).toBe('/tauri/project');
  });

  it('Electron: calls electronAPI.getProjectPath()', async () => {
    const getProjectPath = vi.fn().mockResolvedValue('/electron/project');
    mockElectron({ getProjectPath });
    const result = await bridge.getProjectPath();
    expect(getProjectPath).toHaveBeenCalledTimes(1);
    expect(result).toBe('/electron/project');
  });

  it('Browser: returns lastProjectPath from localStorage when present', async () => {
    window.localStorage.setItem('engine.lastProjectPath', '/local/project');
    const result = await bridge.getProjectPath();
    expect(result).toBe('/local/project');
  });

  it('Browser: returns "." as ultimate fallback', async () => {
    const result = await bridge.getProjectPath();
    expect(['.', '']).toContain(result);
  });
});

// ─── getLocalServerToken ──────────────────────────────────────────────────────

describe('bridge.getLocalServerToken', () => {
  it('Tauri: calls invoke("get_local_server_token")', async () => {
    const invoke = vi.fn().mockResolvedValue('tok-abc');
    mockTauri(invoke);
    const result = await bridge.getLocalServerToken();
    expect(invoke).toHaveBeenCalledWith('get_local_server_token');
    expect(result).toBe('tok-abc');
  });

  it('Electron: calls electronAPI.getLocalServerToken()', async () => {
    const getLocalServerToken = vi.fn().mockResolvedValue('electron-tok');
    mockElectron({ getLocalServerToken });
    expect(await bridge.getLocalServerToken()).toBe('electron-tok');
    expect(getLocalServerToken).toHaveBeenCalledTimes(1);
  });

  it('Electron: returns null when getLocalServerToken is not defined', async () => {
    mockElectron({});
    expect(await bridge.getLocalServerToken()).toBeNull();
  });

  it('Browser: always returns null', async () => {
    expect(await bridge.getLocalServerToken()).toBeNull();
  });
});

// ─── restartLocalServer ───────────────────────────────────────────────────────

describe('bridge.restartLocalServer', () => {
  it('Tauri: calls invoke("restart_local_server")', async () => {
    const invoke = vi.fn().mockResolvedValue(true);
    mockTauri(invoke);
    const result = await bridge.restartLocalServer();
    expect(invoke).toHaveBeenCalledWith('restart_local_server');
    expect(result).toBe(true);
  });

  it('Electron: calls restartLocalServer() on the API', async () => {
    const restartLocalServer = vi.fn().mockResolvedValue(true);
    mockElectron({ restartLocalServer });
    expect(await bridge.restartLocalServer()).toBe(true);
    expect(restartLocalServer).toHaveBeenCalledTimes(1);
  });

  it('Browser: returns false', async () => {
    expect(await bridge.restartLocalServer()).toBe(false);
  });
});

// ─── getGithubToken / setGithubToken ──────────────────────────────────────────

describe('bridge.getGithubToken', () => {
  it('Tauri: calls invoke("get_github_token")', async () => {
    const invoke = vi.fn().mockResolvedValue('ghp_tauri');
    mockTauri(invoke);
    expect(await bridge.getGithubToken()).toBe('ghp_tauri');
    expect(invoke).toHaveBeenCalledWith('get_github_token');
  });

  it('Electron: calls electronAPI.getGithubToken()', async () => {
    const getGithubToken = vi.fn().mockResolvedValue('ghp_electron');
    mockElectron({ getGithubToken });
    expect(await bridge.getGithubToken()).toBe('ghp_electron');
  });

  it('Browser: reads from engine.githubToken key', async () => {
    window.localStorage.setItem('engine.githubToken', 'ghp_local');
    expect(await bridge.getGithubToken()).toBe('ghp_local');
  });

  it('Browser: returns null when key is absent', async () => {
    expect(await bridge.getGithubToken()).toBeNull();
  });
});

describe('bridge.setGithubToken', () => {
  it('Tauri: calls invoke("set_github_token", { token })', async () => {
    const invoke = vi.fn().mockResolvedValue(true);
    mockTauri(invoke);
    await bridge.setGithubToken('ghp_tauri');
    expect(invoke).toHaveBeenCalledWith('set_github_token', { token: 'ghp_tauri' });
  });

  it('Electron: calls electronAPI.setGithubToken()', async () => {
    const setGithubToken = vi.fn().mockResolvedValue(true);
    mockElectron({ setGithubToken });
    await bridge.setGithubToken('ghp_electron');
    expect(setGithubToken).toHaveBeenCalledWith('ghp_electron');
  });

  it('Browser: writes to engine.githubToken key', async () => {
    await bridge.setGithubToken('ghp_local');
    expect(window.localStorage.getItem('engine.githubToken')).toBe('ghp_local');
  });
});

// ─── getGithubRepoOwner / setGithubRepoOwner ─────────────────────────────────

describe('bridge.getGithubRepoOwner', () => {
  it('Tauri: calls invoke("get_github_owner")', async () => {
    const invoke = vi.fn().mockResolvedValue('acme');
    mockTauri(invoke);
    expect(await bridge.getGithubRepoOwner()).toBe('acme');
    expect(invoke).toHaveBeenCalledWith('get_github_owner');
  });

  it('Browser: reads from engine.githubRepoOwner key', async () => {
    window.localStorage.setItem('engine.githubRepoOwner', 'myorg');
    expect(await bridge.getGithubRepoOwner()).toBe('myorg');
  });
});

describe('bridge.setGithubRepoOwner', () => {
  it('Tauri: calls invoke("set_github_owner", { owner })', async () => {
    const invoke = vi.fn().mockResolvedValue(true);
    mockTauri(invoke);
    await bridge.setGithubRepoOwner('acme');
    expect(invoke).toHaveBeenCalledWith('set_github_owner', { owner: 'acme' });
  });

  it('Browser: writes to engine.githubRepoOwner key', async () => {
    await bridge.setGithubRepoOwner('myorg');
    expect(window.localStorage.getItem('engine.githubRepoOwner')).toBe('myorg');
  });
});

// ─── openFolderDialog / openFileDialog ───────────────────────────────────────

describe('bridge.openFolderDialog', () => {
  it('Tauri: calls invoke("open_folder_dialog")', async () => {
    const invoke = vi.fn().mockResolvedValue('/chosen/folder');
    mockTauri(invoke);
    const result = await bridge.openFolderDialog();
    expect(invoke).toHaveBeenCalledWith('open_folder_dialog');
    expect(result).toBe('/chosen/folder');
  });

  it('Browser: returns null (no native dialog)', async () => {
    expect(await bridge.openFolderDialog()).toBeNull();
  });
});

describe('bridge.openFileDialog', () => {
  it('Tauri: calls invoke("open_file_dialog")', async () => {
    const invoke = vi.fn().mockResolvedValue('/chosen/file.ts');
    mockTauri(invoke);
    const result = await bridge.openFileDialog();
    expect(invoke).toHaveBeenCalledWith('open_file_dialog');
    expect(result).toBe('/chosen/file.ts');
  });

  it('Browser: returns null', async () => {
    expect(await bridge.openFileDialog()).toBeNull();
  });
});

// ─── setLastProjectPath ───────────────────────────────────────────────────────

describe('bridge.setLastProjectPath', () => {
  it('Tauri: calls invoke("set_last_project_path", { path })', async () => {
    const invoke = vi.fn().mockResolvedValue(true);
    mockTauri(invoke);
    await bridge.setLastProjectPath('/my/path');
    expect(invoke).toHaveBeenCalledWith('set_last_project_path', { path: '/my/path' });
  });

  it('Browser: writes to engine.lastProjectPath key', async () => {
    await bridge.setLastProjectPath('/local/path');
    expect(window.localStorage.getItem('engine.lastProjectPath')).toBe('/local/path');
  });
});

// ─── openExternal ─────────────────────────────────────────────────────────────

describe('bridge.openExternal', () => {
  it('Tauri with opener: calls opener.openUrl()', async () => {
    const openUrl = vi.fn().mockResolvedValue(undefined);
    const invoke = vi.fn().mockResolvedValue(undefined);
    mockTauri(invoke, { openUrl });
    await bridge.openExternal('https://example.com');
    expect(openUrl).toHaveBeenCalledWith('https://example.com');
    expect(invoke).not.toHaveBeenCalled();
  });

  it('Tauri without opener: falls back to invoke("open_external", { url })', async () => {
    const invoke = vi.fn().mockResolvedValue(undefined);
    mockTauri(invoke);
    await bridge.openExternal('https://example.com');
    expect(invoke).toHaveBeenCalledWith('open_external', { url: 'https://example.com' });
  });

  it('Electron: calls electronAPI.openExternal()', async () => {
    const openExternal = vi.fn().mockResolvedValue(undefined);
    mockElectron({ openExternal });
    await bridge.openExternal('https://example.com');
    expect(openExternal).toHaveBeenCalledWith('https://example.com');
  });

  it('Browser: calls window.open with noopener,noreferrer', async () => {
    const windowOpen = vi.spyOn(window, 'open').mockReturnValue(null);
    await bridge.openExternal('https://example.com');
    expect(windowOpen).toHaveBeenCalledWith('https://example.com', '_blank', 'noopener,noreferrer');
  });
});

// ─── Anthropic / OpenAI keys ─────────────────────────────────────────────────

describe('bridge.getAnthropicKey / setAnthropicKey', () => {
  it('Tauri get: calls invoke("get_anthropic_key")', async () => {
    const invoke = vi.fn().mockResolvedValue('sk-ant');
    mockTauri(invoke);
    expect(await bridge.getAnthropicKey()).toBe('sk-ant');
    expect(invoke).toHaveBeenCalledWith('get_anthropic_key');
  });

  it('Tauri set: calls invoke("set_anthropic_key", { key })', async () => {
    const invoke = vi.fn().mockResolvedValue(true);
    mockTauri(invoke);
    await bridge.setAnthropicKey('sk-ant');
    expect(invoke).toHaveBeenCalledWith('set_anthropic_key', { key: 'sk-ant' });
  });

  it('Browser get: reads from engine.anthropicKey', async () => {
    window.localStorage.setItem('engine.anthropicKey', 'sk-ant-local');
    expect(await bridge.getAnthropicKey()).toBe('sk-ant-local');
  });

  it('Browser set: writes to engine.anthropicKey', async () => {
    await bridge.setAnthropicKey('sk-ant-local');
    expect(window.localStorage.getItem('engine.anthropicKey')).toBe('sk-ant-local');
  });
});

describe('bridge.getOpenAiKey / setOpenAiKey', () => {
  it('Browser get: reads from engine.openaiKey', async () => {
    window.localStorage.setItem('engine.openaiKey', 'sk-oai');
    expect(await bridge.getOpenAiKey()).toBe('sk-oai');
  });

  it('Browser set: writes to engine.openaiKey', async () => {
    await bridge.setOpenAiKey('sk-oai');
    expect(window.localStorage.getItem('engine.openaiKey')).toBe('sk-oai');
  });
});

// ─── Model provider / Ollama URL / Model ─────────────────────────────────────

describe('bridge.getModelProvider / setModelProvider', () => {
  it('Tauri get: calls invoke("get_model_provider")', async () => {
    const invoke = vi.fn().mockResolvedValue('ollama');
    mockTauri(invoke);
    expect(await bridge.getModelProvider()).toBe('ollama');
    expect(invoke).toHaveBeenCalledWith('get_model_provider');
  });

  it('Browser: round-trips via engine.modelProvider key', async () => {
    await bridge.setModelProvider('anthropic');
    expect(await bridge.getModelProvider()).toBe('anthropic');
    expect(window.localStorage.getItem('engine.modelProvider')).toBe('anthropic');
  });
});

describe('bridge.getOllamaBaseUrl / setOllamaBaseUrl', () => {
  it('Browser: round-trips via engine.ollamaBaseUrl key', async () => {
    await bridge.setOllamaBaseUrl('http://localhost:11434');
    expect(await bridge.getOllamaBaseUrl()).toBe('http://localhost:11434');
  });
});

describe('bridge.getModel / setModel', () => {
  it('Browser: round-trips via engine.model key', async () => {
    await bridge.setModel('claude-opus-4');
    expect(await bridge.getModel()).toBe('claude-opus-4');
  });
});

// ─── Editor preferences ───────────────────────────────────────────────────────

describe('bridge.getEditorPreferences', () => {
  it('Tauri: calls invoke("get_editor_preferences") and normalizes the result', async () => {
    const invoke = vi.fn().mockResolvedValue({ fontSize: 99, tabSize: 3 });
    mockTauri(invoke);
    const prefs = await bridge.getEditorPreferences();
    expect(invoke).toHaveBeenCalledWith('get_editor_preferences');
    // normalizeEditorPreferences should clamp fontSize to 20, default tabSize to 2
    expect(prefs.fontSize).toBe(20);
    expect(prefs.tabSize).toBe(2);
  });

  it('Browser: returns defaults when no preferences stored', async () => {
    const prefs = await bridge.getEditorPreferences();
    expect(prefs.fontSize).toBe(13);
    expect(prefs.tabSize).toBe(2);
  });

  it('Browser: reads and normalizes stored preferences', async () => {
    window.localStorage.setItem(
      'engine.editorPreferences',
      JSON.stringify({ fontSize: 16, tabSize: 4, wordWrap: true }),
    );
    const prefs = await bridge.getEditorPreferences();
    expect(prefs.fontSize).toBe(16);
    expect(prefs.tabSize).toBe(4);
    expect(prefs.wordWrap).toBe(true);
  });
});

describe('bridge.setEditorPreferences', () => {
  it('Tauri: calls invoke("set_editor_preferences", { settings }) with normalized settings', async () => {
    const invoke = vi.fn().mockResolvedValue(true);
    mockTauri(invoke);
    const prefs = { fontSize: 14, tabSize: 4, lineHeight: 1.6, fontFamily: '"JetBrains Mono", "IBM Plex Mono", Menlo, Monaco, monospace', wordWrap: false, markdownViewMode: 'text' as const };
    await bridge.setEditorPreferences(prefs);
    expect(invoke).toHaveBeenCalledWith('set_editor_preferences', { settings: expect.objectContaining({ fontSize: 14, tabSize: 4 }) });
  });

  it('Browser: persists normalized preferences to engine.editorPreferences', async () => {
    const prefs = { fontSize: 16, tabSize: 8, lineHeight: 1.8, fontFamily: '"JetBrains Mono", "IBM Plex Mono", Menlo, Monaco, monospace', wordWrap: true, markdownViewMode: 'preview' as const };
    await bridge.setEditorPreferences(prefs);
    const stored = JSON.parse(window.localStorage.getItem('engine.editorPreferences')!);
    expect(stored.fontSize).toBe(16);
    expect(stored.tabSize).toBe(8);
    expect(stored.wordWrap).toBe(true);
    expect(stored.markdownViewMode).toBe('preview');
  });
});

// ─── inspectPath ──────────────────────────────────────────────────────────────

describe('bridge.inspectPath', () => {
  it('Tauri: calls invoke("inspect_path", { path })', async () => {
    const mockResult = { path: '/a/b/c.ts', name: 'c.ts', kind: 'file' as const, parentPath: '/a/b' };
    const invoke = vi.fn().mockResolvedValue(mockResult);
    mockTauri(invoke);
    const result = await bridge.inspectPath('/a/b/c.ts');
    expect(invoke).toHaveBeenCalledWith('inspect_path', { path: '/a/b/c.ts' });
    expect(result).toEqual(mockResult);
  });

  it('Browser: extracts name as the last path segment', async () => {
    const result = await bridge.inspectPath('/home/user/main.ts');
    expect(result.name).toBe('main.ts');
    expect(result.path).toBe('/home/user/main.ts');
    expect(result.kind).toBe('file');
    expect(result.parentPath).toBe('/home/user');
  });

  it('Browser: sets parentPath to null for a root-level file', async () => {
    const result = await bridge.inspectPath('file.ts');
    expect(result.parentPath).toBeNull();
  });
});

// ─── installAgentService / uninstallAgentService ─────────────────────────────

describe('bridge.installAgentService', () => {
  it('Tauri: calls invoke("install_agent_service")', async () => {
    const invoke = vi.fn().mockResolvedValue('Installed');
    mockTauri(invoke);
    const msg = await bridge.installAgentService();
    expect(invoke).toHaveBeenCalledWith('install_agent_service');
    expect(msg).toBe('Installed');
  });

  it('Browser: returns a "not supported" message', async () => {
    const msg = await bridge.installAgentService();
    expect(msg.toLowerCase()).toContain('not supported');
  });
});

describe('bridge.uninstallAgentService', () => {
  it('Tauri: calls invoke("uninstall_agent_service")', async () => {
    const invoke = vi.fn().mockResolvedValue('Removed');
    mockTauri(invoke);
    expect(await bridge.uninstallAgentService()).toBe('Removed');
  });

  it('Browser: returns a "not supported" message', async () => {
    const msg = await bridge.uninstallAgentService();
    expect(msg.toLowerCase()).toContain('not supported');
  });
});

// ─── agentServiceStatus ───────────────────────────────────────────────────────

describe('bridge.agentServiceStatus', () => {
  it('Tauri: calls invoke("agent_service_status")', async () => {
    const mockStatus = { platform: 'macOS', installed: true, running: true, startupTarget: 'launchd' };
    const invoke = vi.fn().mockResolvedValue(mockStatus);
    mockTauri(invoke);
    const status = await bridge.agentServiceStatus();
    expect(invoke).toHaveBeenCalledWith('agent_service_status');
    expect(status).toEqual(mockStatus);
  });

  it('Browser: returns installed=false and running=false', async () => {
    const status = await bridge.agentServiceStatus();
    expect(status.installed).toBe(false);
    expect(status.running).toBe(false);
    expect(typeof status.platform).toBe('string');
  });
});

// ─── Window commands (Tauri invoke wrappers) ─────────────────────────────────

describe('bridge window controls', () => {
  it('minimizeWindow: calls invoke("window_minimize")', async () => {
    const invoke = vi.fn().mockResolvedValue(undefined);
    mockTauri(invoke);
    await bridge.minimizeWindow();
    expect(invoke).toHaveBeenCalledWith('window_minimize');
  });

  it('toggleMaximizeWindow: calls invoke("window_toggle_maximize")', async () => {
    const invoke = vi.fn().mockResolvedValue(undefined);
    mockTauri(invoke);
    await bridge.toggleMaximizeWindow();
    expect(invoke).toHaveBeenCalledWith('window_toggle_maximize');
  });

  it('closeWindow: calls invoke("window_close")', async () => {
    const invoke = vi.fn().mockResolvedValue(undefined);
    mockTauri(invoke);
    await bridge.closeWindow();
    expect(invoke).toHaveBeenCalledWith('window_close');
  });

  it('minimizeWindow: is a no-op in the browser (no invoke)', async () => {
    // Should not throw even without Tauri
    await expect(bridge.minimizeWindow()).resolves.toBeUndefined();
  });

  it('closeWindow: is a no-op in the browser (no invoke)', async () => {
    await expect(bridge.closeWindow()).resolves.toBeUndefined();
  });
});

// ─── getActiveTeam / setActiveTeam ───────────────────────────────────────────

describe('bridge.getActiveTeam / setActiveTeam', () => {
  it('Tauri get: calls invoke("get_active_team")', async () => {
    const invoke = vi.fn().mockResolvedValue('alpha');
    mockTauri(invoke);
    expect(await bridge.getActiveTeam()).toBe('alpha');
    expect(invoke).toHaveBeenCalledWith('get_active_team');
  });

  it('Tauri set: calls invoke("set_active_team", { team })', async () => {
    const invoke = vi.fn().mockResolvedValue(true);
    mockTauri(invoke);
    await bridge.setActiveTeam('alpha');
    expect(invoke).toHaveBeenCalledWith('set_active_team', { team: 'alpha' });
  });

  it('Browser: round-trips via engine.activeTeam key', async () => {
    await bridge.setActiveTeam('beta');
    expect(await bridge.getActiveTeam()).toBe('beta');
    expect(window.localStorage.getItem('engine.activeTeam')).toBe('beta');
  });
});
