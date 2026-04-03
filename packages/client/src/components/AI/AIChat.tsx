import { useRef, useEffect, useState, type KeyboardEvent } from 'react';
import { useStore, type ChatMessage, type ToolCallDisplay } from '../../store/index.js';
import { wsClient } from '../../ws/client.js';
import { randomUUID } from '../../utils.js';
import { Send, ChevronDown, ChevronRight, Terminal, FileText, Search, GitBranch, Loader2 } from 'lucide-react';

function ToolCallBadge({ tc }: { tc: ToolCallDisplay }) {
  const [expanded, setExpanded] = useState(false);

  const icon = () => {
    if (tc.name.includes('file') || tc.name === 'list_directory') return <FileText size={11} />;
    if (tc.name === 'shell' || tc.name === 'git_commit') return <Terminal size={11} />;
    if (tc.name === 'search_files') return <Search size={11} />;
    if (tc.name.startsWith('git')) return <GitBranch size={11} />;
    return <Terminal size={11} />;
  };

  return (
    <div className={`mt-1 rounded text-xs border ${tc.isError ? 'border-red-800 bg-red-950/30' : 'border-gray-700 bg-gray-900/50'}`}>
      <button
        onClick={() => setExpanded(e => !e)}
        className="flex items-center gap-1.5 px-2 py-1 w-full text-left"
      >
        <span className={tc.pending ? 'text-yellow-400' : tc.isError ? 'text-red-400' : 'text-green-400'}>
          {tc.pending ? <Loader2 size={11} className="animate-spin" /> : icon()}
        </span>
        <span className="font-mono text-gray-400">{tc.name}</span>
        <span className="text-gray-600 truncate ml-1">
          {typeof tc.input === 'object' && tc.input !== null
            ? Object.values(tc.input as Record<string, unknown>)[0]?.toString().slice(0, 40)
            : ''}
        </span>
        <span className="ml-auto text-gray-600">
          {expanded ? <ChevronDown size={10} /> : <ChevronRight size={10} />}
        </span>
      </button>
      {expanded && (
        <div className="px-2 pb-2 border-t border-gray-700/50 mt-0.5 pt-1 space-y-1">
          <div className="text-gray-500 font-mono text-xs">{JSON.stringify(tc.input, null, 2)}</div>
          {tc.result !== undefined && (
            <div className={`font-mono text-xs whitespace-pre-wrap max-h-32 overflow-y-auto ${tc.isError ? 'text-red-300' : 'text-gray-300'}`}>
              {String(tc.result).slice(0, 2000)}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function MessageBubble({ msg }: { msg: ChatMessage }) {
  const isUser = msg.role === 'user';
  return (
    <div className={`mb-3 ${isUser ? 'pl-8' : 'pr-4'}`}>
      <div className={`text-xs font-medium mb-1 ${isUser ? 'text-blue-400 text-right' : 'text-gray-500'}`}>
        {isUser ? 'You' : 'AI'}
      </div>
      {msg.content && (
        <div className={`text-sm leading-relaxed whitespace-pre-wrap ${
          isUser ? 'text-gray-200 text-right' : 'text-gray-200'
        }`}>
          {msg.content}
          {msg.streaming && <span className="inline-block w-1 h-3.5 bg-blue-400 animate-pulse ml-0.5 align-middle" />}
        </div>
      )}
      {msg.toolCalls.map((tc, i) => <ToolCallBadge key={i} tc={tc} />)}
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
  }, [chatMessages]);

  const send = () => {
    const content = input.trim();
    if (!content || !activeSession || sending) return;

    const id = randomUUID();
    useStore.getState().addUserMessage(id, content);
    wsClient.send({ type: 'chat', sessionId: activeSession.id, content });
    setInput('');
    setSending(true);

    const off = wsClient.onMessage(msg => {
      if (msg.type === 'chat.chunk' && msg.done) {
        setSending(false);
        off();
      } else if (msg.type === 'chat.error') {
        setSending(false);
        off();
      }
    });
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault();
      send();
    }
  };

  return (
    <div className="flex flex-col h-full bg-editor-bg">
      {/* Header */}
      <div className="flex items-center justify-between px-3 py-1.5 bg-editor-surface border-b border-editor-border shrink-0">
        <span className="text-xs text-gray-500 font-medium uppercase tracking-wide">AI</span>
        {activeSession && (
          <span className="text-xs text-gray-600 font-mono truncate max-w-48">
            {activeSession.branchName}
          </span>
        )}
      </div>

      {/* Messages */}
      <div className="flex-1 overflow-y-auto px-3 py-2">
        {chatMessages.length === 0 ? (
          <div className="flex items-center justify-center h-full text-gray-600 text-xs text-center">
            {connected
              ? 'Ask the AI anything. It can read files, run commands, write code, and commit changes.'
              : 'Connecting to server...'}
          </div>
        ) : (
          chatMessages.map(msg => <MessageBubble key={msg.id} msg={msg} />)
        )}
        <div ref={bottomRef} />
      </div>

      {/* Input */}
      <div className="shrink-0 border-t border-editor-border p-2">
        <div className="flex items-end gap-2 bg-editor-surface rounded-lg border border-editor-border px-3 py-2">
          <textarea
            ref={textareaRef}
            value={input}
            onChange={e => setInput(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder={activeSession ? 'Ask the AI... (⌘↵ to send)' : 'Create a session to start...'}
            disabled={!activeSession || sending}
            rows={1}
            className="flex-1 bg-transparent text-sm text-gray-200 placeholder-gray-600 outline-none resize-none max-h-32 font-mono"
            style={{ minHeight: '20px' }}
          />
          <button
            onClick={send}
            disabled={!input.trim() || !activeSession || sending}
            className="text-blue-400 hover:text-blue-300 disabled:text-gray-700 shrink-0 pb-0.5"
          >
            {sending ? <Loader2 size={16} className="animate-spin" /> : <Send size={16} />}
          </button>
        </div>
        <div className="text-xs text-gray-700 mt-1 px-1">⌘↵ send · AI has full access to your project</div>
      </div>
    </div>
  );
}
