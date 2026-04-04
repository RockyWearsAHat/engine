import { useEffect, useRef, useState, useCallback } from 'react';
import { useStore } from '../../store/index.js';
import { wsClient } from '../../ws/client.js';
import { randomUUID } from '../../utils.js';
import { ArrowUp, ChevronDown, ChevronRight, Loader2, Check, X, Wrench } from 'lucide-react';

export default function AIChat() {
  const { activeSession, chatMessages, addUserMessage, streamingMessageId } = useStore();
  const [input, setInput] = useState('');
  const [expandedTools, setExpandedTools] = useState<Set<string>>(new Set());
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [chatMessages]);

  const send = useCallback(() => {
    const content = input.trim();
    if (!content || !activeSession) return;
    const msgId = randomUUID();
    addUserMessage(msgId, content);
    wsClient.send({ type: 'chat', sessionId: activeSession.id, content });
    setInput('');
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto';
    }
  }, [input, activeSession, addUserMessage]);

  const handleKey = (e: React.KeyboardEvent) => {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault();
      send();
    }
  };

  const adjustHeight = (el: HTMLTextAreaElement) => {
    el.style.height = 'auto';
    el.style.height = Math.min(el.scrollHeight, 120) + 'px';
  };

  const toggleTool = (id: string) => {
    setExpandedTools(prev => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  };

  const noSession = !activeSession;

  return (
    <div className="chat-container">
      {activeSession && (
        <div style={{
          padding: '6px 12px', borderBottom: '1px solid var(--border)',
          fontSize: 11, color: 'var(--tx-3)', display: 'flex', alignItems: 'center', gap: 5,
        }}>
          <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {activeSession.projectPath.split('/').pop()}
          </span>
          {activeSession.branchName && (
            <>
              <span>{'·'}</span>
              <span style={{ color: 'var(--accent-2)', fontWeight: 500 }}>{activeSession.branchName}</span>
            </>
          )}
        </div>
      )}

      <div className="chat-messages">
        {chatMessages.length === 0 && (
          <div className="empty-state" style={{ paddingTop: 32 }}>
            <div style={{
              width: 40, height: 40, borderRadius: '50%',
              background: 'linear-gradient(135deg, var(--accent), var(--purple))',
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              fontSize: 18, fontWeight: 800, color: 'white', marginBottom: 8,
            }}>A</div>
            <span style={{ color: 'var(--tx-2)', fontWeight: 500 }}>
              {noSession ? 'Open a folder to start' : 'How can I help?'}
            </span>
            {!noSession && (
              <span style={{ fontSize: 11, color: 'var(--tx-3)' }}>{'⌘↵ to send'}</span>
            )}
          </div>
        )}

        {chatMessages.map(msg => (
          <div key={msg.id} className={'chat-msg chat-msg-' + msg.role}>
            {msg.role === 'user' ? (
              <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
                <div className="chat-bubble">{msg.content}</div>
              </div>
            ) : (
              <div className="chat-msg-assistant-row">
                <div className="chat-avatar">A</div>
                <div style={{ flex: 1, minWidth: 0 }}>
                  {msg.toolCalls?.map(tc => (
                    <ToolBadge
                      key={tc.id}
                      toolCall={tc}
                      expanded={expandedTools.has(tc.id)}
                      onToggle={() => toggleTool(tc.id)}
                    />
                  ))}
                  {msg.content && (
                    <div className="chat-bubble">
                      <MarkdownText text={msg.content} />
                    </div>
                  )}
                  {msg.id === streamingMessageId && !msg.content && (
                    <div style={{ padding: '6px 0', display: 'flex', gap: 3 }}>
                      {[0, 1, 2].map(i => (
                        <span key={i} style={{
                          width: 5, height: 5, borderRadius: '50%',
                          background: 'var(--accent-2)',
                          animation: 'pulse-dot 1.2s ease-in-out ' + (i * 0.2) + 's infinite',
                          display: 'inline-block',
                        }} />
                      ))}
                    </div>
                  )}
                </div>
              </div>
            )}
          </div>
        ))}
        <div ref={messagesEndRef} />
      </div>

      <div className="chat-input-area">
        <div className="chat-input-wrap">
          <textarea
            ref={textareaRef}
            className="chat-input"
            placeholder={noSession ? 'Open a folder first\u2026' : 'Ask anything\u2026 (\u2318\u21b5 to send)'}
            value={input}
            disabled={noSession}
            onChange={e => {
              setInput(e.target.value);
              adjustHeight(e.target);
            }}
            onKeyDown={handleKey}
            rows={1}
          />
          <button
            className="chat-send-btn"
            onClick={send}
            disabled={!input.trim() || noSession}
            title="Send"
          >
            {streamingMessageId
              ? <Loader2 size={14} className="animate-spin" />
              : <ArrowUp size={14} />}
          </button>
        </div>
      </div>
    </div>
  );
}

