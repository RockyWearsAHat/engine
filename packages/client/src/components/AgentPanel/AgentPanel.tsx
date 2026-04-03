import { useStore, type ToolCallDisplay } from '../../store/index.js';
import { wsClient } from '../../ws/client.js';
import {
  Activity, Plus, Circle, CheckCircle2, XCircle,
  Loader2, FileText, Terminal, Search, GitBranch,
  FolderOpen, Cpu,
} from 'lucide-react';
import type { AgentSession } from '@myeditor/shared';

function ToolIcon({ name }: { name: string }) {
  if (name === 'read_file' || name === 'write_file' || name === 'list_directory') return <FileText size={10} />;
  if (name === 'shell' || name === 'git_commit') return <Terminal size={10} />;
  if (name === 'search_files') return <Search size={10} />;
  if (name.startsWith('git')) return <GitBranch size={10} />;
  if (name === 'open_file') return <FolderOpen size={10} />;
  return <Cpu size={10} />;
}

function ToolCallRow({ tc }: { tc: ToolCallDisplay }) {
  const shortInput = typeof tc.input === 'object' && tc.input !== null
    ? (Object.values(tc.input as Record<string, unknown>)[0]?.toString() ?? '').slice(0, 35)
    : '';

  return (
    <div className={`flex items-center gap-1.5 px-2 py-0.5 text-xs font-mono rounded mx-1 ${
      tc.pending ? 'bg-yellow-950/30 text-yellow-300' :
      tc.isError ? 'bg-red-950/30 text-red-300' :
      'bg-green-950/20 text-green-300'
    }`}>
      <span className="shrink-0">
        {tc.pending
          ? <Loader2 size={9} className="animate-spin" />
          : tc.isError
          ? <XCircle size={9} />
          : <CheckCircle2 size={9} />}
      </span>
      <span className="text-gray-400 shrink-0"><ToolIcon name={tc.name} /></span>
      <span className="text-gray-300 shrink-0">{tc.name}</span>
      {shortInput && <span className="text-gray-500 truncate">{shortInput}</span>}
      {tc.durationMs !== undefined && !tc.pending && (
        <span className="ml-auto text-gray-600 shrink-0">{tc.durationMs}ms</span>
      )}
    </div>
  );
}

function SessionCard({ session, isSelected, onClick }: {
  session: AgentSession;
  isSelected: boolean;
  onClick: () => void;
}) {
  const projectName = session.projectPath.split('/').pop() ?? session.projectPath;

  return (
    <div
      onClick={onClick}
      className={`mx-2 mb-2 rounded-lg border cursor-pointer transition-colors ${
        isSelected
          ? 'border-blue-500/50 bg-blue-950/20'
          : 'border-editor-border hover:border-gray-600 bg-editor-surface/50'
      }`}
    >
      {/* Session header */}
      <div className="flex items-center gap-2 px-3 py-2">
        <span className={`shrink-0 ${session.isStreaming ? 'text-blue-400' : 'text-gray-600'}`}>
          {session.isStreaming
            ? <Loader2 size={12} className="animate-spin" />
            : <Circle size={12} fill={session.isActive ? '#4ade80' : 'transparent'} />}
        </span>
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-1.5">
            <span className="text-xs font-medium text-gray-200 truncate">{projectName}</span>
            <span className="text-xs text-gray-600 font-mono shrink-0">{session.branchName}</span>
          </div>
          {session.isStreaming && session.currentActivity && (
            <div className="text-xs text-blue-400 truncate mt-0.5">{session.currentActivity}</div>
          )}
          {!session.isStreaming && (
            <div className="text-xs text-gray-600 truncate mt-0.5">
              {session.messageCount} messages · {new Date(session.updatedAt).toLocaleTimeString()}
            </div>
          )}
        </div>
      </div>

      {/* Recent tool calls */}
      {session.recentToolCalls.length > 0 && (
        <div className="pb-2 space-y-0.5">
          {session.recentToolCalls.slice(-5).map((tc, i) => (
            <ToolCallRow key={i} tc={tc as ToolCallDisplay} />
          ))}
        </div>
      )}
    </div>
  );
}

export default function AgentPanel() {
  const { agentSessions, activeAgentSessionId, setActiveAgentSession, activeSession, sessions } = useStore();

  const newSession = () => {
    if (activeSession) {
      wsClient.send({ type: 'session.create', projectPath: activeSession.projectPath });
    }
  };

  return (
    <div className="flex flex-col h-full bg-editor-surface border-l border-editor-border">
      {/* Header */}
      <div className="flex items-center justify-between px-3 py-2 border-b border-editor-border shrink-0">
        <div className="flex items-center gap-1.5">
          <Activity size={13} className="text-blue-400" />
          <span className="text-xs font-semibold text-gray-300 uppercase tracking-wide">Agents</span>
        </div>
        <button
          onClick={newSession}
          className="text-gray-500 hover:text-gray-300 p-0.5 rounded hover:bg-editor-hover"
          title="New session"
        >
          <Plus size={14} />
        </button>
      </div>

      {/* Session list */}
      <div className="flex-1 overflow-y-auto py-2">
        {sessions.length === 0 ? (
          <div className="px-3 py-4 text-center text-xs text-gray-600">
            <Activity size={24} className="mx-auto mb-2 opacity-30" />
            <div>No active agents</div>
            <div className="mt-1 text-gray-700">Start a chat to spawn an agent</div>
          </div>
        ) : (
          sessions.map(s => {
            const liveData = agentSessions.find(a => a.id === s.id);
            const session: AgentSession = {
              ...s,
              isActive: s.id === activeSession?.id,
              isStreaming: liveData?.isStreaming ?? false,
              currentActivity: liveData?.currentActivity ?? '',
              recentToolCalls: liveData?.recentToolCalls ?? [],
            };
            return (
              <SessionCard
                key={s.id}
                session={session}
                isSelected={s.id === activeAgentSessionId}
                onClick={() => {
                  setActiveAgentSession(s.id);
                  wsClient.send({ type: 'session.load', sessionId: s.id });
                }}
              />
            );
          })
        )}
      </div>

      {/* Footer: session count */}
      <div className="px-3 py-1.5 border-t border-editor-border shrink-0">
        <span className="text-xs text-gray-600">
          {sessions.length} session{sessions.length !== 1 ? 's' : ''}
        </span>
      </div>
    </div>
  );
}
