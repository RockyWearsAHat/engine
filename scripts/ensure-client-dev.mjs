#!/usr/bin/env node

import { spawn } from 'node:child_process';
import net from 'node:net';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptDir = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(scriptDir, '..');
const host = process.env.ENGINE_CLIENT_DEV_HOST ?? '127.0.0.1';
const port = Number.parseInt(process.env.ENGINE_CLIENT_DEV_PORT ?? '5173', 10);
const acceptedTitles = ['<title>MyEditor</title>', '<title>Engine</title>'];
const hostCandidates = [...new Set([host, 'localhost', '127.0.0.1', '::1'])];

function log(message) {
  console.log(`[ensure-client-dev] ${message}`);
}

function connectable(targetHost, targetPort) {
  return new Promise((resolveConnect) => {
    const socket = net.createConnection({ host: targetHost, port: targetPort });
    const finish = (value) => {
      socket.removeAllListeners();
      socket.destroy();
      resolveConnect(value);
    };

    socket.setTimeout(800);
    socket.once('connect', () => finish(true));
    socket.once('timeout', () => finish(false));
    socket.once('error', () => finish(false));
  });
}

async function isEngineViteServer(targetHost, targetPort) {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 1500);
  const requestHost = targetHost.includes(':') ? `[${targetHost}]` : targetHost;

  try {
    const response = await fetch(`http://${requestHost}:${targetPort}/`, {
      signal: controller.signal,
      headers: { Accept: 'text/html' },
    });
    const body = await response.text();
    return (
      response.ok &&
      body.includes('/@vite/client') &&
      body.includes('/src/main.tsx') &&
      acceptedTitles.some(title => body.includes(title))
    );
  } catch {
    return false;
  } finally {
    clearTimeout(timeout);
  }
}

function spawnClientDev() {
  log(`Starting Engine client dev server on http://${host}:${port}`);
  const child = spawn(
    'pnpm',
    ['--filter', '@engine/client', 'exec', 'vite', '--host', host, '--port', String(port)],
    {
      cwd: repoRoot,
      stdio: 'inherit',
      env: process.env,
    },
  );

  process.on('SIGINT', () => child.kill('SIGINT'));
  process.on('SIGTERM', () => child.kill('SIGTERM'));

  child.on('exit', (code, signal) => {
    if (signal === 'SIGINT' || signal === 'SIGTERM') {
      process.exit(0);
      return;
    }
    if (signal) {
      process.kill(process.pid, signal);
      return;
    }
    process.exit(code ?? 0);
  });
}

let occupiedByEngineHost = null;
let occupiedByOtherProcess = false;

for (const candidate of hostCandidates) {
  if (!(await connectable(candidate, port))) {
    continue;
  }
  if (await isEngineViteServer(candidate, port)) {
    occupiedByEngineHost = candidate;
    break;
  }
  occupiedByOtherProcess = true;
}

if (occupiedByEngineHost) {
  log(`Reusing existing Engine client dev server on http://${occupiedByEngineHost}:${port}`);
  process.exit(0);
}

if (occupiedByOtherProcess) {
  console.error(
    `[ensure-client-dev] Port ${port} is already in use by a non-Engine service. Stop that process or change ENGINE_CLIENT_DEV_PORT.`,
  );
  process.exit(1);
}

spawnClientDev();
