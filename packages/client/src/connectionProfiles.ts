import type { PairingResult } from '@engine/shared';

const profilesStorageKey = 'engine.connectionProfiles';
const activeProfileStorageKey = 'engine.activeConnectionProfileId';

export interface ConnectionProfile {
  id: string;
  name: string;
  host: string;
  port: string;
  workspacePath: string;
  token: string;
  updatedAt: string;
}

function canUseStorage(): boolean {
  return typeof window !== 'undefined';
}

function readJson<T>(key: string): T | null {
  /* istanbul ignore start */
  if (!canUseStorage()) {
    return null;
  }
  /* istanbul ignore stop */

  try {
    const raw = window.localStorage.getItem(key);
    if (!raw) {
      return null;
    }
    return JSON.parse(raw) as T;
  /* istanbul ignore start */
  } catch {
    return null;
  }
  /* istanbul ignore stop */
}

function writeJson(key: string, value: unknown): boolean {
  /* istanbul ignore start */
  if (!canUseStorage()) {
    return false;
  }
  /* istanbul ignore stop */

  try {
    window.localStorage.setItem(key, JSON.stringify(value));
    return true;
  /* istanbul ignore start */
  } catch {
    return false;
  }
  /* istanbul ignore stop */
}

function writeString(key: string, value: string | null): boolean {
  /* istanbul ignore start */
  if (!canUseStorage()) {
    return false;
  }
  /* istanbul ignore stop */

  try {
    if (value && value.trim()) {
      window.localStorage.setItem(key, value.trim());
    } else {
      window.localStorage.removeItem(key);
    }
    return true;
  /* istanbul ignore start */
  } catch {
    return false;
  }
  /* istanbul ignore stop */
}

function generateId(): string {
  /* istanbul ignore start */
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID();
  }
  /* istanbul ignore stop */
  /* istanbul ignore start */
  return `connection_${Date.now()}_${Math.random().toString(16).slice(2)}`;
  /* istanbul ignore stop */
}

export function loadConnectionProfiles(): ConnectionProfile[] {
  const stored = readJson<ConnectionProfile[]>(profilesStorageKey) ?? [];
  return [...stored].sort((left, right) => right.updatedAt.localeCompare(left.updatedAt));
}

export function loadActiveConnectionProfileId(): string | null {
  /* istanbul ignore start */
  if (!canUseStorage()) {
    return null;
  }
  /* istanbul ignore stop */
  try {
    const value = window.localStorage.getItem(activeProfileStorageKey)?.trim();
    return value ? value : null;
  /* istanbul ignore start */
  } catch {
    return null;
  }
  /* istanbul ignore stop */
}

export function loadActiveConnectionProfile(): ConnectionProfile | null {
  const activeId = loadActiveConnectionProfileId();
  if (!activeId) {
    return null;
  }
  return loadConnectionProfiles().find(profile => profile.id === activeId) ?? null;
}

export function saveConnectionProfile(profile: Omit<ConnectionProfile, 'id' | 'updatedAt'> & { id?: string; updatedAt?: string }): ConnectionProfile {
  const now = new Date().toISOString();
  const nextProfile: ConnectionProfile = {
    id: profile.id ?? generateId(),
    name: profile.name.trim() || profile.host.trim() || 'Machine',
    host: profile.host.trim(),
    port: profile.port.trim() || '3443',
    workspacePath: profile.workspacePath.trim(),
    token: profile.token.trim(),
    updatedAt: profile.updatedAt ?? now,
  };

  const profiles = loadConnectionProfiles();
  const nextProfiles = profiles.some(item => item.id === nextProfile.id)
    ? profiles.map(item => item.id === nextProfile.id ? nextProfile : item)
    : [nextProfile, ...profiles];

  /* istanbul ignore start */
  if (!writeJson(profilesStorageKey, nextProfiles)) {
    return nextProfile;
  }
  /* istanbul ignore stop */
  void setActiveConnectionProfile(nextProfile.id);
  return nextProfile;
}

export function deleteConnectionProfile(id: string): ConnectionProfile[] {
  const profiles = loadConnectionProfiles();
  const nextProfiles = profiles.filter(profile => profile.id !== id);
  /* istanbul ignore start */
  if (!writeJson(profilesStorageKey, nextProfiles)) {
    return profiles;
  }
  /* istanbul ignore stop */
  if (loadActiveConnectionProfileId() === id) {
    void setActiveConnectionProfile(nextProfiles[0]?.id ?? null);
  }
  return nextProfiles;
}

export function setActiveConnectionProfile(id: string | null): boolean {
  return writeString(activeProfileStorageKey, id);
}

export function clearConnectionProfiles(): void {
  /* istanbul ignore start */
  if (!canUseStorage()) {
    return;
  }
  /* istanbul ignore stop */

  window.localStorage.removeItem(profilesStorageKey);
  window.localStorage.removeItem(activeProfileStorageKey);
}

export async function pairConnectionCode(host: string, port: string, code: string): Promise<PairingResult> {
  const response = await fetch(`https://${host}:${port}/remote/pair`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ code }),
  });

  const payload = await response.json().catch(() => null) as PairingResult | null;
  if (!response.ok) {
    return {
      ok: false,
      error: payload?.error ?? `pairing failed (${response.status})`,
    };
  }

  return payload ?? { ok: false, error: 'pairing failed' };
}
