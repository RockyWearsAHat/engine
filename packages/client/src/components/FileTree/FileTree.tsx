import { useEffect, useMemo, useRef, useState } from 'react';
import type { GitCommit, GitStatus } from '@engine/shared';
import { useStore } from '../../store/index.js';
import { wsClient } from '../../ws/client.js';
import { bridge } from '../../bridge.js';
import type { FileNode, GitHubIssue, SearchResult } from '@engine/shared';
import {
  FolderOpen, Folder, RefreshCw, GitBranch,
  AlertCircle, FileText, ChevronRight,
  Loader2, Search,
} from 'lucide-react';

type ActivityTab = 'explorer' | 'git' | 'issues' | 'search';

type GitFileStatus = 'modified' | 'staged' | 'untracked' | 'ignored' | null;

interface Props {
  activityTab: ActivityTab;
  onOpenFolder: () => void;
  onOpenFile: () => void;
}

/** Normalize file:// URLs to plain paths (Tauri on macOS wraps them). */
function normalizePath(path: string): string {
  if (!path || !path.startsWith('file://')) return path;
  try {
    let p = decodeURIComponent(new URL(path).pathname);
    if (/^\/[A-Za-z]:/.test(p)) p = p.slice(1);
    return p || path;
  } catch { return path; }
}

/** Build a lookup: absolute path → git file status (highest-priority wins). */
function buildStatusMap(
  gitStatus: GitStatus | null,
  projectPath: string | null,
): Map<string, GitFileStatus> {
  const map = new Map<string, GitFileStatus>();
  if (!gitStatus || !projectPath) return map;
  const normalized = normalizePath(projectPath);
  const base = normalized.endsWith('/') ? normalized : normalized + '/';

  for (const f of gitStatus.untracked ?? []) map.set(base + f, 'untracked');
  for (const f of gitStatus.staged ?? []) map.set(base + f, 'staged');
  for (const f of gitStatus.unstaged ?? []) map.set(base + f, 'modified');
  return map;
}

/** Pre-computed directory status lookup built from a file-level statusMap. */
function buildDirStatusMap(
  statusMap: Map<string, GitFileStatus>,
): Map<string, GitFileStatus> {
  const dirMap = new Map<string, GitFileStatus>();
  const priority: Record<string, number> = { modified: 3, staged: 2, untracked: 1 };
  for (const [filePath, status] of statusMap) {
    if (!status) continue;
    let dir = filePath;
    while (true) {
      const sep = dir.lastIndexOf('/');
      if (sep <= 0) break;
      dir = dir.slice(0, sep);
      const existing = dirMap.get(dir);
      if (existing && (priority[existing] ?? 0) >= (priority[status] ?? 0)) break;
      dirMap.set(dir, status);
    }
  }
  return dirMap;
}

