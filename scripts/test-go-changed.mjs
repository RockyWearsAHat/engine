#!/usr/bin/env node
// Runs `go test` only for packages that contain (a) files changed vs origin/main
// or (b) staged + unstaged + untracked changes. Falls back to all packages when
// the diff is empty. Pass extra args after `--` to forward to `go test`.
//
// Examples:
//   node scripts/test-go-changed.mjs              # quick test of touched pkgs
//   node scripts/test-go-changed.mjs -- -v        # verbose
//   node scripts/test-go-changed.mjs -- -run Foo  # filter to a name
//   BASE=HEAD~5 node scripts/test-go-changed.mjs  # custom diff base

import { spawnSync } from 'node:child_process';
import { dirname, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { existsSync, statSync } from 'node:fs';

const scriptDir = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(scriptDir, '..');
const serverDir = resolve(repoRoot, 'packages/server-go');

const passThrough = (() => {
  const argv = process.argv.slice(2);
  const idx = argv.indexOf('--');
  return idx === -1 ? [] : argv.slice(idx + 1);
})();

function git(...args) {
  const r = spawnSync('git', args, { cwd: repoRoot, encoding: 'utf8' });
  if (r.status !== 0) return '';
  return r.stdout.trim();
}

const base = process.env.BASE || 'HEAD';
const diffFiles = new Set();

// Tracked changes vs base.
for (const line of git('diff', '--name-only', base).split('\n')) {
  if (line) diffFiles.add(line);
}
// Staged but not yet in HEAD (only matters when BASE=HEAD).
for (const line of git('diff', '--name-only', '--cached').split('\n')) {
  if (line) diffFiles.add(line);
}
// Untracked.
for (const line of git('ls-files', '--others', '--exclude-standard').split('\n')) {
  if (line) diffFiles.add(line);
}

const serverDirRel = relative(repoRoot, serverDir);
const changedGo = [...diffFiles].filter(
  (f) => f.startsWith(serverDirRel + '/') && f.endsWith('.go'),
);

if (changedGo.length === 0) {
  console.log('[test-go-changed] no .go changes detected — running full suite');
  const r = spawnSync(
    'go',
    ['test', './...', ...passThrough],
    {
      cwd: serverDir,
      stdio: 'inherit',
      env: { ...process.env, GOWORK: process.env.GOWORK ?? 'off' },
    },
  );
  process.exit(r.status ?? 1);
}

// Map each changed .go file to its package directory (relative to serverDir).
const pkgs = new Set();
for (const file of changedGo) {
  const rel = relative(serverDir, resolve(repoRoot, file));
  const pkgDir = dirname(rel);
  const pkgPath = pkgDir === '.' ? './' : `./${pkgDir}/`;
  const absPkg = resolve(serverDir, pkgDir);
  if (!existsSync(absPkg) || !statSync(absPkg).isDirectory()) continue;
  pkgs.add(pkgPath);
}

if (pkgs.size === 0) {
  console.log('[test-go-changed] no resolvable packages — nothing to do');
  process.exit(0);
}

const pkgList = [...pkgs].sort();
console.log(`[test-go-changed] running ${pkgList.length} package(s): ${pkgList.join(' ')}`);
const r = spawnSync(
  'go',
  ['test', ...pkgList, ...passThrough],
  {
    cwd: serverDir,
    stdio: 'inherit',
    env: { ...process.env, GOWORK: process.env.GOWORK ?? 'off' },
  },
);
process.exit(r.status ?? 1);
