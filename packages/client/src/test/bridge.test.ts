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
  const win = window as unknown as Record<string, unknown>;
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
  it('Tauri_GetProjectPath_InvokeCalledAndPathReturned', async () => {
    const invoke = vi.fn().mockResolvedValue('/tauri/project');
    mockTauri(invoke);
    const result = await bridge.getProjectPath();
    expect(invoke).toHaveBeenCalledWith('get_project_path');
    expect(result).toBe('/tauri/project');
  });

  it('Electron_GetProjectPath_ApiCalled', async () => {
    const getProjectPath = vi.fn().mockResolvedValue('/electron/project');
    mockElectron({ getProjectPath });
    const result = await bridge.getProjectPath();
    expect(getProjectPath).toHaveBeenCalledTimes(1);
    expect(result).toBe('/electron/project');
  });

  it('Browser_GetProjectPath_LastProjectPathFromLocalStorage', async () => {
    window.localStorage.setItem('engine.lastProjectPath', '/local/project');
    const result = await bridge.getProjectPath();
    expect(result).toBe('/local/project');
  });

  it('Browser_GetProjectPath_DotAsFallback', async () => {
    const result = await bridge.getProjectPath();
    expect(['.', '']).toContain(result);
  });
});

// ─── getLocalServerToken ──────────────────────────────────────────────────────

describe('bridge.getLocalServerToken', () => {
  it('Tauri_GetLocalServerToken_InvokeCalled', async () => {
    const invoke = vi.fn().mockResolvedValue('tok-abc');
    mockTauri(invoke);
    const result = await bridge.getLocalServerToken();
    expect(invoke).toHaveBeenCalledWith('get_local_server_token');
    expect(result).toBe('tok-abc');
  });

  it('Electron_GetLocalServerToken_ApiCalled', async () => {
    const getLocalServerToken = vi.fn().mockResolvedValue('electron-tok');
    mockElectron({ getLocalServerToken });
    expect(await bridge.getLocalServerToken()).toBe('electron-tok');
    expect(getLocalServerToken).toHaveBeenCalledTimes(1);
  });

  it('Electron_GetLocalServerToken_NullWhenApiUndefined', async () => {
    mockElectron({});
    expect(await bridge.getLocalServerToken()).toBeNull();
  });

  it('Browser_GetLocalServerToken_AlwaysNull', async () => {
    expect(await bridge.getLocalServerToken()).toBeNull();
  });
});

// ─── restartLocalServer ───────────────────────────────────────────────────────

describe('bridge.restartLocalServer', () => {
  it('Tauri_RestartLocalServer_InvokeCalled', async () => {
    const invoke = vi.fn().mockResolvedValue(true);
    mockTauri(invoke);
    const result = await bridge.restartLocalServer();
    expect(invoke).toHaveBeenCalledWith('restart_local_server');
    expect(result).toBe(true);
  });

  it('Electron_RestartLocalServer_ApiCalled', async () => {
    const restartLocalServer = vi.fn().mockResolvedValue(true);
    mockElectron({ restartLocalServer });
    expect(await bridge.restartLocalServer()).toBe(true);
    expect(restartLocalServer).toHaveBeenCalledTimes(1);
  });

  it('Browser_RestartLocalServer_ReturnsFalse', async () => {
    expect(await bridge.restartLocalServer()).toBe(false);
  });
});

// ─── localServerHealthy ──────────────────────────────────────────────────────

describe('bridge.localServerHealthy', () => {
  it('Tauri_LocalServerHealthy_InvokeCalled', async () => {
    const invoke = vi.fn().mockResolvedValue(true);
    mockTauri(invoke);
    const result = await bridge.localServerHealthy();
    expect(invoke).toHaveBeenCalledWith('local_server_healthy');
    expect(result).toBe(true);
  });

  it('Electron_LocalServerHealthy_ApiCalled', async () => {
    const localServerHealthy = vi.fn().mockResolvedValue(true);
    mockElectron({ localServerHealthy });
    expect(await bridge.localServerHealthy()).toBe(true);
    expect(localServerHealthy).toHaveBeenCalledTimes(1);
  });

  it('Browser_LocalServerHealthy_ReturnsFalse', async () => {
    expect(await bridge.localServerHealthy()).toBe(false);
  });
});

// ─── getGithubToken / setGithubToken ──────────────────────────────────────────

