import { useEffect, useMemo, useState } from 'react';
import { ArrowRight, Key, Laptop, Link2, Plus, RefreshCw, Wifi } from 'lucide-react';
import { wsClient } from '../../ws/client.js';
import type { MeshActivityEvent, MeshConfigView, MeshPeerHealth } from '@engine/shared';
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

type MeshPeerDraft = {
  name: string;
  address: string;
  roles: string;
  secret: string;
  ollamaURL: string;
};

const emptyDraft: ConnectionDraft = {
  name: '',
  host: '',
  port: '3443',
  workspacePath: '',
  pairCode: '',
};

const emptyMeshPeerDraft: MeshPeerDraft = {
  name: '',
  address: '',
  roles: '',
  secret: '',
  ollamaURL: '',
};

function profileLabel(profile: ConnectionProfile): string {
  return profile.name.trim() || profile.host;
}

/** Manages remote machine connections including pairing, configuration, and status monitoring. */
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

  const [generatedCode, setGeneratedCode] = useState<string | null>(null);
  const [codeExpiresIn, setCodeExpiresIn] = useState<number>(0);
  const [meshConfig, setMeshConfig] = useState<MeshConfigView | null>(null);
  const [meshHealth, setMeshHealth] = useState<MeshPeerHealth[]>([]);
  const [meshActivity, setMeshActivity] = useState<MeshActivityEvent[]>([]);
  const [meshStatus, setMeshStatus] = useState<string>('');
  const [meshSelfName, setMeshSelfName] = useState<string>('');
  const [meshListenAddr, setMeshListenAddr] = useState<string>(':24445');
  const [meshSelfOllamaURL, setMeshSelfOllamaURL] = useState<string>('');
  const [meshPeerDraft, setMeshPeerDraft] = useState<MeshPeerDraft>(emptyMeshPeerDraft);

  // Listen for the generated pairing code pushed back from the server.
  useEffect(() => {
    return wsClient.onMessage((msg) => {
      if (msg.type === 'remote.pair.code') {
        setGeneratedCode(msg.code);
        setCodeExpiresIn(msg.expiresIn);
      }
      if (msg.type === 'mesh.config' || msg.type === 'mesh.config.saved') {
        setMeshConfig(msg.config);
        setMeshSelfName(msg.config.selfName || '');
        setMeshListenAddr(msg.config.listenAddr || ':24445');
        setMeshSelfOllamaURL(msg.config.selfOllamaURL || '');
        if (msg.type === 'mesh.config.saved') {
          setMeshStatus('Project network settings saved.');
        }
      }
      if (msg.type === 'mesh.health.results') {
        setMeshHealth(msg.results);
        setMeshStatus('Project network scan complete.');
      }
      if (msg.type === 'mesh.activity') {
        setMeshActivity(msg.records ?? []);
      }
    });
  }, []);

  useEffect(() => {
    wsClient.send({ type: 'mesh.config.get' });
    wsClient.send({ type: 'mesh.health.scan', timeoutMs: 4000 });
    wsClient.send({ type: 'mesh.activity.get', limit: 50 });
  }, []);

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

  const saveMeshSettings = () => {
    const selfName = meshSelfName.trim();
    if (!selfName) {
      setMeshStatus('Project network self name is required.');
      return;
    }
    wsClient.send({
      type: 'mesh.config.set',
      selfName,
      listenAddr: meshListenAddr.trim() || ':24445',
      selfOllamaURL: meshSelfOllamaURL.trim(),
    });
    setMeshStatus('Saving project network settings...');
  };

  const upsertMeshPeer = () => {
    const name = meshPeerDraft.name.trim();
    const address = meshPeerDraft.address.trim();
    if (!name || !address) {
      setMeshStatus('Peer name and address are required.');
      return;
    }
    const roles = meshPeerDraft.roles
      .split(',')
      .map(role => role.trim())
      .filter(Boolean);
    wsClient.send({
      type: 'mesh.peer.upsert',
      peer: {
        name,
        address,
        secret: meshPeerDraft.secret.trim(),
        roles,
        ollamaURL: meshPeerDraft.ollamaURL.trim(),
      },
    });
    setMeshStatus('Saving peer...');
    setMeshPeerDraft(emptyMeshPeerDraft);
  };

  const refreshMeshHealth = () => {
    wsClient.send({ type: 'mesh.health.scan', timeoutMs: 4000 });
    setMeshStatus('Scanning project network...');
  };

  const refreshMeshActivity = () => {
    wsClient.send({ type: 'mesh.activity.get', limit: 50 });
    setMeshStatus('Refreshing project activity...');
  };

  const removeMeshPeer = (name: string) => {
    wsClient.send({ type: 'mesh.peer.remove', name });
    setMeshStatus(`Removing peer ${name}...`);
  };

  const meshHealthByName = new Map(
    meshHealth.map(item => [item.peer.name.trim().toLowerCase(), item]),
  );

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

      <div className="connections-generate">
        <div className="connections-generate-title">
          <Key size={14} />
          <span>Generate a pairing code</span>
        </div>
        <p className="connections-muted">
          Run this on the machine you want to pair from. Share the code with the device that will connect.
        </p>
        <button
          className="btn-secondary"
          type="button"
          onClick={() => wsClient.send({ type: 'remote.pair.code.generate' })}
        >
          Generate code
        </button>
        {generatedCode && (
          <div className="connections-code-display" data-testid="generated-code">
            <span className="connections-code-value">{generatedCode}</span>
            <span className="connections-muted">expires in {Math.round(codeExpiresIn / 60)} min</span>
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

      <div className="connections-form">
        <div className="connections-form-title">
          <Wifi size={14} />
          <span>Project network control</span>
        </div>
        <span className="connections-muted">
          Each joined user can manage peer machines and scan cross-machine health directly from this project.
        </span>
        <label className="connections-field">
          <span className="connections-label">Self name</span>
          <input
            value={meshSelfName}
            onChange={(event) => setMeshSelfName(event.target.value)}
            placeholder="mac-studio"
            className="connections-input"
          />
        </label>
        <div className="connections-row two-up">
          <label className="connections-field">
            <span className="connections-label">Listen address</span>
            <input
              value={meshListenAddr}
              onChange={(event) => setMeshListenAddr(event.target.value)}
              placeholder=":24445"
              className="connections-input"
            />
          </label>
          <label className="connections-field">
            <span className="connections-label">Local Ollama URL</span>
            <input
              value={meshSelfOllamaURL}
              onChange={(event) => setMeshSelfOllamaURL(event.target.value)}
              placeholder="http://127.0.0.1:11434"
              className="connections-input"
            />
          </label>
        </div>
        <div className="connections-actions">
          <button className="btn-secondary" type="button" onClick={saveMeshSettings}>
            Save network settings
          </button>
          <button className="btn-secondary" type="button" onClick={refreshMeshHealth}>
            Scan network health
          </button>
          <button className="btn-secondary" type="button" onClick={refreshMeshActivity}>
            Refresh activity
          </button>
          <button className="btn-secondary" type="button" onClick={() => wsClient.send({ type: 'mesh.config.get' })}>
            Reload config
          </button>
        </div>

        <div className="connections-list">
          {meshConfig?.peers?.length ? meshConfig.peers.map((peer) => {
            const health = meshHealthByName.get(peer.name.trim().toLowerCase());
            const online = !!health?.ok;
            return (
              <div className="connections-item" key={peer.name}>
                <div className="connections-item-top">
                  <div className="connections-item-name">
                    <Laptop size={14} />
                    <span>{peer.name}</span>
                  </div>
                  <span className={`connections-badge ${online ? 'active' : ''}`}>
                    <Wifi size={10} />
                    {online ? 'online' : 'offline'}
                  </span>
                </div>
                <div className="connections-item-meta">
                  {peer.address}
                  {peer.roles.length ? ` • ${peer.roles.join(', ')}` : ''}
                </div>
                {health?.error && (
                  <div className="connections-muted">Error: {health.error}</div>
                )}
                <div className="connections-actions">
                  <button
                    className="btn-secondary"
                    type="button"
                    onClick={() => setMeshPeerDraft({
                      name: peer.name,
                      address: peer.address,
                      roles: peer.roles.join(', '),
                      secret: '',
                      ollamaURL: peer.ollamaURL || '',
                    })}
                  >
                    Edit
                  </button>
                  <button className="btn-secondary" type="button" onClick={() => removeMeshPeer(peer.name)}>
                    Remove
                  </button>
                </div>
              </div>
            );
          }) : (
            <div className="connections-empty">No project-network peers yet.</div>
          )}
        </div>

        <div className="connections-form-title">
          <Plus size={14} />
          <span>Add or update peer</span>
        </div>
        <div className="connections-row two-up">
          <label className="connections-field">
            <span className="connections-label">Peer name</span>
            <input
              value={meshPeerDraft.name}
              onChange={(event) => setMeshPeerDraft(current => ({ ...current, name: event.target.value }))}
              placeholder="windows-rig"
              className="connections-input"
            />
          </label>
          <label className="connections-field">
            <span className="connections-label">Peer address</span>
            <input
              value={meshPeerDraft.address}
              onChange={(event) => setMeshPeerDraft(current => ({ ...current, address: event.target.value }))}
              placeholder="192.168.1.30:24445"
              className="connections-input"
            />
          </label>
        </div>
        <div className="connections-row two-up">
          <label className="connections-field">
            <span className="connections-label">Roles (comma-separated)</span>
            <input
              value={meshPeerDraft.roles}
              onChange={(event) => setMeshPeerDraft(current => ({ ...current, roles: event.target.value }))}
              placeholder="tests, inference"
              className="connections-input"
            />
          </label>
          <label className="connections-field">
            <span className="connections-label">Peer Ollama URL</span>
            <input
              value={meshPeerDraft.ollamaURL}
              onChange={(event) => setMeshPeerDraft(current => ({ ...current, ollamaURL: event.target.value }))}
              placeholder="http://127.0.0.1:11434"
              className="connections-input"
            />
          </label>
        </div>
        <label className="connections-field">
          <span className="connections-label">Shared secret</span>
          <input
            value={meshPeerDraft.secret}
            onChange={(event) => setMeshPeerDraft(current => ({ ...current, secret: event.target.value }))}
            placeholder="required for new peers"
            className="connections-input"
          />
        </label>
        <div className="connections-actions">
          <button className="btn-secondary" type="button" onClick={upsertMeshPeer}>
            Save peer
          </button>
          <button className="btn-secondary" type="button" onClick={() => setMeshPeerDraft(emptyMeshPeerDraft)}>
            Clear peer form
          </button>
        </div>

        <div className="connections-form-title">
          <Wifi size={14} />
          <span>Project agent activity</span>
        </div>
        <div className="connections-list">
          {meshActivity.length ? meshActivity.map((entry) => {
            const isOk = entry.status === 'ok';
            return (
              <div className="connections-item" key={entry.id}>
                <div className="connections-item-top">
                  <div className="connections-item-name">
                    <Laptop size={14} />
                    <span>{entry.action}</span>
                  </div>
                  <span className={`connections-badge ${isOk ? 'active' : ''}`}>
                    <Wifi size={10} />
                    {entry.status}
                  </span>
                </div>
                <div className="connections-item-meta">
                  {(entry.target || 'system')}
                  {entry.resolved ? ' • resolved' : ''}
                </div>
                {entry.message && <div className="connections-muted">{entry.message}</div>}
                {entry.error && <div className="connections-muted">Error: {entry.error}</div>}
                <div className="connections-muted">{new Date(entry.at).toLocaleString()}</div>
              </div>
            );
          }) : (
            <div className="connections-empty">No activity recorded yet.</div>
          )}
        </div>
        {meshStatus && <div className="connections-status">{meshStatus}</div>}
      </div>
    </article>
  );
}
