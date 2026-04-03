import { useStore } from '../../store/index.js';
import { GitBranch, Wifi, WifiOff } from 'lucide-react';

export default function StatusBar() {
  const { gitStatus, activeFilePath, openFiles, connected, githubUser } = useStore();
  const activeFile = openFiles.find(f => f.path === activeFilePath);

  const staged = gitStatus?.staged.length ?? 0;
  const unstaged = gitStatus?.unstaged.length ?? 0;
  const untracked = gitStatus?.untracked.length ?? 0;

  return (
    <div
      className="shrink-0 flex items-center justify-between"
      style={{
        height: 20,
        padding: '0 10px',
        background: '#090910',
        borderTop: '1px solid rgba(255,255,255,0.04)',
        fontSize: 10,
        color: '#383840',
        userSelect: 'none',
      }}
    >
      <div className="flex items-center gap-3">
        <span
          className="flex items-center gap-1"
          style={{ color: connected ? '#3dd68c' : '#f06e6e' }}
        >
          {connected ? <Wifi size={9} /> : <WifiOff size={9} />}
          {connected ? 'connected' : 'reconnecting'}
        </span>

        {gitStatus && (
          <span className="flex items-center gap-1.5" style={{ color: '#55555f' }}>
            <GitBranch size={9} style={{ color: '#5b8def' }} />
            <span style={{ color: '#9898a6' }}>{gitStatus.branch}</span>
            {staged > 0 && <span style={{ color: '#3dd68c' }}>+{staged}</span>}
            {unstaged > 0 && <span style={{ color: '#e5b80b' }}>~{unstaged}</span>}
            {untracked > 0 && <span style={{ color: '#55555f' }}>?{untracked}</span>}
            {gitStatus.ahead > 0 && <span style={{ color: '#5b8def' }}>↑{gitStatus.ahead}</span>}
            {gitStatus.behind > 0 && <span style={{ color: '#e8855f' }}>↓{gitStatus.behind}</span>}
          </span>
        )}

        {githubUser && (
          <span style={{ color: '#383840' }}>{githubUser.login}</span>
        )}
      </div>

      <div className="flex items-center gap-3">
        {activeFile && (
          <>
            <span>{activeFile.language}</span>
            {activeFile.dirty && <span style={{ color: '#5b8def' }}>● unsaved</span>}
          </>
        )}
        <span>MyEditor</span>
      </div>
    </div>
  );
}