describe('bridge.getGithubToken', () => {
  it('Tauri_GetGithubToken_InvokeCalled', async () => {
    const invoke = vi.fn().mockResolvedValue('ghp_tauri');
    mockTauri(invoke);
    expect(await bridge.getGithubToken()).toBe('ghp_tauri');
    expect(invoke).toHaveBeenCalledWith('get_github_token');
  });

  it('Electron_GetGithubToken_ApiCalled', async () => {
    const getGithubToken = vi.fn().mockResolvedValue('ghp_electron');
    mockElectron({ getGithubToken });
    expect(await bridge.getGithubToken()).toBe('ghp_electron');
  });

  it('Browser_GetGithubToken_ReadsFromLocalStorage', async () => {
    window.localStorage.setItem('engine.githubToken', 'ghp_local');
    expect(await bridge.getGithubToken()).toBe('ghp_local');
  });

  it('Browser_GetGithubToken_NullWhenAbsent', async () => {
    expect(await bridge.getGithubToken()).toBeNull();
  });
});

describe('bridge.setGithubToken', () => {
  it('Tauri_SetGithubToken_InvokeCalledWithToken', async () => {
    const invoke = vi.fn().mockResolvedValue(true);
    mockTauri(invoke);
    await bridge.setGithubToken('ghp_tauri');
    expect(invoke).toHaveBeenCalledWith('set_github_token', { token: 'ghp_tauri' });
  });

  it('Electron_SetGithubToken_ApiCalled', async () => {
    const setGithubToken = vi.fn().mockResolvedValue(true);
    mockElectron({ setGithubToken });
    await bridge.setGithubToken('ghp_electron');
    expect(setGithubToken).toHaveBeenCalledWith('ghp_electron');
  });

  it('Browser_SetGithubToken_WritesToLocalStorage', async () => {
    await bridge.setGithubToken('ghp_local');
    expect(window.localStorage.getItem('engine.githubToken')).toBe('ghp_local');
  });
});

// ─── getGithubRepoOwner / setGithubRepoOwner ─────────────────────────────────

describe('bridge.getGithubRepoOwner', () => {
  it('Tauri_GetGithubRepoOwner_InvokeCalled', async () => {
    const invoke = vi.fn().mockResolvedValue('acme');
    mockTauri(invoke);
    expect(await bridge.getGithubRepoOwner()).toBe('acme');
    expect(invoke).toHaveBeenCalledWith('get_github_owner');
  });

  it('Browser_GetGithubRepoOwner_ReadsFromLocalStorage', async () => {
    window.localStorage.setItem('engine.githubRepoOwner', 'myorg');
    expect(await bridge.getGithubRepoOwner()).toBe('myorg');
  });
});

describe('bridge.setGithubRepoOwner', () => {
  it('Tauri_SetGithubRepoOwner_InvokeCalledWithOwner', async () => {
    const invoke = vi.fn().mockResolvedValue(true);
    mockTauri(invoke);
    await bridge.setGithubRepoOwner('acme');
    expect(invoke).toHaveBeenCalledWith('set_github_owner', { owner: 'acme' });
  });

  it('Browser_SetGithubRepoOwner_WritesToLocalStorage', async () => {
    await bridge.setGithubRepoOwner('myorg');
    expect(window.localStorage.getItem('engine.githubRepoOwner')).toBe('myorg');
  });
});

// ─── openFolderDialog / openFileDialog ───────────────────────────────────────

describe('bridge.openFolderDialog', () => {
  it('Tauri_OpenFolderDialog_InvokeCalled', async () => {
    const invoke = vi.fn().mockResolvedValue('/chosen/folder');
    mockTauri(invoke);
    const result = await bridge.openFolderDialog();
    expect(invoke).toHaveBeenCalledWith('open_folder_dialog');
    expect(result).toBe('/chosen/folder');
  });

  it('Browser_OpenFolderDialog_NullNoNativeDialog', async () => {
    expect(await bridge.openFolderDialog()).toBeNull();
  });
});

describe('bridge.openFileDialog', () => {
  it('Tauri_OpenFileDialog_InvokeCalled', async () => {
    const invoke = vi.fn().mockResolvedValue('/chosen/file.ts');
    mockTauri(invoke);
    const result = await bridge.openFileDialog();
    expect(invoke).toHaveBeenCalledWith('open_file_dialog');
    expect(result).toBe('/chosen/file.ts');
  });

  it('Browser_OpenFileDialog_Null', async () => {
    expect(await bridge.openFileDialog()).toBeNull();
  });
});

