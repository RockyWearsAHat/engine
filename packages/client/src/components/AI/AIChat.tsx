import { useRef, useEffect, useState, type KeyboardEvent } from 'react';
import { useStore, type ChatMessage, type ToolCallDisplay } from '../../store/index.js';
import { wsClient } from '../../ws/client.js';
import { randomUUID } from '../../utils.js';
import { ArrowUp, ChevronDown, ChevronRight, Loader2 } from 'lucide-react';

function ToolBadge({ tc }: { tc: ToolCallDisplay }) {
  const [open, setOpen] = useState(false);

  const pending = tc.pending;
  const isErr = tc.isError;
  const borderColor = pending ? 'rgba(245,158,11,0.4)' : isErr ? 'rgba(239,68,68,0.4)' : 'rgba(34,197,94,0.3)';
  const bgColor = pending ? 'rgba(245,158,11,0.08)' : isErr ? 'rgba(239,68,68,0.08)' : 'rgba(34,197,94,0.06)';
  const textColor = pending ? '#f59e0b' : isErr ? '#ef4444' : '#22c55e';

  const shortInput = typeof tc.input === 'object' && tc.input !== null
    ? Object.values(tc.input as Record<string, unknown>)[0]?.toString().slice(0, 50) ?? ''
    : '';

  return (
    <div style={{
      marginTop: 6, borderRadius: 4,
      border: `1px solid ${borderColor}`,
      background: bgColor,
      fontFamily: 'monospace',
      fontSize: '10px',
    }}>
      <button
        onClick={() => setOpen(o => !o)}
        style={{
          display: 'flex', alignItems: 'center', gap: 8,
          padding: '6px 10px', width: '100%', textAlign: 'left',
          background: 'none', border: 'none', cursor: 'pointer',
          color: textColor,
        }}
      >
        <span style={{ flexShrink: 0, display: 'flex', alignItems: 'center' }}>
          {tc.pending
            ? <Loader2 size={9} className="animate-spin" />
            : tc.isError ? '\u2717' : '\u2713'}
        </span>
        <span style={{ color: 'var(--tx-2)', fontWeight: 500 }}>{tc.name}</span>
        {shortInput && <span style={{ color: 'var(--tx-3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flex: 1 }}>{shortInput}</span>}
        {tc.durationMs != null && !tc.pending && (
          <span style={{ color: 'var(--tx-3)', flexShrink: 0, marginLeft: 'auto' }}>{tc.durationMs}ms</span>
        )}
        <span style={{ flexShrink: 0, color: 'var(--tx-3)', display: 'flex', alignItems: 'center' }}>
          {open ? <ChevronDown size={8} /> : <ChevronRight size={8} />}
        </span>
      </button>
      {open && (
        <div style={{ borderTop: `1px solid ${borderColor}`, padding: '8px 10px' }}>
          <pre style={{ color: 'var(--tx-3)', fontSize: '10px', whiteSpace: 'pre-wrap', wordBreak: 'break-all', margin: 0 }}>
            {JSON.stringify(tc.input, null, 2)}
          </pre>
          {tc.result != null && (
            <pre style={{
              color: isErr ? '#fca5a5' : 'var(--tx-2)',
              fontSize: '10px', whiteSpace: 'pre-wrap', wordBreak: 'break-all',
              maxHeight: 112, overflowY: 'auto', marginTop: 6, margin: 0,
            }}>
              {String(tc.result).slice(0, 1500)}
            </pre>
          )}
        </div>
      )}
    </div>
  );
}

function Message({ msg }: { msg: ChatMessage }) {
  if (msg.role === 'user') {
    return (
      <div className="fade-in" style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 16 }}>
        <div style={{
          maxWidth: '80%', padding: '8px 12px', borderRadius: 12,
          fontSize: '12px', color: 'var(--tx)',
          background: 'rgba(77,127,255,0.12)',
          border: '1px solid rgba(77,127,255,0.2)',
          lineHeight: 1.6,
        }}>
          {msg.content}
        </div>
      </div>
    );
  }

  return (
    <div className="fade-in" style={{ marginBottom: 16 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
        <span style={{
          width: 16, height: 16, borderRadius: 4, flexShrink: 0,
          background: 'var(--accent-dim)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
        }}>
          <span style={{ color: 'var(--accent)', fontSize: '9px', fontWeight: 700 }}>A</span>
        </span>
        <span style={{ fontSize: '10px', color: 'var(--tx-3)', fontWeight: 500 }}>AI</span>
        {msg.streaming && (
          <span style={{ display: 'flex', alignItems: 'center', gap: 4, fontSize: '10px', color: 'var(--accent)' }}>
            <span className="pulse-dot" style={{ width: 4, height: 4, background: 'var(--accent)', borderRadius: '50%', display: 'inline-block' }} />
            thinking
          </span>
        )}
      </div>
      {msg.content && (
        <div style={{ fontSize: '12px', color: 'var(--tx)', lineHeight: 1.6, paddingLeft: 24, whiteSpace: 'pre-wrap' }}>
          {msg.content}
          {msg.streaming && !msg.toolCalls.some((t: ToolCallDisplay) => t.pending) && (
            <span className="cursor-blink" style={{
              display: 'inline-block', width: 2, height: 14,
              background: 'var(--accent)', marginLeft: 2, verticalAlign: 'middle',
            }} />
          )}
        </div>
      )}
      {msg.toolCalls.length > 0 && (
        <div style={{ paddingLeft: 24, marginTop: 4 }}>
          {msg.toolCalls.map((tc: ToolCallDisplay, i: number) => <ToolBadge key={i} tc={tc} />)}
        </div>
      )}
    </div>
  );
}

export default function AIChat() {
  const { chatMessages, activeSession, connected } = useStore();
  const [input, setInput] = useState('');
  const [sending, setSending] = useState(false);
  const bottomRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [chatMessages.length, chatMessages[chatMessages.length - 1]?.content?.length]);

  useEffect(() => {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = `${Math.min(el.scrollHeight, 120)}px`;
  }, [input]);

  const send = () => {
    const content = input.trim();
    if (!content || !activeSession || sending) return;
    const id = randomUUID();
    useStore.getState().addUserMessage(id, content);
    wsClient.send({ type: 'chat', sessionId: activeSession.id, content });
    setInput('');
    setSending(true);
    const off = wsClient.onMessage(msg => {
      if ((msg.type === 'chat.chunk' && msg.done) || msg.type === 'chat.error') {
        setSending(false);
        off();
      }
    });
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') { e.preventDefault(); send(); }
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', background: 'var(--bg)' }}>
      <div style={{ flex: 1, overflowY: 'auto', padding: '12px 16px' }}>
        {chatMessages.length === 0 ? (
          <div className="fade-in" style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100%' }}>
            <p style={{ fontSize: '11px', color: 'var(--tx-3)', textAlign: 'center', lineHeight: 1.6 }}>
              {connected
                ? activeSession
                  ? 'Ask anything \u2014 the AI has full access to your project'
                  : 'Opening project...'
                : 'Connecting...'}
            </p>
          </div>
        ) : (
          chatMessages.map(msg => <Message key={msg.id} msg={msg} />)
        )}
        <div ref={bottomRef} />
      </div>

      <div style={{ flexShrink: 0, padding: '0 12px 12px' }}>
        <div style={{
          display: 'flex', alignItems: 'flex-end', gap: 8,
          borderRadius: 8, padding: '8px 12px',
          background: 'var(--surface)',
          border: '1px solid var(--border-2)',
        }}>
          <textarea
            ref={textareaRef}
            value={input}
            onChange={e => setInput(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder={activeSession ? 'Ask the AI...' : 'Opening project...'}
            disabled={!activeSession || sending}
            rows={1}
            style={{
              flex: 1, background: 'transparent',
              fontSize: '12px', color: 'var(--tx)',
              border: 'none', outline: 'none', resize: 'none',
              fontFamily: 'Outfit, sans-serif', lineHeight: 1.6,
              minHeight: '20px', maxHeight: '120px',
            }}
          />
          <button
            onClick={send}
            disabled={!input.trim() || !activeSession || sending}
            style={{
              flexShrink: 0, width: 24, height: 24,
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              borderRadius: 6, border: 'none', cursor: 'pointer',
              background: input.trim() && activeSession && !sending ? 'var(--accent)' : 'var(--surface-3)',
              transition: 'background 150ms, opacity 150ms',
              opacity: !input.trim() || !activeSession || sending ? 0.3 : 1,
            }}
          >
            {sending
              ? <Loader2 size={12} className="animate-spin" style={{ color: 'var(--tx-2)' }} />
              : <ArrowUp size={12} style={{ color: input.trim() ? '#fff' : 'var(--tx-3)' }} />}
          </button>
        </div>
        <div style={{
          display: 'flex', justifyContent: 'space-between',
          marginTop: 6, padding: '0 4px',
          fontSize: '10px', color: 'var(--tx-3)',
        }}>
          <span>\u2318\u21a9 send</span>
          {activeSession && <span style={{ fontFamily: 'monospace' }}>{activeSession.branchName}</span>}
        </div>
      </div>
    </div>
  );
}
