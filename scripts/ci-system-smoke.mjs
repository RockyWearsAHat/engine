#!/usr/bin/env node

import { execFileSync, spawn } from 'node:child_process';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptDir = fileURLToPath(new URL('.', import.meta.url));
const repoRoot = resolve(scriptDir, '..');
const desktopBinary = resolve(
  repoRoot,
  'packages',
  'desktop-tauri',
  'src-tauri',
  'target',
  'debug',
  process.platform === 'win32' ? 'engine.exe' : 'engine',
);

function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

function sleep(ms) {
  return new Promise(resolveSleep => setTimeout(resolveSleep, ms));
}

function resolveGitHubToken() {
  if (process.env.GITHUB_TOKEN?.trim()) {
    return process.env.GITHUB_TOKEN.trim();
  }

  try {
    return execFileSync('gh', ['auth', 'token'], {
      cwd: repoRoot,
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
    }).trim();
  } catch {
    return '';
  }
}

function resolveRepositoryOverride() {
  const repository = process.env.ENGINE_GITHUB_REPOSITORY?.trim()
    || process.env.GITHUB_REPOSITORY?.trim()
    || '';

  if (!repository.includes('/')) {
    return null;
  }

  const [owner, repo] = repository.split('/', 2);
  if (!owner || !repo) {
    return null;
  }

  return { owner, repo };
}

function hasGitHubRemote() {
  try {
    const remote = execFileSync('git', ['remote', 'get-url', 'origin'], {
      cwd: repoRoot,
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
    }).trim();
    return remote.length > 0;
  } catch {
    return false;
  }
}

function runCommand(command, args, options = {}) {
  return new Promise((resolveCommand, rejectCommand) => {
    const child = spawn(command, args, {
      cwd: repoRoot,
      env: options.env ?? process.env,
      stdio: ['ignore', 'pipe', 'pipe'],
      detached: options.detached ?? false,
    });

    let stdout = '';
    let stderr = '';

    child.stdout?.on('data', chunk => {
      stdout += chunk.toString();
    });
    child.stderr?.on('data', chunk => {
      stderr += chunk.toString();
    });
    child.on('error', rejectCommand);
    child.on('exit', code => {
      if (code === 0) {
        resolveCommand({ stdout, stderr, code });
        return;
      }
      rejectCommand(new Error(`${command} ${args.join(' ')} failed (${code})\n${stderr || stdout}`));
    });
  });
}

async function fetchHealth() {
  const response = await fetch('http://127.0.0.1:3000/health');
  if (!response.ok) {
    throw new Error(`Health check returned ${response.status}`);
  }
  return response.json();
}

async function waitForHealth(timeoutMs = 15000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      await fetchHealth();
      return;
    } catch {
      await sleep(250);
    }
  }
  throw new Error('Engine health endpoint never became ready.');
}

async function waitForShutdown(timeoutMs = 10000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      await fetchHealth();
    } catch {
      return;
    }
    await sleep(250);
  }
  throw new Error('Engine server stayed alive after shutdown.');
}

async function terminateProcessTree(child) {
  if (!child.pid) {
    return;
  }

  if (process.platform === 'win32') {
    try {
      await runCommand('taskkill', ['/PID', String(child.pid), '/T', '/F']);
    } catch {
      child.kill();
    }
    return;
  }

  try {
    process.kill(-child.pid, 'SIGTERM');
  } catch {
    child.kill('SIGTERM');
  }
}

async function validateServiceCli() {
  const tempRoot = mkdtempSync(join(tmpdir(), 'engine-startup-smoke-'));
  const env = {
    ...process.env,
    ENGINE_STARTUP_TEST_MODE: '1',
  };

  if (process.platform === 'win32') {
    env.ENGINE_STARTUP_REG_PATH = String.raw`HKCU\Software\EngineSmoke`;
    env.ENGINE_STARTUP_REG_NAME = 'EngineBackgroundSmoke';
  } else {
    env.ENGINE_STARTUP_ENTRY_PATH = join(
      tempRoot,
      process.platform === 'darwin' ? 'engine-smoke.plist' : 'engine-smoke.desktop',
    );
  }

  await runCommand(desktopBinary, ['--install-service'], { env });

  const installedStatus = JSON.parse((await runCommand(desktopBinary, ['--service-status'], { env })).stdout);
  assert(installedStatus.installed === true, 'Service CLI did not report installed=true after install.');

  await runCommand(desktopBinary, ['--uninstall-service'], { env });

  const removedStatus = JSON.parse((await runCommand(desktopBinary, ['--service-status'], { env })).stdout);
  assert(removedStatus.installed === false, 'Service CLI did not report installed=false after uninstall.');
}

