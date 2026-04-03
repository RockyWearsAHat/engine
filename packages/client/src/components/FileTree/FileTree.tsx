import { useEffect, useState } from 'react';
import { useStore } from '../../store/index.js';
import { wsClient } from '../../ws/client.js';
import { bridge } from '../../bridge.js';
import type { FileNode, GitHubIssue } from '@myeditor/shared';
import {
  ChevronRight, ChevronDown, RefreshCw,
  Plus, Minus, Circle, AlertCircle, GitBranch, RotateCcw
} from 'lucide-react';

function fileColor(name: string): string {
  const ext = name.split('.').pop()?.toLowerCase() ?? '';
  const map: Record<string, string> = {
    ts: '#4d7fff', tsx: '#4d7fff', js: '#f59e0b', jsx: '#f59e0b',
    css: '#a78bfa', scss: '#a78bfa', less: '#a78bfa',
    html: '#f97316', json: '#f59e0b', yaml: '#ef4444', yml: '#ef4444',
    md: '#999999', mdx: '#999999',
    py: '#22c55e', go: '#22d3ee', rs: '#f97316',
    sh: '#22c55e', bash: '#22c55e',
    sql: '#f59e0b', graphql: '#e879f9',
    toml: '#f97316',
  };
  return map[ext] ?? '#555555';
}

function FileIcon({ name }: { name: string }) {
  const color = fileColor(name);
  const ext = name.split('.').pop()?.toLowerCase() ?? '';
  return (
    <span style={{ color, flexShrink: 0, fontFamily: 'monospace', fontSize: '9px', fontWeight: 600, letterSpacing: '0.02em', lineHeight: 1 }}>
      {ext ? ext.slice(0, 2).toUpperCase() : '\u00b7\u00b7'}
    </span>
  );
}

function TreeNode({ node, depth = 0, activeFilePath }: { node: FileNode; depth?: number; activeFilePath?: string | null }) {
  const [expanded, setExpanded] = useState(depth < 2);

  const handleClick = () => {
    if (node.type === 'directory') {
      setExpanded(e => !e);
    } else {
      wsClient.send({ type: 'file.read', path: node.path });
    }
  };

  const isActive = node.path === activeFilePath;

  return (
    <div>
      <div
        onClick={handleClick}
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: '6px',
          paddingTop: '3px',
          paddingBottom: '3px',
          paddingLeft: `${10 + depth * 14}px`,
          paddingRight: '8px',
          fontSize: '12px',
          cursor: 'pointer',
          borderRadius: '4px',
          margin: '0 4px',
          userSelect: 'none',
          transition: 'background 150ms, color 150ms',
          background: isActive ? 'rgba(77,127,255,0.08)' : 'transparent',
          color: isActive ? 'var(--tx)' : 'var(--tx-2)',
        }}
        onMouseEnter={e => {
          if (!isActive) (e.currentTarget as HTMLDivElement).style.background = 'var(--surface-3)';
        }}
        onMouseLeave={e => {
          if (!isActive) (e.currentTarget as HTMLDivElement).style.background = 'transparent';
        }}
      >
        {node.type === 'directory' ? (
          <>
            <span style={{ color: 'var(--tx-3)', flexShrink: 0, width: 12, display: 'flex', alignItems: 'center' }}>
              {expanded ? <ChevronDown size={10} /> : <ChevronRight size={10} />}
            </span>
            <span style={{ flexShrink: 0, fontSize: '10px', fontFamily: 'monospace', color: expanded ? 'rgba(245,158,11,0.7)' : 'var(--tx-3)' }}>{'\u25b8'}</span>
          </>
        ) : (
          <>
            <span style={{ width: 12, flexShrink: 0 }} />
            <FileIcon name={node.name} />
          </>
        )}
        <span style={{
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
          flex: 1,
          fontWeight: node.type === 'directory' ? 500 : 400,
          color: isActive ? 'var(--tx)' : undefined,
        }}>
          {node.name}
        </span>
        {isActive && <span style={{ width: 4, height: 12, background: 'var(--accent)', borderRadius: 2, flexShrink: 0 }} />}
      </div>
      {node.type === 'directory' && expanded && node.children?.map(child => (
        <TreeNode key={child.path} node={child} depth={depth + 1} activeFilePath={activeFilePath} />
      ))}
    </div>
  );
}

