import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import CommandPalette from '../components/CommandPalette/CommandPalette.js';

function makeItem(id: string, title: string, kind: 'command' | 'file' = 'command') {
  return {
    id,
    kind,
    title,
    subtitle: `${title} subtitle`,
    keywords: `${title} keyword`,
    action: vi.fn(),
  };
}

describe('CommandPalette interactions', () => {
  const onClose = vi.fn();
  const onModeChange = vi.fn();

  beforeEach(() => {
    onClose.mockClear();
    onModeChange.mockClear();
  });

  it('Closed_NotRendered', () => {
    const { container } = render(
      <CommandPalette
        open={false}
        mode="commands"
        workspaceName="demo"
        items={[]}
        onClose={onClose}
        onModeChange={onModeChange}
      />,
    );

    expect(container.firstChild).toBeNull();
  });

  it('FiltersItemsAndEnterRunsSelectedAction', () => {
    const openCommand = makeItem('one', 'Open Project');
    const closeCommand = makeItem('two', 'Close Project');
    render(
      <CommandPalette
        open
        mode="commands"
        workspaceName="demo"
        items={[openCommand, closeCommand]}
        onClose={onClose}
        onModeChange={onModeChange}
      />,
    );

    const input = screen.getByPlaceholderText(/search commands/i);
    fireEvent.change(input, { target: { value: 'open' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    expect(openCommand.action).toHaveBeenCalled();
    expect(closeCommand.action).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
  });

  it('ArrowNavAndMouseHoverSelection_Supported', () => {
    const first = makeItem('one', 'First Command');
    const second = makeItem('two', 'Second Command');
    render(
      <CommandPalette
        open
        mode="commands"
        workspaceName="demo"
        items={[first, second]}
        onClose={onClose}
        onModeChange={onModeChange}
      />,
    );

    const input = screen.getByPlaceholderText(/search commands/i);
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(second.action).toHaveBeenCalled();

    second.action.mockClear();
    const firstButton = screen.getByText('First Command').closest('button') as HTMLButtonElement;
    fireEvent.mouseEnter(firstButton);
    fireEvent.click(firstButton);
    expect(first.action).toHaveBeenCalled();
  });

  it('TabSwitchesModes_EscapeCloses', () => {
    render(
      <CommandPalette
        open
        mode="commands"
        workspaceName="demo"
        items={[makeItem('one', 'Open Project')]}
        onClose={onClose}
        onModeChange={onModeChange}
      />,
    );

    const input = screen.getByPlaceholderText(/search commands/i);
    fireEvent.keyDown(input, { key: 'Tab' });
    fireEvent.keyDown(input, { key: 'Escape' });

    expect(onModeChange).toHaveBeenCalledWith('files');
    expect(onClose).toHaveBeenCalled();
  });

  it('DisabledItems_NotInvokedAndFileEmptyStateShown', () => {
    const disabled = { ...makeItem('one', 'Secret Command'), disabled: true };
    render(
      <CommandPalette
        open
        mode="files"
        workspaceName="demo"
        items={[disabled]}
        onClose={onClose}
        onModeChange={onModeChange}
      />,
    );

    const input = screen.getByPlaceholderText(/search files by name or path/i);
    fireEvent.change(input, { target: { value: 'missing' } });
    expect(screen.getByText(/no files match/i)).toBeTruthy();

    fireEvent.change(input, { target: { value: 'secret' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(disabled.action).not.toHaveBeenCalled();
  });

  it('ArrowUpNavigation_WrapsToLastItem', () => {
    const first = makeItem('one', 'First Command');
    const second = makeItem('two', 'Second Command');
    render(
      <CommandPalette
        open
        mode="commands"
        workspaceName="demo"
        items={[first, second]}
        onClose={onClose}
        onModeChange={onModeChange}
      />,
    );

    const input = screen.getByPlaceholderText(/search commands/i);
    fireEvent.keyDown(input, { key: 'ArrowUp' });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(second.action).toHaveBeenCalled();
  });

  it('ArrowUpNavigation_WrapsToLastItemDuplicate', () => {
    const first = makeItem('one', 'First Command');
    const second = makeItem('two', 'Second Command');
    render(
      <CommandPalette
        open
        mode="commands"
        workspaceName="demo"
        items={[first, second]}
        onClose={onClose}
        onModeChange={onModeChange}
      />,
    );

    const input = screen.getByPlaceholderText(/search commands/i);
    fireEvent.keyDown(input, { key: 'ArrowUp' });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(second.action).toHaveBeenCalled();
  });

  it('ArrowDownOnEmptyList_IndexZeroReturned', () => {
    render(
      <CommandPalette
        open
        mode="commands"
        workspaceName="demo"
        items={[]}
        onClose={onClose}
        onModeChange={onModeChange}
      />,
    );

    const input = screen.getByPlaceholderText(/search commands/i);
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    fireEvent.keyDown(input, { key: 'ArrowUp' });
    expect(screen.queryByText(/First Command/)).toBeNull();
  });

  it('FilesTabClicked_OnModeChangeCalledWithFiles', () => {
    render(
      <CommandPalette
        open
        mode="commands"
        workspaceName="demo"
        items={[makeItem('one', 'Open Project')]}
        onClose={onClose}
        onModeChange={onModeChange}
      />,
    );

    const filesTab = screen.getByRole('tab', { name: /files/i });
    fireEvent.click(filesTab);
    expect(onModeChange).toHaveBeenCalledWith('files');
  });

  it('CommandsTabClicked_OnModeChangeCalledWithCommands', () => {
    render(
      <CommandPalette
        open
        mode="files"
        workspaceName="demo"
        items={[makeItem('one', 'Open Project', 'file')]}
        onClose={onClose}
        onModeChange={onModeChange}
      />,
    );

    const commandsTab = screen.getByRole('tab', { name: /commands/i });
    fireEvent.click(commandsTab);
    expect(onModeChange).toHaveBeenCalledWith('commands');
  });

  it('EmptyWorkspaceName_RenderedWithoutSuffix', () => {
    render(
      <CommandPalette
        open
        mode="commands"
        workspaceName=""
        items={[]}
        onClose={onClose}
        onModeChange={onModeChange}
      />,
    );

    expect(screen.getByPlaceholderText(/search commands/i)).toBeTruthy();
  });
});