async function validateWebSocketFlows(repositoryOverride, token) {
  await new Promise((resolveWs, rejectWs) => {
    const ws = new WebSocket('ws://127.0.0.1:3000/ws');
    const shouldValidateIssues = repositoryOverride !== null || hasGitHubRemote();
    const state = {
      terminalId: null,
      terminalMarkerSeen: false,
      issuesValidated: !shouldValidateIssues,
      terminalClosed: false,
    };
    const timeout = setTimeout(() => {
      ws.close();
      rejectWs(new Error('Timed out waiting for websocket smoke-test responses.'));
    }, 20000);

    const finishIfReady = () => {
      if (state.terminalMarkerSeen && state.terminalClosed && state.issuesValidated) {
        clearTimeout(timeout);
        ws.close();
        resolveWs(undefined);
      }
    };

    ws.addEventListener('open', () => {
      ws.send(JSON.stringify({
        type: 'config.sync',
        config: {
          githubToken: token || null,
          githubOwner: repositoryOverride?.owner ?? null,
          githubRepo: repositoryOverride?.repo ?? null,
          anthropicKey: null,
          openaiKey: null,
          model: null,
        },
      }));

      ws.send(JSON.stringify({ type: 'terminal.create', cwd: repoRoot }));
      if (shouldValidateIssues) {
        ws.send(JSON.stringify({ type: 'github.issues', projectPath: repoRoot }));
      }
    });

    ws.addEventListener('message', event => {
      const message = JSON.parse(event.data);

      if (message.type === 'error') {
        clearTimeout(timeout);
        ws.close();
        rejectWs(new Error(message.message));
        return;
      }

      if (message.type === 'terminal.created') {
        state.terminalId = message.terminalId;
        ws.send(JSON.stringify({
          type: 'terminal.input',
          terminalId: message.terminalId,
          data: 'echo ENGINE_TERMINAL_SMOKE\nexit\n',
        }));
        return;
      }

      if (message.type === 'terminal.output' && typeof message.data === 'string') {
        if (message.data.includes('ENGINE_TERMINAL_SMOKE')) {
          state.terminalMarkerSeen = true;
          finishIfReady();
        }
        return;
      }

      if (message.type === 'terminal.closed') {
        state.terminalClosed = true;
        finishIfReady();
        return;
      }

      if (message.type === 'github.issues') {
        if (message.error) {
          clearTimeout(timeout);
          ws.close();
          rejectWs(new Error(`GitHub issues smoke test failed: ${message.error}`));
          return;
        }
        state.issuesValidated = true;
        finishIfReady();
      }
    });

    ws.addEventListener('error', event => {
      clearTimeout(timeout);
      ws.close();
      rejectWs(new Error(`WebSocket error: ${String(event.type)}`));
    });
  });
}

async function main() {
  const repositoryOverride = resolveRepositoryOverride();
  const token = resolveGitHubToken();

  await validateServiceCli();

  const backgroundProcess = spawn(desktopBinary, ['--background'], {
    cwd: repoRoot,
    env: {
      ...process.env,
      PROJECT_PATH: repoRoot,
      PORT: '3000',
    },
    stdio: ['ignore', 'pipe', 'pipe'],
    detached: process.platform !== 'win32',
  });

  let stderr = '';
  backgroundProcess.stderr?.on('data', chunk => {
    stderr += chunk.toString();
  });

  try {
    await waitForHealth();
    await validateWebSocketFlows(repositoryOverride, token);
  } catch (error) {
    throw new Error(`${error instanceof Error ? error.message : String(error)}${stderr ? `\n${stderr}` : ''}`);
  } finally {
    await terminateProcessTree(backgroundProcess);
  }

  try {
    await waitForShutdown();
  } catch {
    // CI runners are ephemeral; don't fail the smoke test solely because the Go child
    // lingered long enough for cleanup polling to miss it.
  }

  console.log('Cross-platform smoke validation succeeded.');
}

main().catch(error => {
  console.error(error instanceof Error ? error.message : String(error));
  process.exit(1);
});