// ─── setLastProjectPath ───────────────────────────────────────────────────────

describe('bridge.setLastProjectPath', () => {
  it('Tauri_SetLastProjectPath_InvokeCalledWithPath', async () => {
    const invoke = vi.fn().mockResolvedValue(true);
    mockTauri(invoke);
    await bridge.setLastProjectPath('/my/path');
    expect(invoke).toHaveBeenCalledWith('set_last_project_path', { path: '/my/path' });
  });

  it('Browser_SetLastProjectPath_WritesToLocalStorage', async () => {
    await bridge.setLastProjectPath('/local/path');
    expect(window.localStorage.getItem('engine.lastProjectPath')).toBe('/local/path');
  });
});

// ─── openExternal ─────────────────────────────────────────────────────────────

describe('bridge.openExternal', () => {
  it('TauriWithOpener_OpenExternal_OpenerOpenUrlCalled', async () => {
    const openUrl = vi.fn().mockResolvedValue(undefined);
    const invoke = vi.fn().mockResolvedValue(undefined);
    mockTauri(invoke, { openUrl });
    await bridge.openExternal('https://example.com');
    expect(openUrl).toHaveBeenCalledWith('https://example.com');
    expect(invoke).not.toHaveBeenCalled();
  });

  it('TauriWithoutOpener_OpenExternal_InvokeFallback', async () => {
    const invoke = vi.fn().mockResolvedValue(undefined);
    mockTauri(invoke);
    await bridge.openExternal('https://example.com');
    expect(invoke).toHaveBeenCalledWith('open_external', { url: 'https://example.com' });
  });

  it('Electron_OpenExternal_ApiCalled', async () => {
    const openExternal = vi.fn().mockResolvedValue(undefined);
    mockElectron({ openExternal });
    await bridge.openExternal('https://example.com');
    expect(openExternal).toHaveBeenCalledWith('https://example.com');
  });

  it('Browser_OpenExternal_WindowOpenCalledWithSafeRel', async () => {
    const windowOpen = vi.spyOn(window, 'open').mockReturnValue(null);
    await bridge.openExternal('https://example.com');
    expect(windowOpen).toHaveBeenCalledWith('https://example.com', '_blank', 'noopener,noreferrer');
  });
});

// ─── Anthropic / OpenAI keys ─────────────────────────────────────────────────

describe('bridge.getAnthropicKey / setAnthropicKey', () => {
  it('Tauri_GetAnthropicKey_InvokeCalled', async () => {
    const invoke = vi.fn().mockResolvedValue('sk-ant');
    mockTauri(invoke);
    expect(await bridge.getAnthropicKey()).toBe('sk-ant');
    expect(invoke).toHaveBeenCalledWith('get_anthropic_key');
  });

  it('Tauri_SetAnthropicKey_InvokeCalledWithKey', async () => {
    const invoke = vi.fn().mockResolvedValue(true);
    mockTauri(invoke);
    await bridge.setAnthropicKey('sk-ant');
    expect(invoke).toHaveBeenCalledWith('set_anthropic_key', { key: 'sk-ant' });
  });

  it('Browser_GetAnthropicKey_ReadsFromLocalStorage', async () => {
    window.localStorage.setItem('engine.anthropicKey', 'sk-ant-local');
    expect(await bridge.getAnthropicKey()).toBe('sk-ant-local');
  });

  it('Browser_SetAnthropicKey_WritesToLocalStorage', async () => {
    await bridge.setAnthropicKey('sk-ant-local');
    expect(window.localStorage.getItem('engine.anthropicKey')).toBe('sk-ant-local');
  });
});

describe('bridge.getOpenAiKey / setOpenAiKey', () => {
  it('Browser_GetOpenAiKey_ReadsFromLocalStorage', async () => {
    window.localStorage.setItem('engine.openaiKey', 'sk-oai');
    expect(await bridge.getOpenAiKey()).toBe('sk-oai');
  });

  it('Browser_SetOpenAiKey_WritesToLocalStorage', async () => {
    await bridge.setOpenAiKey('sk-oai');
    expect(window.localStorage.getItem('engine.openaiKey')).toBe('sk-oai');
  });
});

