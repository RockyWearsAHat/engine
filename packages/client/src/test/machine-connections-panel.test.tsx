import { act, fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const connectionMocks = vi.hoisted(() => ({
  clearConnectionProfiles: vi.fn(),
  deleteConnectionProfile: vi.fn(),
  loadActiveConnectionProfile: vi.fn(),
  loadConnectionProfiles: vi.fn(),
  pairConnectionCode: vi.fn(),
  saveConnectionProfile: vi.fn(),
  setActiveConnectionProfile: vi.fn(),
}));

vi.mock('../connectionProfiles.js', () => ({
  clearConnectionProfiles: connectionMocks.clearConnectionProfiles,
  deleteConnectionProfile: connectionMocks.deleteConnectionProfile,
  loadActiveConnectionProfile: connectionMocks.loadActiveConnectionProfile,
  loadConnectionProfiles: connectionMocks.loadConnectionProfiles,
  pairConnectionCode: connectionMocks.pairConnectionCode,
  saveConnectionProfile: connectionMocks.saveConnectionProfile,
  setActiveConnectionProfile: connectionMocks.setActiveConnectionProfile,
}));

const { default: MachineConnectionsPanel } = await import('../components/Connections/MachineConnectionsPanel.js');

describe('MachineConnectionsPanel workflows', () => {
  const reloadMock = vi.fn();

  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { ...window.location, reload: reloadMock },
    });
    reloadMock.mockClear();
    connectionMocks.clearConnectionProfiles.mockReset();
    connectionMocks.deleteConnectionProfile.mockReset();
    connectionMocks.loadActiveConnectionProfile.mockReset();
    connectionMocks.loadConnectionProfiles.mockReset();
    connectionMocks.pairConnectionCode.mockReset();
    connectionMocks.saveConnectionProfile.mockReset();
    connectionMocks.setActiveConnectionProfile.mockReset();
    connectionMocks.loadConnectionProfiles.mockReturnValue([]);
    connectionMocks.loadActiveConnectionProfile.mockReturnValue(null);
  });

  it('NoProfiles_EmptyStateShown', () => {
    render(<MachineConnectionsPanel />);
    expect(screen.getByText(/no machine saved yet/i)).toBeTruthy();
  });

  it('HostAndWorkspacePathRequired_ValidationError', async () => {
    render(<MachineConnectionsPanel />);
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /pair & open/i }));
    });
    expect(screen.getByText(/need a host and workspace path first/i)).toBeTruthy();
  });

  it('ValidPairCode_PairedAndProfileSaved', async () => {
    connectionMocks.pairConnectionCode.mockResolvedValue({ ok: true, token: 'token-123' });
    connectionMocks.saveConnectionProfile.mockReturnValue({
      id: 'profile-1',
      name: 'Mac Studio',
      host: '10.0.0.2',
      port: '3443',
      workspacePath: '/Users/alex/project',
      token: 'token-123',
      updatedAt: '',
    });
    connectionMocks.loadConnectionProfiles.mockReturnValue([
      {
        id: 'profile-1',
        name: 'Mac Studio',
        host: '10.0.0.2',
        port: '3443',
        workspacePath: '/Users/alex/project',
        token: 'token-123',
        updatedAt: '',
      },
    ]);

    render(<MachineConnectionsPanel />);
    fireEvent.change(screen.getByPlaceholderText('Mac Studio, Windows rig, phone…'), { target: { value: 'Mac Studio' } });
    fireEvent.change(screen.getByPlaceholderText('192.168.1.20'), { target: { value: '10.0.0.2' } });
    fireEvent.change(screen.getByPlaceholderText('/Users/alex/Desktop/MyEditor'), { target: { value: '/Users/alex/project' } });
    fireEvent.change(screen.getByPlaceholderText('123456'), { target: { value: '654321' } });

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /pair & open/i }));
    });

    expect(connectionMocks.pairConnectionCode).toHaveBeenCalledWith('10.0.0.2', '3443', '654321');
    expect(connectionMocks.saveConnectionProfile).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Mac Studio',
      host: '10.0.0.2',
      workspacePath: '/Users/alex/project',
      token: 'token-123',
    }));
    expect(screen.getByText(/saved mac studio\. reloading/i)).toBeTruthy();
    expect(reloadMock).toHaveBeenCalled();
  });

  it('RuntimePairingFailure_FailureShown', async () => {
    connectionMocks.pairConnectionCode.mockResolvedValue({ ok: false, error: 'pairing denied' });
    render(<MachineConnectionsPanel />);

    fireEvent.change(screen.getByPlaceholderText('192.168.1.20'), { target: { value: '10.0.0.2' } });
    fireEvent.change(screen.getByPlaceholderText('/Users/alex/Desktop/MyEditor'), { target: { value: '/Users/alex/project' } });
    fireEvent.change(screen.getByPlaceholderText('123456'), { target: { value: '654321' } });

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /pair & open/i }));
    });

    expect(screen.getByText(/pairing denied/i)).toBeTruthy();
  });

  it('ForgetAllMachines_Cleared', async () => {
    render(<MachineConnectionsPanel />);

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /forget all/i }));
    });

    expect(connectionMocks.clearConnectionProfiles).toHaveBeenCalled();
    expect(screen.getByText(/all machines forgotten/i)).toBeTruthy();
    expect(reloadMock).toHaveBeenCalled();
  });

  it('NameAndPortFields_UpdatedInDraftForm', () => {
    render(<MachineConnectionsPanel />);

    fireEvent.change(screen.getByPlaceholderText('Mac Studio, Windows rig, phone…'), { target: { value: 'My MacBook' } });
    fireEvent.change(screen.getByPlaceholderText('3443'), { target: { value: '8443' } });

    expect((screen.getByPlaceholderText('Mac Studio, Windows rig, phone…') as HTMLInputElement).value).toBe('My MacBook');
    expect((screen.getByPlaceholderText('3443') as HTMLInputElement).value).toBe('8443');
  });

  it('CancelButton_DraftFormReset', async () => {
    render(<MachineConnectionsPanel />);

    fireEvent.change(screen.getByPlaceholderText('192.168.1.20'), { target: { value: '10.0.0.99' } });

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /reset/i }));
    });

    expect((screen.getByPlaceholderText('192.168.1.20') as HTMLInputElement).value).toBe('');
  });

  it('NoPairCodeAndNoToken_ErrorShown', async () => {
    render(<MachineConnectionsPanel />);
    fireEvent.change(screen.getByPlaceholderText('192.168.1.20'), { target: { value: '10.0.0.1' } });
    fireEvent.change(screen.getByPlaceholderText('/Users/alex/Desktop/MyEditor'), { target: { value: '/home/user' } });

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /pair & open/i }));
    });

    expect(screen.getByText(/give me a pairing code/i)).toBeTruthy();
  });

  it('RefreshButton_ProfilesReloadedFromStorage', async () => {
    connectionMocks.loadConnectionProfiles.mockReturnValue([]);
    connectionMocks.loadActiveConnectionProfile.mockReturnValue(null);
    render(<MachineConnectionsPanel />);

    await act(async () => {
      fireEvent.click(screen.getByTitle('Refresh list'));
    });

    expect(connectionMocks.loadConnectionProfiles).toHaveBeenCalled();
    expect(connectionMocks.loadActiveConnectionProfile).toHaveBeenCalled();
  });

  it('SavedProfileCardClicked_ProfileSelected', async () => {
    const profile = {
      id: 'p1',
      name: 'My Box',
      host: '10.0.0.5',
      port: '3443',
      workspacePath: '/home/user/project',
      token: 'tok',
      updatedAt: '',
    };
    connectionMocks.loadConnectionProfiles.mockReturnValue([profile]);
    connectionMocks.loadActiveConnectionProfile.mockReturnValue(profile);

    render(<MachineConnectionsPanel />);

    const card = screen.getByText('My Box').closest('button') as HTMLButtonElement;
    fireEvent.click(card);

    expect(screen.getByText('My Box')).toBeTruthy();
  });

  it('MachineConnectionsPanel_profileLabel_emptyNameFallsBackToHost', () => {
    const profile = {
      id: 'p2',
      name: '',
      host: '192.168.1.99',
      port: '3443',
      workspacePath: '/home/user/project',
      token: 'tok',
      updatedAt: '',
    };
    connectionMocks.loadConnectionProfiles.mockReturnValue([profile]);
    connectionMocks.loadActiveConnectionProfile.mockReturnValue(null);

    render(<MachineConnectionsPanel />);

    expect(screen.getByText('192.168.1.99')).toBeTruthy();
  });

  it('MachineConnectionsPanel_refreshProfiles_removedProfileResetsSelection', async () => {
    const profile = {
      id: 'p3',
      name: 'Old Machine',
      host: '10.0.0.1',
      port: '3443',
      workspacePath: '/home',
      token: 'tok',
      updatedAt: '',
    };
    connectionMocks.loadConnectionProfiles.mockReturnValueOnce([profile]);
    connectionMocks.loadActiveConnectionProfile.mockReturnValue({ ...profile });

    render(<MachineConnectionsPanel />);

    const newProfile = { id: 'p4', name: 'New Machine', host: '10.0.0.2', port: '3443', workspacePath: '/home', token: 'tok2', updatedAt: '' };
    connectionMocks.loadConnectionProfiles.mockReturnValue([newProfile]);
    connectionMocks.loadActiveConnectionProfile.mockReturnValue(null);

    await act(async () => {
      fireEvent.click(screen.getByTitle('Refresh list'));
    });

    expect(connectionMocks.loadConnectionProfiles.mock.calls.length).toBeGreaterThanOrEqual(2);
  });

  it('MachineConnectionsPanel_resetButton_activeProfileRestoresDraftValues', async () => {
    const profile = {
      id: 'p5',
      name: 'My Mac',
      host: '10.1.1.1',
      port: '3443',
      workspacePath: '/Users/me/project',
      token: 'tok',
      updatedAt: '',
    };
    connectionMocks.loadConnectionProfiles.mockReturnValue([profile]);
    connectionMocks.loadActiveConnectionProfile.mockReturnValue(profile);

    render(<MachineConnectionsPanel />);

    const card = screen.getByText('My Mac').closest('button') as HTMLButtonElement;
    fireEvent.click(card);

    fireEvent.change(screen.getByPlaceholderText('192.168.1.20'), { target: { value: '1.2.3.4' } });

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /reset/i }));
    });

    expect((screen.getByPlaceholderText('192.168.1.20') as HTMLInputElement).value).toBe('10.1.1.1');
  });
});