import { useEffect, useState, useCallback } from 'react';
import type { GitStatus } from '@engine/shared';
import { useStore } from '../../store/index.js';
import { wsClient } from '../../ws/client.js';
import { bridge } from '../../bridge.js';
import type { FileNode, GitHubIssue } from '@engine/shared';
import {
  FolderOpen, Folder, RefreshCw, Plus, GitBranch,
  AlertCircle, Settings2, FileText, ChevronRight,
  Loader2, Search, Check, ServerCog,
} from 'lucide-react';

type ActivityTab = 'explorer' | 'git' | 'issues' | 'search' | 'settings';

interface Props {
  activityTab: ActivityTab;
  onOpenFolder: () => void;
}

export default function FileTree({ activityTab, onOpenFolder }: Props) {
  const { fileTree, activeSession, gitStatus, githubIssues, githubIssuesLoading,
          activeFilePath, setGithubIssuesLoading } = useStore();

  useEffect(() => {
    if (!activeSession) return;
    wsClient.send({ type: 'file.tree', path: activeSession.projectPath });
    wsClient.send({ type: 'git.status' });
  }, [activeSession?.id]);

  const refresh = () => {
    if (!activeSession) return;
    wsClient.send({ type: 'file.tree', path: activeSession.projectPath });
    wsClient.send({ type: 'git.status' });
  };

  const loadIssues = () => {
    if (!activeSession) return;
    setGithubIssuesLoading(true);
    wsClient.send({ type: 'github.issues', projectPath: activeSession.projectPath });
  };

  return (
    <>
      {activityTab === 'explorer' && (
        <>
          <div className="sidebar-header">
            <span className="sidebar-title">Explorer</span>
            <button className="sidebar-action" onClick={onOpenFolder} title="Open Folder">
              <FolderOpen size={13} />
            </button>
            <button className="sidebar-action" onClick={refresh} title="Refresh">
              <RefreshCw size={12} />
            </button>
          </div>
          <div className="sidebar-body">
            {fileTree ? (
              <TreeDir node={fileTree} depth={0} defaultOpen activePath={activeFilePath} />
            ) : (
              <div style={{ padding: '20px 12px', textAlign: 'center' }}>
                <div style={{ color: 'var(--tx-3)', fontSize: 12, marginBottom: 12 }}>No folder open</div>
                <button className="btn-secondary" style={{ fontSize: 12, padding: '6px 14px' }} onClick={onOpenFolder}>
                  <FolderOpen size={13} />
                  Open Folder
                </button>
              </div>
            )}
          </div>
        </>
      )}

      {activityTab === 'git' && (
        <>
          <div className="sidebar-header">
            <GitBranch size={13} style={{ color: 'var(--accent-2)' }} />
            <span className="sidebar-title">Source Control</span>
            <button className="sidebar-action" onClick={refresh}><RefreshCw size={12} /></button>
          </div>
          <div className="sidebar-body">
            <GitPanel status={gitStatus} />
          </div>
        </>
      )}

      {activityTab === 'search' && (
        <>
          <div className="sidebar-header">
            <Search size={13} style={{ color: 'var(--accent-2)' }} />
            <span className="sidebar-title">Search</span>
          </div>
          <div className="sidebar-body">
            <div className="empty-state" style={{ marginTop: 16 }}>
              <Search size={28} style={{ opacity: 0.2 }} />
              <span>File search coming soon</span>
            </div>
          </div>
        </>
      )}

      {activityTab === 'issues' && (
        <>
          <div className="sidebar-header">
            <AlertCircle size={13} style={{ color: 'var(--accent-2)' }} />
            <span className="sidebar-title">Issues</span>
            <button className="sidebar-action" onClick={loadIssues}><RefreshCw size={12} /></button>
          </div>
          <div className="sidebar-body">
            <IssuesPanel
              issues={githubIssues}
              loading={githubIssuesLoading}
              onLoad={loadIssues}
            />
          </div>
        </>
      )}

      {activityTab === 'settings' && (
        <>
          <div className="sidebar-header">
            <Settings2 size={13} style={{ color: 'var(--accent-2)' }} />
            <span className="sidebar-title">Settings</span>
          </div>
          <div className="sidebar-body">
            <SettingsPanel />
          </div>
        </>
      )}
    </>
  );
}

// Tree