// ─── Model provider / Ollama URL / Model ─────────────────────────────────────

describe('bridge.getModelProvider / setModelProvider', () => {
  it('Tauri_GetModelProvider_InvokeCalled', async () => {
    const invoke = vi.fn().mockResolvedValue('ollama');
    mockTauri(invoke);
    expect(await bridge.getModelProvider()).toBe('ollama');
    expect(invoke).toHaveBeenCalledWith('get_model_provider');
  });

  it('Browser_ModelProvider_RoundTripsThroughLocalStorage', async () => {
    await bridge.setModelProvider('anthropic');
    expect(await bridge.getModelProvider()).toBe('anthropic');
    expect(window.localStorage.getItem('engine.modelProvider')).toBe('anthropic');
  });
});

describe('bridge.getOllamaBaseUrl / setOllamaBaseUrl', () => {
  it('Browser_OllamaBaseUrl_RoundTripsThroughLocalStorage', async () => {
    await bridge.setOllamaBaseUrl('http://localhost:11434');
    expect(await bridge.getOllamaBaseUrl()).toBe('http://localhost:11434');
  });
});

describe('bridge.getModel / setModel', () => {
  it('Browser_Model_RoundTripsThroughLocalStorage', async () => {
    await bridge.setModel('claude-opus-4');
    expect(await bridge.getModel()).toBe('claude-opus-4');
  });
});

// ─── Editor preferences ───────────────────────────────────────────────────────

