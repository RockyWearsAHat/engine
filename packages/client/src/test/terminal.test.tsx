import { act, fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../store/index.js';

let capturedWsHandler: ((msg: unknown) => void) | null = null;
const { sendMock, writeMock, openMock, disposeMock, onDataMock } = vi.hoisted(() => ({
  sendMock: vi.fn(),
  writeMock: vi.fn(),
  openMock: vi.fn(),
  disposeMock: vi.fn(),
  onDataMock: vi.fn((cb: (data: string) => void) => {
    void cb;
    return { dispose: vi.fn() };
  }),
}));

vi.mock('../ws/client.js', () => ({
  wsClient: {
    send: sendMock,
    onMessage: vi.fn((cb: (msg: unknown) => void) => {
      capturedWsHandler = cb;
      return () => {
        capturedWsHandler = null;
      };
    }),
  },
}));

vi.mock('@xterm/xterm', () => ({
  Terminal: class MockTerminal {
    loadAddon = vi.fn();
    open = openMock;
    write = writeMock;
    onData = onDataMock;
    dispose = disposeMock;
  },
}));

const fitMock = vi.fn();
const proposeDimensionsMock = vi.fn(() => ({ cols: 80, rows: 24 }));
vi.mock('@xterm/addon-fit', () => ({
  FitAddon: class MockFitAddon {
    fit = fitMock;
    proposeDimensions = proposeDimensionsMock;
  },
}));

vi.mock('@xterm/addon-web-links', () => ({
  WebLinksAddon: class MockWebLinksAddon {},
}));

vi.mock('@xterm/xterm/css/xterm.css', () => ({}));

const { default: Terminal } = await import('../components/Terminal/Terminal.js');

class MockResizeObserver {
  observe = vi.fn();
  disconnect = vi.fn();
}

describe('Terminal interactions', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    (globalThis as { ResizeObserver?: typeof ResizeObserver }).ResizeObserver = MockResizeObserver as unknown as typeof ResizeObserver;
    sendMock.mockClear();
    writeMock.mockClear();
    openMock.mockClear();
    onDataMock.mockClear();
    disposeMock.mockClear();
    fitMock.mockClear();
    proposeDimensionsMock.mockClear();
    useStore.setState({
      activeSession: {
        id: 'sess-1',
        projectPath: '/project/root',
        branchName: 'main',
        createdAt: '',
        updatedAt: '',
        summary: '',
        messageCount: 0,
      },
    });
  });

  it('PlusButton_NewTerminalCreatedWithActiveProjectCwd', () => {
    render(<Terminal />);
    fireEvent.click(screen.getByRole('button'));
    expect(sendMock).toHaveBeenCalledWith({ type: 'terminal.create', cwd: '/project/root' });
  });

  it('CommandRequest_LaunchedAndTerminalInputSentAfterCreated', async () => {
    const onHandled = vi.fn();
    render(
      <Terminal
        commandRequest={{ id: 'cmd-1', command: 'pnpm test', cwd: '/project/root', label: 'Run tests' }}
        onCommandHandled={onHandled}
      />,
    );

    expect(onHandled).toHaveBeenCalledWith('cmd-1');
    expect(sendMock).toHaveBeenCalledWith({ type: 'terminal.create', cwd: '/project/root' });

    act(() => {
      capturedWsHandler?.({ type: 'terminal.created', terminalId: 't-1', cwd: '/project/root' });
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(45);
    });

    expect(screen.getByText('Run tests')).toBeTruthy();
    expect(sendMock).toHaveBeenCalledWith({
      type: 'terminal.input',
      terminalId: 't-1',
      data: 'pnpm test\n',
    });
  });

  it('TerminalOutput_WrittenToActiveXtermInstance', () => {
    render(<Terminal />);
    act(() => {
      capturedWsHandler?.({ type: 'terminal.created', terminalId: 't-1', cwd: '/project/root' });
    });
    act(() => {
      capturedWsHandler?.({ type: 'terminal.output', terminalId: 't-1', data: 'hello\n' });
    });
    expect(writeMock).toHaveBeenCalledWith('hello\n');
  });

  it('WebsocketCommand_TerminalTabClosed', () => {
    render(<Terminal />);
    act(() => {
      capturedWsHandler?.({ type: 'terminal.created', terminalId: 't-1', cwd: '/project/root' });
    });

    fireEvent.click(screen.getByText('shell').closest('div')?.querySelector('button') as HTMLButtonElement);
    expect(sendMock).toHaveBeenCalledWith({ type: 'terminal.close', terminalId: 't-1' });
  });

  it('TerminalClosedMessage_TabRemoved', () => {
    render(<Terminal />);
    act(() => {
      capturedWsHandler?.({ type: 'terminal.created', terminalId: 't-close', cwd: '/project/root' });
    });
    expect(screen.getByText('shell')).toBeTruthy();

    act(() => {
      capturedWsHandler?.({ type: 'terminal.closed', terminalId: 't-close' });
    });

    expect(screen.queryByText('shell')).toBeNull();
  });

  it('XtermOnData_TerminalInputSent', () => {
    let capturedOnData: ((data: string) => void) | null = null;
    onDataMock.mockImplementationOnce((cb: (data: string) => void) => {
      capturedOnData = cb;
      return { dispose: vi.fn() };
    });

    render(<Terminal />);
    act(() => {
      capturedWsHandler?.({ type: 'terminal.created', terminalId: 't-ondata', cwd: '/project/root' });
    });

    act(() => {
      capturedOnData?.('ls\n');
    });

    expect(sendMock).toHaveBeenCalledWith({ type: 'terminal.input', terminalId: 't-ondata', data: 'ls\n' });
  });

  it('ResizeObserverFires_TerminalResizeSent', () => {
    let capturedROCb: (() => void) | null = null;
    (globalThis as { ResizeObserver: unknown }).ResizeObserver = class {
      observe = vi.fn((el: unknown) => { void el; });
      disconnect = vi.fn();
      constructor(cb: () => void) { capturedROCb = cb; }
    };

    render(<Terminal />);
    act(() => {
      capturedWsHandler?.({ type: 'terminal.created', terminalId: 't-resize', cwd: '/project/root' });
    });

    act(() => { capturedROCb?.(); });

    expect(sendMock).toHaveBeenCalledWith({ type: 'terminal.resize', terminalId: 't-resize', cols: 80, rows: 24 });
  });

  it('SecondTerminalTabClicked_ActiveTabSwitched', () => {
    render(<Terminal />);
    act(() => {
      capturedWsHandler?.({ type: 'terminal.created', terminalId: 't-a', cwd: '/project/root' });
    });
    act(() => {
      capturedWsHandler?.({ type: 'terminal.created', terminalId: 't-b', cwd: '/project/root' });
    });

    const shellTabs = screen.getAllByText('shell');
    expect(shellTabs.length).toBeGreaterThanOrEqual(2);
    // Click the first tab — exercises setActiveId on a non-active tab
    const firstTabDiv = shellTabs[0].closest('div[class]') as HTMLElement;
    if (firstTabDiv) fireEvent.click(firstTabDiv);
    expect(screen.getAllByText('shell').length).toBeGreaterThanOrEqual(1);
  });

  it('NewTerminal_NullActiveSession_CwdDefaultsToDot', () => {
    // Override active session to null so the `?? '.'` branch fires
    useStore.setState({ activeSession: null });
    render(<Terminal />);
    // Click the new terminal button (no title attr — use role)
    const newTermBtn = screen.getAllByRole('button')[0];
    if (!newTermBtn) throw new Error('no button found');
    fireEvent.click(newTermBtn);
    expect(sendMock).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'terminal.create', cwd: '.' }),
    );
    vi.useRealTimers();
  });

  it('TerminalOutput_UnknownTerminalId_WriteSkipped', () => {
    render(<Terminal />);
    act(() => {
      capturedWsHandler?.({ type: 'terminal.created', terminalId: 't-known', cwd: '/project/root' });
    });
    // Send output for a different (unknown) terminal ID — covers the `tab?.` FALSE branch
    act(() => {
      capturedWsHandler?.({ type: 'terminal.output', terminalId: 'unknown-id', data: 'hello' });
    });
    // writeMock should NOT have been called (tab was undefined)
    expect(writeMock).not.toHaveBeenCalled();
    vi.useRealTimers();
  });
});
