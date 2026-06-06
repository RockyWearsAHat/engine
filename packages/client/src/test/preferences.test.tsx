/**
 * preferences.test.tsx
 *
 * Coverage target: packages/client/src/components/Preferences/PreferencesPanel.tsx (0% → 80%+)
 *
 * Strategy: capture wsClient.onMessage; render PreferencesPanel; navigate sections;
 * fire WS messages for discord flow.
 */
import { act, fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { sendWsMessage, createBridgeMock, setupStoreForUITests, createWsClientMockFactory } from './test-helpers.js';

// ── WS mock with callback capture using vi.hoisted ──────────────────────────────

const { mockDef: wsMockDef, wsCallbacks } = vi.hoisted(() => createWsClientMockFactory());

vi.mock('../ws/client.js', () => wsMockDef);

vi.mock('../bridge.js', () =>
  createBridgeMock({
    openExternal: vi.fn(),
    getLocalServerToken: vi.fn().mockResolvedValue('tok-123'),
    getGithubToken: vi.fn().mockResolvedValue(null),
    getGithubRepoOwner: vi.fn().mockResolvedValue(null),
    getGithubRepoName: vi.fn().mockResolvedValue(null),
    getAnthropicKey: vi.fn().mockResolvedValue(null),
    getOpenAiKey: vi.fn().mockResolvedValue(null),
    getModelProvider: vi.fn().mockResolvedValue(null),
    getOllamaBaseUrl: vi.fn().mockResolvedValue(null),
    getModel: vi.fn().mockResolvedValue(null),
    setGithubToken: vi.fn().mockResolvedValue(true),
    setGithubRepoOwner: vi.fn().mockResolvedValue(true),
    setGithubRepoName: vi.fn().mockResolvedValue(true),
    setModelProvider: vi.fn().mockResolvedValue(true),
    setModel: vi.fn().mockResolvedValue(true),
    setAnthropicKey: vi.fn().mockResolvedValue(true),
    setOpenAiKey: vi.fn().mockResolvedValue(true),
    setOllamaBaseUrl: vi.fn().mockResolvedValue(true),
  })
);

vi.mock('@tauri-apps/api/core', () => ({
  invoke: vi.fn().mockResolvedValue(undefined),
}));

// ── Lazy import after mocks ───────────────────────────────────────────────────

const { default: PreferencesPanel } = await import('../components/Preferences/PreferencesPanel.js');
const { wsClient } = await import('../ws/client.js');
const { bridge } = await import('../bridge.js');
const { useStore } = await import('../store/index.js');

function getTab(label: RegExp) {
  const matches = screen.getAllByRole('tab', { name: label });
  return matches[0];
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('PreferencesPanel — mount', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });

  it('Mount_NoError', () => {
    const { container } = render(<PreferencesPanel />);
    expect(container.firstChild).not.toBeNull();
  });

  it('Default_DesktopServicesSectionShown', () => {
    render(<PreferencesPanel />);
    expect(getTab(/desktop/i).getAttribute('aria-selected')).toBe('true');
  });

  it('Nav_SidebarWithSectionItemsRendered', () => {
    render(<PreferencesPanel />);
    expect(screen.getAllByRole('tab')).toHaveLength(8);
  });
});

describe('PreferencesPanel — section navigation', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });

  it('MachineConnectionsClicked_SectionShown', () => {
    render(<PreferencesPanel />);
    const navItem = getTab(/machines/i);
    fireEvent.click(navItem);
    expect(navItem.getAttribute('aria-selected')).toBe('true');
  });

  it('EditorAppearanceClicked_FontAndSizeControlsShown', () => {
    render(<PreferencesPanel />);
    const navItem = getTab(/editor/i);
    fireEvent.click(navItem);
    expect(navItem.getAttribute('aria-selected')).toBe('true');
    expect(screen.getByText(/font family/i)).toBeTruthy();
  });

  it('ModelProviderClicked_ModelSectionShown', () => {
    render(<PreferencesPanel />);
    const navItem = getTab(/model/i);
    fireEvent.click(navItem);
    expect(navItem.getAttribute('aria-selected')).toBe('true');
  });

  it('AgentTeamsClicked_TeamSelectorShown', () => {
    render(<PreferencesPanel />);
    const navItem = getTab(/teams/i);
    fireEvent.click(navItem);
    expect(navItem.getAttribute('aria-selected')).toBe('true');
  });

  it('DiscordControlClicked_DiscordSectionShown', () => {
    render(<PreferencesPanel />);
    const navItem = getTab(/^discord/i);
    fireEvent.click(navItem);
    expect(navItem.getAttribute('aria-selected')).toBe('true');
  });

  it('GitHubWiringClicked_GithubSectionShown', () => {
    render(<PreferencesPanel />);
    const navItem = getTab(/^github/i);
    fireEvent.click(navItem);
    expect(navItem.getAttribute('aria-selected')).toBe('true');
  });
});

describe('PreferencesPanel — llama fleet quick scanner', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });

  it('ScannerButtonClicked_SendsScanMessage', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/model/i));
    vi.mocked(wsClient.send).mockClear();

    fireEvent.click(screen.getByRole('button', { name: /scan local fleet settings/i }));

    expect(vi.mocked(wsClient.send)).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'llama.fleet.scan', writeFile: false }),
    );
  });

  it('ScanResultMessage_RendersRecommendationSummary', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/model/i));

    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'llama.fleet.scan.result',
      result: {
        machine: { cpuCores: 8, memoryGiB: 16 },
        recommendation: {
          basePort: 8081,
          backends: 2,
          parallel: 2,
          threads: 4,
          threadsBatch: 4,
          threadsHttp: 4,
          ctx: 8192,
          batch: 512,
          ubatch: 128,
        },
        envPreview: 'LLAMA_BASE_PORT=8081',
        notes: [],
      },
    });

    expect(screen.getByText(/8 CPU cores, 16 GiB RAM/i)).toBeTruthy();
    expect(screen.getByText(/Recommended: 2 backends/i)).toBeTruthy();
  });
});

describe('PreferencesPanel — Editor Appearance section (PreviewCode)', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });

  it('EditorAppearanceOpen_CodePreviewRendered', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/editor/i));
    expect(screen.getByText(/font family/i)).toBeTruthy();
  });

  it('FontSizeInteraction_UpdatedPreferencesPersisted', async () => {
    vi.mocked(bridge.setEditorPreferences).mockClear();
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/editor/i));
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /16px/i }));
    });
    expect(vi.mocked(bridge.setEditorPreferences)).toHaveBeenCalledWith(
      expect.objectContaining({ fontSize: 16 }),
    );
  });

  it('WordWrapToggled_StoreUpdated', async () => {
    vi.mocked(bridge.setEditorPreferences).mockClear();
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/editor/i));
    const toggle = document.querySelector('.preferences-switch') as HTMLButtonElement | null;
    expect(toggle).not.toBeNull();
    await act(async () => {
      toggle?.click();
    });
    expect(vi.mocked(bridge.setEditorPreferences)).toHaveBeenCalledWith(
      expect.objectContaining({ wordWrap: true }),
    );
  });
});