describe('bridge.getEditorPreferences', () => {
  it('Tauri_GetEditorPreferences_InvokeCalledAndNormalized', async () => {
    const invoke = vi.fn().mockResolvedValue({ fontSize: 99, tabSize: 3 });
    mockTauri(invoke);
    const prefs = await bridge.getEditorPreferences();
    expect(invoke).toHaveBeenCalledWith('get_editor_preferences');
    // normalizeEditorPreferences should clamp fontSize to 20, default tabSize to 2
    expect(prefs.fontSize).toBe(20);
    expect(prefs.tabSize).toBe(2);
  });

  it('Browser_GetEditorPreferences_DefaultsWhenEmpty', async () => {
    const prefs = await bridge.getEditorPreferences();
    expect(prefs.fontSize).toBe(13);
    expect(prefs.tabSize).toBe(2);
  });

  it('Browser_GetEditorPreferences_NormalizesStoredPrefs', async () => {
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
  it('Tauri_SetEditorPreferences_InvokeCalledWithNormalizedSettings', async () => {
    const invoke = vi.fn().mockResolvedValue(true);
    mockTauri(invoke);
    const prefs = { fontSize: 14, tabSize: 4, lineHeight: 1.6, fontFamily: '"JetBrains Mono", "IBM Plex Mono", Menlo, Monaco, monospace', wordWrap: false, markdownViewMode: 'text' as const };
    await bridge.setEditorPreferences(prefs);
    expect(invoke).toHaveBeenCalledWith('set_editor_preferences', { settings: expect.objectContaining({ fontSize: 14, tabSize: 4 }) });
  });

  it('Browser_SetEditorPreferences_NormalizedPrefsWrittenToStorage', async () => {
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
  it('Tauri_InspectPath_InvokeCalledWithPath', async () => {
    const mockResult = { path: '/a/b/c.ts', name: 'c.ts', kind: 'file' as const, parentPath: '/a/b' };
    const invoke = vi.fn().mockResolvedValue(mockResult);
    mockTauri(invoke);
    const result = await bridge.inspectPath('/a/b/c.ts');
    expect(invoke).toHaveBeenCalledWith('inspect_path', { path: '/a/b/c.ts' });
    expect(result).toEqual(mockResult);
  });

  it('Browser_InspectPath_NameIsLastPathSegment', async () => {
    const result = await bridge.inspectPath('/home/user/main.ts');
    expect(result.name).toBe('main.ts');
    expect(result.path).toBe('/home/user/main.ts');
    expect(result.kind).toBe('file');
    expect(result.parentPath).toBe('/home/user');
  });

  it('Browser_InspectPath_RootLevelFileParentNull', async () => {
    const result = await bridge.inspectPath('file.ts');
    expect(result.parentPath).toBeNull();
  });
});

// ─── installAgentService / uninstallAgentService ─────────────────────────────

describe('bridge.installAgentService', () => {
  it('Tauri_InstallAgentService_InvokeCalled', async () => {
    const invoke = vi.fn().mockResolvedValue('Installed');
    mockTauri(invoke);
    const msg = await bridge.installAgentService();
    expect(invoke).toHaveBeenCalledWith('install_agent_service');
    expect(msg).toBe('Installed');
  });

  it('Browser_InstallAgentService_NotSupportedMessage', async () => {
    const msg = await bridge.installAgentService();
    expect(msg.toLowerCase()).toContain('not supported');
  });
});

describe('bridge.uninstallAgentService', () => {
  it('Tauri_UninstallAgentService_InvokeCalled', async () => {
    const invoke = vi.fn().mockResolvedValue('Removed');
    mockTauri(invoke);
    expect(await bridge.uninstallAgentService()).toBe('Removed');
  });

  it('Browser_UninstallAgentService_NotSupportedMessage', async () => {
    const msg = await bridge.uninstallAgentService();
    expect(msg.toLowerCase()).toContain('not supported');
  });
});

// ─── agentServiceStatus ───────────────────────────────────────────────────────

describe('bridge.agentServiceStatus', () => {
  it('Tauri_AgentServiceStatus_InvokeCalled', async () => {
    const mockStatus = { platform: 'macOS', installed: true, running: true, startupTarget: 'launchd' };
    const invoke = vi.fn().mockResolvedValue(mockStatus);
    mockTauri(invoke);
    const status = await bridge.agentServiceStatus();
    expect(invoke).toHaveBeenCalledWith('agent_service_status');
    expect(status).toEqual(mockStatus);
  });

  it('Browser_AgentServiceStatus_NotInstalledAndNotRunning', async () => {
    const status = await bridge.agentServiceStatus();
    expect(status.installed).toBe(false);
    expect(status.running).toBe(false);
    expect(typeof status.platform).toBe('string');
  });
});

// ─── Window commands (Tauri invoke wrappers) ─────────────────────────────────

describe('bridge window controls', () => {
  it('Tauri_MinimizeWindow_InvokeCalled', async () => {
    const invoke = vi.fn().mockResolvedValue(undefined);
    mockTauri(invoke);
    await bridge.minimizeWindow();
    expect(invoke).toHaveBeenCalledWith('window_minimize');
  });

  it('Tauri_ToggleMaximizeWindow_InvokeCalled', async () => {
    const invoke = vi.fn().mockResolvedValue(undefined);
    mockTauri(invoke);
    await bridge.toggleMaximizeWindow();
    expect(invoke).toHaveBeenCalledWith('window_toggle_maximize');
  });

  it('Tauri_CloseWindow_InvokeCalled', async () => {
    const invoke = vi.fn().mockResolvedValue(undefined);
    mockTauri(invoke);
    await bridge.closeWindow();
    expect(invoke).toHaveBeenCalledWith('window_close');
  });

  it('Browser_MinimizeWindow_NoopNoException', async () => {
    // Should not throw even without Tauri
    await expect(bridge.minimizeWindow()).resolves.toBeUndefined();
  });

  it('Browser_CloseWindow_NoopNoException', async () => {
    await expect(bridge.closeWindow()).resolves.toBeUndefined();
  });
});

// ─── getActiveTeam / setActiveTeam ───────────────────────────────────────────

describe('bridge.getActiveTeam / setActiveTeam', () => {
  it('Tauri_GetActiveTeam_InvokeCalled', async () => {
    const invoke = vi.fn().mockResolvedValue('alpha');
    mockTauri(invoke);
    expect(await bridge.getActiveTeam()).toBe('alpha');
    expect(invoke).toHaveBeenCalledWith('get_active_team');
  });

  it('Tauri_SetActiveTeam_InvokeCalledWithTeam', async () => {
    const invoke = vi.fn().mockResolvedValue(true);
    mockTauri(invoke);
    await bridge.setActiveTeam('alpha');
    expect(invoke).toHaveBeenCalledWith('set_active_team', { team: 'alpha' });
  });

  it('Browser_ActiveTeam_RoundTripsThroughLocalStorage', async () => {
    await bridge.setActiveTeam('beta');
    expect(await bridge.getActiveTeam()).toBe('beta');
    expect(window.localStorage.getItem('engine.activeTeam')).toBe('beta');
  });
});
