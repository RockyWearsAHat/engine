import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Bot,
  Check,
  FolderGit2,
  KeyRound,
  MessageSquare,
  ServerCog,
  Settings2,
  Type,
  WrapText,
} from 'lucide-react';
import { bridge, type BackgroundServiceStatus } from '../../bridge.js';
import { useStore } from '../../store/index.js';
import { wsClient } from '../../ws/client.js';
import type {
  DiscordConfig,
  DiscordValidationResult,
} from '@engine/shared';
import {
  DEFAULT_EDITOR_PREFERENCES,
  editorFontOptions,
  editorFontSizeOptions,
  editorLineHeightOptions,
  editorTabSizeOptions,
  normalizeEditorPreferences,
} from '../../editorPreferences.js';
import { highlightCode } from '../Editor/editorSyntax.js';
import MachineConnectionsPanel from '../Connections/MachineConnectionsPanel.js';

const previewSnippet = `export async function openWorkspace(path: string) {
  const response = await bridge.setLastProjectPath(path);
  return response ? "ready" : "retry";
}`;

function PreviewCode({ fontFamily, fontSize, lineHeight }: {
  fontFamily: string;
  fontSize: number;
  lineHeight: number;
}) {
  const previewHtml = useMemo(
    () => ({ __html: highlightCode(previewSnippet, 'typescript') }),
    [],
  );

  return (
    <div
      className="preferences-preview"
      style={{
        fontFamily,
        fontSize,
        lineHeight,
      }}
    >
      <pre
        className="preferences-preview-code language-typescript"
        dangerouslySetInnerHTML={previewHtml}
      />
    </div>
  );
}

function SaveBadge({ label, active }: { label: string; active: boolean }) {
  return (
    <span className={`preferences-save-badge ${active ? 'active' : ''}`}>
      {active ? <><Check size={11} /> Saved</> : label}
    </span>
  );
}