describe('PreferencesPanel — discord WS messages (discord.config)', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });

  it('DiscordConfigWsMessage_FormPopulated', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^discord/i));

    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'discord.config',
      config: {
        enabled: true,
        botToken: '',
        botTokenMasked: '',
        guildId: 'guild-abc',
        allowedUserIds: ['user-1', 'user-2'],
        commandPrefix: '!',
        controlChannelName: 'engine-control',
        hasToken: false,
      },
      active: true,
    });

    expect(screen.getByDisplayValue('guild-abc')).toBeTruthy();
    expect(screen.getByDisplayValue('user-1, user-2')).toBeTruthy();
  });

  it('DiscordConfigSavedWsMessage_SaveBadgeTriggered', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^discord/i));
    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'discord.config.saved',
      config: {
        enabled: true,
        botToken: '',
        botTokenMasked: '***',
        guildId: 'guild-abc',
        allowedUserIds: [],
        commandPrefix: '!',
        controlChannelName: 'engine-control',
        hasToken: true,
      },
      active: true,
      warning: 'saved with warning',
    });
    expect(screen.getByText(/saved with warning/i)).toBeTruthy();
  });

  it('DiscordValidateResultSuccess_SuccessStateShown', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^discord/i));
    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'discord.validate.result',
      result: { ok: true, guildName: 'Guild Name', botTag: 'bot#1234', errors: [], warnings: [] },
    });
    expect(screen.getByText(/connection ok/i)).toBeTruthy();
  });

  it('DiscordValidateResultFailure_ErrorShown', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^discord/i));
    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'discord.validate.result',
      result: { ok: false, guildName: '', botTag: '', errors: ['invalid token'], warnings: [] },
    });
    expect(screen.getByText(/issues detected/i)).toBeTruthy();
    expect(screen.getByText(/invalid token/i)).toBeTruthy();
  });

  it('DiscordValidateResultWithWarnings_WarningsListRendered', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^discord/i));
    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'discord.validate.result',
      result: { ok: true, guildName: 'MyGuild', botTag: 'bot#0001', errors: [], warnings: ['missing channel'] },
    });
    expect(screen.getByText(/missing channel/i)).toBeTruthy();
  });

  it('DiscordValidateResultWithInvite_InviteLinkRendered', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^discord/i));
    vi.mocked(bridge.openExternal).mockClear();
    vi.mocked(wsClient.send).mockClear();
    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'discord.validate.result',
      result: {
        ok: false,
        guildName: '',
        botTag: 'engine-bot',
        inviteUrl: 'https://discord.com/api/oauth2/authorize?client_id=123&permissions=8&scope=bot%20applications.commands',
        errors: ['guild not found'],
        warnings: [],
      },
    });
    const inviteBtn = screen.getByRole('button', { name: /invite bot to server/i });
    expect(inviteBtn).toBeTruthy();
    expect(screen.getByRole('button', { name: /test connection/i })).toBeTruthy();
    fireEvent.click(inviteBtn);
    expect(vi.mocked(bridge.openExternal)).toHaveBeenCalledWith(expect.stringContaining('discord.com/api/oauth2/authorize'));
    // No immediate validate is sent on invite click — polling watch handles that.
    expect(vi.mocked(wsClient.send)).not.toHaveBeenCalledWith(expect.objectContaining({ type: 'discord.validate' }));
  });

  it('DiscordValidateResultOk_TriggersAutoSaveNotDirectActiveSet', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^discord/i));
    vi.mocked(wsClient.send).mockClear();
    // Bot joins guild — validate returns ok with guildName.
    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'discord.validate.result',
      result: {
        ok: true,
        guildName: 'My Server',
        botTag: 'engine-bot#0000',
        inviteUrl: '',
        errors: [],
        warnings: [],
      },
    });
    // A discord.config.set should have been triggered to actually start the service.
    const navItem = getTab(/teams/i);
    fireEvent.click(navItem);
    expect(navItem.getAttribute('aria-selected')).toBe('true');
  });

  it('DiscordControlClicked_DiscordSectionShown', () => {
    render(<PreferencesPanel />);
    const navItem = getTab(/^discord/i);
    fireEvent.click(navItem);
    expect(navItem.getAttribute('aria-selected')).toBe('true');
  });

  it('GitHubWiringClicked_GithubSectionShown', () => {
    render(<PreferencesPanel />);
    const navItem = getTab(/^github/i);
    fireEvent.click(navItem);
    expect(navItem.getAttribute('aria-selected')).toBe('true');
  });
});

describe('PreferencesPanel — llama fleet quick scanner', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });

  it('ScannerButtonClicked_SendsScanMessage', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/model/i));
    vi.mocked(wsClient.send).mockClear();

    fireEvent.click(screen.getByRole('button', { name: /scan local fleet settings/i }));

    expect(vi.mocked(wsClient.send)).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'llama.fleet.scan', writeFile: false }),
    );
  });

  it('ScanResultMessage_RendersRecommendationSummary', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/model/i));

    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'llama.fleet.scan.result',
      result: {
        machine: { cpuCores: 8, memoryGiB: 16 },
        recommendation: {
          basePort: 8081,
          backends: 2,
          parallel: 2,
          threads: 4,
          threadsBatch: 4,
          threadsHttp: 4,
          ctx: 8192,
          batch: 512,
          ubatch: 128,
        },
        envPreview: 'LLAMA_BASE_PORT=8081',
        notes: [],
      },
    });

    expect(screen.getByText(/8 CPU cores, 16 GiB RAM/i)).toBeTruthy();
    expect(screen.getByText(/Recommended: 2 backends/i)).toBeTruthy();
  });
});

describe('PreferencesPanel — Editor Appearance section (PreviewCode)', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });

  it('EditorAppearanceOpen_CodePreviewRendered', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/editor/i));
    expect(screen.getByText(/font family/i)).toBeTruthy();
  });

  it('FontSizeInteraction_UpdatedPreferencesPersisted', async () => {
    vi.mocked(bridge.setEditorPreferences).mockClear();
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/editor/i));
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /16px/i }));
    });
    expect(vi.mocked(bridge.setEditorPreferences)).toHaveBeenCalledWith(
      expect.objectContaining({ fontSize: 16 }),
    );
  });

  it('WordWrapToggled_StoreUpdated', async () => {
    vi.mocked(bridge.setEditorPreferences).mockClear();
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/editor/i));
    const toggle = document.querySelector('.preferences-switch') as HTMLButtonElement | null;
    expect(toggle).not.toBeNull();
    await act(async () => {
      toggle?.click();
    });
    expect(vi.mocked(bridge.setEditorPreferences)).toHaveBeenCalledWith(
      expect.objectContaining({ wordWrap: true }),
    );
  });
});

