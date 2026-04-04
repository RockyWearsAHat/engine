#!/usr/bin/env node

import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptDir = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(scriptDir, '..');
const desktopDir = join(repoRoot, 'packages', 'desktop-tauri');
const tauriCrateDir = join(desktopDir, 'src-tauri');
const home = process.env.HOME ?? process.env.USERPROFILE ?? '';
const cargoExt = process.platform === 'win32' ? '.exe' : '';

const candidates = [
  process.env.CARGO_BIN,
  'cargo',
  'cargo' + cargoExt,
  home ? join(home, '.cargo', 'bin', 'cargo' + cargoExt) : undefined,
].filter(Boolean);

function canRun(command) {
  try {
    const child = spawn(command, ['--version'], { stdio: 'ignore' });
    return new Promise((resolveRun) => {
      child.on('error', () => resolveRun(false));
      child.on('exit', (code) => resolveRun(code === 0));
    });
  } catch {
    return Promise.resolve(false);
  }
}

async function resolveCargo() {
  for (const candidate of candidates) {
    if ((candidate.includes('/') || candidate.includes('\\')) && !existsSync(candidate)) {
      continue;
    }
    if (await canRun(candidate)) {
      return candidate;
    }
  }
  throw new Error('cargo executable not found on PATH');
}

const cargo = await resolveCargo();
const cargoArgs = process.argv.slice(2);
const child = spawn(cargo, process.argv.slice(2), {
  cwd: cargoArgs[0] === 'tauri' ? desktopDir : tauriCrateDir,
  stdio: 'inherit',
  env: process.env,
});

process.on('SIGINT', () => child.kill('SIGINT'));
process.on('SIGTERM', () => child.kill('SIGTERM'));

child.on('exit', (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code ?? 0);
});
