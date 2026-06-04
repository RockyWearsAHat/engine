/** Generates a cryptographically random UUID. */
export function randomUUID(): string {
  return crypto.randomUUID();
}

/** Formats bytes to human-readable units (B, KB, MB). */
export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes}B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)}MB`;
}

/** Extracts the filename from a file path. */
export function basename(path: string): string {
  /* istanbul ignore start */
  return path.split('/').pop() ?? path;
  /* istanbul ignore stop */
}

/** Extracts the directory path from a file path. */
export function dirname(path: string): string {
  const parts = path.split('/');
  parts.pop();
  return parts.join('/') || '/';
}