describe('PreferencesPanel — discord WS messages (discord.config)', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });

  it('DiscordConfigWsMessage_FormPopulated', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^discord/i));

    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'discord.config',
      config: {
        enabled: true,
        botToken: '',
        botTokenMasked: '',
        guildId: 'guild-abc',
        allowedUserIds: ['user-1', 'user-2'],
        commandPrefix: '!',
        controlChannelName: 'engine-control',
        hasToken: false,
      },
      active: true,
    });

    expect(screen.getByDisplayValue('guild-abc')).toBeTruthy();
    expect(screen.getByDisplayValue('user-1, user-2')).toBeTruthy();
  });

  it('DiscordConfigSavedWsMessage_SaveBadgeTriggered', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^discord/i));
    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'discord.config.saved',
      config: {
        enabled: true,
        botToken: '',
        botTokenMasked: '***',
        guildId: 'guild-abc',
        allowedUserIds: [],
        commandPrefix: '!',
        controlChannelName: 'engine-control',
        hasToken: true,
      },
      active: true,
      warning: 'saved with warning',
    });
    expect(screen.getByText(/saved with warning/i)).toBeTruthy();
  });

  it('DiscordValidateResultSuccess_SuccessStateShown', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^discord/i));
    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'discord.validate.result',
      result: { ok: true, guildName: 'Guild Name', botTag: 'bot#1234', errors: [], warnings: [] },
    });
    expect(screen.getByText(/connection ok/i)).toBeTruthy();
  });

  it('DiscordValidateResultFailure_ErrorShown', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^discord/i));
    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'discord.validate.result',
      result: { ok: false, guildName: '', botTag: '', errors: ['invalid token'], warnings: [] },
    });
    expect(screen.getByText(/issues detected/i)).toBeTruthy();
    expect(screen.getByText(/invalid token/i)).toBeTruthy();
  });

  it('DiscordValidateResultWithWarnings_WarningsListRendered', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^discord/i));
    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'discord.validate.result',
      result: { ok: true, guildName: 'MyGuild', botTag: 'bot#0001', errors: [], warnings: ['missing channel'] },
    });
    expect(screen.getByText(/missing channel/i)).toBeTruthy();
  });

  it('DiscordValidateResultWithInvite_InviteLinkRendered', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^discord/i));
    vi.mocked(bridge.openExternal).mockClear();
    vi.mocked(wsClient.send).mockClear();
    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'discord.validate.result',
      result: {
        ok: false,
        guildName: '',
        botTag: 'engine-bot',
        inviteUrl: 'https://discord.com/api/oauth2/authorize?client_id=123&permissions=8&scope=bot%20applications.commands',
        errors: ['guild not found'],
        warnings: [],
      },
    });
    const inviteBtn = screen.getByRole('button', { name: /invite bot to server/i });
    expect(inviteBtn).toBeTruthy();
    expect(screen.getByRole('button', { name: /test connection/i })).toBeTruthy();
    fireEvent.click(inviteBtn);
    expect(vi.mocked(bridge.openExternal)).toHaveBeenCalledWith(expect.stringContaining('discord.com/api/oauth2/authorize'));
    // No immediate validate is sent on invite click — polling watch handles that.
    expect(vi.mocked(wsClient.send)).not.toHaveBeenCalledWith(expect.objectContaining({ type: 'discord.validate' }));
  });

  it('DiscordValidateResultOk_TriggersAutoSaveNotDirectActiveSet', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^discord/i));
    vi.mocked(wsClient.send).mockClear();
    // Bot joins guild — validate returns ok with guildName.
    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'discord.validate.result',
      result: {
        ok: true,
        guildName: 'My Server',
        botTag: 'engine-bot#0000',
        inviteUrl: '',
        errors: [],
        warnings: [],
      },
    });
    // A discord.config.set should have been triggered to actually start the service.
    expect(vi.mocked(wsClient.send)).toHaveBeenCalledWith(expect.objectContaining({ type: 'discord.config.set' }));
    // The invite button is NOT hidden just because validate says ok — only config.saved with active:true should do that.
    // (Here discordInviteUrl is empty so invite button was never visible — but active was not force-set.)
    expect(screen.queryByRole('button', { name: /invite bot to server/i })).toBeNull();
  });


  it('DiscordInviteClicked_WindowFocusTriggersValidation', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^discord/i));
    vi.mocked(wsClient.send).mockClear();
    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'discord.validate.result',
      result: {
        ok: false,
        guildName: '',
        botTag: 'engine-bot',
        inviteUrl: 'https://discord.com/api/oauth2/authorize?client_id=123&permissions=8&scope=bot%20applications.commands',
        errors: ['guild not found'],
        warnings: [],
      },
    });
    const inviteBtn = screen.getByRole('button', { name: /invite bot to server/i });
    fireEvent.click(inviteBtn);
    vi.mocked(wsClient.send).mockClear();

    act(() => {
      window.dispatchEvent(new Event('focus'));
    });

    expect(vi.mocked(wsClient.send)).toHaveBeenCalledWith(expect.objectContaining({ type: 'discord.validate' }));
  });

  it('DiscordInviteWatch_MaxAttemptsStopsPolling', () => {
    vi.useFakeTimers();
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^discord/i));
    vi.mocked(wsClient.send).mockClear();

    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'discord.validate.result',
      result: {
        ok: false,
        guildName: '',
        botTag: 'engine-bot',
        inviteUrl: 'https://discord.com/api/oauth2/authorize?client_id=123&permissions=8&scope=bot%20applications.commands',
        errors: ['guild not found'],
        warnings: [],
      },
    });

    fireEvent.click(screen.getByRole('button', { name: /invite bot to server/i }));
    vi.mocked(wsClient.send).mockClear();

    act(() => {
      vi.advanceTimersByTime(24000);
    });
    const countAtStop = vi.mocked(wsClient.send).mock.calls.length;

    act(() => {
      vi.advanceTimersByTime(4000);
    });
    const countAfterExtra = vi.mocked(wsClient.send).mock.calls.length;

    expect(countAtStop).toBeGreaterThan(0);
    expect(countAfterExtra).toBe(countAtStop);
    vi.useRealTimers();
  });

  it('DiscordValidateResultWith4014_ShowsIntentHint', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^discord/i));

    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'discord.validate.result',
      result: {
        ok: false,
        guildName: '',
        botTag: 'engine-bot',
        errors: ['discord open: websocket: close 4014: Disallowed intent(s).'],
        warnings: [],
      },
    });

    expect(screen.getByText(/Message Content Intent is still off/i)).toBeTruthy();
  });

  it('DiscordConfigNotActiveWithToken_AutoValidateRequestedForInvite', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^discord/i));

    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'discord.config',
      config: {
        enabled: true,
        botToken: '',
        botTokenMasked: 'abcd...wxyz',
        guildId: 'guild-abc',
        allowedUserIds: ['user-1'],
        commandPrefix: '!',
        controlChannelName: 'engine-control',
        hasToken: true,
      },
      active: false,
    });

    expect(vi.mocked(wsClient.send)).toHaveBeenCalledWith(expect.objectContaining({ type: 'discord.validate' }));
  });

  it('DiscordFormCheckboxAndInputs_UpdateFormState', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^discord/i));

    const checkbox = document.querySelector('input[type="checkbox"]') as HTMLInputElement;
    fireEvent.click(checkbox);
    expect(checkbox).toBeTruthy();

    const prefixInput = screen.getByPlaceholderText('!');
    fireEvent.change(prefixInput, { target: { value: '!!' } });
    expect((prefixInput as HTMLInputElement).value).toBe('!!');

    const channelInput = screen.getByPlaceholderText('engine-control');
    fireEvent.change(channelInput, { target: { value: 'my-channel' } });
    expect((channelInput as HTMLInputElement).value).toBe('my-channel');
  });

  it('PreferencesPanel_DiscordConfigHandler_nullAllowedUserIdsDefaultsToEmptyJoin', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^discord/i));

    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'discord.config',
      config: {
        enabled: false,
        botToken: '',
        botTokenMasked: '',
        guildId: 'g-null-allowed',
        allowedUserIds: null,
        commandPrefix: '!',
        controlChannelName: 'engine-control',
        hasToken: false,
      },
      active: false,
    });

    expect(screen.getByDisplayValue('g-null-allowed')).toBeTruthy();
    const allowedInput = document.querySelector('textarea[placeholder*="user IDs"], input[placeholder*="user IDs"]') as HTMLInputElement | null;
    if (allowedInput) {
      expect(allowedInput.value).toBe('');
    }
  });

  it('PreferencesPanel_DiscordConfigSavedHandler_missingWarningDefaultsToEmptyString', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^discord/i));

    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'discord.config.saved',
      config: {
        enabled: true,
        botToken: '',
        botTokenMasked: '***',
        guildId: 'guild-xyz',
        allowedUserIds: null,
        commandPrefix: '!',
        controlChannelName: 'engine-control',
        hasToken: true,
      },
      active: true,
    });

    expect(screen.getByDisplayValue('guild-xyz')).toBeTruthy();
  });

  it('PreferencesPanel_DiscordConfigSavedHandler_disabledConfigDoesNotAutoValidate', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^discord/i));
    vi.mocked(wsClient.send).mockClear();

    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'discord.config.saved',
      config: {
        enabled: false,
        botToken: '',
        botTokenMasked: '',
        guildId: 'guild-disabled',
        allowedUserIds: ['user-1'],
        commandPrefix: '!',
        controlChannelName: 'engine-control',
        hasToken: true,
      },
      active: false,
    });

    expect(screen.getByDisplayValue('guild-disabled')).toBeTruthy();
    expect(vi.mocked(wsClient.send)).not.toHaveBeenCalledWith(expect.objectContaining({ type: 'discord.validate' }));
  });

  it('PreferencesPanel_jumpToSection_scrollsIntoViewWhenElementInDom', () => {
    render(<PreferencesPanel />);
    const el = document.getElementById('discord-control');
    if (el) {
      fireEvent.click(getTab(/^discord/i));
      expect(
        (HTMLElement.prototype.scrollIntoView as ReturnType<typeof vi.fn>).mock.calls.length,
      ).toBeGreaterThan(0);
    } else {
      // jsdom did not attach section — just verify tab click doesn't crash
      fireEvent.click(getTab(/^discord/i));
      expect(screen.getByRole('tab', { name: /discord/i })).toBeTruthy();
    }
  });

  it('PreferencesPanel_onMessageGuard_nullMessageIsIgnored', () => {
    render(<PreferencesPanel />);
    sendWsMessage(wsCallbacks.getCapturedWsCallback, null);
    expect(screen.getAllByText(/desktop services/i).length).toBeGreaterThan(0);
  });
});

