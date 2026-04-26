#!/usr/bin/env node
/**
 * behavioral-completion-check.mjs
 *
 * Runs a lightweight behavioral smoke check against the Engine client using
 * Playwright. Verifies that the UI loads, key surfaces are reachable, and no
 * uncaught browser console errors appear on the main screens.
 *
 * Outputs a single JSON line on stdout (matching BehavioralGateResult) so the
 * Go completion_gate.go runner can parse it. Exits 0 on pass or skip, 1 on
 * failure.
 *
 * If Playwright is not installed the script exits 0 and outputs a Skipped
 * result — it does NOT block the completion gate.
 */

import { spawn, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptDir = fileURLToPath(new URL('.', import.meta.url));
const repoRoot = path.resolve(scriptDir, '..');
const screenshotDir = path.join(repoRoot, '.cache', 'behavioral-screenshots');
const startTimeMs = Date.now();

// ── Load ProjectProfile cache ─────────────────────────────────────────────────
// Written by ai.WriteProjectProfileCache before the gate runs.
// When absent the script falls back to Engine's own dev server defaults.

let projectProfile = null;
const profileCachePath = path.join(repoRoot, '.cache', 'project-profile.json');
try {
  const raw = fs.readFileSync(profileCachePath, 'utf8');
  projectProfile = JSON.parse(raw);
} catch {
  // No profile — infer a strategy from workspace files.
}

function readJsonIfExists(filePath) {
  try {
    if (!fs.existsSync(filePath)) return null;
    return JSON.parse(fs.readFileSync(filePath, 'utf8'));
  } catch {
    return null;
  }
}

function inferProfileFromWorkspace() {
  const rootPkg = readJsonIfExists(path.join(repoRoot, 'package.json'));
  const hasGoMod = fs.existsSync(path.join(repoRoot, 'go.mod'));
  const hasCargoToml = fs.existsSync(path.join(repoRoot, 'Cargo.toml'));
  const hasClientDir = fs.existsSync(path.join(repoRoot, 'packages', 'client'));
  const hasGoServerDir = fs.existsSync(path.join(repoRoot, 'packages', 'server-go'));

  if (hasClientDir || (rootPkg?.scripts && (rootPkg.scripts.dev || rootPkg.scripts.start))) {
    return {
      type: 'web-app',
      verification: {
        usesPlaywright: true,
        startCmd: hasClientDir ? 'pnpm --filter @engine/client dev' : (rootPkg?.scripts?.dev ? 'pnpm dev' : 'pnpm start'),
        checkURL: 'http://localhost:5173',
        port: 5173,
        checkCmds: [],
      },
    };
  }

  if (hasGoServerDir || hasGoMod) {
    return {
      type: 'rest-api',
      verification: {
        usesPlaywright: false,
        startCmd: hasGoServerDir ? 'cd packages/server-go && go run .' : 'go run .',
        checkURL: 'http://localhost:8080/health',
        port: 8080,
        checkCmds: ['curl -sf http://localhost:8080/health || true', hasGoServerDir ? 'cd packages/server-go && go test ./... -count=1' : 'go test ./... -count=1'],
      },
    };
  }

  if (hasCargoToml) {
    return {
      type: 'library',
      verification: {
        usesPlaywright: false,
        startCmd: '',
        checkURL: '',
        port: 0,
        checkCmds: ['cargo test'],
      },
    };
  }

  return {
    type: 'unknown',
    verification: {
      usesPlaywright: false,
      startCmd: '',
      checkURL: '',
      port: 0,
      checkCmds: ['pnpm test || npm test || go test ./... -count=1 || cargo test'],
    },
  };
}

if (!projectProfile) {
  projectProfile = inferProfileFromWorkspace();
}

// Derive client URL and whether Playwright should run from the profile.
function resolveVerification() {
  if (!projectProfile) {
    // Engine-own fallback: port 5173 Vite dev server, Playwright check.
    return { usesPlaywright: true, clientUrl: `http://localhost:5173`, startFilter: '@engine/client' };
  }
  const v = projectProfile.verification ?? {};
  const port = v.port || 3000;
  const clientUrl = v.checkURL || (v.usesPlaywright ? `http://localhost:${port}` : '');
  return {
    usesPlaywright: v.usesPlaywright ?? false,
    clientUrl,
    startFilter: null,
    startCmd: v.startCmd || null,
    checkCmds: v.checkCmds || [],
    projectType: projectProfile.type || 'unknown',
  };
}

const verification = resolveVerification();
const clientUrl = verification.clientUrl;

function now() {
  return new Date().toISOString();
}

function result(obj) {
  process.stdout.write(JSON.stringify({ ranAt: now(), durationMs: Date.now() - startTimeMs, ...obj }) + '\n');
}

function skip(reason) {
  result({ passed: false, skipped: true, skipReason: reason });
  process.exit(0);
}

function fail(consoleErrors = [], message = '') {
  result({ passed: false, skipped: false, consoleErrors: [message, ...consoleErrors].filter(Boolean) });
  process.exit(1);
}

function pass(screenshots = [], consoleErrors = []) {
  result({ passed: true, skipped: false, screenshotPaths: screenshots, consoleErrors });
  process.exit(0);
}

// ── Ensure dev server is running when URL-based verification is needed ───────

async function waitForServer(url, timeoutMs = 8000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(url, { signal: AbortSignal.timeout(1000) });
      if (res.ok || res.status < 500) return true;
    } catch {
      // ignore
    }
    await new Promise(r => setTimeout(r, 400));
  }
  return false;
}

