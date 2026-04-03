import { simpleGit, type SimpleGit } from 'simple-git';
import type { GitStatus, GitCommit } from '@myeditor/shared';

export function getGit(cwd: string): SimpleGit {
  return simpleGit(cwd);
}

export async function getStatus(cwd: string): Promise<GitStatus> {
  const git = getGit(cwd);
  const [status, branch] = await Promise.all([
    git.status(),
    git.revparse(['--abbrev-ref', 'HEAD']).catch(() => 'unknown'),
  ]);

  let ahead = 0;
  let behind = 0;
  try {
    const tracking = await git.revparse(['--abbrev-ref', '@{u}']);
    if (tracking) {
      const counts = await git.raw(['rev-list', '--count', '--left-right', `${tracking}...HEAD`]);
      const parts = counts.trim().split(/\s+/);
      behind = parseInt(parts[0] ?? '0', 10);
      ahead = parseInt(parts[1] ?? '0', 10);
    }
  } catch { /* no upstream */ }

  return {
    branch: branch.trim(),
    staged: status.staged,
    unstaged: status.modified.filter(f => !status.staged.includes(f)),
    untracked: status.not_added,
    ahead,
    behind,
  };
}

export async function getDiff(cwd: string, filePath?: string): Promise<string> {
  const git = getGit(cwd);
  const args = filePath ? ['--', filePath] : [];
  const [unstaged, staged] = await Promise.all([
    git.diff(args),
    git.diff(['--staged', ...args]),
  ]);
  return (staged + unstaged).trim() || '(no changes)';
}

export async function getLog(cwd: string, limit = 20): Promise<GitCommit[]> {
  const git = getGit(cwd);
  const log = await git.log({ maxCount: limit });
  return log.all.map(c => ({
    hash: c.hash.slice(0, 8),
    message: c.message,
    author: c.author_name,
    date: c.date,
  }));
}

export async function commit(cwd: string, message: string): Promise<string> {
  const git = getGit(cwd);
  await git.add('-A');
  const result = await git.commit(message);
  return result.commit;
}

export async function getCurrentBranch(cwd: string): Promise<string> {
  try {
    const branch = await getGit(cwd).revparse(['--abbrev-ref', 'HEAD']);
    return branch.trim();
  } catch {
    return 'unknown';
  }
}