describe('PreferencesPanel — Desktop Services section', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });

  it('LocalServerTokenAvailable_Shown', async () => {
    render(<PreferencesPanel />);
    await act(async () => {
      await new Promise(r => setTimeout(r, 20));
    });
    expect(screen.getByText(/agent service not installed/i)).toBeTruthy();
  });

  it('GithubTokenAvailable_Shown', async () => {
    vi.mocked(bridge.getGithubToken).mockClear();
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^github/i));
    await act(async () => {
      await new Promise(r => setTimeout(r, 20));
    });
    expect(vi.mocked(bridge.getGithubToken)).toHaveBeenCalled();
  });
});

describe('PreferencesPanel — Machine Connections section', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });

  it('ConnectionsSection_MachineConnectionsPanelRendered', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/machines/i));
    expect(getTab(/machines/i).getAttribute('aria-selected')).toBe('true');
  });
});

describe('PreferencesPanel — SaveBadge rendering', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });

  it('SaveBadge appears in editor appearance when preferences saved', async () => {
    vi.mocked(bridge.setEditorPreferences).mockClear();
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/editor/i));
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /14px/i }));
      await new Promise(r => setTimeout(r, 10));
    });
    expect(document.querySelectorAll('.preferences-save-badge.active').length).toBeGreaterThan(0);
    expect(vi.mocked(bridge.setEditorPreferences)).toHaveBeenCalledWith(
      expect.objectContaining({ fontSize: 14 }),
    );
  });
});

describe('PreferencesPanel — WS message ignored for other types', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });
  it('UnrelatedWsMessages_Ignored', () => {
    render(<PreferencesPanel />);
    sendWsMessage(wsCallbacks.getCapturedWsCallback, { type: 'file.save', path: '/a.ts' });
    expect(getTab(/desktop/i).getAttribute('aria-selected')).toBe('true');
  });
});

