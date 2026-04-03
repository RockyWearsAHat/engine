import { useStore, type ToolCallDisplay } from '../../store/index.js';
import { wsClient } from '../../ws/client.js';
import { Activity, Plus, CheckCircle2, XCircle, Loader2, FileText, Terminal, Search, GitBranch, FolderOpen, ChevronDown } from 'lucide-react';
import { useState } from 'react';
import type { AgentSession, Session } from '@myeditor/shared';

const TOOL_ICONS: Record<string, React.ReactNode> = {
  read_file: <FileText size={9} />,
  write_file: <FileText size={9} />,
  list_directory: <FolderOpen size={9} />,
  shell: <Terminal size={9} />,
  search_files: <Search size={9} />,
  git_status: <GitBranch size={9} />,
  git_diff: <GitBranch size={9} />,
  git_commit: <GitBranch size={9} />,
  open_file: <FolderOpen size={9} />,
};

function ToolRow({ tc }: { tc: ToolCallDisplay }) {
  const inputPreview = typeof tc.input === 'object' && tc.input !== null
    ? (Object.values(tc.input as Record<string, unknown>)[0]?.toString() ?? '').slice(0, 28)
    : '';

  return (
    <div
      className="flex items-center gap-1.5 px-3 py-[3px] font-mono"
      style={{ fontSize: 10, color: tc.pending ? '#e5b80b' : tc.isError ? '#f06e6e' : '#3dd68c' }}
    >
      <span className="shrink-0">
        {tc.pending
          ? <Loader2 size={8} className="animate-spin inline" />
          : tc.isError ? <XCircle size={8} className="inline" />
          : <CheckCircle2 size={8} className="inline" />}
      </span>
      <span style={{ color: '#55555f' }}>{TOOL_ICONS[tc.name] ?? <Terminal size={9} />}</span>
      <span style={{ color: '#9898a6' }}>{tc.name}</span>
      {inputPreview && <span style={{ color: '#55555f' }} className="truncate">{inputPreview}</span>}
      {tc.durationMs !== undefined && !tc.pending && (
        <span className="ml-auto shrink-0" style={{ color: '#383840' }}>{tc.durationMs}ms</span>
      )}
    </div>
  );
}

