import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  loadConnectionProfiles,
  loadActiveConnectionProfileId,
  loadActiveConnectionProfile,
  saveConnectionProfile,
  deleteConnectionProfile,
  setActiveConnectionProfile,
  clearConnectionProfiles,
  pairConnectionCode,
} from '../connectionProfiles.js';

const PROFILES_KEY = 'engine.connectionProfiles';
const ACTIVE_KEY = 'engine.activeConnectionProfileId';

const makeProfile = (overrides: Partial<Parameters<typeof saveConnectionProfile>[0]> = {}) =>
  saveConnectionProfile({
    name: 'Test Machine',
    host: '10.0.0.1',
    port: '3443',
    workspacePath: '/home/user/project',
    token: 'tok123',
    ...overrides,
  });

describe('connectionProfiles', () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  afterEach(() => {
    window.localStorage.clear();
  });

  // ─── Load ─────────────────────────────────────────────────────────────────

  describe('loadConnectionProfiles', () => {
    it('returns an empty array when nothing is stored', () => {
      expect(loadConnectionProfiles()).toEqual([]);
    });

    it('returns profiles sorted by updatedAt descending', () => {
      const old = makeProfile({ name: 'Old', updatedAt: '2025-01-01T00:00:00.000Z' });
      const recent = makeProfile({ name: 'Recent', updatedAt: '2026-01-01T00:00:00.000Z' });
      const list = loadConnectionProfiles();
      expect(list[0].id).toBe(recent.id);
      expect(list[1].id).toBe(old.id);
    });
  });

  describe('loadActiveConnectionProfileId', () => {
    it('returns null when nothing is stored', () => {
      expect(loadActiveConnectionProfileId()).toBeNull();
    });

    it('returns the stored id after save', () => {
      const profile = makeProfile();
      expect(loadActiveConnectionProfileId()).toBe(profile.id);
    });
  });

  describe('loadActiveConnectionProfile', () => {
    it('returns null when there is no active profile', () => {
      expect(loadActiveConnectionProfile()).toBeNull();
    });

    it('returns the matching profile after save', () => {
      const profile = makeProfile({ name: 'My Server' });
      const active = loadActiveConnectionProfile();
      expect(active?.id).toBe(profile.id);
      expect(active?.name).toBe('My Server');
    });

    it('returns null when the active id no longer exists in the profiles list', () => {
      window.localStorage.setItem(ACTIVE_KEY, 'ghost-id');
      expect(loadActiveConnectionProfile()).toBeNull();
    });
  });

  // ─── Save ─────────────────────────────────────────────────────────────────

  describe('saveConnectionProfile', () => {
    it('creates a new profile and returns it', () => {
      const profile = makeProfile({ name: 'New Box' });
      expect(profile.id).toBeTruthy();
      expect(profile.name).toBe('New Box');
      expect(profile.host).toBe('10.0.0.1');
    });

    it('auto-generates an id when none supplied', () => {
      const profile = makeProfile();
      expect(profile.id.length).toBeGreaterThan(0);
    });

    it('trims whitespace from name, host, port, workspacePath, token', () => {
      const profile = saveConnectionProfile({
        name: '  Padded  ',
        host: ' 192.168.1.1 ',
        port: ' 3443 ',
        workspacePath: ' /tmp/p ',
        token: ' tok ',
      });
      expect(profile.name).toBe('Padded');
      expect(profile.host).toBe('192.168.1.1');
      expect(profile.port).toBe('3443');
      expect(profile.workspacePath).toBe('/tmp/p');
      expect(profile.token).toBe('tok');
    });

    it('defaults port to 3443 when blank', () => {
      const profile = saveConnectionProfile({
        name: 'X', host: 'x.local', port: '', workspacePath: '/', token: 'tok',
      });
      expect(profile.port).toBe('3443');
    });

    it('falls back to host as name when name is blank', () => {
      const profile = saveConnectionProfile({
        name: '', host: 'server.local', port: '3443', workspacePath: '/', token: 'tok',
      });
      expect(profile.name).toBe('server.local');
    });

    it('falls back to "Machine" when both name and host are blank', () => {
      const profile = saveConnectionProfile({
        name: '', host: '', port: '3443', workspacePath: '/', token: 'tok',
      });
      expect(profile.name).toBe('Machine');
    });

    it('updates an existing profile when the same id is supplied', () => {
      const original = makeProfile({ name: 'Original' });
      const updated = saveConnectionProfile({ ...original, name: 'Updated' });
      expect(updated.id).toBe(original.id);
      const list = loadConnectionProfiles();
      expect(list.find(p => p.id === original.id)?.name).toBe('Updated');
      expect(list).toHaveLength(1);
    });

    it('sets the saved profile as active', () => {
      const profile = makeProfile();
      expect(loadActiveConnectionProfileId()).toBe(profile.id);
    });
  });

  // ─── Delete ───────────────────────────────────────────────────────────────

  describe('deleteConnectionProfile', () => {
    it('removes the target profile from the list', () => {
      const profile = makeProfile();
      const remaining = deleteConnectionProfile(profile.id);
      expect(remaining).toHaveLength(0);
      expect(loadConnectionProfiles()).toHaveLength(0);
    });

    it('returns the updated list after deletion', () => {
      makeProfile({ name: 'A' });
      const b = makeProfile({ name: 'B' });
      const remaining = deleteConnectionProfile(b.id);
      expect(remaining.some(p => p.id === b.id)).toBe(false);
      expect(remaining).toHaveLength(1);
    });

    it('clears active id when the active profile is deleted', () => {
      const profile = makeProfile();
      deleteConnectionProfile(profile.id);
      expect(loadActiveConnectionProfileId()).toBeNull();
    });

    it('does not change the active id when a different profile is deleted', () => {
      const a = makeProfile({ name: 'A' });
      const b = makeProfile({ name: 'B' });
      const activeId = loadActiveConnectionProfileId();
      deleteConnectionProfile(a.id === activeId ? b.id : a.id);
      expect(loadActiveConnectionProfileId()).toBe(activeId);
    });
  });

  // ─── Set active ───────────────────────────────────────────────────────────

  describe('setActiveConnectionProfile', () => {
    it('persists the given id to localStorage', () => {
      setActiveConnectionProfile('abc-123');
      expect(window.localStorage.getItem(ACTIVE_KEY)).toBe('abc-123');
    });

    it('removes the entry when null is supplied', () => {
      setActiveConnectionProfile('abc-123');
      setActiveConnectionProfile(null);
      expect(window.localStorage.getItem(ACTIVE_KEY)).toBeNull();
    });
  });

  // ─── Clear ────────────────────────────────────────────────────────────────

  describe('clearConnectionProfiles', () => {
    it('removes both profiles and active id from localStorage', () => {
      makeProfile();
      clearConnectionProfiles();
      expect(window.localStorage.getItem(PROFILES_KEY)).toBeNull();
      expect(window.localStorage.getItem(ACTIVE_KEY)).toBeNull();
      expect(loadConnectionProfiles()).toHaveLength(0);
    });
  });

  // ─── Pairing ──────────────────────────────────────────────────────────────

  describe('pairConnectionCode', () => {
    afterEach(() => {
      vi.restoreAllMocks();
    });

    it('POSTs to /remote/pair with the pairing code and returns the result', async () => {
      const payload = { ok: true, token: 'desktop-tok', workspacePath: '/home/user' };
      const fetchStub = vi.spyOn(globalThis, 'fetch').mockResolvedValue({
        ok: true,
        json: async () => payload,
      } as Response);

      const result = await pairConnectionCode('engine.dev', '3443', 'ABC-123');
      expect(fetchStub).toHaveBeenCalledWith(
        'https://engine.dev:3443/remote/pair',
        expect.objectContaining({
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ code: 'ABC-123' }),
        }),
      );
      expect(result).toEqual(payload);
    });

    it('returns an error object when the server responds with a non-ok status', async () => {
      vi.spyOn(globalThis, 'fetch').mockResolvedValue({
        ok: false,
        status: 401,
        json: async () => ({ ok: false, error: 'Invalid code' }),
      } as Response);

      const result = await pairConnectionCode('engine.dev', '3443', 'BAD');
      expect(result.ok).toBe(false);
      expect(result.error).toContain('Invalid code');
    });

    it('returns a generic error when the response body cannot be parsed as json', async () => {
      vi.spyOn(globalThis, 'fetch').mockResolvedValue({
        ok: false,
        status: 500,
        json: () => Promise.reject(new Error('not json')),
      } as Response);

      const result = await pairConnectionCode('engine.dev', '3443', 'BAD');
      expect(result.ok).toBe(false);
      expect(typeof result.error).toBe('string');
    });
  });
});