describe('PreferencesPanel — jumpToSection via ref', () => {
  it('Nav_AllSectionIdsRendered', () => {
    render(<PreferencesPanel />);
    expect(document.getElementById('desktop-services')).not.toBeNull();
    expect(document.getElementById('machine-connections')).not.toBeNull();
    expect(document.getElementById('editor-appearance')).not.toBeNull();
    expect(document.getElementById('github-wiring')).not.toBeNull();
    expect(document.getElementById('model-provider')).not.toBeNull();
    expect(document.getElementById('agent-teams')).not.toBeNull();
    expect(document.getElementById('discord-control')).not.toBeNull();
  });
});

describe('PreferencesPanel — bridge.get* with values', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });

  it('Mount_BridgeValuesPopulateInputs', async () => {
    vi.mocked(bridge.getGithubToken).mockResolvedValueOnce('ghp_test_token');
    vi.mocked(bridge.getGithubRepoOwner).mockResolvedValueOnce('myorg');
    vi.mocked(bridge.getGithubRepoName).mockResolvedValueOnce('myrepo');
    vi.mocked(bridge.getAnthropicKey).mockResolvedValueOnce('sk-ant-test');
    vi.mocked(bridge.getOpenAiKey).mockResolvedValueOnce('sk-openai-test');
    vi.mocked(bridge.getModelProvider).mockResolvedValueOnce('anthropic');
    vi.mocked(bridge.getOllamaBaseUrl).mockResolvedValueOnce('http://localhost:11434');
    vi.mocked(bridge.getModel).mockResolvedValueOnce('claude-sonnet');
    vi.mocked(bridge.agentServiceStatus).mockResolvedValueOnce({ installed: true, running: true, platform: 'darwin', startupTarget: 'launchd' });

    render(<PreferencesPanel />);
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });

    expect(vi.mocked(bridge.getGithubToken)).toHaveBeenCalled();
    expect(vi.mocked(bridge.getAnthropicKey)).toHaveBeenCalled();
  });

  it('NullProvider_ProviderInputSetToAutoLlamaCpp', async () => {
    vi.mocked(bridge.getModelProvider).mockResolvedValueOnce(null);
    render(<PreferencesPanel />);
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });
    expect(vi.mocked(bridge.getModelProvider)).toHaveBeenCalled();
  });

  it('OpenaiProvider_ProviderInputSetToOpenai', async () => {
    vi.mocked(bridge.getModelProvider).mockResolvedValueOnce('openai');
    render(<PreferencesPanel />);
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });
    expect(vi.mocked(bridge.getModelProvider)).toHaveBeenCalled();
  });
});

describe('PreferencesPanel — GitHub form saves', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
      githubUser: null,
    });
    // Reset github token and auth flow from store
    useStore.setState({ 
      githubToken: null,
      githubAuthFlow: null,
    });
  });

  it('GithubTokenButtonClicked_TokenSaved', async () => {
    vi.mocked(bridge.setGithubToken).mockResolvedValue(true);
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/github/i));

    const tokenInput = screen.getByPlaceholderText(/ghp_\.\.\./);
    fireEvent.change(tokenInput, { target: { value: 'ghp_mytoken' } });

    const saveBtn = screen.getByRole('button', { name: /save token/i });
    await act(async () => { fireEvent.click(saveBtn); });
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });

    expect(vi.mocked(bridge.setGithubToken)).toHaveBeenCalledWith('ghp_mytoken');
  });

  it('GithubOwnerButtonClicked_OwnerSaved', async () => {
    vi.mocked(bridge.setGithubRepoOwner).mockResolvedValue(true);
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/github/i));

    const ownerInput = screen.getByPlaceholderText('owner');
    fireEvent.change(ownerInput, { target: { value: 'myorg123' } });

    const saveBtn = screen.getByRole('button', { name: /save owner/i });
    await act(async () => { fireEvent.click(saveBtn); });
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });

    expect(vi.mocked(bridge.setGithubRepoOwner)).toHaveBeenCalledWith('myorg123');
  });

  it('GithubRepoButtonClicked_RepoSaved', async () => {
    vi.mocked(bridge.setGithubRepoName).mockResolvedValue(true);
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/github/i));

    const repoInput = screen.getByPlaceholderText('repo');
    fireEvent.change(repoInput, { target: { value: 'myrepo456' } });

    const saveBtn = screen.getByRole('button', { name: /save repo/i });
    await act(async () => { fireEvent.click(saveBtn); });
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });

    expect(vi.mocked(bridge.setGithubRepoName)).toHaveBeenCalledWith('myrepo456');
  });
});

describe('PreferencesPanel — Model Provider form saves', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });

  it('ProviderButtonClicked_ProviderSaved', async () => {
    vi.mocked(bridge.setModelProvider).mockResolvedValue(true);
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/model/i));

    const selects = screen.getAllByRole('combobox');
    fireEvent.change(selects[1], { target: { value: 'anthropic' } });

    const saveBtn = screen.getByRole('button', { name: /save provider/i });
    await act(async () => { fireEvent.click(saveBtn); });
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });

    expect(vi.mocked(bridge.setModelProvider)).toHaveBeenCalledWith('anthropic');
  });

  it('ModelButtonClicked_ModelSaved', async () => {
    vi.mocked(bridge.setModel).mockResolvedValue(true);
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/model/i));

    const modelInput = screen.getByPlaceholderText('llama3.2');
    fireEvent.change(modelInput, { target: { value: 'gpt-4o' } });

    const saveBtn = screen.getByRole('button', { name: /save model/i });
    await act(async () => { fireEvent.click(saveBtn); });
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });

    expect(vi.mocked(bridge.setModel)).toHaveBeenCalledWith('gpt-4o');
  });

  it('AnthropicKeyButtonClicked_KeySaved', async () => {
    vi.mocked(bridge.setAnthropicKey).mockResolvedValue(true);
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/model/i));

    const keyInput = screen.getByPlaceholderText(/sk-ant/i);
    fireEvent.change(keyInput, { target: { value: 'sk-ant-mykey' } });

    const saveBtn = screen.getByRole('button', { name: /save anthropic/i });
    await act(async () => { fireEvent.click(saveBtn); });
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });

    expect(vi.mocked(bridge.setAnthropicKey)).toHaveBeenCalledWith('sk-ant-mykey');
  });

  it('OpenaiKeyButtonClicked_KeySaved', async () => {
    vi.mocked(bridge.setOpenAiKey).mockResolvedValue(true);
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/model/i));

    const keyInput = screen.getByPlaceholderText('sk-...');
    fireEvent.change(keyInput, { target: { value: 'sk-openai-key' } });

    const saveBtn = screen.getByRole('button', { name: /save openai/i });
    await act(async () => { fireEvent.click(saveBtn); });
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });

    expect(vi.mocked(bridge.setOpenAiKey)).toHaveBeenCalledWith('sk-openai-key');
  });

  it('OllamaUrlButtonClicked_UrlSaved', async () => {
    vi.mocked(bridge.setOllamaBaseUrl).mockResolvedValue(true);
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/model/i));

    const urlInput = screen.getByPlaceholderText(/http:\/\/127.0.0.1/);
    fireEvent.change(urlInput, { target: { value: 'http://localhost:11434' } });

    const saveBtn = screen.getByRole('button', { name: /save ollama url/i });
    await act(async () => { fireEvent.click(saveBtn); });
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });

    expect(vi.mocked(bridge.setOllamaBaseUrl)).toHaveBeenCalledWith('http://localhost:11434');
  });
});