export default function FileTree({ activityTab, onOpenFolder, onOpenFile }: Props) {
  const { fileTree, activeSession, gitStatus, githubIssues, githubIssuesLoading,
          githubIssuesError, activeFilePath, setGithubIssuesLoading, setGithubIssuesError,
          searchQuery, searchResults, searchLoading, searchError,
          setSearchQuery, setSearchLoading, clearSearch, showDotfiles } = useStore();

  const [contextMenu, setContextMenu] = useState<{ x: number; y: number; dirPath: string } | null>(null);

  // .git is hidden by default; other dotfiles (.github, .vscode, etc) always show.
  // Cmd+. toggles .git visibility.
  const filterTree = (node: FileNode): FileNode | null => {
    if (!showDotfiles && node.name === '.git') return null;
    if (node.type === 'directory' && node.children) {
      const filtered = node.children
        .map(filterTree)
        .filter((n): n is FileNode => n !== null);
      return { ...node, children: filtered };
    }
    return node;
  };
  const visibleTree = fileTree ? filterTree(fileTree) : null;

  const statusMap = useMemo(
    () => buildStatusMap(gitStatus, activeSession?.projectPath ?? null),
    [gitStatus, activeSession?.projectPath],
  );

  const dirStatusMap = useMemo(
    () => buildDirStatusMap(statusMap),
    [statusMap],
  );

  useEffect(() => {
    if (!activeSession) return;
    wsClient.send({ type: 'file.tree', path: activeSession.projectPath });
    wsClient.send({ type: 'git.status' });
    clearSearch();
  }, [activeSession?.id]);

  const refresh = () => {
    if (!activeSession) return;
    wsClient.send({ type: 'file.tree', path: activeSession.projectPath });
    wsClient.send({ type: 'git.status' });
  };

  const loadIssues = () => {
    if (!activeSession) return;
    setGithubIssuesLoading(true);
    setGithubIssuesError(null);
    wsClient.send({ type: 'github.issues', projectPath: activeSession.projectPath });
  };

  const runSearch = () => {
    if (!activeSession) return;
    const query = searchQuery.trim();
    if (!query) return;
    setSearchLoading(true);
    wsClient.send({ type: 'file.search', query, root: activeSession.projectPath });
  };

  return (
    <>
      {activityTab === 'explorer' && (
        <>
          <div className="sidebar-header">
            <span className="sidebar-title">Explorer</span>
            <button className="sidebar-action" onClick={onOpenFile} title="Open File">
              <FileText size={13} />
            </button>
            <button className="sidebar-action" onClick={onOpenFolder} title="Open Folder">
              <FolderOpen size={13} />
            </button>
            <button className="sidebar-action" onClick={refresh} title="Refresh">
              <RefreshCw size={12} />
            </button>
          </div>
          <div className="sidebar-body">
            {visibleTree ? (
              <>
                <TreeDir node={visibleTree} depth={0} defaultOpen activePath={activeFilePath} statusMap={statusMap} dirStatusMap={dirStatusMap} showDotfiles={showDotfiles} onContextMenu={(x, y, path) => setContextMenu({ x, y, dirPath: path })} />
                {contextMenu && (
                  <TreeContextMenu
                    x={contextMenu.x}
                    y={contextMenu.y}
                    dirPath={contextMenu.dirPath}
                    onClose={() => setContextMenu(null)}
                    projectPath={activeSession?.projectPath ?? ''}
                  />
                )}
              </>
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
            <GitPanel status={gitStatus} projectPath={activeSession?.projectPath ?? null} />
          </div>
        </>
      )}

      {activityTab === 'search' && (
        <>
          <div className="sidebar-header">
            <Search size={13} style={{ color: 'var(--accent-2)' }} />
            <span className="sidebar-title">Search</span>
            <button className="sidebar-action" onClick={runSearch} disabled={!activeSession || !searchQuery.trim() || searchLoading} title="Search">
              {searchLoading ? <Loader2 size={12} className="animate-spin" /> : <RefreshCw size={12} />}
            </button>
          </div>
          <div className="sidebar-body">
            <div style={{ padding: 12, borderBottom: '1px solid var(--border)' }}>
              <div style={{ display: 'flex', gap: 6 }}>
                <input
                  type="text"
                  value={searchQuery}
                  onChange={e => setSearchQuery(e.target.value)}
                  onKeyDown={e => {
                    if (e.key === 'Enter') {
                      e.preventDefault();
                      runSearch();
                    }
                  }}
                  placeholder={activeSession ? 'Search in files…' : 'Open a folder first…'}
                  disabled={!activeSession}
                  style={{
                    flex: 1,
                    background: 'var(--surface-2)',
                    border: '1px solid var(--border-2)',
                    borderRadius: 'var(--radius)',
                    padding: '7px 9px',
                    color: 'var(--tx)',
                    fontSize: 12,
                    outline: 'none',
                  }}
                />
                <button
                  className="btn-secondary"
                  style={{ fontSize: 11, padding: '6px 10px' }}
                  onClick={runSearch}
                  disabled={!activeSession || !searchQuery.trim() || searchLoading}
                >
                  Search
                </button>
              </div>
              <div style={{ fontSize: 10, color: 'var(--tx-3)', marginTop: 6 }}>
                Search the current workspace with ripgrep.
              </div>
            </div>
            <SearchPanel
              activeSessionPath={activeSession?.projectPath ?? null}
              results={searchResults}
              loading={searchLoading}
              error={searchError}
              hasQuery={searchQuery.trim().length > 0}
            />
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
              error={githubIssuesError}
              onLoad={loadIssues}
            />
          </div>
        </>
      )}

    </>
  );
}

// Tree

function TreeDir({ node, depth, defaultOpen = false, activePath, statusMap, dirStatusMap, showDotfiles, onContextMenu }: {
  node: FileNode; depth: number; defaultOpen?: boolean; activePath: string | null;
  statusMap: Map<string, GitFileStatus>; dirStatusMap: Map<string, GitFileStatus>; showDotfiles: boolean;
  onContextMenu?: (x: number, y: number, path: string) => void;
}) {
  const [open, setOpen] = useState(defaultOpen || depth < 1);
  const [loading, setLoading] = useState(false);
  const loadTimerRef = useRef<ReturnType<typeof window.setTimeout> | null>(null);
  const canExpand = Boolean(node.hasChildren || node.children?.length);

  useEffect(() => {
    if (node.loaded || !open) {
      if (loadTimerRef.current) {
        window.clearTimeout(loadTimerRef.current);
        loadTimerRef.current = null;
      }
      setLoading(false);
    }
  }, [node.loaded, open]);

  useEffect(() => () => {
    if (loadTimerRef.current) {
      window.clearTimeout(loadTimerRef.current);
    }
  }, []);

  if (node.type === 'file') return <TreeFile node={node} depth={depth} activePath={activePath} statusMap={statusMap} showDotfiles={showDotfiles} />;

  // Only dim entries that were hidden by default and revealed via Cmd+.
  const isRevealedHidden = showDotfiles && node.name === '.git';
  const dirStatus = dirStatusMap.get(node.path) ?? null;

  const toggleNode = () => {
    if (!canExpand) {
      return;
    }
    const nextOpen = !open;
    setOpen(nextOpen);
    if (nextOpen && !node.loaded && !loading) {
      setLoading(true);
      if (loadTimerRef.current) {
        window.clearTimeout(loadTimerRef.current);
      }
      loadTimerRef.current = window.setTimeout(() => {
        setLoading(false);
        loadTimerRef.current = null;
      }, 15000);
      wsClient.send({ type: 'file.tree', path: node.path });
    }
  };

  const nodeClass = [
    'tree-node',
    isRevealedHidden ? 'tree-dimmed' : '',
    dirStatus ? `tree-git-${dirStatus}` : '',
  ].filter(Boolean).join(' ');

  return (
    <>
      <div
        className={nodeClass}
        style={{ paddingLeft: 6 + depth * 14 }}
        onClick={toggleNode}
        onContextMenu={(e) => {
          e.preventDefault();
          if (onContextMenu) {
            onContextMenu(e.clientX, e.clientY, node.path);
          }
        }}
      >
        {canExpand ? (
          <ChevronRight size={12} className={`tree-chevron ${open ? 'open' : ''}`} />
        ) : (
          <span className="tree-chevron-spacer" />
        )}
        {open ? <FolderOpen size={13} style={{ color: 'var(--accent-2)', flexShrink: 0 }} />
               : <Folder size={13} style={{ color: 'var(--accent-2)', flexShrink: 0 }} />}
        <span className="tree-name">{node.name}</span>
      </div>
      {open && loading && !node.loaded && (
        <div className="tree-node tree-loading" style={{ paddingLeft: 6 + (depth + 1) * 14 + 16 }}>
          <Loader2 size={12} className="animate-spin" />
          <span className="tree-name">Loading…</span>
        </div>
      )}
      {open && node.children?.map(child => (
        <TreeDir key={child.path} node={child} depth={depth + 1} activePath={activePath} statusMap={statusMap} dirStatusMap={dirStatusMap} showDotfiles={showDotfiles} onContextMenu={onContextMenu} />
      ))}
    </>
  );
}

const GIT_BADGE: Record<string, string> = {
  modified: 'M',
  staged: 'A',
  untracked: 'U',
  ignored: 'I',
};

function TreeFile({ node, depth, activePath, statusMap, showDotfiles }: {
  node: FileNode; depth: number; activePath: string | null;
  statusMap: Map<string, GitFileStatus>; showDotfiles: boolean;
}) {
  const isActive = activePath === node.path;
  const { color, Icon } = getFileStyle(node.name);
  const status = statusMap.get(node.path) ?? null;
  // Dim files that were hidden by default (revealed via Cmd+.) or are gitignored
  const isRevealedHidden = showDotfiles && node.name === '.git';
  const isDimmed = isRevealedHidden || status === 'ignored';

  const nodeClass = [
    'tree-node',
    isActive ? 'active' : '',
    isDimmed ? 'tree-dimmed' : '',
    status ? `tree-git-${status}` : '',
  ].filter(Boolean).join(' ');

  return (
    <div
      className={nodeClass}
      style={{ paddingLeft: 6 + depth * 14 + 16 }}
      onClick={() => wsClient.send({ type: 'file.read', path: node.path })}
    >
      <Icon size={13} style={{ color, flexShrink: 0 }} />
      <span className="tree-name">{node.name}</span>
      {status && GIT_BADGE[status] && (
        <span className={`tree-git-badge tree-git-badge-${status}`}>{GIT_BADGE[status]}</span>
      )}
    </div>
  );
}

// Git panel

function GitPanel({ status, projectPath }: { status: GitStatus | null; projectPath: string | null }) {
  const [commitMessage, setCommitMessage] = useState('');
  const [commitBusy, setCommitBusy] = useState(false);
  const [commitFeedback, setCommitFeedback] = useState<string | null>(null);
  const [selectedDiffPath, setSelectedDiffPath] = useState<string | null>(null);
  const [diffText, setDiffText] = useState('(select a changed file to preview its diff)');
  const [diffLoading, setDiffLoading] = useState(false);
  const [commits, setCommits] = useState<GitCommit[]>([]);
  const selectedDiffRef = useRef<string | null>(null);

  selectedDiffRef.current = selectedDiffPath;

  useEffect(() => {
    if (!projectPath) {
      setCommits([]);
      setSelectedDiffPath(null);
      setDiffText('(select a changed file to preview its diff)');
      return;
    }
    wsClient.send({ type: 'git.log', limit: 8 });
  }, [projectPath]);

  useEffect(() => {
    const off = wsClient.onMessage((msg) => {
      if (msg.type === 'git.log') {
        setCommits(msg.commits);
        return;
      }

      if (msg.type === 'git.diff') {
        if (msg.path && selectedDiffRef.current && msg.path !== selectedDiffRef.current) {
          return;
        }
        setDiffText(msg.diff);
        setDiffLoading(false);
        return;
      }

      if (msg.type === 'git.commit.result') {
        setCommitBusy(false);
        setCommitFeedback(msg.ok ? `Committed ${msg.hash}` : msg.message);
        if (msg.ok) {
          setCommitMessage('');
          wsClient.send({ type: 'git.status' });
          wsClient.send({ type: 'git.log', limit: 8 });
        }
      }
    });
    return () => off();
  }, []);

  useEffect(() => {
    if (!commitFeedback) {
      return;
    }
    const timer = window.setTimeout(() => setCommitFeedback(null), 3200);
    return () => window.clearTimeout(timer);
  }, [commitFeedback]);

  if (!status) {
    return <div className="empty-state"><GitBranch size={28} style={{ opacity: 0.2 }} /><span>No git repository</span></div>;
  }

  const total = status.staged.length + status.unstaged.length + status.untracked.length;
  const changedFiles = [
    ...status.staged.map((path) => ({ path, section: 'Staged', color: 'var(--green)' })),
    ...status.unstaged.map((path) => ({ path, section: 'Changes', color: 'var(--yellow)' })),
    ...status.untracked.map((path) => ({ path, section: 'Untracked', color: 'var(--tx-3)' })),
  ];

  const openDiff = (path: string) => {
    setSelectedDiffPath(path);
    setDiffLoading(true);
    setDiffText('Loading diff…');
    wsClient.send({ type: 'git.diff', path });
  };

  return (
    <div style={{ padding: '8px 0' }}>
      <div style={{ padding: '4px 12px 8px', display: 'flex', flexDirection: 'column', gap: 10 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
          <GitBranch size={12} style={{ color: 'var(--accent-2)' }} />
          <span style={{ fontWeight: 600, fontSize: 12, color: 'var(--tx)' }}>{status.branch}</span>
          {total > 0 && <span style={{ background: 'var(--accent)', color: 'white', borderRadius: 3, padding: '0 5px', fontSize: 10, fontWeight: 700 }}>{total}</span>}
          {status.ahead > 0 && <span style={{ fontSize: 10, color: 'var(--accent-2)' }}>↑{status.ahead}</span>}
          {status.behind > 0 && <span style={{ fontSize: 10, color: '#f97316' }}>↓{status.behind}</span>}
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          <textarea
            value={commitMessage}
            onChange={(event) => setCommitMessage(event.target.value)}
            placeholder="Write a commit message…"
            style={{
              width: '100%',
              minHeight: 74,
              resize: 'vertical',
              background: 'var(--surface-2)',
              border: '1px solid var(--border-2)',
              borderRadius: 3,
              padding: '7px 10px',
              color: 'var(--tx)',
              fontSize: 12,
              fontFamily: 'inherit',
              outline: 'none',
              boxSizing: 'border-box',
            }}
          />
          <div style={{ display: 'flex', justifyContent: 'space-between', gap: 8, alignItems: 'center' }}>
            <span style={{ fontSize: 10, color: 'var(--tx-3)' }}>
              Stage and commit everything from the current workspace.
            </span>
            <button
              className="btn-primary"
              style={{ fontSize: 11, padding: '6px 12px' }}
              disabled={commitBusy || !commitMessage.trim()}
              onClick={() => {
                setCommitBusy(true);
                wsClient.send({ type: 'git.commit', message: commitMessage.trim() });
              }}
            >
              {commitBusy ? 'Committing…' : 'Commit all'}
            </button>
          </div>
          {commitFeedback && (
            <div style={{
              padding: '8px 10px',
              borderRadius: 3,
              fontSize: 11,
              background: 'rgba(99,102,241,0.08)',
              border: '1px solid rgba(129,140,248,0.16)',
              color: 'var(--tx-2)',
            }}>
              {commitFeedback}
            </div>
          )}
        </div>
      </div>

      {status.staged.length > 0 && <GitSection title="Staged" files={status.staged} color="var(--green)" onSelect={openDiff} />}
      {status.unstaged.length > 0 && <GitSection title="Changes" files={status.unstaged} color="var(--yellow)" onSelect={openDiff} />}
      {status.untracked.length > 0 && <GitSection title="Untracked" files={status.untracked} color="var(--tx-3)" onSelect={openDiff} />}
      {total === 0 && <div style={{ padding: '8px 12px', color: 'var(--tx-3)', fontSize: 12 }}>No changes</div>}

      <div style={{ padding: '10px 12px 0' }}>
        <div style={{ fontSize: 11, fontWeight: 600, letterSpacing: '0.06em', textTransform: 'uppercase', color: 'var(--tx-3)', marginBottom: 8 }}>
          Diff preview
        </div>
        <div style={{
          borderRadius: 3,
          border: '1px solid var(--border)',
          background: 'rgba(8,10,15,0.86)',
          overflow: 'hidden',
        }}>
          <div style={{
            padding: '7px 10px',
            borderBottom: '1px solid var(--border)',
            fontSize: 11,
            color: 'var(--tx-3)',
            display: 'flex',
            justifyContent: 'space-between',
            gap: 8,
          }}>
            <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {selectedDiffPath ?? 'No file selected'}
            </span>
            <span>{diffLoading ? 'Loading…' : 'Ready'}</span>
          </div>
          <pre style={{
            margin: 0,
            padding: '12px 12px 14px',
            maxHeight: 220,
            overflow: 'auto',
            fontSize: 11,
            lineHeight: 1.6,
            color: 'var(--tx-2)',
            background: 'transparent',
            fontFamily: '"JetBrains Mono", Menlo, Monaco, monospace',
          }}>
            {diffText}
          </pre>
        </div>
      </div>

      <div style={{ padding: '14px 12px 0' }}>
        <div style={{ fontSize: 11, fontWeight: 600, letterSpacing: '0.06em', textTransform: 'uppercase', color: 'var(--tx-3)', marginBottom: 8 }}>
          Recent commits
        </div>
        {commits.length === 0 ? (
          <div style={{ padding: '8px 0', color: 'var(--tx-3)', fontSize: 12 }}>No commits loaded yet.</div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            {commits.map((commit) => (
              <div
                key={commit.hash}
                style={{
                  padding: '10px 12px',
                  borderRadius: 3,
                  border: '1px solid rgba(255,255,255,0.05)',
                  background: 'rgba(255,255,255,0.02)',
                }}
              >
                <div style={{ fontSize: 12, color: 'var(--tx)', lineHeight: 1.4 }}>{commit.message}</div>
                <div style={{ marginTop: 4, fontSize: 10, color: 'var(--tx-3)', display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                  <span>{commit.hash.slice(0, 7)}</span>
                  <span>{commit.author}</span>
                  <span>{new Date(commit.date).toLocaleString()}</span>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {changedFiles.length > 0 && !selectedDiffPath && (
        <div style={{ padding: '12px', color: 'var(--tx-3)', fontSize: 11 }}>
          Select a changed file above to preview the patch.
        </div>
      )}
    </div>
  );
}

function GitSection({
  title,
  files,
  color,
  onSelect,
}: {
  title: string;
  files: string[];
  color: string;
  onSelect: (path: string) => void;
}) {
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
        <button
          key={f}
          onClick={() => onSelect(f)}
          style={{
            width: '100%',
            padding: '6px 12px 6px 26px',
            fontSize: 12,
            color: 'var(--tx-2)',
            display: 'flex',
            gap: 6,
            alignItems: 'center',
            overflow: 'hidden',
            border: 'none',
            background: 'transparent',
            cursor: 'pointer',
            textAlign: 'left',
          }}
        >
          <span style={{ width: 6, height: 6, borderRadius: '50%', background: color, flexShrink: 0 }} />
          <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{f.split('/').pop()}</span>
        </button>
      ))}
    </div>
  );
}

// Issues panel

function IssuesPanel({ issues, loading, error, onLoad }: { issues: GitHubIssue[]; loading: boolean; error: string | null; onLoad: () => void }) {
  if (loading) return <div className="empty-state"><Loader2 size={20} className="animate-spin" /><span>Loading</span></div>;
  if (error) return (
    <div className="empty-state">
      <AlertCircle size={28} style={{ opacity: 0.2 }} />
      <span>{error}</span>
      <button className="btn-secondary" style={{ fontSize: 11, padding: '5px 12px', marginTop: 4 }} onClick={onLoad}>Retry</button>
    </div>
  );
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
                <span key={l.name} style={{ fontSize: 10, padding: '1px 6px', borderRadius: 3,
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

function SearchPanel({
  activeSessionPath,
  results,
  loading,
  error,
  hasQuery,
}: {
  activeSessionPath: string | null;
  results: SearchResult[];
  loading: boolean;
  error: string | null;
  hasQuery: boolean;
}) {
  if (!activeSessionPath) {
    return (
      <div className="empty-state" style={{ marginTop: 16 }}>
        <Search size={28} style={{ opacity: 0.2 }} />
        <span>Open a folder to search</span>
      </div>
    );
  }

  if (loading) {
    return <div className="empty-state"><Loader2 size={20} className="animate-spin" /><span>Searching</span></div>;
  }

  if (error) {
    return (
      <div className="empty-state" style={{ marginTop: 16 }}>
        <AlertCircle size={28} style={{ opacity: 0.2 }} />
        <span>{error}</span>
      </div>
    );
  }

  if (!hasQuery) {
    return (
      <div className="empty-state" style={{ marginTop: 16 }}>
        <Search size={28} style={{ opacity: 0.2 }} />
        <span>Search for text in this workspace</span>
      </div>
    );
  }

  if (results.length === 0) {
    return (
      <div className="empty-state" style={{ marginTop: 16 }}>
        <Search size={28} style={{ opacity: 0.2 }} />
        <span>No matches found</span>
      </div>
    );
  }

  return (
    <div style={{ padding: '4px 0' }}>
      {results.map(result => {
        const relativePath = result.path.startsWith(activeSessionPath)
          ? result.path.slice(activeSessionPath.length + 1)
          : result.path;
        return (
          <div
            key={`${result.path}:${result.line}:${result.column ?? 0}`}
            onClick={() => wsClient.send({ type: 'file.read', path: result.path })}
            style={{
              padding: '8px 12px',
              borderBottom: '1px solid var(--border)',
              cursor: 'pointer',
              transition: 'background 80ms',
            }}
            onMouseEnter={e => (e.currentTarget.style.background = 'var(--surface-2)')}
            onMouseLeave={e => (e.currentTarget.style.background = '')}
          >
            <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 3 }}>
              <FileText size={12} style={{ color: 'var(--accent-2)', flexShrink: 0 }} />
              <span style={{ fontSize: 11, color: 'var(--tx-2)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {relativePath}
              </span>
              <span style={{ fontSize: 10, color: 'var(--tx-3)', flexShrink: 0 }}>
                {result.line}{result.column ? `:${result.column}` : ''}
              </span>
            </div>
            <div style={{
              fontSize: 11,
              color: 'var(--tx)',
              fontFamily: 'JetBrains Mono, monospace',
              lineHeight: 1.5,
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
            }}>
              {result.preview}
            </div>
          </div>
        );
      })}
    </div>
  );
}

function TreeContextMenu({ x, y, dirPath, onClose, projectPath }: { x: number; y: number; dirPath: string; onClose: () => void; projectPath: string }) {
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(event.target as Node)) {
        onClose();
      }
    };
    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, [onClose]);

  const createNewFile = () => {
    const name = prompt('New file name:');
    if (name) {
      const path = dirPath.endsWith('/') ? dirPath + name : dirPath + '/' + name;
      wsClient.send({ type: 'file.create', path });
    }
    onClose();
  };

  const createNewFolder = () => {
    const name = prompt('New folder name:');
    if (name) {
      const path = dirPath.endsWith('/') ? dirPath + name : dirPath + '/' + name;
      wsClient.send({ type: 'folder.create', path });
    }
    onClose();
  };

  return (
    <div
      ref={menuRef}
      className="tree-context-menu"
      style={{
        position: 'fixed',
        left: `${x}px`,
        top: `${y}px`,
        background: 'var(--surface)',
        border: '1px solid var(--border)',
        borderRadius: '4px',
        boxShadow: '0 2px 8px rgba(0,0,0,0.15)',
        zIndex: 10000,
        minWidth: '180px',
        overflow: 'hidden',
      }}
    >
      <button
        onClick={createNewFile}
        style={{
          display: 'block',
          width: '100%',
          padding: '8px 12px',
          textAlign: 'left',
          background: 'none',
          border: 'none',
          cursor: 'pointer',
          fontSize: '12px',
          color: 'var(--tx)',
          borderBottom: '1px solid var(--border)',
        }}
        onMouseEnter={(e) => e.currentTarget.style.background = 'var(--accent-light)'}
        onMouseLeave={(e) => e.currentTarget.style.background = 'none'}
      >
        New File
      </button>
      <button
        onClick={createNewFolder}
        style={{
          display: 'block',
          width: '100%',
          padding: '8px 12px',
          textAlign: 'left',
          background: 'none',
          border: 'none',
          cursor: 'pointer',
          fontSize: '12px',
          color: 'var(--tx)',
        }}
        onMouseEnter={(e) => e.currentTarget.style.background = 'var(--accent-light)'}
        onMouseLeave={(e) => e.currentTarget.style.background = 'none'}
      >
        New Folder
      </button>
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