function GitPanel() {
  const { gitStatus, activeSession } = useStore();

  useEffect(() => {
    if (activeSession) wsClient.send({ type: 'git.status' });
  }, [activeSession?.id]);

  if (!gitStatus) {
    return (
      <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', height: 128, gap: 8 }}>
        <GitBranch size={20} style={{ color: 'var(--tx-3)' }} />
        <span style={{ fontSize: '10px', color: 'var(--tx-3)' }}>No repository</span>
      </div>
    );
  }

  const all = gitStatus.staged.length + gitStatus.unstaged.length + gitStatus.untracked.length;

  return (
    <div style={{ overflowY: 'auto', paddingTop: 8, paddingBottom: 8 }}>
      <div style={{ padding: '0 12px 12px', display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: '11px', color: 'var(--tx-2)' }}>
          <GitBranch size={11} style={{ color: 'var(--accent)' }} />
          <span style={{ fontWeight: 500, color: 'var(--tx)' }}>{gitStatus.branch}</span>
          {gitStatus.ahead > 0 && <span style={{ color: 'var(--accent)', fontSize: '10px' }}>{'\u2191'}{gitStatus.ahead}</span>}
          {gitStatus.behind > 0 && <span style={{ color: '#f59e0b', fontSize: '10px' }}>{'\u2193'}{gitStatus.behind}</span>}
        </div>
        <button
          onClick={() => wsClient.send({ type: 'git.status' })}
          style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--tx-3)', padding: 2, transition: 'color 150ms' }}
          onMouseEnter={e => (e.currentTarget.style.color = 'var(--tx-2)')}
          onMouseLeave={e => (e.currentTarget.style.color = 'var(--tx-3)')}
        >
          <RotateCcw size={11} />
        </button>
      </div>

      {all === 0 ? (
        <div style={{ padding: '16px 12px', textAlign: 'center', fontSize: '10px', color: 'var(--tx-3)' }}>
          Working tree clean
        </div>
      ) : (
        <>
          {gitStatus.staged.length > 0 && (
            <GitSection label="Staged" count={gitStatus.staged.length} icon={<Plus size={9} style={{ color: '#22c55e' }} />} files={gitStatus.staged} projectPath={activeSession?.projectPath} />
          )}
          {gitStatus.unstaged.length > 0 && (
            <GitSection label="Changes" count={gitStatus.unstaged.length} icon={<Minus size={9} style={{ color: '#f59e0b' }} />} files={gitStatus.unstaged} projectPath={activeSession?.projectPath} />
          )}
          {gitStatus.untracked.length > 0 && (
            <GitSection label="Untracked" count={gitStatus.untracked.length} icon={<Circle size={9} style={{ color: 'var(--tx-3)' }} />} files={gitStatus.untracked} projectPath={activeSession?.projectPath} />
          )}
        </>
      )}
    </div>
  );
}