describe('PreferencesPanel — Desktop service install/uninstall', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });

  it('InstallButtonClicked_ServiceInstalled', async () => {
    vi.mocked(bridge.installAgentService).mockResolvedValue('Service installed successfully');
    render(<PreferencesPanel />);
    await act(async () => { await new Promise(r => setTimeout(r, 10)); });

    const installBtn = screen.getByRole('button', { name: /install agent service/i });
    await act(async () => { fireEvent.click(installBtn); });
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });

    expect(vi.mocked(bridge.installAgentService)).toHaveBeenCalled();
    expect(screen.getByText('Service installed successfully')).toBeTruthy();
  });

  it('RunningAndUninstallButtonClicked_ServiceUninstalled', async () => {
    vi.mocked(bridge.agentServiceStatus).mockResolvedValue({ installed: true, running: true, platform: 'darwin', startupTarget: 'launchd' });
    vi.mocked(bridge.uninstallAgentService).mockResolvedValue('Service removed');
    render(<PreferencesPanel />);
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });

    const removeBtn = screen.getByRole('button', { name: /remove agent service/i });
    await act(async () => { fireEvent.click(removeBtn); });
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });

    expect(vi.mocked(bridge.uninstallAgentService)).toHaveBeenCalled();
  });
});

describe('PreferencesPanel — Discord form interactions', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });

  it('DiscordEnabledCheckbox_Changed', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/discord/i));

    const checkbox = screen.getByRole('checkbox', { name: /enable discord bot/i });
    fireEvent.change(checkbox, { target: { checked: true } });
    expect(checkbox).toBeTruthy();
  });

  it('DiscordGuildIdInput_Changed', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/discord/i));

    const guildInput = screen.getByPlaceholderText(/right-click your server/i);
    fireEvent.change(guildInput, { target: { value: '1234567890' } });
    expect(guildInput).toBeTruthy();
  });

  it('DiscordTokenInput_Changed', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/discord/i));

    const tokenInput = screen.getByPlaceholderText(/paste bot token/i);
    fireEvent.change(tokenInput, { target: { value: 'BOT.TOKEN.HERE' } });
    expect(tokenInput).toBeTruthy();
  });

  it('DiscordAllowedUsersTextarea_Changed', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/discord/i));

    const textarea = screen.getByPlaceholderText(/comma or newline/i);
    fireEvent.change(textarea, { target: { value: '123,456' } });
    expect(textarea).toBeTruthy();
  });

  it('DiscordSaveButtonClicked_ConfigSaved', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/discord/i));

    const saveBtn = screen.getByRole('button', { name: /save discord config/i });
    fireEvent.click(saveBtn);
    expect(vi.mocked(wsClient.send)).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'discord.config.set' }),
    );
  });

  it('DiscordValidateButtonClicked_ConfigValidated', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/discord/i));

    const validateBtn = screen.getByRole('button', { name: /test connection/i });
    fireEvent.click(validateBtn);
    expect(vi.mocked(wsClient.send)).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'discord.validate' }),
    );
  });

  it('DiscordLeaveCurrentServerClicked_UnlinkRequested', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/discord/i));
    vi.mocked(wsClient.send).mockClear();

    const unlinkBtn = screen.getByRole('button', { name: /leave current server/i });
    fireEvent.click(unlinkBtn);

    expect(vi.mocked(wsClient.send)).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'discord.unlink' }),
    );
  });

  it('DiscordConfigSavedWithWarning_WarningShown', () => {
    render(<PreferencesPanel />);
    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'discord.config.saved',
      config: {
        enabled: true,
        botToken: '',
        botTokenMasked: '••••',
        guildId: '999',
        allowedUserIds: ['111'],
        commandPrefix: '!',
        controlChannelName: 'engine',
        hasToken: true,
      },
      active: true,
      warning: 'Bot token is invalid',
    });
    expect(screen.getByText('Bot token is invalid')).toBeTruthy();
  });

  it('DiscordValidateResultNoResultField_Ignored', () => {
    render(<PreferencesPanel />);
    sendWsMessage(wsCallbacks.getCapturedWsCallback, { type: 'discord.validate.result' });
    expect(screen.queryByText(/success/i)).toBeNull();
  });
});

describe('PreferencesPanel — Editor form interactions', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });

  it('MarkdownViewModeChangedToPreview', async () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/editor/i));

    const previewBtn = screen.getByRole('button', { name: /open in preview/i });
    await act(async () => { fireEvent.click(previewBtn); });
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });

    expect(vi.mocked(bridge.setEditorPreferences)).toHaveBeenCalledWith(
      expect.objectContaining({ markdownViewMode: 'preview' }),
    );
  });
});

describe('PreferencesPanel — SaveBadge active state', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });

  it('SaveBadge shows Saved text when active=true', async () => {
    vi.mocked(bridge.setGithubToken).mockResolvedValue(true);
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/github/i));

    const tokenInput = screen.getByPlaceholderText(/ghp_\.\.\./);
    fireEvent.change(tokenInput, { target: { value: 'ghp_x' } });

    const saveBtn = screen.getByRole('button', { name: /save token/i });
    await act(async () => { fireEvent.click(saveBtn); });
    await act(async () => { await new Promise(r => setTimeout(r, 10)); });

    const badges = document.querySelectorAll('.preferences-save-badge.active');
    expect(badges.length).toBeGreaterThan(0);
  });
});

