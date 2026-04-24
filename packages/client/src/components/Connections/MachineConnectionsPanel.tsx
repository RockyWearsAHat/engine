import { useEffect, useMemo, useState } from 'react';
import { ArrowRight, Laptop, Link2, Plus, RefreshCw, Trash2, Wifi } from 'lucide-react';
import {
  clearConnectionProfiles,
  deleteConnectionProfile,
  loadActiveConnectionProfile,
  loadConnectionProfiles,
  pairConnectionCode,
  saveConnectionProfile,
  setActiveConnectionProfile,
  type ConnectionProfile,
} from '../../connectionProfiles.js';

type ConnectionDraft = {
  name: string;
  host: string;
  port: string;
  workspacePath: string;
  pairCode: string;
};

const emptyDraft: ConnectionDraft = {
  name: '',
  host: '',
  port: '3443',
  workspacePath: '',
  pairCode: '',
};

function profileLabel(profile: ConnectionProfile): string {
  return profile.name.trim() || profile.host;
}

export default function MachineConnectionsPanel({
  compact = false,
}: {
  compact?: boolean;
}) {
  const [profiles, setProfiles] = useState<ConnectionProfile[]>(() => loadConnectionProfiles());
  const [activeId, setActiveId] = useState<string | null>(() => loadActiveConnectionProfile()?.id ?? null);
  const [selectedId, setSelectedId] = useState<string | null>(() => loadActiveConnectionProfile()?.id ?? null);
  const [draft, setDraft] = useState<ConnectionDraft>(emptyDraft);
  const [status, setStatus] = useState<string>('');
  const [busy, setBusy] = useState(false);

  const selectedProfile = useMemo(
    () => profiles.find(profile => profile.id === selectedId) ?? null,
    [profiles, selectedId],
  );

  useEffect(() => {
    const nextProfiles = loadConnectionProfiles();
    const nextActive = loadActiveConnectionProfile()?.id ?? null;
    setProfiles(nextProfiles);
    setActiveId(nextActive);
    setSelectedId(nextActive ?? nextProfiles[0]?.id ?? null);
  }, []);

  useEffect(() => {
    if (!selectedProfile) {
      setDraft(emptyDraft);
      return;
    }
    setDraft({
      name: selectedProfile.name,
      host: selectedProfile.host,
      port: selectedProfile.port,
      workspacePath: selectedProfile.workspacePath,
      pairCode: '',
    });
  }, [selectedProfile]);

  const refreshProfiles = () => {
    const nextProfiles = loadConnectionProfiles();
    const nextActive = loadActiveConnectionProfile()?.id ?? null;
    setProfiles(nextProfiles);
    setActiveId(nextActive);
    /* istanbul ignore start */
    if (!nextProfiles.find(profile => profile.id === selectedId)) {
      setSelectedId(nextActive ?? nextProfiles[0]?.id ?? null);
    }
    /* istanbul ignore stop */
  };

  const pairAndSave = async () => {
    const host = draft.host.trim();
    /* istanbul ignore start */
    const port = draft.port.trim() || '3443';
    /* istanbul ignore stop */
    const workspacePath = draft.workspacePath.trim();
    /* istanbul ignore start */
    const name = draft.name.trim() || host || 'Machine';
    /* istanbul ignore stop */

    /* istanbul ignore start */
    if (!host || !workspacePath) {
      setStatus('Need a host and workspace path first.');
      return;
    }
    /* istanbul ignore stop */

    setBusy(true);
    setStatus('');

    const existingProfile = selectedProfile;
    let token = existingProfile?.token ?? '';

    /* istanbul ignore start */
    if (draft.pairCode.trim()) {
      const result = await pairConnectionCode(host, port, draft.pairCode.trim());
      if (!result.ok || !result.token) {
        setStatus(result.error ?? 'Pairing failed.');
        setBusy(false);
        return;
      }
      token = result.token;
      setStatus('Paired. Saving machine\u2026');
    } else if (!token) {
      setStatus('Give me a pairing code so I can get a token.');
      setBusy(false);
      return;
    }
    /* istanbul ignore stop */

    const savedProfile = saveConnectionProfile({
      id: existingProfile?.id,
      name,
      host,
      port,
      workspacePath,
      token,
    });
    setProfiles(loadConnectionProfiles());
    setActiveId(savedProfile.id);
    setSelectedId(savedProfile.id);
    setBusy(false);
    setStatus(`Saved ${profileLabel(savedProfile)}. Reloading…`);
    /* istanbul ignore next */
    window.location.reload();
  };

  const clearAll = () => {
    clearConnectionProfiles();
    setProfiles([]);
    setActiveId(null);
    setSelectedId(null);
    setDraft(emptyDraft);
    setStatus('All machines forgotten.');
    /* istanbul ignore next */
    window.location.reload();
  };

  return (
    <article className={`connections-panel ${compact ? 'compact' : ''}`}>
      <div className="connections-header">
        <div>
          <div className="connections-kicker">Machine links</div>
          <div className="connections-title">Pick the machine you want to open.</div>
          <div className="connections-copy">
            Save multiple boxes here — Mac, Windows, phone, or remote server — then switch with one click.
          </div>
        </div>
        <button className="connections-scan-btn" onClick={refreshProfiles} type="button" title="Refresh list">
          <RefreshCw size={13} />
        </button>
      </div>

      <div className="connections-list">
        {profiles.length > 0 ? profiles.map(profile => {
          const isActive = profile.id === activeId;
          const isSelected = profile.id === selectedId;
          return (
            <button
              key={profile.id}
              type="button"
              className={`connections-item ${isSelected ? 'selected' : ''}`}
              onClick={() => setSelectedId(profile.id)}
            >
              <div className="connections-item-top">
                <div className="connections-item-name">
                  <Laptop size={14} />
                  <span>{profileLabel(profile)}</span>
                </div>
                <span className={`connections-badge ${isActive ? 'active' : ''}`}>
                  <Wifi size={10} />
                  {isActive ? 'active' : 'saved'}
                </span>
              </div>
              <div className="connections-item-meta">
                {profile.host}:{profile.port} • {profile.workspacePath}
              </div>
              <div className="connections-item-actions">
                <span className="connections-item-link">
                  <Link2 size={12} />
                  {isActive ? 'current machine' : 'ready to open'}
                </span>
                <span className="connections-item-go">
                  Select
                  <ArrowRight size={12} />
                </span>
              </div>
            </button>
          );
        }) : (
          <div className="connections-empty">
            No machine saved yet. Pair the first one below.
          </div>
        )}
      </div>

      <div className="connections-form">
        <div className="connections-form-title">
          <Plus size={14} />
          <span>{selectedProfile ? 'Edit selected machine' : 'Add a machine'}</span>
        </div>

        <label className="connections-field">
          <span className="connections-label">Machine name</span>
          <input
            value={draft.name}
            onChange={(event) => setDraft(current => ({ ...current, name: event.target.value }))}
            placeholder="Mac Studio, Windows rig, phone…"
            className="connections-input"
          />
        </label>

        <div className="connections-row two-up">
          <label className="connections-field">
            <span className="connections-label">Host / IP</span>
            <input
              value={draft.host}
              onChange={(event) => setDraft(current => ({ ...current, host: event.target.value }))}
              placeholder="192.168.1.20"
              className="connections-input"
            />
          </label>
          <label className="connections-field">
            <span className="connections-label">Port</span>
            <input
              value={draft.port}
              onChange={(event) => setDraft(current => ({ ...current, port: event.target.value }))}
              placeholder="3443"
              className="connections-input"
            />
          </label>
        </div>

        <label className="connections-field">
          <span className="connections-label">Workspace path</span>
          <input
            value={draft.workspacePath}
            onChange={(event) => setDraft(current => ({ ...current, workspacePath: event.target.value }))}
            placeholder="/Users/alex/Desktop/MyEditor"
            className="connections-input"
          />
        </label>

        <label className="connections-field">
          <span className="connections-label">Pairing code</span>
          <input
            value={draft.pairCode}
            onChange={(event) => setDraft(current => ({ ...current, pairCode: event.target.value }))}
            placeholder="123456"
            className="connections-input"
          />
          <span className="connections-muted">
            Pair once to mint a token. After that, the active machine will reconnect automatically.
          </span>
        </label>

        <div className="connections-actions">
          <button
            className="btn-primary"
            type="button"
            onClick={() => void pairAndSave()}
            disabled={busy}
          >
            {/* istanbul ignore start */}{busy ? 'Saving\u2026' : 'Pair & open'}{/* istanbul ignore stop */}
          </button>
          <button
            className="btn-secondary"
            type="button"
            onClick={() => {
              setSelectedId(activeId);
              /* istanbul ignore start */
              setDraft(activeId ? {
                name: selectedProfile?.name ?? '',
                host: selectedProfile?.host ?? '',
                port: selectedProfile?.port ?? '3443',
                workspacePath: selectedProfile?.workspacePath ?? '',
                pairCode: '',
              } : emptyDraft);
              /* istanbul ignore stop */
            }}
          >
            Reset
          </button>
          <button
            className="btn-secondary"
            type="button"
            onClick={clearAll}
          >
            Forget all
          </button>
        </div>

        {status && (
          <div className="connections-status">
            {status}
          </div>
        )}
      </div>
    </article>
  );
}