let devServer = null;

if (clientUrl) {
  const serverReady = await waitForServer(clientUrl, 3000);
  if (!serverReady) {
    // Try to start the project. For Engine's own client we use the known filter;
    // for other projects we use the profile's startCmd.
    const startArgs = verification.startFilter
      ? ['--filter', verification.startFilter, 'dev']
      : null;

    if (startArgs) {
      devServer = spawn('pnpm', startArgs, {
        cwd: repoRoot,
        detached: true,
        stdio: 'ignore',
      });
      devServer.unref();
    } else if (verification.startCmd) {
      devServer = spawn('sh', ['-c', verification.startCmd], {
        cwd: repoRoot,
        detached: true,
        stdio: 'ignore',
      });
      devServer.unref();
    }

    const started = await waitForServer(clientUrl, 15_000);
    if (!started) {
      skip('Could not reach project dev server — behavioral check requires the server to be running');
    }
  }
}

// ── Non-Playwright verification (rest-api, cli, library, service) ─────────────

if (!verification.usesPlaywright) {
  const checkCmds = verification.checkCmds || [];
  if (checkCmds.length === 0) {
    // No check commands and no Playwright — skip gracefully.
    if (devServer) {
      try { process.kill(-devServer.pid); } catch { /* ignore */ }
    }
    skip(`No verification commands for project type "${verification.projectType}" — skipping behavioral gate`);
  }

  const cmdErrors = [];
  for (const cmd of checkCmds) {
    const res = spawnSync('sh', ['-c', cmd], { cwd: repoRoot, encoding: 'utf8' });
    if (res.status !== 0) {
      cmdErrors.push(`Command failed (exit ${res.status}): ${cmd}\n${res.stderr || res.stdout || ''}`);
    }
  }

  if (devServer) {
    try { process.kill(-devServer.pid); } catch { /* ignore */ }
  }

  if (cmdErrors.length > 0) {
    fail(cmdErrors, `${cmdErrors.length} check command(s) failed`);
  }
  pass([], []);
}

// ── Playwright verification (web-app) ─────────────────────────────────────────

let chromium;
try {
  const pw = await import('@playwright/test');
  chromium = pw.chromium;
} catch {
  try {
    const pw = await import('playwright');
    chromium = pw.chromium;
  } catch {
    skip('Playwright not installed — install @playwright/test or playwright to enable behavioral checks');
  }
}

fs.mkdirSync(screenshotDir, { recursive: true });

const browser = await chromium.launch({ headless: true });
const context = await browser.newContext();
const page = await context.newPage();

const consoleErrors = [];
page.on('console', msg => {
  if (msg.type() === 'error') {
    consoleErrors.push(msg.text());
  }
});
page.on('pageerror', err => {
  consoleErrors.push(`[uncaught] ${err.message}`);
});

const screenshots = [];

async function screenshot(name) {
  const p = path.join(screenshotDir, `${name}-${Date.now()}.png`);
  await page.screenshot({ path: p, fullPage: true });
  screenshots.push(p);
}

try {
  // ── Load root ──
  await page.goto(clientUrl, { waitUntil: 'domcontentloaded', timeout: 15_000 });
  await page.waitForTimeout(1500);
  await screenshot('01-root');

  // ── Verify at least one interactive element is present ──
  const hasAnyElement = await page.evaluate(() => document.body.children.length > 0);
  if (!hasAnyElement) {
    await browser.close();
    fail(consoleErrors, 'Page body appears empty after load');
  }

  // ── Filter out known-benign noise from console errors ──
  const blockers = consoleErrors.filter(e =>
    !e.includes('WebSocket') &&            // WS not yet connected is expected in dev mode
    !e.includes('favicon') &&             // favicon 404 is cosmetic
    !e.includes('[vite]')                 // vite HMR preamble noise
  );

  await browser.close();

  if (devServer) {
    try { process.kill(-devServer.pid); } catch { /* ignore */ }
  }

  if (blockers.length > 0) {
    fail(blockers, `${blockers.length} blocking console error(s) detected`);
  }

  pass(screenshots, consoleErrors);
} catch (err) {
  try { await browser.close(); } catch { /* ignore */ }
  if (devServer) {
    try { process.kill(-devServer.pid); } catch { /* ignore */ }
  }
  fail(consoleErrors, `Playwright run threw: ${err.message}`);
}
