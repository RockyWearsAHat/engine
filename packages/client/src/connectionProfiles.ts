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
  if (!canUseStorage()) {
    return null;
  }

  try {
    const raw = window.localStorage.getItem(key);
    if (!raw) {
      return null;
    }
    return JSON.parse(raw) as T;
  } catch {
    return null;
  }
}

function writeJson(key: string, value: unknown): boolean {
  if (!canUseStorage()) {
    return false;
  }

  try {
    window.localStorage.setItem(key, JSON.stringify(value));
    return true;
  } catch {
    return false;
  }
}

function writeString(key: string, value: string | null): boolean {
  if (!canUseStorage()) {
    return false;
  }

  try {
    if (value && value.trim()) {
      window.localStorage.setItem(key, value.trim());
    } else {
      window.localStorage.removeItem(key);
    }
    return true;
  } catch {
    return false;
  }
}

function generateId(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID();
  }
  return `connection_${Date.now()}_${Math.random().toString(16).slice(2)}`;
}

export function loadConnectionProfiles(): ConnectionProfile[] {
  const stored = readJson<ConnectionProfile[]>(profilesStorageKey) ?? [];
  return [...stored].sort((left, right) => right.updatedAt.localeCompare(left.updatedAt));
}

export function loadActiveConnectionProfileId(): string | null {
  if (!canUseStorage()) {
    return null;
  }
  try {
    const value = window.localStorage.getItem(activeProfileStorageKey)?.trim();
    return value ? value : null;
  } catch {
    return null;
  }
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

  if (!writeJson(profilesStorageKey, nextProfiles)) {
    return nextProfile;
  }
  void setActiveConnectionProfile(nextProfile.id);
  return nextProfile;
}

export function deleteConnectionProfile(id: string): ConnectionProfile[] {
  const profiles = loadConnectionProfiles();
  const nextProfiles = profiles.filter(profile => profile.id !== id);
  if (!writeJson(profilesStorageKey, nextProfiles)) {
    return profiles;
  }
  if (loadActiveConnectionProfileId() === id) {
    void setActiveConnectionProfile(nextProfiles[0]?.id ?? null);
  }
  return nextProfiles;
}

export function setActiveConnectionProfile(id: string | null): boolean {
  return writeString(activeProfileStorageKey, id);
}

export function clearConnectionProfiles(): void {
  if (!canUseStorage()) {
    return;
  }

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