function GitSection({ label, count, icon, files, projectPath }: {
  label: string; count: number; icon: React.ReactNode; files: string[]; projectPath?: string;
}) {
  const [open, setOpen] = useState(true);
  return (
    <div style={{ marginBottom: 4 }}>
      <button
        onClick={() => setOpen(o => !o)}
        style={{
          width: '100%', display: 'flex', alignItems: 'center', gap: 8,
          padding: '4px 12px', fontSize: '10px', color: 'var(--tx-3)',
          background: 'none', border: 'none', cursor: 'pointer',
          textTransform: 'uppercase', letterSpacing: '0.06em', fontWeight: 600,
          transition: 'color 150ms',
        }}
        onMouseEnter={e => (e.currentTarget.style.color = 'var(--tx-2)')}
        onMouseLeave={e => (e.currentTarget.style.color = 'var(--tx-3)')}
      >
        {open ? <ChevronDown size={9} /> : <ChevronRight size={9} />}
        {icon}
        <span>{label}</span>
        <span style={{ marginLeft: 'auto', color: 'var(--tx-3)' }}>{count}</span>
      </button>
      {open && files.map(f => (
        <div
          key={f}
          style={{
            display: 'flex', alignItems: 'center', gap: 8,
            padding: '3px 12px 3px 20px', fontSize: '11px',
            color: 'var(--tx-2)', cursor: 'pointer', transition: 'background 150ms',
            margin: '0 4px', borderRadius: 4,
            overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
          }}
          onMouseEnter={e => (e.currentTarget.style.background = 'var(--surface-3)')}
          onMouseLeave={e => (e.currentTarget.style.background = 'transparent')}
          onClick={() => projectPath && wsClient.send({ type: 'file.read', path: `${projectPath}/${f}` })}
        >
          {icon}
          <span style={{ fontFamily: 'monospace', fontSize: '10px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{f}</span>
        </div>
      ))}
    </div>
  );
}

function IssuesPanel() {
  const { githubIssues, githubIssuesLoading, activeSession } = useStore();

  useEffect(() => {
    if (activeSession) wsClient.send({ type: 'github.issues', projectPath: activeSession.projectPath });
  }, [activeSession?.id]);

  if (githubIssuesLoading) {
    return (
      <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', height: 128, gap: 8 }}>
        <span style={{ fontSize: '10px', color: 'var(--tx-3)' }}>Loading issues...</span>
      </div>
    );
  }

  if (!githubIssues || githubIssues.length === 0) {
    return (
      <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', height: 128, gap: 8, padding: '0 16px' }}>
        <AlertCircle size={20} style={{ color: 'var(--tx-3)' }} />
        <span style={{ fontSize: '10px', color: 'var(--tx-3)', textAlign: 'center', lineHeight: 1.5 }}>
          No open issues or connect GitHub to see issues
        </span>
        <button
          style={{
            marginTop: 4, padding: '4px 12px', fontSize: '10px',
            background: 'rgba(77,127,255,0.1)', color: 'var(--accent)',
            border: '1px solid rgba(77,127,255,0.2)', borderRadius: 4, cursor: 'pointer',
            transition: 'background 150ms',
          }}
          onMouseEnter={e => (e.currentTarget.style.background = 'rgba(77,127,255,0.2)')}
          onMouseLeave={e => (e.currentTarget.style.background = 'rgba(77,127,255,0.1)')}
        >
          Connect GitHub
        </button>
      </div>
    );
  }

  return (
    <div style={{ overflowY: 'auto' }}>
      {githubIssues.map((issue: GitHubIssue) => (
        <div
          key={issue.number}
          onClick={() => bridge.openExternal(issue.htmlUrl)}
          style={{
            padding: '6px 12px', borderBottom: '1px solid var(--border)',
            cursor: 'pointer', transition: 'background 150ms',
          }}
          onMouseEnter={e => (e.currentTarget.style.background = 'var(--surface-3)')}
          onMouseLeave={e => (e.currentTarget.style.background = 'transparent')}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <span style={{ fontSize: '10px', color: '#22c55e', fontFamily: 'monospace', flexShrink: 0 }}>#{issue.number}</span>
            <span style={{ fontSize: '11px', color: 'var(--tx)', lineHeight: 1.4, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{issue.title}</span>
          </div>
          {issue.labels.length > 0 && (
            <div style={{ display: 'flex', gap: 4, marginTop: 4, flexWrap: 'wrap' }}>
              {issue.labels.slice(0, 3).map(l => (
                <span
                  key={l.name}
                  style={{
                    fontSize: '9px', padding: '1px 5px', borderRadius: 10,
                    background: `#${l.color}22`, color: `#${l.color}`, border: `1px solid #${l.color}44`,
                    fontFamily: 'monospace',
                  }}
                >
                  {l.name}
                </span>
              ))}
            </div>
          )}
          <div style={{ fontSize: '10px', color: 'var(--tx-3)', marginTop: 2 }}>
            {issue.author} {'\u00b7'} {new Date(issue.createdAt).toLocaleDateString()}
          </div>
        </div>
      ))}
    </div>
  );
}

export default function FileTree({ activeTab }: { activeTab: 'explorer' | 'git' | 'issues' | 'settings' }) {
  const { fileTree, activeSession, activeFilePath } = useStore();

  useEffect(() => {
    if (activeSession) {
      wsClient.send({ type: 'file.tree', path: activeSession.projectPath });
      wsClient.send({ type: 'git.status' });
    }
  }, [activeSession?.id]);

  const projectName = activeSession?.projectPath.split('/').pop()?.toUpperCase() ?? 'EXPLORER';

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', background: 'var(--surface)' }}>
      <div style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        padding: '0 8px 0 12px', height: 30, flexShrink: 0,
        borderBottom: '1px solid var(--border)',
      }}>
        <span style={{ fontSize: '10px', fontWeight: 700, color: 'var(--tx-3)', letterSpacing: '0.08em' }}>
          {activeTab === 'explorer' ? projectName :
           activeTab === 'git' ? 'SOURCE CONTROL' :
           activeTab === 'issues' ? 'ISSUES' : 'SETTINGS'}
        </span>
        {activeTab === 'explorer' && (
          <button
            onClick={() => activeSession && wsClient.send({ type: 'file.tree', path: activeSession.projectPath })}
            style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--tx-3)', padding: 2, borderRadius: 3, transition: 'color 150ms' }}
            onMouseEnter={e => (e.currentTarget.style.color = 'var(--tx-2)')}
            onMouseLeave={e => (e.currentTarget.style.color = 'var(--tx-3)')}
          >
            <RefreshCw size={11} />
          </button>
        )}
      </div>

      <div style={{ flex: 1, overflowY: 'auto', paddingTop: 4 }}>
        {activeTab === 'explorer' ? (
          fileTree
            ? <div className="fade-in">{fileTree.children?.map(node => <TreeNode key={node.path} node={node} activeFilePath={activeFilePath} />)}</div>
            : <div style={{ padding: 12, fontSize: '11px', color: 'var(--tx-3)', textAlign: 'center' }}>Loading...</div>
        ) : activeTab === 'git' ? <GitPanel />
        : activeTab === 'issues' ? <IssuesPanel />
        : (
          <div style={{ padding: 12, fontSize: '11px', color: 'var(--tx-3)' }}>
            Settings coming soon
          </div>
        )}
      </div>
    </div>
  );
}
