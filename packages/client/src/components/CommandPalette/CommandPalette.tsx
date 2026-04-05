import { useEffect, useMemo, useRef, useState } from 'react';
import { Command, FileText, Search } from 'lucide-react';

export type CommandPaletteMode = 'commands' | 'files';

export interface CommandPaletteItem {
  id: string;
  kind: 'command' | 'file';
  title: string;
  subtitle: string;
  keywords?: string;
  badge?: string;
  disabled?: boolean;
  action: () => void | Promise<void>;
}

export default function CommandPalette({
  open,
  mode,
  workspaceName,
  items,
  onClose,
  onModeChange,
}: {
  open: boolean;
  mode: CommandPaletteMode;
  workspaceName?: string;
  items: CommandPaletteItem[];
  onClose: () => void;
  onModeChange: (mode: CommandPaletteMode) => void;
}) {
  const inputRef = useRef<HTMLInputElement | null>(null);
  const [query, setQuery] = useState('');
  const [selectedIndex, setSelectedIndex] = useState(0);

  useEffect(() => {
    if (!open) {
      setQuery('');
      setSelectedIndex(0);
      return;
    }
    setQuery('');
    setSelectedIndex(0);
    requestAnimationFrame(() => inputRef.current?.focus());
  }, [mode, open]);

  const filteredItems = useMemo(() => {
    const tokens = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
    if (tokens.length === 0) {
      return items.slice(0, 120);
    }
    return items.filter((item) => {
      const haystack = `${item.title} ${item.subtitle} ${item.keywords ?? ''}`.toLowerCase();
      return tokens.every((token) => haystack.includes(token));
    }).slice(0, 120);
  }, [items, query]);

  useEffect(() => {
    setSelectedIndex((current) => {
      if (filteredItems.length === 0) {
        return 0;
      }
      return Math.min(current, filteredItems.length - 1);
    });
  }, [filteredItems]);

  if (!open) {
    return null;
  }

  const selectedItem = filteredItems[selectedIndex] ?? null;
  const modeTitle = mode === 'commands' ? 'Workspace commands' : 'Quick open';
  const modeCopy = mode === 'commands'
    ? 'Run actions, switch panels, and drive the workspace without hunting through the UI.'
    : 'Jump straight to files in the current workspace by name or path.';

  const confirmSelection = (item: CommandPaletteItem | null) => {
    if (!item || item.disabled) {
      return;
    }
    onClose();
    void item.action();
  };

  return (
    <div className="command-palette-overlay">
      <button
        className="command-palette-backdrop"
        aria-label="Close command palette"
        onClick={onClose}
      />
      <div className="command-palette-card animate-appear" role="dialog" aria-modal="true" aria-labelledby="command-palette-title">
        <div className="command-palette-kicker">Engine control surface</div>
        <div className="command-palette-header">
          <div>
            <div id="command-palette-title" className="command-palette-title">
              {modeTitle}
            </div>
            <div className="command-palette-copy">
              {modeCopy}
              {workspaceName ? ` • ${workspaceName}` : ''}
            </div>
          </div>
          <div className="command-palette-tabs" role="tablist" aria-label="Command palette mode">
            <button
              className={`command-palette-tab ${mode === 'commands' ? 'active' : ''}`}
              role="tab"
              aria-selected={mode === 'commands'}
              onClick={() => onModeChange('commands')}
            >
              <Command size={13} />
              Commands
            </button>
            <button
              className={`command-palette-tab ${mode === 'files' ? 'active' : ''}`}
              role="tab"
              aria-selected={mode === 'files'}
              onClick={() => onModeChange('files')}
            >
              <FileText size={13} />
              Files
            </button>
          </div>
        </div>

        <div className="command-palette-search-shell">
          <Search size={15} />
          <input
            ref={inputRef}
            className="command-palette-search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Escape') {
                event.preventDefault();
                onClose();
                return;
              }
              if (event.key === 'ArrowDown') {
                event.preventDefault();
                setSelectedIndex((current) => {
                  if (filteredItems.length === 0) {
                    return 0;
                  }
                  return (current + 1) % filteredItems.length;
                });
                return;
              }
              if (event.key === 'ArrowUp') {
                event.preventDefault();
                setSelectedIndex((current) => {
                  if (filteredItems.length === 0) {
                    return 0;
                  }
                  return current <= 0 ? filteredItems.length - 1 : current - 1;
                });
                return;
              }
              if (event.key === 'Tab') {
                event.preventDefault();
                onModeChange(mode === 'commands' ? 'files' : 'commands');
                return;
              }
              if (event.key === 'Enter') {
                event.preventDefault();
                confirmSelection(selectedItem);
              }
            }}
            placeholder={mode === 'commands' ? 'Search commands…' : 'Search files by name or path…'}
            spellCheck={false}
          />
        </div>

        <div className="command-palette-results">
          {filteredItems.length > 0 ? (
            filteredItems.map((item, index) => (
              <button
                key={item.id}
                className={`command-palette-item ${index === selectedIndex ? 'selected' : ''}`}
                onClick={() => confirmSelection(item)}
                onMouseEnter={() => setSelectedIndex(index)}
                disabled={item.disabled}
              >
                <div className={`command-palette-item-icon ${item.kind}`}>
                  {item.kind === 'command' ? <Command size={13} /> : <FileText size={13} />}
                </div>
                <div className="command-palette-item-copy">
                  <div className="command-palette-item-row">
                    <span className="command-palette-item-title">{item.title}</span>
                    <span className={`command-palette-item-badge ${item.kind}`}>
                      {item.badge ?? (item.kind === 'command' ? 'Command' : 'File')}
                    </span>
                  </div>
                  <div className="command-palette-item-subtitle">{item.subtitle}</div>
                </div>
              </button>
            ))
          ) : (
            <div className="command-palette-empty">
              {mode === 'commands'
                ? 'No commands match that search yet.'
                : 'No files match that search in the current workspace.'}
            </div>
          )}
        </div>

        <div className="command-palette-footer">
          <span>Enter to open</span>
          <span>↑↓ to move</span>
          <span>Tab to switch</span>
          <span>Esc to close</span>
        </div>
      </div>
    </div>
  );
}
