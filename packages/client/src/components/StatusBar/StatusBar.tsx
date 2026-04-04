import { useStore } from '../../store/index.js';
import { GitBranch, Wifi, WifiOff, Github, Circle } from 'lucide-react';

export default function StatusBar() {
  const { gitStatus, activeFilePath, openFiles, connected, githubUser } = useStore();
  const activeFile = openFiles.find(f => f.path === activeFilePath);
  const staged = gitStatus?.staged.length ?? 0;
  const unstaged = gitStatus?.unstaged.length ?? 0;
  const untracked = gitStatus?.untracked.length ?? 0;

  return (
    <div
      style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        padding: '0 12px', flexShrink: 0,
        background: 'var(--surface)',
        borderTop: '1px solid var(--border)',
        height: 22,
        fontSize: '11px',
        color: 'var(--tx-3)',
        fontFamily: 'Outfit, sans-serif',
        userSelect: 'none',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        <span style={{
          display: 'flex', alignItems: 'center', gap: 6,
          color: connected ? '#22c55e' : '#ef4444',
        }}>
          {connected ? <Wifi size={10} /> : <WifiOff size={10} />}
          <span>{connected ? 'connected' : 'offline'}</span>
        </span>

        {gitStatus && (
          <span style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <GitBranch size={10} style={{ color: 'var(--accent)' }} />
            <span style={{ color: 'var(--tx-2)' }}>{gitStatus.branch}</span>
            {staged > 0 && <span style={{ color: '#22c55e' }}>+{staged}</span>}
            {unstaged > 0 && <span style={{ color: '#f59e0b' }}>~{unstaged}</span>}
            {untracked > 0 && <span style={{ color: 'var(--tx-3)' }}>?{untracked}</span>}
            {gitStatus.ahead > 0 && <span style={{ color: 'var(--accent)' }}>{'\u2191'}{gitStatus.ahead}</span>}
            {gitStatus.behind > 0 && <span style={{ color: '#f97316' }}>{'\u2193'}{gitStatus.behind}</span>}
          </span>
        )}

        {githubUser && (
          <span style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <Github size={10} />
            <span>{githubUser.login}</span>
          </span>
        )}
      </div>

      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        {activeFile && (
          <>
            <span>{activeFile.language}</span>
            {activeFile.dirty && (
              <span style={{ display: 'flex', alignItems: 'center', gap: 4, color: 'var(--accent)' }}>
                <Circle size={5} fill="currentColor" />
                <span>unsaved</span>
              </span>
            )}
          </>
        )}
        <span>Engine</span>
      </div>
    </div>
  );
}
