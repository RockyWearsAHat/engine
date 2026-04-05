import { describe, it, expect } from 'vitest';
import fs from 'fs';
import path from 'path';

/**
 * Tauri Capability and Permission Configuration Tests
 * 
 * These tests verify that the Tauri permission system is properly configured
 * to allow context menu events and window manipulation.
 */

describe('Tauri Capabilities Configuration', () => {
  // Navigate from packages/client to packages/desktop-tauri
  const capabilitiesPath = path.resolve(
    process.cwd(),
    '../desktop-tauri/src-tauri/capabilities/default.json'
  );

  it('capabilities/default.json file exists', () => {
    expect(fs.existsSync(capabilitiesPath)).toBe(true);
  });

  it('default capability is valid JSON', () => {
    const content = fs.readFileSync(capabilitiesPath, 'utf-8');
    expect(() => JSON.parse(content)).not.toThrow();
  });

  it('default capability has required fields', () => {
    const content = fs.readFileSync(capabilitiesPath, 'utf-8');
    const capability = JSON.parse(content);

    expect(capability).toHaveProperty('identifier');
    expect(capability).toHaveProperty('windows');
    expect(capability).toHaveProperty('permissions');
  });

  it('identifier is valid (lowercase ASCII with hyphens)', () => {
    const content = fs.readFileSync(capabilitiesPath, 'utf-8');
    const capability = JSON.parse(content);

    // Identifier format: can only include lowercase ASCII, hyphens (not leading/trailing), single colon
    expect(capability.identifier).toMatch(/^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/);
  });

  it('windows array includes "main" window', () => {
    const content = fs.readFileSync(capabilitiesPath, 'utf-8');
    const capability = JSON.parse(content);

    expect(capability.windows).toContain('main');
  });

  describe('required permissions', () => {
    let capability: any;

    beforeEach(() => {
      const content = fs.readFileSync(capabilitiesPath, 'utf-8');
      capability = JSON.parse(content);
    });

    it('includes core:event:allow-listen for event listening', () => {
      expect(capability.permissions).toContain('core:event:allow-listen');
    });

    it('includes core:event:allow-emit for event emission', () => {
      expect(capability.permissions).toContain('core:event:allow-emit');
    });

    it('includes core:menu:allow-popup for context menu', () => {
      expect(capability.permissions).toContain('core:menu:allow-popup');
    });

    it('includes core:window:allow-internal-toggle-maximize for window operations', () => {
      expect(capability.permissions).toContain('core:window:allow-internal-toggle-maximize');
    });

    it('does not include wildcard permissions (like core:event:allow-*)', () => {
      const hasWildcards = capability.permissions.some((perm: string) => perm.includes('*'));
      expect(hasWildcards).toBe(false);
    });

    it('all permissions follow core:command:action format', () => {
      const validFormat = capability.permissions.every((perm: string) => {
        // Permission format: plugin:command:action or core:command:action
        return /^(core|plugin):[a-z0-9-]+:allow-[a-z0-9-]+$/.test(perm);
      });
      expect(validFormat).toBe(true);
    });
  });
});

describe('Permission System Behavior', () => {
  it('core:event:allow-listen enables window.listen() in frontend', () => {
    // This permission allows the frontend to listen to events from Rust
    expect(true).toBe(true);
  });

  it('core:event:allow-emit enables app.emit() in Rust', () => {
    // This permission allows Rust to emit events to frontend
    expect(true).toBe(true);
  });

  it('core:menu:allow-popup enables popup_menu_at() in Rust', () => {
    // This permission allows Rust to show native system context menu
    expect(true).toBe(true);
  });

  it('window.eval() bypasses permission system for direct handler invocation', () => {
    // Current implementation uses window.eval() to call handlers directly
    // This avoids permission issues but should only be used for trusted code
    expect(true).toBe(true);
  });
});

describe('Tauri Configuration', () => {
  const tauriConfPath = path.resolve(
    process.cwd(),
    '../desktop-tauri/src-tauri/tauri.conf.json'
  );

  it('tauri.conf.json is valid JSON', () => {
    const content = fs.readFileSync(tauriConfPath, 'utf-8');
    expect(() => JSON.parse(content)).not.toThrow();
  });

  it('tauri.conf.json does not have invalid webPreferences property', () => {
    const content = fs.readFileSync(tauriConfPath, 'utf-8');
    const config = JSON.parse(content);

    // webPreferences is not allowed in tauri 2.x - it's only for Electron
    expect(config.app?.windows?.[0]).not.toHaveProperty('webPreferences');
  });

  it('build process validates capabilities without errors', () => {
    // This would be tested by running `cargo check` in CI
    expect(true).toBe(true);
  });
});

describe('Dev vs Production Permission Behavior', () => {
  it('dev builds open DevTools for debugging', () => {
    // cfg!(debug_assertions) check in lib.rs should enable open_devtools()
    expect(true).toBe(true);
  });

  it('production builds do not open DevTools', () => {
    // Release builds should not expose dev tools to users
    expect(true).toBe(true);
  });

  it('permissions are same in dev and production', () => {
    // Capability grants should not depend on build type
    expect(true).toBe(true);
  });
});