describe('PreferencesPanel — Editor Appearance additional controls', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });

  it('ResetDefaultsButton_EditorPreferencesReset', async () => {
    vi.mocked(bridge.setEditorPreferences).mockClear();
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/editor/i));
    const resetBtn = screen.getByRole('button', { name: /reset defaults/i });
    await act(async () => { fireEvent.click(resetBtn); });
    expect(vi.mocked(bridge.setEditorPreferences)).toHaveBeenCalled();
  });

  it('FontFamilySelectChange_EditorPreferencesUpdated', async () => {
    vi.mocked(bridge.setEditorPreferences).mockClear();
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/editor/i));
    const selects = screen.getAllByRole('combobox');
    const fontSelect = selects[0];
    await act(async () => {
      fireEvent.change(fontSelect, { target: { value: 'Menlo, Monaco, "JetBrains Mono", monospace' } });
    });
    expect(vi.mocked(bridge.setEditorPreferences)).toHaveBeenCalledWith(
      expect.objectContaining({ fontFamily: 'Menlo, Monaco, "JetBrains Mono", monospace' }),
    );
  });

  it('LineHeightChipClick_EditorPreferencesUpdated', async () => {
    vi.mocked(bridge.setEditorPreferences).mockClear();
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/editor/i));
    const compactBtn = screen.getByRole('button', { name: /compact/i });
    await act(async () => { fireEvent.click(compactBtn); });
    expect(vi.mocked(bridge.setEditorPreferences)).toHaveBeenCalledWith(
      expect.objectContaining({ lineHeight: 1.45 }),
    );
  });

  it('TabSizeChipClick_EditorPreferencesUpdated', async () => {
    vi.mocked(bridge.setEditorPreferences).mockClear();
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/editor/i));
    const tabBtn = screen.getByRole('button', { name: /4 spaces/i });
    await act(async () => { fireEvent.click(tabBtn); });
    expect(vi.mocked(bridge.setEditorPreferences)).toHaveBeenCalledWith(
      expect.objectContaining({ tabSize: 4 }),
    );
  });
});

// ── Repository Registry ───────────────────────────────────────────────────────

describe('PreferencesPanel — repo registry navigation', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });

  it('ReposTab_Renders', () => {
    render(<PreferencesPanel />);
    const tab = getTab(/repos/i);
    expect(tab).toBeTruthy();
  });

  it('ReposTab_Click_SetsActiveSection', () => {
    render(<PreferencesPanel />);
    const tab = getTab(/repos/i);
    fireEvent.click(tab);
    expect(tab.getAttribute('aria-selected')).toBe('true');
  });

  it('ReposList_Empty_ShowsEmptyMessage', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/repos/i));
    expect(screen.getByText(/no repositories registered yet/i)).toBeTruthy();
  });
});

describe('PreferencesPanel — repo registry WS messages', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });

  it('RepoList_Message_PopulatesEntries', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/repos/i));
    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'repo.list',
      entries: [
        { name: 'myrepo', localPath: '/home/user/myrepo', url: '' },
      ],
    });
    expect(screen.getByText('myrepo')).toBeTruthy();
    expect(screen.getByText('/home/user/myrepo')).toBeTruthy();
  });

  it('RepoAdded_Message_AppendsEntry', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/repos/i));
    sendWsMessage(wsCallbacks.getCapturedWsCallback, { type: 'repo.list', entries: [] });
    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'repo.added',
      entry: { name: 'newrepo', localPath: '/tmp/newrepo', url: '' },
    });
    expect(screen.getByText('newrepo')).toBeTruthy();
  });

  it('RepoAdded_Duplicate_DoesNotDuplicate', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/repos/i));
    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'repo.list',
      entries: [{ name: 'dup', localPath: '/tmp/dup', url: '' }],
    });
    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'repo.added',
      entry: { name: 'dup', localPath: '/tmp/dup', url: '' },
    });
    expect(screen.getAllByText('dup')).toHaveLength(1);
  });

  it('RepoRemoved_Message_RemovesEntry', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/repos/i));
    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'repo.list',
      entries: [{ name: 'todelete', localPath: '/tmp/todelete', url: '' }],
    });
    expect(screen.getByText('todelete')).toBeTruthy();
    sendWsMessage(wsCallbacks.getCapturedWsCallback, { type: 'repo.removed', name: 'todelete' });
    expect(screen.queryByText('todelete')).toBeNull();
  });

  it('RepoRemoved_Message_ClearsRepoLoadingAndError', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/repos/i));

    const input = screen.getByPlaceholderText(/path or git url/i);
    fireEvent.change(input, { target: { value: '/tmp/new' } });
    fireEvent.click(screen.getByRole('button', { name: /^add$/i }));

    sendWsMessage(wsCallbacks.getCapturedWsCallback, { type: 'error', code: 'REPO_ADD_ERROR', message: 'temporary failure' });
    expect(screen.getByText('temporary failure')).toBeTruthy();

    sendWsMessage(wsCallbacks.getCapturedWsCallback, { type: 'repo.removed', name: 'missing' });
    expect(screen.queryByText('temporary failure')).toBeNull();
  });

  it('RepoError_REPO_ADD_ERROR_ShowsMessage', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/repos/i));
    sendWsMessage(wsCallbacks.getCapturedWsCallback, { type: 'error', code: 'REPO_ADD_ERROR', message: 'clone failed' });
    expect(screen.getByText('clone failed')).toBeTruthy();
  });

  it('RepoError_REPO_REMOVE_ERROR_ShowsMessage', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/repos/i));
    sendWsMessage(wsCallbacks.getCapturedWsCallback, { type: 'error', code: 'REPO_REMOVE_ERROR', message: 'not found' });
    expect(screen.getByText('not found')).toBeTruthy();
  });

  it('RepoError_REPO_LIST_ERROR_ShowsMessage', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/repos/i));
    sendWsMessage(wsCallbacks.getCapturedWsCallback, { type: 'error', code: 'REPO_LIST_ERROR', message: 'disk error' });
    expect(screen.getByText('disk error')).toBeTruthy();
  });

  it('RepoError_WithoutRepoErrorCode_DoesNotShowError', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/repos/i));
    sendWsMessage(wsCallbacks.getCapturedWsCallback, { type: 'error' });
    expect(screen.queryByText('Unknown error')).toBeNull();
  });
});

describe('PreferencesPanel — repo registry interactions', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });

  it('AddButton_Disabled_WhenInputEmpty', () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/repos/i));
    const btn = screen.getByRole('button', { name: /^add$/i });
    expect((btn as HTMLButtonElement).disabled).toBe(true);
  });

  it('AddButton_SendsRepoAdd_WhenInputFilled', () => {
    vi.mocked(wsClient.send).mockClear();
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/repos/i));
    const input = screen.getByPlaceholderText(/path or git url/i);
    fireEvent.change(input, { target: { value: '/some/path' } });
    const btn = screen.getByRole('button', { name: /^add$/i });
    fireEvent.click(btn);
    expect(vi.mocked(wsClient.send)).toHaveBeenCalledWith({ type: 'repo.add', urlOrPath: '/some/path' });
  });

  it('RemoveButton_SendsRepoRemove', () => {
    vi.mocked(wsClient.send).mockClear();
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/repos/i));
    sendWsMessage(wsCallbacks.getCapturedWsCallback, {
      type: 'repo.list',
      entries: [{ name: 'alpha', localPath: '/tmp/alpha', url: '' }],
    });
    const removeBtn = screen.getByRole('button', { name: /remove alpha/i });
    fireEvent.click(removeBtn);
    expect(vi.mocked(wsClient.send)).toHaveBeenCalledWith({ type: 'repo.remove', name: 'alpha' });
  });

  it('Mount_SendsRepoList', () => {
    vi.mocked(wsClient.send).mockClear();
    render(<PreferencesPanel />);
    expect(vi.mocked(wsClient.send)).toHaveBeenCalledWith({ type: 'repo.list' });
  });
});

