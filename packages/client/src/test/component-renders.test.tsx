/**
 * component-renders.test.tsx
 *
 * Scalability contract: to test that a new component mounts and maintains its
 * root styling, add ONE entry to the COMPONENTS table below. No new describe
 * blocks, no new test functions. The loop does the rest.
 *
 * What this file proves:
 *   1. The component mounts without throwing.
 *   2. The root element is present in the DOM.
 *   3. The root element carries the expected CSS class (design system anchor).
 *
 * Behavioral tests (interactions, state machines, keyboard handling) live in
 * dedicated files. This file is intentionally shallow.
 */
import { render } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useStore } from '../store/index.js';

// ─── Shared mocks (all components get the same environment) ──────────────────

vi.mock('../ws/client.js', () => ({
  wsClient: { send: vi.fn(), connect: vi.fn(), disconnect: vi.fn(), onMessage: vi.fn(() => () => {}), onOpen: vi.fn(() => () => {}), onClose: vi.fn(() => () => {}) },
}));

vi.mock('../bridge.js', () => ({
  bridge: {
    openExternal: vi.fn(),
    setEditorPreferences: vi.fn(),
    getLocalServerToken: vi.fn().mockResolvedValue(null),
    getGithubToken: vi.fn().mockResolvedValue(null),
  },
}));

vi.mock('../connectionProfiles.js', () => ({
  loadConnectionProfiles: vi.fn().mockReturnValue([]),
  loadActiveConnectionProfile: vi.fn().mockReturnValue(null),
  loadActiveConnectionProfileId: vi.fn().mockReturnValue(null),
  saveConnectionProfile: vi.fn(),
  deleteConnectionProfile: vi.fn(),
  setActiveConnectionProfile: vi.fn(),
  clearConnectionProfiles: vi.fn(),
  pairConnectionCode: vi.fn(),
}));

vi.mock('@xterm/xterm', () => ({
  Terminal: vi.fn().mockImplementation(() => ({
    open: vi.fn(), dispose: vi.fn(), onData: vi.fn(() => ({ dispose: vi.fn() })),
    onResize: vi.fn(() => ({ dispose: vi.fn() })), loadAddon: vi.fn(), write: vi.fn(),
  })),
}));
vi.mock('@xterm/addon-fit', () => ({ FitAddon: vi.fn().mockImplementation(() => ({ fit: vi.fn() })) }));
vi.mock('@xterm/addon-web-links', () => ({ WebLinksAddon: vi.fn().mockImplementation(() => ({})) }));
vi.mock('@xterm/xterm/css/xterm.css', () => ({}));

// Lazy imports after mocks are in place
const { default: AgentPanel } = await import('../components/AgentPanel/AgentPanel.js');
const { default: StatusBar } = await import('../components/StatusBar/StatusBar.js');
const { default: MarkdownPreview } = await import('../components/Editor/MarkdownPreview.js');
const { default: SyntacticalPreview } = await import('../components/Editor/SyntacticalPreview.js');
const { default: Terminal } = await import('../components/Terminal/Terminal.js');
const { default: MachineConnectionsPanel } = await import('../components/Connections/MachineConnectionsPanel.js');
const { default: CommandPalette } = await import('../components/CommandPalette/CommandPalette.js');

// ─── Component table ──────────────────────────────────────────────────────────
//
// Each entry: { name, element, rootClass }
//   name       — label shown in test output
//   element    — JSX to render (with minimal required props)
//   rootClass  — CSS class the root element must carry (design system anchor)
//                Use null to only assert the component mounts without crashing.
//
// To register a new component: add one object here.

useStore.setState({
  sessions: [],
  connected: false,
  gitStatus: null,
  githubUser: null,
  openFiles: [],
  activeFilePath: null,
  editorPreferences: { fontFamily: 'default', fontSize: 13, lineHeight: 1.5, tabSize: 2, markdownViewMode: 'text', wordWrap: false },
});

const COMPONENTS: { name: string; element: React.ReactElement; rootClass: string | null }[] = [
  {
    name: 'AgentPanel',
    element: <AgentPanel />,
    rootClass: null, // inline-styled root — no class to anchor yet
  },
  {
    name: 'StatusBar',
    element: <StatusBar />,
    rootClass: 'status-bar',
  },
  {
    name: 'MarkdownPreview',
    element: <MarkdownPreview value="# Hello" />,
    rootClass: 'markdown-preview',
  },
  {
    name: 'SyntacticalPreview',
    element: <SyntacticalPreview value="# Hello" />,
    rootClass: 'markdown-preview',
  },
  {
    name: 'Terminal',
    element: <Terminal />,
    rootClass: null,
  },
  {
    name: 'MachineConnectionsPanel',
    element: <MachineConnectionsPanel />,
    rootClass: null,
  },
  {
    name: 'CommandPalette (open)',
    element: <CommandPalette open={true} mode="commands" items={[]} onClose={vi.fn()} onModeChange={vi.fn()} />,
    rootClass: 'command-palette-overlay',
  },
];

// ─── Single loop — one it() per row ──────────────────────────────────────────

describe('component renders', () => {
  it.each(COMPONENTS)('$name mounts without crashing', ({ element, rootClass }) => {
    const { container } = render(element);
    expect(container.firstChild).not.toBeNull();
    if (rootClass) {
      expect(container.firstChild).toHaveClass(rootClass);
    }
  });
});