function SessionCard({
  session,
  liveState,
  isSelected,
  onClick,
}: {
  session: Session;
  liveState?: AgentSession;
  isSelected: boolean;
  onClick: () => void;
}) {
  const [expanded, setExpanded] = useState(true);
  const name = session.projectPath.split('/').pop() ?? session.projectPath;
  const isStreaming = liveState?.isStreaming ?? false;
  const toolCalls = liveState?.recentToolCalls ?? [];
  const activity = liveState?.currentActivity ?? '';

  return (
    <div
      style={{
        margin: '4px 8px',
        borderRadius: 8,
        border: isSelected
          ? '1px solid rgba(91,141,239,0.4)'
          : '1px solid rgba(255,255,255,0.07)',
        background: isSelected ? 'rgba(91,141,239,0.06)' : 'rgba(255,255,255,0.02)',
        overflow: 'hidden',
        transition: 'border-color 120ms ease, background 120ms ease',
      }}
    >
      {/* Header */}
      <div
        onClick={onClick}
        className="flex items-center gap-2 cursor-pointer"
        style={{ padding: '7px 10px' }}
      >
        {/* Live indicator */}
        <span
          className={isStreaming ? 'pulse-live' : ''}
          style={{
            width: 6, height: 6, borderRadius: '50%', flexShrink: 0,
            background: isStreaming ? '#5b8def' : isSelected ? '#3dd68c' : 'rgba(255,255,255,0.15)',
          }}
        />

        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span style={{ fontSize: 12, fontWeight: 500, color: '#f0f0f2' }} className="truncate">{name}</span>
            <span style={{ fontSize: 10, color: '#383840', fontFamily: 'monospace' }} className="shrink-0">
              {session.branchName}
            </span>
          </div>
          {isStreaming && activity ? (
            <div style={{ fontSize: 10, color: '#5b8def', marginTop: 1 }} className="truncate">{activity}</div>
          ) : (
            <div style={{ fontSize: 10, color: '#383840', marginTop: 1 }}>
              {session.messageCount} msg · {new Date(session.updatedAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
            </div>
          )}
        </div>

        {toolCalls.length > 0 && (
          <button
            onClick={e => { e.stopPropagation(); setExpanded(v => !v); }}
            style={{ color: '#383840', border: 'none', background: 'none', cursor: 'pointer', padding: 0 }}
          >
            <ChevronDown size={11} style={{ transform: expanded ? 'rotate(0deg)' : 'rotate(-90deg)', transition: 'transform 120ms' }} />
          </button>
        )}
      </div>

      {/* Tool calls */}
      {expanded && toolCalls.length > 0 && (
        <div style={{ borderTop: '1px solid rgba(255,255,255,0.05)', paddingBottom: 4, paddingTop: 2 }}>
          {toolCalls.slice(-6).map((tc, i) => <ToolRow key={i} tc={tc as ToolCallDisplay} />)}
        </div>
      )}
    </div>
  );
}

export default function AgentPanel() {
  const { sessions, activeSession, agentSessions, activeAgentSessionId, setActiveAgentSession } = useStore();

  return (
    <div className="flex flex-col h-full" style={{ background: '#0c0c0e' }}>
      {/* Header */}
      <div
        className="shrink-0 flex items-center justify-between"
        style={{ height: 36, padding: '0 12px', borderBottom: '1px solid rgba(255,255,255,0.06)' }}
      >
        <div className="flex items-center gap-2">
          <Activity size={12} style={{ color: '#5b8def' }} />
          <span style={{ fontSize: 11, fontWeight: 600, color: '#9898a6', letterSpacing: '0.06em', textTransform: 'uppercase' }}>
            Agents
          </span>
        </div>
        <button
          onClick={() => {
            if (activeSession) wsClient.send({ type: 'session.create', projectPath: activeSession.projectPath });
          }}
          title="New session"
          style={{ background: 'none', border: 'none', cursor: 'pointer', color: '#383840', padding: 2, borderRadius: 4 }}
          onMouseEnter={e => (e.currentTarget.style.color = '#9898a6')}
          onMouseLeave={e => (e.currentTarget.style.color = '#383840')}
        >
          <Plus size={13} />
        </button>
      </div>

      {/* Sessions */}
      <div className="flex-1 overflow-y-auto" style={{ paddingTop: 4 }}>
        {sessions.length === 0 ? (
          <div className="flex flex-col items-center justify-center h-full" style={{ color: '#383840', gap: 8, padding: 24 }}>
            <Activity size={28} style={{ opacity: 0.3 }} />
            <span style={{ fontSize: 11, textAlign: 'center', lineHeight: 1.5 }}>
              No active agents.<br />
              <span style={{ color: '#55555f' }}>Start a chat to spawn one.</span>
            </span>
          </div>
        ) : (
          sessions.map(s => (
            <SessionCard
              key={s.id}
              session={s}
              liveState={agentSessions.find(a => a.id === s.id)}
              isSelected={s.id === (activeAgentSessionId ?? activeSession?.id)}
              onClick={() => {
                setActiveAgentSession(s.id);
                wsClient.send({ type: 'session.load', sessionId: s.id });
              }}
            />
          ))
        )}
      </div>

      {/* Footer */}
      <div
        className="shrink-0 flex items-center justify-between"
        style={{ height: 24, padding: '0 12px', borderTop: '1px solid rgba(255,255,255,0.06)' }}
      >
        <span style={{ fontSize: 10, color: '#383840' }}>
          {sessions.length} session{sessions.length !== 1 ? 's' : ''}
        </span>
        {sessions.some(s => agentSessions.find(a => a.id === s.id)?.isStreaming) && (
          <span style={{ fontSize: 10, color: '#5b8def' }} className="flex items-center gap-1">
            <span className="pulse-live inline-block w-1.5 h-1.5 rounded-full" style={{ background: '#5b8def' }} />
            active
          </span>
        )}
      </div>
    </div>
  );
}

