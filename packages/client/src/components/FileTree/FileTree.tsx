import { useEffect, useState } from 'react';
import { useStore } from '../../store/index.js';
import { wsClient } from '../../ws/client.js';
import type { FileNode } from '@myeditor/shared';
import { ChevronRight, ChevronDown, File, Folder, FolderOpen, RefreshCw, GitBranch, Plus, Minus, Circle } from 'lucide-react';

function TreeNode({ node, depth = 0 }: { node: FileNode; depth?: number }) {
  const [expanded, setExpanded] = useState(depth < 1);

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
        className="flex items-center gap-1 py-0.5 text-xs cursor-pointer hover:bg-editor-hover rounded mx-1 truncate"
        style={{ paddingLeft: `${8 + depth * 12}px` }}
      >
        {node.type === 'directory' ? (
          <>
            <span className="text-gray-600 shrink-0">
              {expanded ? <ChevronDown size={10} /> : <ChevronRight size={10} />}
            </span>
            <span className="text-yellow-500/80 shrink-0">
              {expanded ? <FolderOpen size={12} /> : <Folder size={12} />}
            </span>
          </>
        ) : (
          <>
            <span className="w-[10px] shrink-0" />
            <span className="text-blue-400/70 shrink-0"><File size={12} /></span>
          </>
        )}
        <span className="truncate ml-1 text-gray-300">{node.name}</span>
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
  }, [activeSession]);

  if (!gitStatus) {
    return <div className="px-3 py-4 text-xs text-gray-600 text-center">No git repository</div>;
  }

  const sections = [
    { label: 'Staged', files: gitStatus.staged, icon: <Plus size={10} className="text-green-400" /> },
    { label: 'Changes', files: gitStatus.unstaged, icon: <Minus size={10} className="text-yellow-400" /> },
    { label: 'Untracked', files: gitStatus.untracked, icon: <Circle size={10} className="text-gray-500" /> },
  ].filter(s => s.files.length > 0);

  return (
    <div className="overflow-y-auto">
      {sections.length === 0 ? (
        <div className="px-3 py-4 text-xs text-gray-600 text-center">No changes</div>
      ) : (
        sections.map(section => (
          <div key={section.label} className="mb-3">
            <div className="px-3 py-1 text-xs text-gray-500 font-medium uppercase tracking-wide flex items-center gap-1">
              {section.icon} {section.label} ({section.files.length})
            </div>
            {section.files.map(f => (
              <div
                key={f}
                className="flex items-center gap-2 px-3 py-0.5 text-xs text-gray-300 hover:bg-editor-hover cursor-pointer truncate"
                onClick={() => wsClient.send({ type: 'file.read', path: `${useStore.getState().activeSession?.projectPath}/${f}` })}
              >
                {section.icon}
                <span className="truncate font-mono">{f}</span>
              </div>
            ))}
          </div>
        ))
      )}
    </div>
  );
}

export default function FileTree({ activeTab }: { activeTab: 'explorer' | 'git' | 'settings' }) {
  const { fileTree, activeSession } = useStore();

  useEffect(() => {
    if (activeSession) {
      wsClient.send({ type: 'file.tree', path: activeSession.projectPath });
      wsClient.send({ type: 'git.status' });
    }
  }, [activeSession]);

  const refresh = () => {
    if (activeSession) wsClient.send({ type: 'file.tree', path: activeSession.projectPath });
  };

  const projectName = activeSession?.projectPath.split('/').pop() ?? 'EXPLORER';

  return (
    <div className="flex flex-col h-full bg-editor-surface">
      <div className="flex items-center justify-between px-2 py-1.5 border-b border-editor-border shrink-0">
        <span className="text-xs text-gray-400 font-semibold uppercase tracking-wide truncate">
          {activeTab === 'explorer' ? projectName : activeTab === 'git' ? 'Source Control' : 'Settings'}
        </span>
        {activeTab === 'explorer' && (
          <button onClick={refresh} className="text-gray-600 hover:text-gray-300 shrink-0">
            <RefreshCw size={11} />
          </button>
        )}
      </div>

      <div className="flex-1 overflow-y-auto py-1">
        {activeTab === 'explorer' ? (
          fileTree
            ? fileTree.children?.map(node => <TreeNode key={node.path} node={node} />)
            : <div className="px-3 py-2 text-xs text-gray-600">No project loaded</div>
        ) : activeTab === 'git' ? (
          <GitPanel />
        ) : (
          <div className="px-3 py-2 text-xs text-gray-600">Settings coming soon</div>
        )}
      </div>
    </div>
  );
}
