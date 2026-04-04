#!/usr/bin/env node

import { spawn, spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptDir = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(scriptDir, '..');
const serverDir = join(repoRoot, 'packages', 'server-go');
const args = new Set(process.argv.slice(2));

function candidateList(name) {
  const home = process.env.HOME ?? process.env.USERPROFILE ?? '';
  const ext = process.platform === 'win32' ? '.exe' : '';
  return [
    process.env[`${name.toUpperCase()}_BIN`],
    name,
    name + ext,
    process.platform === 'darwin' ? `/opt/homebrew/bin/${name}` : undefined,
    home ? join(home, '.local', 'bin', name + ext) : undefined,
  ].filter(Boolean);
}

function canRun(command, versionArgs) {
  const result = spawnSync(command, versionArgs, { stdio: 'ignore' });
  return !result.error && result.status === 0;
}

function resolveBinary(name, versionArgs) {
  for (const candidate of candidateList(name)) {
    if ((candidate.includes('/') || candidate.includes('\\')) && !existsSync(candidate)) {
      continue;
    }
    if (canRun(candidate, versionArgs)) {
      return candidate;
    }
  }
  throw new Error(`${name} executable not found on PATH`);
}

function resolveServerBinary() {
  const candidates = [
    join(serverDir, 'engine-server'),
    join(serverDir, 'engine-server.exe'),
  ];
  const found = candidates.find(existsSync);
  if (!found) {
    throw new Error('engine-server binary was not produced by go build');
  }
  return found;
}

const goBin = resolveBinary('go', ['version']);
const buildArgs = args.has('--dev')
  ? ['build', '-o', 'engine-server', '.']
  : ['build', '-ldflags=-s -w', '-o', 'engine-server', '.'];

const build = spawnSync(goBin, buildArgs, {
  cwd: serverDir,
  stdio: 'inherit',
});

if (build.status !== 0) {
  process.exit(build.status ?? 1);
}

if (!args.has('--run')) {
  process.exit(0);
}

const serverBinary = resolveServerBinary();
const child = spawn(serverBinary, [], {
  cwd: serverDir,
  stdio: 'inherit',
  env: {
    ...process.env,
    PROJECT_PATH: process.env.PROJECT_PATH ?? repoRoot,
    PORT: process.env.PORT ?? '3000',
  },
});

const stopChild = (signal) => {
  if (!child.killed) {
    child.kill(signal);
  }
};

process.on('SIGINT', () => stopChild('SIGINT'));
process.on('SIGTERM', () => stopChild('SIGTERM'));

child.on('exit', (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code ?? 0);
});
