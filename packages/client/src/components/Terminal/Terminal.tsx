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

function readCssVar(name: string, fallback: string): string {
  const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return value || fallback;
}

function createTerminalTheme() {
  return {
    background: readCssVar('--terminal-bg', '#0b0f12'),
    foreground: readCssVar('--terminal-fg', '#d7e2da'),
    cursor: readCssVar('--terminal-cursor', '#8de8c9'),
    selectionBackground: readCssVar('--terminal-selection', 'rgba(102, 221, 184, 0.26)'),
  };
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
      theme: createTerminalTheme(),
      fontFamily: readCssVar('--font-mono', "'JetBrains Mono', Menlo, monospace"),
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
    <div className="terminal-shell">
      {/* Tab bar */}
      <div className="terminal-tabs" role="tablist" aria-label="Terminals">
        {tabs.map(tab => (
          <div
            key={tab.id}
            onClick={() => setActiveId(tab.id)}
            className={`terminal-tab-item ${tab.id === activeId ? 'active' : ''}`}
            role="tab"
            aria-selected={tab.id === activeId}
            aria-label={`Terminal ${tab.label}`}
            title={tab.cwd}
          >
            <span className="terminal-tab-label">{tab.label}</span>
            <button
              onClick={(e) => { e.stopPropagation(); closeTab(tab.id); }}
              className="terminal-tab-close"
              aria-label={`Close terminal ${tab.label}`}
              title="Close terminal"
            >
              <X size={10} />
            </button>
          </div>
        ))}
        <button
          onClick={newTerminal}
          className="terminal-new-btn"
          aria-label="Create terminal"
          title="Create terminal"
        >
          <Plus size={14} />
        </button>
        {tabs.length === 0 && (
          <span className="terminal-empty">No terminals — click + to open one</span>
        )}
      </div>

      {/* xterm container */}
      <div ref={containerRef} className="terminal-body" />
    </div>
  );
}
