import fs from 'node:fs/promises';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import type { FileNode, FileContent } from '@myeditor/shared';

const execFileAsync = promisify(execFile);

const IGNORED = new Set(['.git', 'node_modules', 'dist', 'out', 'build', '.myeditor', '.DS_Store']);

export async function readFile(filePath: string): Promise<FileContent> {
  const content = await fs.readFile(filePath, 'utf-8');
  const stats = await fs.stat(filePath);
  return {
    path: filePath,
    content,
    language: detectLanguage(filePath),
    size: stats.size,
  };
}

export async function writeFile(filePath: string, content: string): Promise<void> {
  await fs.mkdir(path.dirname(filePath), { recursive: true });
  await fs.writeFile(filePath, content, 'utf-8');
}

export async function getTree(dirPath: string, depth = 0, maxDepth = 4): Promise<FileNode> {
  const stats = await fs.stat(dirPath);
  const name = path.basename(dirPath);

  if (!stats.isDirectory()) {
    return { name, path: dirPath, type: 'file', size: stats.size, modified: stats.mtime.toISOString() };
  }

  if (depth >= maxDepth) {
    return { name, path: dirPath, type: 'directory', children: [] };
  }

  let entries: string[] = [];
  try {
    entries = await fs.readdir(dirPath);
  } catch {
    return { name, path: dirPath, type: 'directory', children: [] };
  }

  const children = await Promise.all(
    entries
      .filter(e => !IGNORED.has(e) && !e.startsWith('.'))
      .sort((a, b) => a.localeCompare(b))
      .map(async entry => {
        const entryPath = path.join(dirPath, entry);
        try {
          return await getTree(entryPath, depth + 1, maxDepth);
        } catch {
          return null;
        }
      })
  );

  return {
    name,
    path: dirPath,
    type: 'directory',
    children: children.filter((c): c is FileNode => c !== null),
  };
}

export async function searchFiles(pattern: string, directory: string, fileGlob?: string): Promise<string> {
  try {
    const args = ['-r', '--line-number', '--no-heading', '--color=never'];
    if (fileGlob) args.push('--glob', fileGlob);
    args.push(pattern, directory);
    const { stdout } = await execFileAsync('rg', args, { maxBuffer: 1024 * 1024 });
    return stdout || '(no matches)';
  } catch (err: unknown) {
    if ((err as { code?: number }).code === 1) return '(no matches)';
    throw err;
  }
}

export function detectLanguage(filePath: string): string {
  const ext = path.extname(filePath).toLowerCase().slice(1);
  const map: Record<string, string> = {
    ts: 'typescript', tsx: 'typescriptreact',
    js: 'javascript', jsx: 'javascriptreact',
    py: 'python', rb: 'ruby', go: 'go', rs: 'rust',
    java: 'java', c: 'c', cpp: 'cpp', h: 'c', hpp: 'cpp',
    cs: 'csharp', swift: 'swift', kt: 'kotlin',
    html: 'html', css: 'css', scss: 'scss', less: 'less',
    json: 'json', yaml: 'yaml', yml: 'yaml', toml: 'toml',
    md: 'markdown', mdx: 'markdown',
    sh: 'shell', bash: 'shell', zsh: 'shell',
    sql: 'sql', graphql: 'graphql',
    xml: 'xml', svg: 'xml',
    dockerfile: 'dockerfile',
  };
  return map[ext] ?? 'plaintext';
}
