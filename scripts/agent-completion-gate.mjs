#!/usr/bin/env node

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

const root = process.cwd();
const reportPath = path.join(root, '.github', 'session-memory', 'agent-completion-report.json');
const goCoverPath = path.join(root, 'packages', 'server-go', '.agent-cover.out');
const requiredGoCoverage = 100;

function emitResult(continueRun, message, details = []) {
  const payload = {
    continue: continueRun,
    systemMessage: details.length > 0 ? `${message}\n${details.join('\n')}` : message,
  };

  if (!continueRun) {
    payload.stopReason = message;
  }

  process.stdout.write(`${JSON.stringify(payload)}\n`);
}

function fail(message, details = []) {
  emitResult(false, message, details);
  process.exit(2);
}

function runCommand(command, args, cwd = root) {
  const result = spawnSync(command, args, {
    cwd,
    encoding: 'utf8',
    env: process.env,
    maxBuffer: 20 * 1024 * 1024,
  });

  return {
    status: result.status ?? 1,
    stdout: result.stdout || '',
    stderr: result.stderr || '',
    command: `${command} ${args.join(' ')}`,
  };
}

function requireSuccess(stepName, result) {
  if (result.status === 0) {
    return;
  }

  const stderr = result.stderr.trim();
  const stdout = result.stdout.trim();
  const tail = (stderr || stdout)
    .split('\n')
    .slice(-12)
    .join('\n');

  fail(`Completion gate failed: ${stepName}`, [
    `Command: ${result.command}`,
    tail || 'No output was captured.',
  ]);
}

if (process.env.ENGINE_AGENT_GATE_BYPASS === '1') {
  emitResult(true, 'Completion gate bypassed via ENGINE_AGENT_GATE_BYPASS=1');
  process.exit(0);
}

requireSuccess('lint', runCommand('pnpm', ['lint']));
requireSuccess('typecheck', runCommand('pnpm', ['typecheck']));
requireSuccess('desktop debug build for smoke test', runCommand('pnpm', ['build:desktop-debug']));

requireSuccess(
  'system smoke test (functionality/integration)',
  runCommand('pnpm', ['smoke:system']),
);

requireSuccess(
  'client coverage at configured thresholds',
  runCommand('pnpm', ['--filter', '@engine/client', 'test:coverage', '--run']),
);

requireSuccess(
  'go tests with coverage profile',
  runCommand('go', ['test', './...', '-coverprofile=.agent-cover.out'], path.join(root, 'packages', 'server-go')),
);

const goCover = runCommand('go', ['tool', 'cover', '-func=.agent-cover.out'], path.join(root, 'packages', 'server-go'));
requireSuccess('go coverage summary', goCover);

const totalMatch = goCover.stdout.match(/total:\s+\(statements\)\s+([0-9]+(?:\.[0-9]+)?)%/);
if (!totalMatch) {
  fail('Completion gate failed: unable to parse Go total coverage.', [goCover.stdout.trim()]);
}

const goCoverage = Number.parseFloat(totalMatch[1]);
if (!Number.isFinite(goCoverage) || goCoverage < requiredGoCoverage) {
  fail('Completion gate failed: Go total coverage is below required threshold.', [
    `Detected Go total coverage: ${goCoverage.toFixed(1)}%`,
    `Required coverage: ${requiredGoCoverage.toFixed(1)}%`,
  ]);
}

if (fs.existsSync(goCoverPath)) {
  fs.unlinkSync(goCoverPath);
}

if (!fs.existsSync(reportPath)) {
  fail('Completion gate failed: completion report file is missing.', [reportPath]);
}

let report;
try {
  report = JSON.parse(fs.readFileSync(reportPath, 'utf8'));
} catch (error) {
  fail('Completion gate failed: completion report JSON is invalid.', [String(error)]);
}

const requiredFields = [
  'requestFullyCompleted',
  'chatHistoryReviewed',
  'cs3500PrinciplesVerified',
  'diagnosticsClean',
  'coverage100',
];

const failedFields = requiredFields.filter((field) => report[field] !== true);
if (failedFields.length > 0) {
  fail('Completion gate failed: report attestation fields are incomplete.', failedFields.map((field) => `Missing true field: ${field}`));
}

if (typeof report.generatedAt !== 'string' || report.generatedAt.trim() === '') {
  fail('Completion gate failed: report.generatedAt is required.');
}

const generatedAtMs = Date.parse(report.generatedAt);
if (!Number.isFinite(generatedAtMs)) {
  fail('Completion gate failed: report.generatedAt is not a valid ISO timestamp.', [report.generatedAt]);
}

const ageMinutes = (Date.now() - generatedAtMs) / 60000;
if (ageMinutes < -1) {
  fail('Completion gate failed: report.generatedAt is in the future.', [`Report time: ${report.generatedAt}`]);
}

if (ageMinutes > 60) {
  fail('Completion gate failed: report.generatedAt is stale. Refresh report before finishing.', [`Report age: ${ageMinutes.toFixed(1)} minutes`]);
}

emitResult(true, 'Completion gate passed.');
