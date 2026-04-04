import { useStore, type ToolCallDisplay } from '../../store/index.js';
import { wsClient } from '../../ws/client.js';
import { Activity, Plus, CheckCircle2, XCircle, Loader2 } from 'lucide-react';
import type { Session } from '@engine/shared';

function ToolRow({ tc }: { tc: ToolCallDisplay }) {
  const shortInput = typeof tc.input === 'object' && tc.input !== null
    ? (Object.values(tc.input as Record<string, unknown>)[0]?.toString() ?? '').slice(0, 30)
    : '';

  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 8,
      padding: '3px 12px', fontSize: '10px', fontFamily: 'monospace',
    }}>
      <span style={{ flexShrink: 0, display: 'flex', alignItems: 'center' }}>
        {tc.pending
          ? <Loader2 size={9} className="animate-spin" style={{ color: '#f59e0b' }} />
          : tc.isError
          ? <XCircle size={9} style={{ color: '#ef4444' }} />
          : <CheckCircle2 size={9} style={{ color: '#22c55e' }} />}
      </span>
      <span style={{ color: 'var(--tx-3)' }}>{tc.name}</span>
      {shortInput && <span style={{ color: 'var(--tx-3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', opacity: 0.6 }}>{shortInput}</span>}
      {tc.durationMs != null && !tc.pending && (
        <span style={{ marginLeft: 'auto', color: 'var(--tx-3)', flexShrink: 0, opacity: 0.5 }}>{tc.durationMs}ms</span>
      )}
    </div>
  );
}

function SessionCard({
  session,
  isActive,
  isSelected,
  onClick,
}: {
  session: Session;
  isActive: boolean;
  isSelected: boolean;
  onClick: () => void;
}) {
  const { agentSessions } = useStore();
  const agentData = agentSessions.find(a => a.id === session.id);
  const isStreaming = agentData?.isStreaming ?? false;
  const toolCalls = agentData?.recentToolCalls ?? [];
  const projectName = session.projectPath.split('/').pop() ?? session.projectPath;

  return (
    <div
      onClick={onClick}
      style={{
        margin: '0 8px 6px',
        borderRadius: 8,
        border: isSelected ? '1px solid rgba(77,127,255,0.3)' : '1px solid var(--border)',
        background: isSelected ? 'rgba(77,127,255,0.05)' : 'rgba(255,255,255,0.01)',
        cursor: 'pointer',
        overflow: 'hidden',
        transition: 'border-color 150ms, background 150ms',
      }}
      onMouseEnter={e => {
        if (!isSelected) (e.currentTarget as HTMLDivElement).style.borderColor = 'var(--border-2)';
      }}
      onMouseLeave={e => {
        if (!isSelected) (e.currentTarget as HTMLDivElement).style.borderColor = 'var(--border)';
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '8px 10px' }}>
        <span style={{ flexShrink: 0 }}>
          {isStreaming ? (
            <span className="pulse-dot" style={{ display: 'block', width: 8, height: 8, background: 'var(--accent)', borderRadius: '50%' }} />
          ) : (
            <span style={{
              display: 'block', width: 8, height: 8, borderRadius: '50%',
              background: isActive ? '#22c55e' : 'rgba(255,255,255,0.15)',
            }} />
          )}
        </span>

        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <span style={{ fontSize: '12px', fontWeight: 500, color: 'var(--tx)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {projectName}
            </span>
            <span style={{ fontSize: '10px', color: 'var(--tx-3)', fontFamily: 'monospace', flexShrink: 0 }}>
              {session.branchName}
            </span>
          </div>
          <div style={{ fontSize: '10px', color: isStreaming && agentData?.currentActivity ? 'var(--accent)' : 'var(--tx-3)', marginTop: 2, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {isStreaming && agentData?.currentActivity
              ? agentData.currentActivity
              : `${session.messageCount} msg${session.messageCount === 1 ? '' : 's'} · ${new Date(session.updatedAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`}
          </div>
        </div>
      </div>

      {toolCalls.length > 0 && (
        <div style={{ borderTop: '1px solid var(--border)', paddingTop: 4, paddingBottom: 4 }}>
          {toolCalls.slice(-4).map(tc => (
            <ToolRow key={tc.id} tc={tc as ToolCallDisplay} />
          ))}
        </div>
      )}
    </div>
  );
}

export default function AgentPanel() {
  const { sessions, activeSession, activeAgentSessionId, setActiveAgentSession } = useStore();

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', background: 'var(--surface)', borderLeft: '1px solid var(--border)' }}>
      {/* Header */}
      <div style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        padding: '0 12px', height: 36, flexShrink: 0,
        borderBottom: '1px solid var(--border)',
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <Activity size={12} style={{ color: 'var(--accent)' }} />
          <span style={{ fontSize: '10px', fontWeight: 700, color: 'var(--tx-3)', letterSpacing: '0.06em', textTransform: 'uppercase' }}>
            Agents
          </span>
        </div>
        <button
          onClick={() => {
            if (activeSession) wsClient.send({ type: 'session.create', projectPath: activeSession.projectPath });
          }}
          disabled={!activeSession}
          title="New session"
          style={{
            background: 'none', border: 'none', cursor: 'pointer',
            color: 'var(--tx-3)', padding: 2, borderRadius: 4,
            transition: 'color 150ms, background 150ms',
            opacity: !activeSession ? 0.3 : 1,
          }}
          onMouseEnter={e => {
            (e.currentTarget.style.color = 'var(--tx-2)');
            (e.currentTarget.style.background = 'var(--surface-3)');
          }}
          onMouseLeave={e => {
            (e.currentTarget.style.color = 'var(--tx-3)');
            (e.currentTarget.style.background = 'transparent');
          }}
        >
          <Plus size={13} />
        </button>
      </div>

      {/* Sessions */}
      <div style={{ flex: 1, overflowY: 'auto', paddingTop: 8 }}>
        {sessions.length === 0 ? (
          <div style={{
            display: 'flex', flexDirection: 'column', alignItems: 'center',
            justifyContent: 'center', height: 96, gap: 8, padding: '0 12px',
          }}>
            <Activity size={18} style={{ color: 'var(--tx-3)', opacity: 0.3 }} />
            <span style={{ fontSize: '10px', color: 'var(--tx-3)', textAlign: 'center' }}>No active agents</span>
          </div>
        ) : (
          <div className="fade-in">
            {sessions.map(s => (
              <SessionCard
                key={s.id}
                session={s}
                isActive={s.id === activeSession?.id}
                isSelected={s.id === (activeAgentSessionId ?? activeSession?.id)}
                onClick={() => {
                  setActiveAgentSession(s.id);
                  wsClient.send({ type: 'session.load', sessionId: s.id });
                }}
              />
            ))}
          </div>
        )}
      </div>

      {/* Footer */}
      <div style={{
        padding: '0 12px', height: 24, flexShrink: 0,
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        borderTop: '1px solid var(--border)',
      }}>
        <span style={{ fontSize: '10px', color: 'var(--tx-3)' }}>
          {sessions.length} session{sessions.length !== 1 ? 's' : ''}
        </span>
        {sessions.some(s => {
          const { agentSessions } = useStore.getState();
          return agentSessions.find(a => a.id === s.id)?.isStreaming;
        }) && (
          <span style={{ fontSize: '10px', color: 'var(--accent)', display: 'flex', alignItems: 'center', gap: 4 }}>
            <span className="pulse-live" style={{ display: 'inline-block', width: 6, height: 6, borderRadius: '50%', background: 'var(--accent)' }} />
            active
          </span>
        )}
      </div>
    </div>
  );
}