export default function PreferencesPanel() {
  const {
    githubToken: token,
    setGithubToken,
    setGithubUser,
    editorPreferences,
    setEditorPreferences,
  } = useStore();

  const [ghInput, setGhInput] = useState('');
  const [ghOwnerInput, setGhOwnerInput] = useState('');
  const [ghRepoInput, setGhRepoInput] = useState('');
  const [anthropicInput, setAnthropicInput] = useState('');
  const [openaiInput, setOpenaiInput] = useState('');
  const [providerInput, setProviderInput] = useState<'auto' | 'anthropic' | 'openai' | 'ollama'>('ollama');
  const [ollamaBaseUrlInput, setOllamaBaseUrlInput] = useState('');
  const [modelInput, setModelInput] = useState('');
  const [saved, setSaved] = useState<string | null>(null);
  const [serviceStatus, setServiceStatus] = useState<BackgroundServiceStatus | null>(null);
  const [serviceMsg, setServiceMsg] = useState('');
  const [serviceLoading, setServiceLoading] = useState(false);

  // Discord control plane ────────────────────────────────────────────────
  const emptyDiscord: DiscordConfig = {
    enabled: false,
    botToken: '',
    botTokenMasked: '',
    guildId: '',
    allowedUserIds: [],
    commandPrefix: '!',
    controlChannelName: '',
    hasToken: false,
  };
  const [discordForm, setDiscordForm] = useState<DiscordConfig>(emptyDiscord);
  const [discordAllowedInput, setDiscordAllowedInput] = useState('');
  const [discordTokenInput, setDiscordTokenInput] = useState('');
  const [discordValidation, setDiscordValidation] = useState<DiscordValidationResult | null>(null);
  const [discordValidating, setDiscordValidating] = useState(false);
  const [discordActive, setDiscordActive] = useState(false);
  const [activeSection, setActiveSection] = useState('desktop-services');

  const sections = [
    { id: 'desktop-services', label: 'Desktop' },
    { id: 'machine-connections', label: 'Machines' },
    { id: 'editor-appearance', label: 'Editor' },
    { id: 'github-wiring', label: 'GitHub' },
    { id: 'model-provider', label: 'Model' },
    { id: 'discord-control', label: 'Discord' },
  ] as const;

  const jumpToSection = useCallback((id: string) => {
    setActiveSection(id);
    const element = document.getElementById(id);
    if (element) {
      element.scrollIntoView({ behavior: 'smooth', block: 'start' });
    }
  }, []);

  useEffect(() => {
    const unsub = wsClient.onMessage((msg: unknown) => {
      const m = msg as { type?: string; config?: DiscordConfig; active?: boolean; result?: DiscordValidationResult; warning?: string };
      if (!m || typeof m.type !== 'string') return;
      if (m.type === 'discord.config' && m.config) {
        setDiscordForm(m.config);
        setDiscordAllowedInput((m.config.allowedUserIds || []).join(', '));
        setDiscordActive(Boolean(m.active));
      } else if (m.type === 'discord.config.saved' && m.config) {
        setDiscordForm(m.config);
        setDiscordAllowedInput((m.config.allowedUserIds || []).join(', '));
        setDiscordTokenInput('');
        markSaved('discord');
      } else if (m.type === 'discord.validate.result' && m.result) {
        setDiscordValidation(m.result);
        setDiscordValidating(false);
      }
    });
    // Ask for current discord config once the WS is usable.
    const requestConfig = () => wsClient.send({ type: 'discord.config.get' } as never);
    requestConfig();
    const unsubOpen = wsClient.onOpen(() => requestConfig());
    return () => {
      unsub();
      unsubOpen();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    bridge.getGithubToken().then(k => {
      if (k) {
        setGhInput(k);
        setGithubToken(k);
      }
    });
    bridge.getGithubRepoOwner().then(owner => { if (owner) setGhOwnerInput(owner); });
    bridge.getGithubRepoName().then(repo => { if (repo) setGhRepoInput(repo); });
    bridge.getAnthropicKey().then(k => { if (k) setAnthropicInput(k); });
    bridge.getOpenAiKey().then(k => { if (k) setOpenaiInput(k); });
    bridge.getModelProvider().then(provider => {
      if (provider === 'anthropic' || provider === 'openai' || provider === 'ollama') {
        setProviderInput(provider);
        return;
      }
      setProviderInput('ollama');
    });
    bridge.getOllamaBaseUrl().then(url => { if (url) setOllamaBaseUrlInput(url); });
    bridge.getModel().then(m => { if (m) setModelInput(m); });
    bridge.getEditorPreferences().then(setEditorPreferences);
    bridge.agentServiceStatus().then(setServiceStatus);
  }, [setEditorPreferences, setGithubToken]);

  const markSaved = (field: string) => {
    setSaved(field);
    window.setTimeout(() => {
      setSaved((current) => (current === field ? null : current));
    }, 1800);
  };

  const pushRuntimeConfig = (overrides?: {
    githubToken?: string | null;
    githubOwner?: string | null;
      githubRepo?: string | null;
      anthropicKey?: string | null;
      openaiKey?: string | null;
      modelProvider?: string | null;
      ollamaBaseUrl?: string | null;
      model?: string | null;
    }) => {
      const nextConfig = {
        githubToken: overrides?.githubToken ?? (ghInput.trim() || null),
        githubOwner: overrides?.githubOwner ?? (ghOwnerInput.trim() || null),
        githubRepo: overrides?.githubRepo ?? (ghRepoInput.trim() || null),
        anthropicKey: overrides?.anthropicKey ?? (anthropicInput.trim() || null),
        openaiKey: overrides?.openaiKey ?? (openaiInput.trim() || null),
        modelProvider: overrides?.modelProvider ?? (providerInput === 'auto' ? null : providerInput),
        ollamaBaseUrl: overrides?.ollamaBaseUrl ?? (ollamaBaseUrlInput.trim() || null),
        model: overrides?.model ?? (modelInput.trim() || null),
      };

    setGithubToken(nextConfig.githubToken);
    wsClient.send({ type: 'config.sync', config: nextConfig });
    if (nextConfig.githubToken) {
      wsClient.send({ type: 'github.user' });
    } else {
      setGithubUser(null);
    }
  };

  const saveField = async (field: string, fn: () => Promise<boolean>) => {
    const ok = await fn();
    if (ok) {
      markSaved(field);
    }
  };

  const updateEditorPreferences = async (overrides: Partial<typeof editorPreferences>) => {
    const next = normalizeEditorPreferences({
      ...editorPreferences,
      ...overrides,
    });
    setEditorPreferences(next);
    const ok = await bridge.setEditorPreferences(next);
    if (ok) {
      markSaved('editor');
    }
  };

  const installService = async () => {
    setServiceLoading(true);
    setServiceMsg('');
    const msg = await bridge.installAgentService();
    setServiceMsg(msg);
    bridge.agentServiceStatus().then(setServiceStatus);
    setServiceLoading(false);
  };

  const uninstallService = async () => {
    setServiceLoading(true);
    setServiceMsg('');
    const msg = await bridge.uninstallAgentService();
    setServiceMsg(msg);
    bridge.agentServiceStatus().then(setServiceStatus);
    setServiceLoading(false);
  };

  // Discord save / validate handlers ────────────────────────────────────
  const parseDiscordAllowed = (raw: string): string[] =>
    raw.split(/[\s,]+/).map((s) => s.trim()).filter((s) => s.length > 0);

  const buildDiscordPayload = (): DiscordConfig => {
    return {
      enabled: discordForm.enabled,
      // Only send botToken if the user typed a new value; empty means "keep".
      botToken: discordTokenInput.trim(),
      guildId: discordForm.guildId.trim(),
      allowedUserIds: parseDiscordAllowed(discordAllowedInput),
      commandPrefix: discordForm.commandPrefix.trim() || '!',
      controlChannelName: discordForm.controlChannelName.trim(),
    };
  };

  const saveDiscordConfig = () => {
    wsClient.send({ type: 'discord.config.set', config: buildDiscordPayload() } as never);
  };

  const validateDiscordConfig = () => {
    setDiscordValidating(true);
    setDiscordValidation(null);
    wsClient.send({ type: 'discord.validate', config: buildDiscordPayload() } as never);
    window.setTimeout(() => setDiscordValidating(false), 6000);
  };

  const inputStyle: React.CSSProperties = {
    width: '100%',
    background: 'rgba(9, 11, 16, 0.6)',
    border: '1px solid rgba(255, 255, 255, 0.08)',
    borderRadius: 4,
    padding: '9px 12px',
    color: 'var(--tx)',
    fontSize: 12,
    fontFamily: 'inherit',
    outline: 'none',
    boxSizing: 'border-box',
    transition: 'all 120ms ease',
  };

  return (
    <div className="preferences-layout">
      <div className="preferences-hero">
        <div>
          <div className="preferences-kicker">Settings</div>
          <div className="preferences-title">Keep the shell plain, quiet, and useful.</div>
          <div className="preferences-copy">
            Fonts, wrap behavior, model routing, machine links, and desktop services live here instead of being scattered through the shell.
          </div>
        </div>
        <SaveBadge
          label="Persisted to Engine config"
          active={saved !== null}
        />
      </div>

      <div className="preferences-nav" role="tablist" aria-label="Settings sections">
        {sections.map((section) => (
          <button
            key={section.id}
            type="button"
            role="tab"
            aria-selected={activeSection === section.id}
            className={`preferences-nav-btn ${activeSection === section.id ? 'active' : ''}`}
            onClick={() => jumpToSection(section.id)}
          >
            {section.label}
          </button>
        ))}
      </div>

      <section id="desktop-services" className="preferences-card preferences-extensions">
        <div className="preferences-card-header">
          <div className="preferences-card-title">
            <ServerCog size={15} />
            Desktop services
          </div>
          <SaveBadge label="Agent service" active={serviceStatus?.installed ?? false} />
        </div>

        <div className="preferences-stack">
          <div className="preferences-service-status">
            <span className={`preferences-status-dot ${serviceStatus?.installed ? 'online' : ''}`} />
            <div>
              <div className="preferences-service-title">
                {serviceStatus?.installed ? 'Agent service installed' : 'Agent service not installed'}
              </div>
              <div className="preferences-muted">
                {serviceStatus
                  ? `${serviceStatus.platform} • ${serviceStatus.running ? 'running' : 'stopped'} • ${serviceStatus.startupTarget}`
                  : 'Checking desktop add-on status…'}
              </div>
            </div>
          </div>

          <div className="preferences-muted">
            Engine keeps the local agent service here. Extension install flow is not exposed yet, so this section stays honest about what exists.
          </div>

          {!serviceStatus?.installed ? (
            <button className="btn-primary" onClick={installService} disabled={serviceLoading}>
              <ServerCog size={14} />
              {serviceLoading ? 'Installing…' : 'Install agent service'}
            </button>
          ) : (
            <button className="btn-secondary" onClick={uninstallService} disabled={serviceLoading}>
              <Settings2 size={14} />
              {serviceLoading ? 'Removing…' : 'Remove agent service'}
            </button>
          )}

          {serviceMsg && (
            <div className="preferences-message">
              {serviceMsg}
            </div>
          )}
        </div>
      </section>

      <div id="machine-connections" className="preferences-connections">
        <MachineConnectionsPanel compact />
      </div>

      <div className="preferences-grid">
        <section id="editor-appearance" className="preferences-card">
          <div className="preferences-card-header">
            <div className="preferences-card-title">
              <Type size={15} />
              Editor appearance
            </div>
            <div className="preferences-inline-actions">
              <SaveBadge label="Live preview" active={saved === 'editor'} />
              <button
                className="btn-secondary"
                style={{ width: 'fit-content' }}
                onClick={() => void updateEditorPreferences(DEFAULT_EDITOR_PREFERENCES)}
              >
                Reset defaults
              </button>
            </div>
          </div>

          <div className="preferences-row">
            <label className="preferences-field">
              <span className="preferences-label">Font family</span>
              <select
                value={editorPreferences.fontFamily}
                style={inputStyle}
                onChange={event => void updateEditorPreferences({ fontFamily: event.target.value })}
              >
                {editorFontOptions.map(option => (
                  <option key={option.label} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
            </label>
          </div>

          <div className="preferences-row">
            <div className="preferences-field">
              <span className="preferences-label">Font size</span>
              <div className="preferences-chip-group">
                {editorFontSizeOptions.map(size => (
                  <button
                    key={size}
                    className={`preferences-chip ${editorPreferences.fontSize === size ? 'active' : ''}`}
                    onClick={() => void updateEditorPreferences({ fontSize: size })}
                  >
                    {size}px
                  </button>
                ))}
              </div>
            </div>
          </div>

          <div className="preferences-row two-up">
            <div className="preferences-field">
              <span className="preferences-label">Line height</span>
              <div className="preferences-chip-group">
                {editorLineHeightOptions.map(option => (
                  <button
                    key={option.label}
                    className={`preferences-chip ${editorPreferences.lineHeight === option.value ? 'active' : ''}`}
                    onClick={() => void updateEditorPreferences({ lineHeight: option.value })}
                  >
                    {option.label}
                  </button>
                ))}
              </div>
            </div>
            <div className="preferences-field">
              <span className="preferences-label">Tab width</span>
              <div className="preferences-chip-group">
                {editorTabSizeOptions.map(size => (
                  <button
                    key={size}
                    className={`preferences-chip ${editorPreferences.tabSize === size ? 'active' : ''}`}
                    onClick={() => void updateEditorPreferences({ tabSize: size })}
                  >
                    {size} spaces
                  </button>
                ))}
              </div>
            </div>
          </div>

          <div className="preferences-toggle-row">
            <div>
              <div className="preferences-card-title" style={{ fontSize: 13 }}>
                <WrapText size={14} />
                Word wrap
              </div>
              <div className="preferences-muted">
                Syntax highlighting automatically backs off for very large files so the editor stays fast.
              </div>
            </div>
            <button
              className={`preferences-switch ${editorPreferences.wordWrap ? 'active' : ''}`}
              onClick={() => void updateEditorPreferences({ wordWrap: !editorPreferences.wordWrap })}
            >
              <span />
            </button>
          </div>

          <div className="preferences-row">
            <div className="preferences-field">
              <span className="preferences-label">Markdown files</span>
              <div className="preferences-chip-group">
                {([
                  ['text', 'Open in text view'],
                  ['preview', 'Open in preview'],
                ] as const).map(([mode, label]) => (
                  <button
                    key={mode}
                    className={`preferences-chip ${editorPreferences.markdownViewMode === mode ? 'active' : ''}`}
                    onClick={() => void updateEditorPreferences({ markdownViewMode: mode })}
                  >
                    {label}
                  </button>
                ))}
              </div>
              <div className="preferences-muted">
                Markdown always wraps in text view so README files stay readable even when the source is one long line.
              </div>
            </div>
          </div>

          <PreviewCode
            fontFamily={editorPreferences.fontFamily}
            fontSize={editorPreferences.fontSize}
            lineHeight={editorPreferences.lineHeight}
          />
        </section>

        <section id="github-wiring" className="preferences-card">
          <div className="preferences-card-header">
            <div className="preferences-card-title">
              <FolderGit2 size={15} />
              GitHub and project wiring
            </div>
          </div>

          <div className="preferences-stack">
            <label className="preferences-field">
              <span className="preferences-label">GitHub token</span>
              <input
                type="password"
                placeholder={token ? '••••••••' : 'ghp_...'}
                value={ghInput}
                onChange={event => setGhInput(event.target.value)}
                style={inputStyle}
              />
            </label>
            <button
              className="btn-primary"
              style={{ width: 'fit-content' }}
              onClick={() => void saveField('gh', async () => {
                const nextToken = ghInput.trim() || '';
                const ok = await bridge.setGithubToken(nextToken);
                if (ok) {
                  pushRuntimeConfig({ githubToken: nextToken || null });
                }
                return ok;
              })}
            >
              {saved === 'gh' ? <><Check size={12} /> Token saved</> : 'Save token'}
            </button>

            <div className="preferences-row two-up">
              <label className="preferences-field">
                <span className="preferences-label">Repository owner</span>
                <input
                  type="text"
                  placeholder="owner"
                  value={ghOwnerInput}
                  onChange={event => setGhOwnerInput(event.target.value)}
                  style={inputStyle}
                />
              </label>
              <label className="preferences-field">
                <span className="preferences-label">Repository name</span>
                <input
                  type="text"
                  placeholder="repo"
                  value={ghRepoInput}
                  onChange={event => setGhRepoInput(event.target.value)}
                  style={inputStyle}
                />
              </label>
            </div>

            <div className="preferences-inline-actions">
              <button
                className="btn-secondary"
                onClick={() => void saveField('gh-owner', async () => {
                  const nextOwner = ghOwnerInput.trim() || '';
                  const ok = await bridge.setGithubRepoOwner(nextOwner);
                  if (ok) {
                    pushRuntimeConfig({ githubOwner: nextOwner || null });
                  }
                  return ok;
                })}
              >
                {saved === 'gh-owner' ? <><Check size={12} /> Owner saved</> : 'Save owner'}
              </button>
              <button
                className="btn-secondary"
                onClick={() => void saveField('gh-repo', async () => {
                  const nextRepo = ghRepoInput.trim() || '';
                  const ok = await bridge.setGithubRepoName(nextRepo);
                  if (ok) {
                    pushRuntimeConfig({ githubRepo: nextRepo || null });
                  }
                  return ok;
                })}
              >
                {saved === 'gh-repo' ? <><Check size={12} /> Repo saved</> : 'Save repo'}
              </button>
            </div>
          </div>
        </section>

        <section id="model-provider" className="preferences-card">
          <div className="preferences-card-header">
            <div className="preferences-card-title">
              <Bot size={15} />
              Model and provider keys
            </div>
          </div>

          <div className="preferences-stack">
            <div className="preferences-row two-up">
              <label className="preferences-field">
                <span className="preferences-label">Model provider</span>
                <select
                  value={providerInput}
                  onChange={event => setProviderInput(event.target.value as 'auto' | 'anthropic' | 'openai' | 'ollama')}
                  style={inputStyle}
                >
                  <option value="auto">Auto</option>
                  <option value="anthropic">Anthropic</option>
                  <option value="openai">OpenAI</option>
                  <option value="ollama">Ollama</option>
                </select>
                <span className="preferences-muted">Auto now infers Ollama for local model names like gemma, llama, qwen, and tagged models such as gemma4:31b.</span>
              </label>
              <label className="preferences-field">
                <span className="preferences-label">Preferred model</span>
              <input
                type="text"
                placeholder={providerInput === 'ollama' ? 'llama3.2' : 'claude-sonnet-4-6'}
                value={modelInput}
                onChange={event => setModelInput(event.target.value)}
                style={inputStyle}
              />
                <span className="preferences-muted">Examples: claude-sonnet-4-6, gpt-4o, o3, llama3.2, qwen2.5-coder. Leave blank on Ollama to use the running local model first.</span>
              </label>
            </div>

            <div className="preferences-inline-actions">
              <button
                className="btn-secondary"
                onClick={() => void saveField('provider', async () => {
                  const nextProvider = providerInput === 'auto' ? '' : providerInput;
                  const ok = await bridge.setModelProvider(nextProvider);
                  if (ok) {
                    pushRuntimeConfig({ modelProvider: nextProvider || null });
                  }
                  return ok;
                })}
              >
                {saved === 'provider' ? <><Check size={12} /> Provider saved</> : 'Save provider'}
              </button>
              <button
                className="btn-primary"
                style={{ width: 'fit-content' }}
                onClick={() => void saveField('model', async () => {
                  const nextModel = modelInput.trim() || '';
                  const ok = await bridge.setModel(nextModel);
                  if (ok) {
                    pushRuntimeConfig({ model: nextModel || null });
                  }
                  return ok;
                })}
              >
                {saved === 'model' ? <><Check size={12} /> Model saved</> : 'Save model'}
              </button>
            </div>

            <div className="preferences-row two-up">
              <label className="preferences-field">
                <span className="preferences-label">Anthropic API key</span>
                <input
                  type="password"
                  placeholder="sk-ant-..."
                  value={anthropicInput}
                  onChange={event => setAnthropicInput(event.target.value)}
                  style={inputStyle}
                />
              </label>
              <label className="preferences-field">
                <span className="preferences-label">OpenAI API key</span>
                <input
                  type="password"
                  placeholder="sk-..."
                  value={openaiInput}
                  onChange={event => setOpenaiInput(event.target.value)}
                  style={inputStyle}
                />
              </label>
            </div>

            <label className="preferences-field">
              <span className="preferences-label">Ollama base URL</span>
              <input
                type="text"
                placeholder="http://127.0.0.1:11434"
                value={ollamaBaseUrlInput}
                onChange={event => setOllamaBaseUrlInput(event.target.value)}
                style={inputStyle}
              />
              <span className="preferences-muted">Engine uses Ollama&apos;s OpenAI-compatible `/v1/chat/completions` endpoint. Leave blank for the local default.</span>
            </label>

            <div className="preferences-inline-actions">
              <button
                className="btn-secondary"
                onClick={() => void saveField('anthropic', async () => {
                  const nextKey = anthropicInput.trim() || '';
                  const ok = await bridge.setAnthropicKey(nextKey);
                  if (ok) {
                    pushRuntimeConfig({ anthropicKey: nextKey || null });
                  }
                  return ok;
                })}
              >
                {saved === 'anthropic' ? <><Check size={12} /> Anthropic saved</> : <><KeyRound size={12} /> Save Anthropic</>}
              </button>
              <button
                className="btn-secondary"
                onClick={() => void saveField('openai', async () => {
                  const nextKey = openaiInput.trim() || '';
                  const ok = await bridge.setOpenAiKey(nextKey);
                  if (ok) {
                    pushRuntimeConfig({ openaiKey: nextKey || null });
                  }
                  return ok;
                })}
              >
                {saved === 'openai' ? <><Check size={12} /> OpenAI saved</> : <><KeyRound size={12} /> Save OpenAI</>}
              </button>
              <button
                className="btn-secondary"
                onClick={() => void saveField('ollamaUrl', async () => {
                  const nextBaseUrl = ollamaBaseUrlInput.trim() || '';
                  const ok = await bridge.setOllamaBaseUrl(nextBaseUrl);
                  if (ok) {
                    pushRuntimeConfig({ ollamaBaseUrl: nextBaseUrl || null });
                  }
                  return ok;
                })}
              >
                {saved === 'ollamaUrl' ? <><Check size={12} /> Ollama URL saved</> : 'Save Ollama URL'}
              </button>
            </div>
          </div>
        </section>

        <section id="discord-control" className="preferences-card">
          <div className="preferences-card-header">
            <div className="preferences-card-title">
              <MessageSquare size={15} />
              Discord control plane
            </div>
            <SaveBadge label={discordActive ? 'Live' : 'Config only'} active={saved === 'discord'} />
          </div>

          <div className="preferences-stack">
            <div className="preferences-muted">
              Private Discord bot that accepts commands from your own account and
              archives every interaction to a per-project channel with one thread
              per chat. Searchable history, never auto-fed to the model.
            </div>

            <label className="preferences-field" style={{ flexDirection: 'row', alignItems: 'center', gap: 8 }}>
              <input
                type="checkbox"
                checked={discordForm.enabled}
                onChange={(e) => setDiscordForm({ ...discordForm, enabled: e.target.checked })}
              />
              <span className="preferences-label" style={{ margin: 0 }}>Enable Discord bot</span>
            </label>

            <label className="preferences-field">
              <span className="preferences-label">Bot token</span>
              <input
                type="password"
                style={inputStyle}
                placeholder={discordForm.hasToken ? (discordForm.botTokenMasked || 'stored — leave blank to keep') : 'Paste bot token'}
                value={discordTokenInput}
                onChange={(e) => setDiscordTokenInput(e.target.value)}
                autoComplete="off"
              />
            </label>

            <label className="preferences-field">
              <span className="preferences-label">Guild (server) ID</span>
              <input
                type="text"
                style={inputStyle}
                value={discordForm.guildId}
                onChange={(e) => setDiscordForm({ ...discordForm, guildId: e.target.value })}
                placeholder="Right-click your server → Copy Server ID"
              />
            </label>

            <label className="preferences-field">
              <span className="preferences-label">Allowed user IDs</span>
              <textarea
                style={{ ...inputStyle, minHeight: 56, resize: 'vertical', fontFamily: 'var(--mono)' }}
                value={discordAllowedInput}
                onChange={(e) => setDiscordAllowedInput(e.target.value)}
                placeholder="Comma or newline separated Discord user IDs"
              />
            </label>

            <div className="preferences-row">
              <label className="preferences-field">
                <span className="preferences-label">Command prefix</span>
                <input
                  type="text"
                  style={inputStyle}
                  value={discordForm.commandPrefix}
                  onChange={(e) => setDiscordForm({ ...discordForm, commandPrefix: e.target.value })}
                  placeholder="!"
                />
              </label>
              <label className="preferences-field">
                <span className="preferences-label">Control channel name</span>
                <input
                  type="text"
                  style={inputStyle}
                  value={discordForm.controlChannelName}
                  onChange={(e) => setDiscordForm({ ...discordForm, controlChannelName: e.target.value })}
                  placeholder="engine-control"
                />
              </label>
            </div>

            <div className="preferences-inline-actions">
              <button className="btn-primary" onClick={saveDiscordConfig}>
                {saved === 'discord' ? <><Check size={12} /> Saved</> : <><MessageSquare size={12} /> Save Discord config</>}
              </button>
              <button className="btn-secondary" onClick={validateDiscordConfig} disabled={discordValidating}>
                {discordValidating ? 'Testing…' : 'Test connection'}
              </button>
            </div>

            {discordValidation && (
              <div className="preferences-message" style={{
                borderColor: discordValidation.ok ? 'rgba(77, 224, 190, 0.35)' : 'rgba(255, 107, 107, 0.35)',
              }}>
                <div style={{ fontWeight: 600, marginBottom: 4 }}>
                  {discordValidation.ok ? 'Connection OK' : 'Issues detected'}
                </div>
                {discordValidation.guildName && (
                  <div className="preferences-muted">Guild: {discordValidation.guildName}</div>
                )}
                {discordValidation.botTag && (
                  <div className="preferences-muted">Bot: {discordValidation.botTag}</div>
                )}
                {discordValidation.errors?.length > 0 && (
                  <ul style={{ margin: '6px 0 0 18px', padding: 0, color: '#ff9a9a' }}>
                    {discordValidation.errors.map((err, i) => <li key={i}>{err}</li>)}
                  </ul>
                )}
                {discordValidation.warnings?.length > 0 && (
                  <ul style={{ margin: '6px 0 0 18px', padding: 0, color: '#e8c46b' }}>
                    {discordValidation.warnings.map((warn, i) => <li key={i}>{warn}</li>)}
                  </ul>
                )}
              </div>
            )}
          </div>
        </section>

      </div>
    </div>
  );
}
