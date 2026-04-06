import { useEffect, useRef, useState, useCallback } from 'react';
import { useStore } from '../../store/index.js';
import { wsClient } from '../../ws/client.js';
import { randomUUID } from '../../utils.js';
import { ArrowUp, ChevronDown, ChevronRight, Loader2, Check, X, Wrench, Square, ArrowDown } from 'lucide-react';

export default function AIChat() {
  const { activeSession, chatMessages, addUserMessage, streamingMessageId } = useStore();
  const [input, setInput] = useState('');
  const [expandedTools, setExpandedTools] = useState<Set<string>>(new Set());
  const [showScrollBtn, setShowScrollBtn] = useState(false);
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const scrollContainerRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  // True when the user is already scrolled to (or near) the bottom.
  const isAtBottomRef = useRef(true);
  // Set to true when the user explicitly sends a message so we force-scroll once.
  const forceScrollRef = useRef(false);

  // Track scroll position to decide whether to auto-scroll on new content.
  const handleScroll = useCallback(() => {
    const el = scrollContainerRef.current;
    if (!el) return;
    const distanceFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
    isAtBottomRef.current = distanceFromBottom < 48;
    setShowScrollBtn(!isAtBottomRef.current && !!streamingMessageId);
  }, [streamingMessageId]);

  // Auto-scroll when messages update — only if already at bottom or forced.
  useEffect(() => {
    if (forceScrollRef.current || isAtBottomRef.current) {
      messagesEndRef.current?.scrollIntoView({ behavior: forceScrollRef.current ? 'auto' : 'smooth' });
      forceScrollRef.current = false;
    } else if (streamingMessageId) {
      // New content arrived while user is scrolled up — show the FAB.
      setShowScrollBtn(true);
    }
  }, [chatMessages, streamingMessageId]);

  const scrollToBottom = useCallback(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
    isAtBottomRef.current = true;
    setShowScrollBtn(false);
  }, []);

  const send = useCallback(() => {
    const content = input.trim();
    if (!content || !activeSession) return;
    const msgId = randomUUID();
    addUserMessage(msgId, content);
    wsClient.send({ type: 'chat', sessionId: activeSession.id, content });
    setInput('');
    forceScrollRef.current = true;
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto';
    }
  }, [input, activeSession, addUserMessage]);

  const cancel = useCallback(() => {
    if (!activeSession || !streamingMessageId) return;
    wsClient.send({ type: 'chat.stop', sessionId: activeSession.id });
  }, [activeSession, streamingMessageId]);

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
        <div style={{ borderBottom: '1px solid var(--border)' }}>
          <div style={{
            padding: '6px 12px',
            fontSize: 11,
            color: 'var(--tx-3)',
            display: 'flex',
            alignItems: 'center',
            gap: 5,
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
          {activeSession.summary && (
            <div style={{
              margin: '0 12px 10px',
              padding: '10px 12px',
              borderRadius: 3,
              border: '1px solid rgba(125, 211, 252, 0.14)',
              background: 'rgba(125,211,252,0.06)',
            }}>
              <div style={{
                fontSize: 10,
                fontWeight: 700,
                letterSpacing: '0.12em',
                textTransform: 'uppercase',
                color: '#7dd3fc',
                marginBottom: 6,
              }}>
                Project memory
              </div>
              <div style={{
                fontSize: 11,
                color: 'var(--tx-2)',
                lineHeight: 1.6,
                whiteSpace: 'pre-line',
                display: '-webkit-box',
                WebkitBoxOrient: 'vertical',
                WebkitLineClamp: 4,
                overflow: 'hidden',
              }}>
                {activeSession.summary}
              </div>
            </div>
          )}
        </div>
      )}

      <div
        ref={scrollContainerRef}
        className="chat-messages"
        onScroll={handleScroll}
        style={{ position: 'relative' }}
      >
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

      {/* Jump-to-bottom FAB — only visible when scrolled up during streaming */}
      {showScrollBtn && (
        <button
          onClick={scrollToBottom}
          style={{
            position: 'absolute',
            bottom: 72,
            left: '50%',
            transform: 'translateX(-50%)',
            zIndex: 10,
            display: 'flex',
            alignItems: 'center',
            gap: 5,
            padding: '5px 12px',
            borderRadius: 20,
            border: '1px solid var(--border)',
            background: 'var(--surface)',
            color: 'var(--tx-2)',
            fontSize: 11,
            fontWeight: 500,
            cursor: 'pointer',
            boxShadow: '0 2px 8px rgba(0,0,0,0.25)',
          }}
          title="Jump to latest"
        >
          <ArrowDown size={12} />
          Jump to latest
        </button>
      )}

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
          {streamingMessageId ? (
            <button
              className="chat-send-btn"
              onClick={cancel}
              title="Stop generating"
              style={{ background: 'transparent', border: '1px solid var(--red)', color: 'var(--red)' }}
            >
              <Square size={12} style={{ fill: 'currentColor' }} />
            </button>
          ) : (
            <button
              className="chat-send-btn"
              onClick={send}
              disabled={!input.trim() || noSession}
              title="Send"
            >
              <ArrowUp size={14} />
            </button>
          )}
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
