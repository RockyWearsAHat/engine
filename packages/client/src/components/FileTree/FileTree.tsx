import { useEffect, useState } from 'react';
import { useStore } from '../../store/index.js';
import { wsClient } from '../../ws/client.js';
import type { FileNode } from '@myeditor/shared';
import { ChevronRight, ChevronDown, File, Folder, FolderOpen, RefreshCw } from 'lucide-react';

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
        className="flex items-center gap-1 px-2 py-0.5 text-xs cursor-pointer hover:bg-editor-hover rounded mx-1 truncate"
        style={{ paddingLeft: `${8 + depth * 12}px` }}
      >
        {node.type === 'directory' ? (
          <>
            <span className="text-gray-500 shrink-0">
              {expanded ? <ChevronDown size={10} /> : <ChevronRight size={10} />}
            </span>
            <span className="text-yellow-500 shrink-0">
              {expanded ? <FolderOpen size={12} /> : <Folder size={12} />}
            </span>
          </>
        ) : (
          <>
            <span className="w-[10px] shrink-0" />
            <span className="text-blue-400 shrink-0"><File size={12} /></span>
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

export default function FileTree() {
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

  return (
    <div className="flex flex-col h-full bg-editor-surface">
      <div className="flex items-center justify-between px-3 py-1.5 border-b border-editor-border shrink-0">
        <span className="text-xs text-gray-500 font-medium uppercase tracking-wide">Explorer</span>
        <button onClick={refresh} className="text-gray-600 hover:text-gray-300">
          <RefreshCw size={12} />
        </button>
      </div>
      <div className="flex-1 overflow-y-auto py-1">
        {fileTree ? (
          fileTree.children?.map(node => <TreeNode key={node.path} node={node} />)
        ) : (
          <div className="px-3 py-2 text-xs text-gray-600">No project loaded</div>
        )}
      </div>
    </div>
  );
}