function TreeDir({ node, depth, defaultOpen = false, activePath }: {
  node: FileNode; depth: number; defaultOpen?: boolean; activePath: string | null;
}) {
  const [open, setOpen] = useState(defaultOpen || depth < 2);
  if (node.type === 'file') return <TreeFile node={node} depth={depth} activePath={activePath} />;

  return (
    <>
      <div
        className="tree-node"
        style={{ paddingLeft: 6 + depth * 14 }}
        onClick={() => setOpen(v => !v)}
      >
        <ChevronRight size={12} className={`tree-chevron ${open ? 'open' : ''}`} />
        {open ? <FolderOpen size={13} style={{ color: 'var(--accent-2)', flexShrink: 0 }} />
               : <Folder size={13} style={{ color: 'var(--accent-2)', flexShrink: 0 }} />}
        <span className="tree-name">{node.name}</span>
      </div>
      {open && node.children?.map(child => (
        <TreeDir key={child.path} node={child} depth={depth + 1} activePath={activePath} />
      ))}
    </>
  );
}

function TreeFile({ node, depth, activePath }: {
  node: FileNode; depth: number; activePath: string | null;
}) {
  const isActive = activePath === node.path;
  const { color, Icon } = getFileStyle(node.name);

  return (
    <div
      className={`tree-node ${isActive ? 'active' : ''}`}
      style={{ paddingLeft: 6 + depth * 14 + 16 }}
      onClick={() => wsClient.send({ type: 'file.read', path: node.path })}
    >
      <Icon size={13} style={{ color, flexShrink: 0 }} />
      <span className="tree-name">{node.name}</span>
    </div>
  );
}

// Git panel

function GitPanel({ status }: { status: GitStatus | null }) {
  if (!status) {
    return <div className="empty-state"><GitBranch size={28} style={{ opacity: 0.2 }} /><span>No git repository</span></div>;
  }
  const total = status.staged.length + status.unstaged.length + status.untracked.length;
  return (
    <div style={{ padding: '8px 0' }}>
      <div style={{ padding: '4px 12px 8px', display: 'flex', alignItems: 'center', gap: 6 }}>
        <GitBranch size={12} style={{ color: 'var(--accent-2)' }} />
        <span style={{ fontWeight: 600, fontSize: 12, color: 'var(--tx)' }}>{status.branch}</span>
        {total > 0 && <span style={{ background: 'var(--accent)', color: 'white', borderRadius: 10, padding: '0 5px', fontSize: 10, fontWeight: 700 }}>{total}</span>}
      </div>
      {status.staged.length > 0 && <GitSection title="Staged" files={status.staged} color="var(--green)" />}
      {status.unstaged.length > 0 && <GitSection title="Changes" files={status.unstaged} color="var(--yellow)" />}
      {status.untracked.length > 0 && <GitSection title="Untracked" files={status.untracked} color="var(--tx-3)" />}
      {total === 0 && <div style={{ padding: '8px 12px', color: 'var(--tx-3)', fontSize: 12 }}>No changes</div>}
    </div>
  );
}