function ToolBadge({ toolCall, expanded, onToggle }: {
  toolCall: { id: string; name: string; input: unknown; result?: unknown; isError?: boolean; pending: boolean; durationMs?: number };
  expanded: boolean;
  onToggle: () => void;
}) {
  const state = toolCall.pending ? 'pending' : toolCall.isError ? 'error' : 'success';
  return (
    <div style={{ marginBottom: 4 }}>
      <div className={'tool-badge ' + state} onClick={onToggle} style={{ cursor: 'pointer', userSelect: 'none' }}>
        {state === 'pending' && <Loader2 size={11} className="animate-spin" />}
        {state === 'success' && <Check size={11} />}
        {state === 'error'   && <X size={11} />}
        <Wrench size={10} style={{ opacity: 0.6 }} />
        <span className="tool-badge-name">{toolCall.name}</span>
        {toolCall.durationMs !== undefined && (
          <span style={{ opacity: 0.5, fontSize: 10 }}>{toolCall.durationMs}ms</span>
        )}
        {expanded
          ? <ChevronDown size={10} style={{ marginLeft: 'auto' }} />
          : <ChevronRight size={10} style={{ marginLeft: 'auto' }} />}
      </div>
      {expanded && (
        <div style={{
          background: 'var(--surface-2)', borderRadius: '0 0 var(--radius) var(--radius)',
          border: '1px solid var(--border)', borderTop: 'none',
          padding: '6px 8px', fontSize: 11, fontFamily: 'JetBrains Mono, monospace',
          color: 'var(--tx-2)', overflow: 'auto', maxHeight: 160, lineHeight: 1.5,
        }}>
          <div style={{ opacity: 0.5, fontSize: 10, marginBottom: 3 }}>INPUT</div>
          <pre style={{ margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
            {JSON.stringify(toolCall.input, null, 2)}
          </pre>
          {toolCall.result !== undefined && (
            <>
              <div style={{ opacity: 0.5, fontSize: 10, marginTop: 6, marginBottom: 3 }}>RESULT</div>
              <pre style={{ margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-all',
                            color: toolCall.isError ? 'var(--red)' : 'var(--green)' }}>
                {typeof toolCall.result === 'string' ? toolCall.result
                                                      : JSON.stringify(toolCall.result, null, 2)}
              </pre>
            </>
          )}
        </div>
      )}
    </div>
  );
}

function MarkdownText({ text }: { text: string }) {
  const lines = text.split('\n');
  const elements: React.ReactNode[] = [];
  let codeBlock: string[] = [];
  let inCode = false;

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (line.startsWith('```')) {
      if (!inCode) {
        inCode = true;
        codeBlock = [];
      } else {
        elements.push(
          <pre key={i} style={{
            background: 'var(--surface-2)', border: '1px solid var(--border)',
            borderRadius: 'var(--radius)', padding: '8px 10px', margin: '6px 0',
            overflowX: 'auto', fontSize: 11.5, fontFamily: 'JetBrains Mono, monospace',
            lineHeight: 1.6, color: 'var(--tx)',
          }}>
            <code>{codeBlock.join('\n')}</code>
          </pre>
        );
        inCode = false;
        codeBlock = [];
      }
    } else if (inCode) {
      codeBlock.push(line);
    } else if (line.trim() === '') {
      elements.push(<br key={i} />);
    } else {
      elements.push(
        <span key={i} style={{ display: 'block' }}>
          {inlineFormat(line)}
        </span>
      );
    }
  }

  return <>{elements}</>;
}

function inlineFormat(text: string): React.ReactNode {
  const parts = text.split(/(`[^`]+`)/);
  return parts.map((part, i) =>
    part.startsWith('`') && part.endsWith('`')
      ? <code key={i} style={{
          fontFamily: 'JetBrains Mono, monospace', fontSize: 11,
          background: 'var(--surface-3)', padding: '1px 4px',
          borderRadius: 3, color: 'var(--accent-2)',
        }}>{part.slice(1, -1)}</code>
      : part
  );
}