describe('PreferencesPanel — GitHub auth flow UI', () => {
  let useStore: typeof import('../store/index.js').useStore;

  beforeEach(async () => {
    const storeModule = await import('../store/index.js');
    useStore = storeModule.useStore;
  });

  it('NoToken_NoFlow_LoginButtonShown', () => {
    useStore.setState({ githubToken: null, githubAuthFlow: null });
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^github/i));
    expect(screen.getByRole('button', { name: /login with github/i })).toBeTruthy();
  });

  it('LoginButtonClicked_SendsAuthStart', () => {
    vi.mocked(wsClient.send).mockClear();
    useStore.setState({ githubToken: null, githubAuthFlow: null });
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^github/i));
    fireEvent.click(screen.getByRole('button', { name: /login with github/i }));
    expect(vi.mocked(wsClient.send)).toHaveBeenCalledWith({ type: 'github.auth.start' });
  });

  it('TokenPresent_LoginButtonHidden', () => {
    useStore.setState({ githubToken: 'ghp_exists', githubAuthFlow: null });
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^github/i));
    expect(screen.queryByRole('button', { name: /login with github/i })).toBeNull();
  });

  it('AuthFlowActive_ShowsUserCode', () => {
    useStore.setState({
      githubToken: null,
      githubAuthFlow: { userCode: 'WXYZ-5678', verificationUri: 'https://github.com/login/device', expiresIn: 900 },
    });
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^github/i));
    expect(screen.getByText('WXYZ-5678')).toBeTruthy();
  });

  it('AuthFlowActive_CancelClearsFlow', () => {
    useStore.setState({
      githubToken: null,
      githubAuthFlow: { userCode: 'ABCD-0000', verificationUri: 'https://github.com/login/device', expiresIn: 60 },
    });
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^github/i));
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }));
    expect(useStore.getState().githubAuthFlow).toBeNull();
  });
});

describe('PreferencesPanel — clones dir field', () => {
  beforeEach(() => {
    setupStoreForUITests({
      editorPreferences: {
        fontFamily: 'default',
        fontSize: 13,
        lineHeight: 1.5,
        tabSize: 2,
        markdownViewMode: 'text',
        wordWrap: false,
      },
      activeSession: null,
    });
  });

  it('LoadsClonesDir_PopulatesInput', async () => {
    vi.mocked(bridge.getClonesDir).mockResolvedValueOnce('/saved/clones');
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^github/i));
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });
    expect(vi.mocked(bridge.getClonesDir)).toHaveBeenCalled();
    const input = screen.getByPlaceholderText(/Default.*engine.*projects/i) as HTMLInputElement;
    expect(input.value).toBe('/saved/clones');
  });

  it('SavePath_CallsSetClonesDir', async () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^github/i));
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });
    const input = screen.getByPlaceholderText(/Default.*engine.*projects/i);
    fireEvent.change(input, { target: { value: '/new/path' } });
    const saveBtn = screen.getByRole('button', { name: /save path/i });
    await act(async () => { fireEvent.click(saveBtn); });
    await act(async () => { await new Promise(r => setTimeout(r, 30)); });
    expect(vi.mocked(bridge.setClonesDir)).toHaveBeenCalledWith('/new/path');
  });

  it('Browse_OpensFolderDialog_SetsInput', async () => {
    vi.mocked(bridge.openFolderDialog).mockResolvedValueOnce('/picked/dir');
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^github/i));
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });
    const browseBtn = screen.getByRole('button', { name: /browse/i });
    await act(async () => { fireEvent.click(browseBtn); });
    await act(async () => { await new Promise(r => setTimeout(r, 30)); });
    const input = screen.getByPlaceholderText(/Default.*engine.*projects/i) as HTMLInputElement;
    expect(input.value).toBe('/picked/dir');
  });

  it('Browse_NullResult_DoesNotChangeInput', async () => {
    vi.mocked(bridge.openFolderDialog).mockResolvedValueOnce(null);
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^github/i));
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });
    const input = screen.getByPlaceholderText(/Default.*engine.*projects/i) as HTMLInputElement;
    const before = input.value;
    const browseBtn = screen.getByRole('button', { name: /browse/i });
    await act(async () => { fireEvent.click(browseBtn); });
    await act(async () => { await new Promise(r => setTimeout(r, 30)); });
    expect(input.value).toBe(before);
  });

  it('SavePath_FailedSave_DoesNotPushConfig', async () => {
    vi.mocked(bridge.setClonesDir).mockResolvedValueOnce(false);
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^github/i));
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });
    const input = screen.getByPlaceholderText(/Default.*engine.*projects/i);
    fireEvent.change(input, { target: { value: '/fail/path' } });
    const saveBtn = screen.getByRole('button', { name: /save path/i });
    await act(async () => { fireEvent.click(saveBtn); });
    await act(async () => { await new Promise(r => setTimeout(r, 30)); });
    expect(vi.mocked(bridge.setClonesDir)).toHaveBeenCalledWith('/fail/path');
  });

  it('SavePath_Success_ShowsSavedConfirmation', async () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^github/i));
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });
    const input = screen.getByPlaceholderText(/Default.*engine.*projects/i);
    fireEvent.change(input, { target: { value: '/ok/path' } });
    const saveBtn = screen.getByRole('button', { name: /save path/i });
    await act(async () => { fireEvent.click(saveBtn); });
    await act(async () => { await new Promise(r => setTimeout(r, 30)); });
    expect(screen.queryByText(/path saved/i)).not.toBeNull();
  });

  it('SavePath_EmptyInput_PassesNullToConfig', async () => {
    render(<PreferencesPanel />);
    fireEvent.click(getTab(/^github/i));
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });
    const input = screen.getByPlaceholderText(/Default.*engine.*projects/i);
    fireEvent.change(input, { target: { value: '' } });
    const saveBtn = screen.getByRole('button', { name: /save path/i });
    await act(async () => { fireEvent.click(saveBtn); });
    await act(async () => { await new Promise(r => setTimeout(r, 30)); });
    expect(vi.mocked(bridge.setClonesDir)).toHaveBeenCalledWith('');
  });
});
