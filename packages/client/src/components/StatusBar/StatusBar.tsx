import { useStore } from '../../store/index.js';
import { GitBranch, Circle, Wifi, WifiOff, Github } from 'lucide-react';

export default function StatusBar() {
  const { gitStatus, activeFilePath, openFiles, connected, githubUser } = useStore();
  const activeFile = openFiles.find(f => f.path === activeFilePath);

  const staged = gitStatus?.staged.length ?? 0;
  const unstaged = gitStatus?.unstaged.length ?? 0;
  const untracked = gitStatus?.untracked.length ?? 0;
  const total = staged + unstaged + untracked;

  return (
    <div className="flex items-center justify-between px-3 py-0.5 bg-[#1a1a2e] border-t border-editor-border text-xs text-gray-400 shrink-0">
      <div className="flex items-center gap-3">
        <span className={`flex items-center gap-1 ${connected ? 'text-green-400' : 'text-red-400'}`}>
          {connected ? <Wifi size={11} /> : <WifiOff size={11} />}
          {connected ? 'connected' : 'reconnecting'}
        </span>
        {gitStatus && (
          <span className="flex items-center gap-1">
            <GitBranch size={11} className="text-blue-400" />
            <span className="text-gray-300">{gitStatus.branch}</span>
            {total > 0 && (
              <span className="text-yellow-400 ml-1">
                {staged > 0 ? `+${staged}` : ''}{unstaged > 0 ? ` ~${unstaged}` : ''}{untracked > 0 ? ` ?${untracked}` : ''}
              </span>
            )}
            {gitStatus.ahead > 0 && <span className="text-blue-400">↑{gitStatus.ahead}</span>}
            {gitStatus.behind > 0 && <span className="text-orange-400">↓{gitStatus.behind}</span>}
          </span>
        )}
        {githubUser && (
          <span className="flex items-center gap-1 text-gray-500">
            <Github size={11} />
            <span>{githubUser.login}</span>
          </span>
        )}
      </div>
      <div className="flex items-center gap-3">
        {activeFile && (
          <>
            <span className="text-gray-500">{activeFile.language}</span>
            {activeFile.dirty && <span className="text-blue-400 flex items-center gap-0.5"><Circle size={6} fill="currentColor" /> unsaved</span>}
          </>
        )}
        <span className="text-gray-700">MyEditor v0.1</span>
      </div>
    </div>
  );
}
