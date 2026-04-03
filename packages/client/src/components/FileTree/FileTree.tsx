import { useEffect, useState } from 'react';
import { useStore } from '../../store/index.js';
import { wsClient } from '../../ws/client.js';
import type { FileNode, GitHubIssue } from '@myeditor/shared';
import { ChevronRight, ChevronDown, RefreshCw } from 'lucide-react';

// File color by extension (VS Code style)
function fileColor(name: string): string {
  const ext = name.split('.').pop()?.toLowerCase() ?? '';
  const map: Record<string, string> = {
    ts: '#3178c6', tsx: '#3178c6', js: '#e5b80b', jsx: '#5b8def',
    py: '#3dd68c', rb: '#f06e6e', go: '#5b8def', rs: '#e8855f',
    json: '#e5b80b', yaml: '#e5b80b', yml: '#e5b80b', toml: '#e8855f',
    md: '#9898a6', mdx: '#9898a6', css: '#5b8def', scss: '#f06e6e',
    html: '#e8855f', svg: '#3dd68c', sh: '#3dd68c', bash: '#3dd68c',
    sql: '#e5b80b', graphql: '#e8855f', prisma: '#5b8def',
  };
  return map[ext] ?? '#55555f';
}

function TreeNode({ node, depth = 0 }: { node: FileNode; depth?: number }) {
  const [expanded, setExpanded] = useState(depth === 0);

  const handleClick = () => {
    if (node.type === 'directory') {
      setExpanded(e => !e);
    } else {
      wsClient.send({ type: 'file.read', path: node.path });
    }
  };

  return (
    <div>
      <div
        onClick={handleClick}
        className="flex items-center cursor-pointer truncate"
        style={{
          height: 22,
          paddingLeft: `${6 + depth * 12}px`,
          paddingRight: 8,
          gap: 4,
          borderRadius: 4,
          margin: '0 4px',
          fontSize: 12,
          color: '#c8c8d0',
          userSelect: 'none',
        }}
        onMouseEnter={e => (e.currentTarget.style.background = 'rgba(255,255,255,0.05)')}
        onMouseLeave={e => (e.currentTarget.style.background = 'transparent')}
      >
        {node.type === 'directory' ? (
          <span style={{ color: '#383840', flexShrink: 0, width: 12, display: 'flex', alignItems: 'center' }}>
            {expanded ? <ChevronDown size={10} /> : <ChevronRight size={10} />}
          </span>
        ) : (
          <span style={{ flexShrink: 0, width: 12 }} />
        )}
        {node.type === 'file' && (
          <span
            style={{
              width: 6, height: 6, borderRadius: '50%', flexShrink: 0,
              background: fileColor(node.name),
              opacity: 0.8,
            }}
          />
        )}
        {node.type === 'directory' && (
          <span style={{ fontSize: 11, flexShrink: 0 }}>📁</span>
        )}
        <span className="truncate">{node.name}</span>
      </div>
      {node.type === 'directory' && expanded && node.children?.map(child => (
        <TreeNode key={child.path} node={child} depth={depth + 1} />
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
      <div style={{ padding: '16px 12px', textAlign: 'center', fontSize: 11, color: '#383840' }}>
        No git repository detected
      </div>
    );
  }

  const groups = [
    { label: 'Staged', files: gitStatus.staged, color: '#3dd68c', symbol: '+' },
    { label: 'Changes', files: gitStatus.unstaged, color: '#e5b80b', symbol: '~' },
    { label: 'Untracked', files: gitStatus.untracked, color: '#55555f', symbol: '?' },
  ].filter(g => g.files.length > 0);

  return (
    <div className="overflow-y-auto">
      {groups.length === 0 ? (
        <div style={{ padding: '16px 12px', textAlign: 'center', fontSize: 11, color: '#383840' }}>
          ✓ No changes
        </div>
      ) : (
        groups.map(g => (
          <div key={g.label} style={{ marginBottom: 8 }}>
            <div style={{ padding: '4px 12px 2px', fontSize: 10, fontWeight: 600, color: '#55555f', textTransform: 'uppercase', letterSpacing: '0.06em' }}>
              {g.label} ({g.files.length})
            </div>
            {g.files.map(f => (
              <div
                key={f}
                onClick={() => wsClient.send({ type: 'file.read', path: `${useStore.getState().activeSession?.projectPath}/${f}` })}
                className="flex items-center gap-2 truncate cursor-pointer"
                style={{ height: 22, padding: '0 12px 0 20px', fontSize: 11, color: '#9898a6' }}
                onMouseEnter={e => (e.currentTarget.style.background = 'rgba(255,255,255,0.05)')}
                onMouseLeave={e => (e.currentTarget.style.background = 'transparent')}
              >
                <span style={{ color: g.color, fontWeight: 600, fontFamily: 'monospace', fontSize: 10, flexShrink: 0 }}>{g.symbol}</span>
                <span className="truncate">{f}</span>
              </div>
            ))}
          </div>
        ))
      )}
    </div>
  );
}

function IssuesPanel() {
  const { githubIssues, githubIssuesLoading, activeSession } = useStore();

  useEffect(() => {
    if (activeSession) wsClient.send({ type: 'github.issues', projectPath: activeSession.projectPath });
  }, [activeSession?.id]);

  if (githubIssuesLoading) {
    return <div style={{ padding: '16px 12px', textAlign: 'center', fontSize: 11, color: '#383840' }}>Loading issues...</div>;
  }

  if (!githubIssues || githubIssues.length === 0) {
    return <div style={{ padding: '16px 12px', textAlign: 'center', fontSize: 11, color: '#383840' }}>No open issues</div>;
  }

  return (
    <div className="overflow-y-auto">
      {githubIssues.map((issue: GitHubIssue) => (
        <div
          key={issue.number}
          onClick={() => window.electronAPI?.openExternal(issue.htmlUrl)}
          className="cursor-pointer"
          style={{ padding: '6px 12px', borderBottom: '1px solid rgba(255,255,255,0.04)' }}
          onMouseEnter={e => (e.currentTarget.style.background = 'rgba(255,255,255,0.03)')}
          onMouseLeave={e => (e.currentTarget.style.background = 'transparent')}
        >
          <div className="flex items-center gap-2">
            <span style={{ fontSize: 10, color: '#3dd68c', fontFamily: 'monospace', flexShrink: 0 }}>#{issue.number}</span>
            <span style={{ fontSize: 11, color: '#c8c8d0', lineHeight: 1.4 }} className="truncate">{issue.title}</span>
          </div>
          {issue.labels.length > 0 && (
            <div className="flex gap-1 mt-1 flex-wrap">
              {issue.labels.slice(0, 3).map(l => (
                <span
                  key={l.name}
                  style={{
                    fontSize: 9, padding: '1px 5px', borderRadius: 10,
                    background: `#${l.color}22`, color: `#${l.color}`, border: `1px solid #${l.color}44`,
                    fontFamily: 'monospace',
                  }}
                >
                  {l.name}
                </span>
              ))}
            </div>
          )}
          <div style={{ fontSize: 10, color: '#383840', marginTop: 2 }}>
            {issue.author} · {new Date(issue.createdAt).toLocaleDateString()}
          </div>
        </div>
      ))}
    </div>
  );
}

export default function FileTree({ activeTab }: { activeTab: 'explorer' | 'git' | 'issues' | 'settings' }) {
  const { fileTree, activeSession } = useStore();

  useEffect(() => {
    if (activeSession) {
      wsClient.send({ type: 'file.tree', path: activeSession.projectPath });
      wsClient.send({ type: 'git.status' });
    }
  }, [activeSession?.id]);

  const projectName = activeSession?.projectPath.split('/').pop() ?? 'Explorer';

  const titles: Record<string, string> = {
    explorer: projectName.toUpperCase(),
    git: 'SOURCE CONTROL',
    issues: 'ISSUES',
    settings: 'SETTINGS',
  };

  return (
    <div className="flex flex-col h-full" style={{ background: '#111113' }}>
      {/* Section header */}
      <div
        className="shrink-0 flex items-center justify-between"
        style={{ height: 30, padding: '0 8px 0 12px', borderBottom: '1px solid rgba(255,255,255,0.06)' }}
      >
        <span style={{ fontSize: 10, fontWeight: 700, color: '#55555f', letterSpacing: '0.08em' }}>
          {titles[activeTab]}
        </span>
        {activeTab === 'explorer' && (
          <button
            onClick={() => activeSession && wsClient.send({ type: 'file.tree', path: activeSession.projectPath })}
            style={{ background: 'none', border: 'none', cursor: 'pointer', color: '#383840', padding: 2, borderRadius: 3 }}
            onMouseEnter={e => (e.currentTarget.style.color = '#9898a6')}
            onMouseLeave={e => (e.currentTarget.style.color = '#383840')}
          >
            <RefreshCw size={11} />
          </button>
        )}
      </div>

      <div className="flex-1 overflow-y-auto" style={{ paddingTop: 4 }}>
        {activeTab === 'explorer' && (
          fileTree
            ? fileTree.children?.map(node => <TreeNode key={node.path} node={node} />)
            : <div style={{ padding: '12px', fontSize: 11, color: '#383840', textAlign: 'center' }}>No project loaded</div>
        )}
        {activeTab === 'git' && <GitPanel />}
        {activeTab === 'issues' && <IssuesPanel />}
        {activeTab === 'settings' && (
          <div style={{ padding: '12px', fontSize: 11, color: '#383840' }}>
            <div style={{ marginBottom: 8, color: '#55555f', fontWeight: 600 }}>GitHub</div>
            <div>Connect your account to see issues and CI status.</div>
            <div style={{ marginTop: 8 }}>Settings coming soon.</div>
          </div>
        )}
      </div>
    </div>
  );
}

