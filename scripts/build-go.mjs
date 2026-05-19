#!/usr/bin/env node

import { spawn, spawnSync } from 'node:child_process';
import { existsSync, watch as fsWatch } from 'node:fs';
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

function runBuild() {
  const result = spawnSync(goBin, buildArgs, {
    cwd: serverDir,
    stdio: 'inherit',
    env: { ...process.env, GOWORK: process.env.GOWORK ?? 'off' },
  });
  return result.status ?? 1;
}

const initialStatus = runBuild();
if (initialStatus !== 0) {
  if (!args.has('--watch')) {
    process.exit(initialStatus);
  }
  console.error('[build-go] initial build failed — entering watch mode to retry on next change');
}

if (!args.has('--run')) {
  process.exit(initialStatus);
}

let serverBinary;
try {
  serverBinary = resolveServerBinary();
} catch (err) {
  if (!args.has('--watch')) throw err;
  console.error('[build-go] no binary to launch yet — watching for next successful build');
}

let child = null;

function startServer() {
  if (!serverBinary) return;
  child = spawn(serverBinary, [], {
    cwd: serverDir,
    stdio: 'inherit',
    env: {
      ...process.env,
      PROJECT_PATH: process.env.PROJECT_PATH ?? repoRoot,
      PORT: process.env.PORT ?? '24444',
    },
  });
  const launched = child;
  launched.on('exit', (code, signal) => {
    if (launched !== child) return; // superseded by rebuild
    if (signal && !args.has('--watch')) {
      process.kill(process.pid, signal);
      return;
    }
    if (!args.has('--watch')) {
      process.exit(code ?? 0);
    }
  });
}

function stopServer(signal = 'SIGTERM') {
  return new Promise((resolveStop) => {
    if (!child || child.killed) {
      resolveStop();
      return;
    }
    const dying = child;
    child = null;
    dying.once('exit', () => resolveStop());
    dying.kill(signal);
    // Hard-kill if it doesn't exit in 2s.
    setTimeout(() => {
      if (!dying.killed) dying.kill('SIGKILL');
    }, 2000);
  });
}

startServer();

const stopAndExit = async (signal) => {
  await stopServer(signal);
  process.exit(0);
};
process.on('SIGINT', () => stopAndExit('SIGINT'));
process.on('SIGTERM', () => stopAndExit('SIGTERM'));

if (args.has('--watch')) {
  let pending = false;
  let inFlight = false;
  const debounceMs = 250;
  let timer = null;

  const trigger = () => {
    if (timer) clearTimeout(timer);
    timer = setTimeout(async () => {
      timer = null;
      if (inFlight) {
        pending = true;
        return;
      }
      inFlight = true;
      try {
        do {
          pending = false;
          console.log('[build-go] change detected — rebuilding');
          const t0 = Date.now();
          const status = runBuild();
          if (status !== 0) {
            console.error(`[build-go] rebuild failed (status ${status}); keeping previous server running`);
            continue;
          }
          console.log(`[build-go] rebuilt in ${Date.now() - t0}ms — restarting server`);
          await stopServer();
          try {
            serverBinary = resolveServerBinary();
          } catch (err) {
            console.error(`[build-go] ${err.message}`);
            continue;
          }
          startServer();
        } while (pending);
      } finally {
        inFlight = false;
      }
    }, debounceMs);
  };

  // Recursive watch on the server-go directory; filter to .go files only.
  // node's recursive fs.watch is supported on macOS.
  try {
    fsWatch(serverDir, { recursive: true }, (_event, filename) => {
      if (!filename) return;
      // Skip the build artifact, test caches, and runtime state.
      if (filename === 'engine-server' || filename === 'engine-server.exe') return;
      if (filename.startsWith('.engine/')) return;
      if (filename.includes('/.engine/')) return;
      if (filename.endsWith('~') || filename.endsWith('.swp')) return;
      if (!filename.endsWith('.go')) return;
      // Skip generated test files churn during compile.
      if (filename.endsWith('_test.go') && process.env.WATCH_INCLUDE_TESTS !== '1') return;
      trigger();
    });
    console.log('[build-go] watching packages/server-go for .go changes (set WATCH_INCLUDE_TESTS=1 to also rebuild on test edits)');
  } catch (err) {
    console.error(`[build-go] failed to start watcher: ${err.message}`);
    process.exit(1);
  }
}