function GitSection({ title, files, color }: { title: string; files: string[]; color: string }) {
  const [open, setOpen] = useState(true);
  return (
    <div style={{ marginBottom: 4 }}>
      <div
        onClick={() => setOpen(v => !v)}
        style={{ padding: '3px 12px', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 5,
                 color: 'var(--tx-3)', fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.06em' }}
      >
        <ChevronRight size={10} style={{ transform: open ? 'rotate(90deg)' : undefined, transition: 'transform 120ms' }} />
        {title} <span style={{ color }}>{files.length}</span>
      </div>
      {open && files.map(f => (
        <div key={f} style={{ padding: '3px 12px 3px 26px', fontSize: 12, color: 'var(--tx-2)',
                              display: 'flex', gap: 6, alignItems: 'center', overflow: 'hidden' }}
        >
          <span style={{ width: 6, height: 6, borderRadius: '50%', background: color, flexShrink: 0 }} />
          <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{f.split('/').pop()}</span>
        </div>
      ))}
    </div>
  );
}

// Issues panel

function IssuesPanel({ issues, loading, onLoad }: { issues: GitHubIssue[]; loading: boolean; onLoad: () => void }) {
  if (loading) return <div className="empty-state"><Loader2 size={20} className="animate-spin" /><span>Loading </span></div>;issues
  if (issues.length === 0) return (
    <div className="empty-state">
      <AlertCircle size={28} style={{ opacity: 0.2 }} />
      <span>No open issues</span>
      <button className="btn-secondary" style={{ fontSize: 11, padding: '5px 12px', marginTop: 4 }} onClick={onLoad}>Load issues</button>
    </div>
  );
  return (
    <div style={{ padding: '4px 0' }}>
      {issues.map(issue => (
        <div
          key={issue.number}
          onClick={() => bridge.openExternal(issue.htmlUrl)}
          style={{ padding: '8px 12px', borderBottom: '1px solid var(--border)', cursor: 'pointer',
                   transition: 'background 80ms' }}
          onMouseEnter={e => (e.currentTarget.style.background = 'var(--surface-2)')}
          onMouseLeave={e => (e.currentTarget.style.background = '')}
        >
          <div style={{ display: 'flex', alignItems: 'flex-start', gap: 6 }}>
            <span style={{ color: 'var(--green)', fontSize: 11, fontWeight: 700, flexShrink: 0, marginTop: 1 }}>#{issue.number}</span>
            <span style={{ fontSize: 12, color: 'var(--tx)', lineHeight: 1.4 }}>{issue.title}</span>
          </div>
          {issue.labels.length > 0 && (
            <div style={{ display: 'flex', gap: 4, marginTop: 4, flexWrap: 'wrap' }}>
              {issue.labels.map(l => (
                <span key={l.name} style={{ fontSize: 10, padding: '1px 6px', borderRadius: 10,
                                            background: `#${l.color}22`, color: `#${l.color}`, fontWeight: 600 }}>
                  {l.name}
                </span>
              ))}
            </div>
          )}
        </div>
      ))}
    </div>
  );
}

// Settings panel

function SettingsPanel() {
  const { githubToken: token } = useStore();

  const [ghInput, setGhInput] = useState('');
  const [anthropicInput, setAnthropicInput] = useState('');
  const [openaiInput, setOpenaiInput] = useState('');
  const [modelInput, setModelInput] = useState('');
  const [saved, setSaved] = useState<string | null>(null);
  const [serviceStatus, setServiceStatus] = useState<string>('');
  const [serviceMsg, setServiceMsg] = useState('');
  const [serviceLoading, setServiceLoading] = useState(false);

  useEffect(() => {
    bridge.getAnthropicKey().then(k => { if (k) setAnthropicInput(k); });
    bridge.getOpenAiKey().then(k => { if (k) setOpenaiInput(k); });
    bridge.getModel().then(m => { if (m) setModelInput(m); });
    bridge.agentServiceStatus().then(setServiceStatus);
  }, []);

  const saveField = async (field: string, fn: () => Promise<boolean>) => {
    await fn();
    setSaved(field);
    setTimeout(() => setSaved(null), 2000);
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

  const inputStyle: React.CSSProperties = {
    width: '100%', background: 'var(--surface-2)', border: '1px solid var(--border-2)',
    borderRadius: 'var(--radius)', padding: '6px 8px', color: 'var(--tx)',
    fontSize: 12, fontFamily: 'inherit', outline: 'none', boxSizing: 'border-box',
  };
  const labelStyle: React.CSSProperties = {
    fontSize: 11, fontWeight: 600, color: 'var(--tx-3)',
    textTransform: 'uppercase', letterSpacing: '0.07em', marginBottom: 5, display: 'block',
  };

  return (
    <div style={{ padding: 12, display: 'flex', flexDirection: 'column', gap: 16 }}>

      {/* GitHub Token */}
      <div>
        <span style={labelStyle}>GitHub Token</span>
        <input
          type="password"
          placeholder={token ? '••••••••' : 'ghp_...'}
          value={ghInput}
          onChange={e => setGhInput(e.target.value)}
          style={inputStyle}
          onFocus={e => (e.target.style.borderColor = 'var(--accent)')}
          onBlur={e => (e.target.style.borderColor = 'var(--border-2)')}
        />
        <button
          className="btn-primary"
          style={{ marginTop: 6, fontSize: 11, padding: '4px 12px', display: 'flex', alignItems: 'center', gap: 4 }}
          onClick={() => saveField('gh', () => bridge.setGithubToken(ghInput))}
        >
          {saved === 'gh' ? <><Check size={11} /> Saved</> : 'Save'}
        </button>
      </div>

      {/* Anthropic API Key */}
      <div>
        <span style={labelStyle}>Anthropic API Key</span>
        <input
          type="password"
          placeholder="sk-ant-..."
          value={anthropicInput}
          onChange={e => setAnthropicInput(e.target.value)}
          style={inputStyle}
          onFocus={e => (e.target.style.borderColor = 'var(--accent)')}
          onBlur={e => (e.target.style.borderColor = 'var(--border-2)')}
        />
        <button
          className="btn-primary"
          style={{ marginTop: 6, fontSize: 11, padding: '4px 12px', display: 'flex', alignItems: 'center', gap: 4 }}
          onClick={() => saveField('anthropic', () => bridge.setAnthropicKey(anthropicInput))}
        >
          {saved === 'anthropic' ? <><Check size={11} /> Saved</> : 'Save'}
        </button>
      </div>

      {/* OpenAI API Key */}
      <div>
        <span style={labelStyle}>OpenAI API Key</span>
        <input
          type="password"
          placeholder="sk-..."
          value={openaiInput}
          onChange={e => setOpenaiInput(e.target.value)}
          style={inputStyle}
          onFocus={e => (e.target.style.borderColor = 'var(--accent)')}
          onBlur={e => (e.target.style.borderColor = 'var(--border-2)')}
        />
        <button
          className="btn-primary"
          style={{ marginTop: 6, fontSize: 11, padding: '4px 12px', display: 'flex', alignItems: 'center', gap: 4 }}
          onClick={() => saveField('openai', () => bridge.setOpenAiKey(openaiInput))}
        >
          {saved === 'openai' ? <><Check size={11} /> Saved</> : 'Save'}
        </button>
      </div>

      {/* Model */}
      <div>
        <span style={labelStyle}>Model</span>
        <input
          type="text"
          placeholder="claude-opus-4-5"
          value={modelInput}
          onChange={e => setModelInput(e.target.value)}
          style={inputStyle}
          onFocus={e => (e.target.style.borderColor = 'var(--accent)')}
          onBlur={e => (e.target.style.borderColor = 'var(--border-2)')}
        />
        <div style={{ fontSize: 10, color: 'var(--tx-3)', marginTop: 3 }}>
          claude-opus-4-5 · claude-sonnet-4-6 · gpt-4o · o3
        </div>
        <button
          className="btn-primary"
          style={{ marginTop: 6, fontSize: 11, padding: '4px 12px', display: 'flex', alignItems: 'center', gap: 4 }}
          onClick={() => saveField('model', () => bridge.setModel(modelInput))}
        >
          {saved === 'model' ? <><Check size={11} /> Saved</> : 'Save'}
        </button>
      </div>

      {/* Agent Service */}
      <div style={{ borderTop: '1px solid var(--border)', paddingTop: 14 }}>
        <span style={labelStyle}>Agent Service</span>
        <div style={{ fontSize: 11, color: 'var(--tx-3)', marginBottom: 8, lineHeight: 1.5 }}>
          Run Engine as a background service that starts at login.
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 8 }}>
          <span style={{
            width: 6, height: 6, borderRadius: '50%', flexShrink: 0,
            background: serviceStatus === 'installed' ? 'var(--green)' : 'var(--tx-3)',
          }} />
          <span style={{ fontSize: 11, color: 'var(--tx-2)' }}>
            {serviceStatus === 'installed' ? 'Installed' : 'Not installed'}
          </span>
        </div>
        {serviceStatus !== 'installed' ? (
          <button
            className="btn-primary"
            style={{ fontSize: 11, padding: '5px 12px', display: 'flex', alignItems: 'center', gap: 5, width: '100%', justifyContent: 'center' }}
            onClick={installService}
            disabled={serviceLoading}
          >
            {serviceLoading ? <Loader2 size={11} className="animate-spin" /> : <ServerCog size={11} />}
            Install at Login
          </button>
        ) : (
          <button
            style={{
              fontSize: 11, padding: '5px 12px', width: '100%', justifyContent: 'center',
              display: 'flex', alignItems: 'center', gap: 5,
              background: 'transparent', border: '1px solid var(--border-2)',
              borderRadius: 'var(--radius)', color: 'var(--tx-3)', cursor: 'pointer',
            }}
            onClick={uninstallService}
            disabled={serviceLoading}
          >
            {serviceLoading ? <Loader2 size={11} className="animate-spin" /> : null}
            Remove Service
          </button>
        )}
        {serviceMsg && (
          <div style={{ marginTop: 8, fontSize: 11, color: 'var(--tx-2)', lineHeight: 1.4 }}>
            {serviceMsg}
          </div>
        )}
      </div>
    </div>
  );
}

// File style helpers

function getFileStyle(name: string): { color: string; Icon: React.ComponentType<{ size?: number; style?: React.CSSProperties }> } {
  const ext = name.split('.').pop()?.toLowerCase() ?? '';
  const map: Record<string, string> = {
    ts: '#6366f1', tsx: '#6366f1', js: '#f59e0b', jsx: '#f59e0b',
    css: '#a78bfa', scss: '#a78bfa', less: '#a78bfa',
    html: '#fb923c', json: '#f59e0b', yaml: '#f43f5e', yml: '#f43f5e',
    md: '#8888aa', mdx: '#8888aa', py: '#22c55e', go: '#22d3ee',
    rs: '#fb923c', sh: '#22c55e', bash: '#22c55e', sql: '#f59e0b',
    toml: '#fb923c', graphql: '#e879f9', vue: '#22c55e', svelte: '#fb923c',
  };
  return { color: map[ext] ?? 'var(--tx-3)', Icon: FileText as React.ComponentType<{ size?: number; style?: React.CSSProperties }> };
}
