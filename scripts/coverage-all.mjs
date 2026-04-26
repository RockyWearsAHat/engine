#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { spawnSync } from 'node:child_process';

const root = process.cwd();
const goDir = path.join(root, 'packages', 'server-go');
const coverageCacheDir = path.join(root, '.cache', 'coverage');
const reportPath = path.join(coverageCacheDir, 'coverage-summary.json');
const goCoverageProfile = path.join(coverageCacheDir, 'go', 'coverage.out');

function run(command, args, cwd = root) {
  const result = spawnSync(command, args, {
    cwd,
    stdio: 'pipe',
    encoding: 'utf8',
    env: process.env,
    maxBuffer: 20 * 1024 * 1024,
  });

  if (result.stdout) process.stdout.write(result.stdout);
  if (result.stderr) process.stderr.write(result.stderr);

  return {
    code: result.status ?? 1,
    stdout: result.stdout || '',
    stderr: result.stderr || '',
  };
}

function requireOk(step, result) {
  if (result.code !== 0) {
    const tail = `${result.stderr}\n${result.stdout}`.trim().split('\n').slice(-16).join('\n');
    throw new Error(`${step} failed\n${tail}`);
  }
}

function parseGoCoverageTotal(text) {
  const m = text.match(/total:\s+\(statements\)\s+([0-9]+(?:\.[0-9]+)?)%/);
  if (!m) return null;
  return Number.parseFloat(m[1]);
}

function parseRustLlvmCovTotal(text) {
  const line = text
    .split('\n')
    .map((l) => l.trim())
    .find((l) => /^TOTAL\s+/i.test(l) && /%/.test(l));
  if (!line) return null;
  const numbers = line.match(/[0-9]+(?:\.[0-9]+)?%/g);
  if (!numbers || numbers.length === 0) return null;
  const last = numbers[numbers.length - 1].replace('%', '');
  return Number.parseFloat(last);
}

const RUSTUP_LLVM_BIN = path.join(
  process.env.HOME || '/Users/' + process.env.USER,
  '.rustup/toolchains/stable-aarch64-apple-darwin/lib/rustlib/aarch64-apple-darwin/bin',
);

function llvmEnv() {
  const llvmCov = path.join(RUSTUP_LLVM_BIN, 'llvm-cov');
  const llvmProfdata = path.join(RUSTUP_LLVM_BIN, 'llvm-profdata');
  if (fs.existsSync(llvmCov) && fs.existsSync(llvmProfdata)) {
    return { ...process.env, LLVM_COV: llvmCov, LLVM_PROFDATA: llvmProfdata };
  }
  return process.env;
}

function hasCargoLlvmCov() {
  const check = run('cargo', ['llvm-cov', '--version'], root);
  return check.code === 0;
}

function main() {
  fs.mkdirSync(path.join(coverageCacheDir, 'go'), { recursive: true });
  fs.mkdirSync(path.join(coverageCacheDir, 'rust'), { recursive: true });

  const startedAt = new Date().toISOString();
  const summary = {
    generatedAt: startedAt,
    client: {
      tests: 'pass',
      coverage: 'see .cache/coverage/client/index.html',
    },
    go: {
      tests: 'pass',
      coverage: null,
      profile: path.relative(root, goCoverageProfile),
    },
    rust: {
      tests: 'pass',
      coverage: null,
      coverageTool: null,
      note: null,
    },
  };

  console.log('\n== Client (Vitest + Istanbul) ==');
  requireOk('client coverage', run('pnpm', ['--filter', '@engine/client', 'test:coverage'], root));

  console.log('\n== Go (go test + coverprofile) ==');
  requireOk('go tests', run('go', ['test', './...', '-coverprofile=' + goCoverageProfile, '-covermode=atomic'], goDir));
  const goCover = run('go', ['tool', 'cover', '-func=' + goCoverageProfile], goDir);
  requireOk('go coverage summary', goCover);
  summary.go.coverage = parseGoCoverageTotal(goCover.stdout);

  console.log('\n== Rust (cargo test) ==');
  requireOk('rust tests', run('node', ['scripts/run-cargo.mjs', 'test'], root));

  if (hasCargoLlvmCov()) {
    console.log('\n== Rust Coverage (cargo llvm-cov) ==');
    const rustCov = spawnSync(
      'node',
      [
        'scripts/run-cargo.mjs',
        'llvm-cov',
        '--summary-only',
        '--ignore-filename-regex',
        'lib.rs|main.rs',
      ],
      { cwd: root, stdio: 'pipe', encoding: 'utf8', env: process.env, maxBuffer: 20 * 1024 * 1024 },
    );
    if (rustCov.stdout) process.stdout.write(rustCov.stdout);
    if (rustCov.stderr) process.stderr.write(rustCov.stderr);
    requireOk('rust llvm-cov', { code: rustCov.status ?? 1, stdout: rustCov.stdout || '', stderr: rustCov.stderr || '' });
    summary.rust.coverageTool = 'cargo-llvm-cov';
    summary.rust.coverage = parseRustLlvmCovTotal(rustCov.stdout);

    // Also emit lcov so coverage-gutters can display per-line coverage in the editor.
    const rustLcovPath = path.join(coverageCacheDir, 'rust', 'lcov.info');
    const rustLcov = spawnSync(
      'node',
      [
        'scripts/run-cargo.mjs',
        'llvm-cov',
        '--lcov',
        '--ignore-filename-regex',
        'lib.rs|main.rs',
        `--output-path=${rustLcovPath}`,
      ],
      { cwd: root, stdio: 'pipe', encoding: 'utf8', env: process.env, maxBuffer: 20 * 1024 * 1024 },
    );
    if (rustLcov.status === 0) {
      summary.rust.lcov = path.relative(root, rustLcovPath);
    }
  } else {
    summary.rust.note = 'Install cargo-llvm-cov to enable Rust coverage metrics in this report.';
  }

  fs.writeFileSync(reportPath, `${JSON.stringify(summary, null, 2)}\n`);

  console.log('\n== Unified Coverage Summary ==');
  console.log(`Client: ${summary.client.coverage}`);
  console.log(`Go total: ${summary.go.coverage ?? 'unknown'}%`);
  if (summary.rust.coverage != null) {
    console.log(`Rust total (${summary.rust.coverageTool}): ${summary.rust.coverage}%`);
  } else {
    console.log(`Rust coverage: ${summary.rust.note}`);
  }
  console.log(`Summary JSON: ${path.relative(root, reportPath)}`);
}

try {
  main();
} catch (error) {
  console.error(`\nUnified coverage failed: ${error instanceof Error ? error.message : String(error)}`);
  process.exit(1);
}
