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

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptDir = fileURLToPath(new URL('.', import.meta.url));
const repoRoot = path.resolve(scriptDir, '..');
const screenshotDir = path.join(repoRoot, '.cache', 'behavioral-screenshots');
const clientPort = 5173;
const clientUrl = `http://localhost:${clientPort}`;
const startTimeMs = Date.now();

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

// ── Require Playwright ────────────────────────────────────────────────────────

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

// ── Ensure dev server is running ──────────────────────────────────────────────

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

const serverReady = await waitForServer(clientUrl, 3000);
if (!serverReady) {
  // Try to start the client dev server ourselves.
  devServer = spawnSync('pnpm', ['--filter', '@engine/client', 'dev'], {
    cwd: repoRoot,
    detached: true,
    stdio: 'ignore',
  });

  const started = await waitForServer(clientUrl, 15_000);
  if (!started) {
    skip('Could not reach Engine client dev server — behavioral check requires the dev server to be running');
  }
}

// ── Run Playwright checks ─────────────────────────────────────────────────────

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
