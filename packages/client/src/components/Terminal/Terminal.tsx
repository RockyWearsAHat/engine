import { useEffect, useRef, useState } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { WebLinksAddon } from '@xterm/addon-web-links';
import '@xterm/xterm/css/xterm.css';
import { wsClient } from '../../ws/client.js';
import { useStore } from '../../store/index.js';
import { Plus, X } from 'lucide-react';

interface TermTab {
  id: string;
  cwd: string;
  label: string;
  xterm: XTerm;
  fitAddon: FitAddon;
}

interface CommandRequest {
  id: string;
  command: string;
  cwd: string;
  label: string;
}

export default function Terminal({
  commandRequest,
  onCommandHandled,
}: {
  commandRequest?: CommandRequest | null;
  onCommandHandled?: (id: string) => void;
}) {
  const { activeSession } = useStore();
  const [tabs, setTabs] = useState<TermTab[]>([]);
  const [activeId, setActiveId] = useState<string | null>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const tabsRef = useRef<TermTab[]>([]);
  const pendingLaunchesRef = useRef<Array<CommandRequest>>([]);
  const handledRequestIdsRef = useRef<Set<string>>(new Set());
  tabsRef.current = tabs;

  const createTerminal = (terminalId: string, cwd: string, label: string) => {
    const xterm = new XTerm({
      theme: {
        background: '#0d0d0d',
        foreground: '#e2e2e2',
        cursor: '#528bff',
        selectionBackground: '#264f78',
      },
      fontFamily: "'JetBrains Mono', 'Fira Code', Menlo, monospace",
      fontSize: 13,
      lineHeight: 1.4,
      cursorBlink: true,
    });
    const fitAddon = new FitAddon();
    xterm.loadAddon(fitAddon);
    xterm.loadAddon(new WebLinksAddon());

    const tab: TermTab = { id: terminalId, cwd, label, xterm, fitAddon };
    setTabs(prev => [...prev, tab]);
    setActiveId(terminalId);
  };

  useEffect(() => {
    const off = wsClient.onMessage(msg => {
      if (msg.type === 'terminal.created') {
        const nextLaunch = pendingLaunchesRef.current.shift();
        createTerminal(msg.terminalId, msg.cwd, nextLaunch?.label ?? 'shell');
        if (nextLaunch) {
          window.setTimeout(() => {
            wsClient.send({ type: 'terminal.input', terminalId: msg.terminalId, data: `${nextLaunch.command}\n` });
          }, 40);
        }
      } else if (msg.type === 'terminal.output') {
        const tab = tabsRef.current.find(t => t.id === msg.terminalId);
        /* istanbul ignore next */
        tab?.xterm.write(msg.data);
      /* istanbul ignore start */
      } else if (msg.type === 'terminal.closed') {
        setTabs(prev => {
          const tab = prev.find(t => t.id === msg.terminalId);
          tab?.xterm.dispose();
          const next = prev.filter(t => t.id !== msg.terminalId);
          setActiveId(next[next.length - 1]?.id ?? null);
          return next;
        });
      }
      /* istanbul ignore stop */
    });
    return () => off();
  }, []);

  useEffect(() => {
    if (!commandRequest || handledRequestIdsRef.current.has(commandRequest.id)) {
      return;
    }
    handledRequestIdsRef.current.add(commandRequest.id);
    onCommandHandled?.(commandRequest.id);
    pendingLaunchesRef.current.push(commandRequest);
    wsClient.send({ type: 'terminal.create', cwd: commandRequest.cwd });
  }, [commandRequest, onCommandHandled]);

  // Mount active xterm to DOM
  /* istanbul ignore start */
  useEffect(() => {
    if (!containerRef.current || !activeId) return;
    const tab = tabs.find(t => t.id === activeId);
    if (!tab) return;

    containerRef.current.innerHTML = '';
    tab.xterm.open(containerRef.current);
    tab.fitAddon.fit();

    const inputDisposable = tab.xterm.onData(data => {
      wsClient.send({ type: 'terminal.input', terminalId: activeId, data });
    });

    const ro = new ResizeObserver(() => {
      tab.fitAddon.fit();
      const dims = tab.fitAddon.proposeDimensions();
      if (dims) {
        wsClient.send({ type: 'terminal.resize', terminalId: activeId, cols: dims.cols, rows: dims.rows });
      }
    });
    ro.observe(containerRef.current);
    return () => {
      inputDisposable.dispose();
      ro.disconnect();
    };
  }, [activeId, tabs.length]);
  /* istanbul ignore stop */

  const newTerminal = () => {
    const cwd = activeSession?.projectPath ?? '.';
    wsClient.send({ type: 'terminal.create', cwd });
  };

  const closeTab = (id: string) => {
    wsClient.send({ type: 'terminal.close', terminalId: id });
  };

  return (
    <div className="flex flex-col h-full bg-editor-bg">
      {/* Tab bar */}
      <div className="flex items-center bg-editor-surface border-b border-editor-border shrink-0">
        {tabs.map(tab => (
          <div
            key={tab.id}
            onClick={() => setActiveId(tab.id)}
            className={`flex items-center gap-1.5 px-3 py-1.5 text-xs cursor-pointer border-r border-editor-border ${
              tab.id === activeId ? 'bg-editor-bg text-gray-200' : 'text-gray-500 hover:bg-editor-hover'
            }`}
          >
            <span>{tab.label}</span>
            <button onClick={(e) => { e.stopPropagation(); closeTab(tab.id); }} className="hover:text-red-400">
              <X size={10} />
            </button>
          </div>
        ))}
        <button onClick={newTerminal} className="p-1.5 text-gray-500 hover:text-gray-300 hover:bg-editor-hover ml-1">
          <Plus size={14} />
        </button>
        {tabs.length === 0 && (
          <span className="px-3 text-xs text-gray-600 py-1.5">No terminals — click + to open one</span>
        )}
      </div>

      {/* xterm container */}
      <div ref={containerRef} className="flex-1 overflow-hidden p-1" />
    </div>
  );
}
