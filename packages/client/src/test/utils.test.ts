import { describe, expect, it } from 'vitest';
import { formatBytes, basename, dirname, randomUUID } from '../utils.js';

describe('utils', () => {
  describe('randomUUID', () => {
    it('returns a non-empty string', () => {
      expect(typeof randomUUID()).toBe('string');
      expect(randomUUID().length).toBeGreaterThan(0);
    });

    it('returns a different value on each call', () => {
      const a = randomUUID();
      const b = randomUUID();
      expect(a).not.toBe(b);
    });

    it('returns a value in UUID v4 format', () => {
      const uuid = randomUUID();
      expect(uuid).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    });
  });

  describe('formatBytes', () => {
    it('formats 0 as "0B"', () => {
      expect(formatBytes(0)).toBe('0B');
    });

    it('formats 1 byte as "1B"', () => {
      expect(formatBytes(1)).toBe('1B');
    });

    it('formats 1023 bytes as "1023B"', () => {
      expect(formatBytes(1023)).toBe('1023B');
    });

    it('formats exactly 1 KB as "1.0KB"', () => {
      expect(formatBytes(1024)).toBe('1.0KB');
    });

    it('formats 1536 bytes as "1.5KB"', () => {
      expect(formatBytes(1536)).toBe('1.5KB');
    });

    it('formats 1023 * 1024 bytes still as KB', () => {
      expect(formatBytes(1023 * 1024)).toBe('1023.0KB');
    });

    it('formats exactly 1 MB as "1.0MB"', () => {
      expect(formatBytes(1024 * 1024)).toBe('1.0MB');
    });

    it('formats 2.5 MB as "2.5MB"', () => {
      expect(formatBytes(2.5 * 1024 * 1024)).toBe('2.5MB');
    });
  });

  describe('basename', () => {
    it('returns the filename from a standard unix path', () => {
      expect(basename('/home/user/project/main.ts')).toBe('main.ts');
    });

    it('returns a single segment path as-is', () => {
      expect(basename('file.txt')).toBe('file.txt');
    });

    it('returns the last segment for deeply nested paths', () => {
      expect(basename('/a/b/c/d/e.go')).toBe('e.go');
    });

    it('returns an empty string for an empty input', () => {
      expect(basename('')).toBe('');
    });

    it('returns an empty string for a trailing-slash path', () => {
      expect(basename('/home/user/')).toBe('');
    });
  });

  describe('dirname', () => {
    it('returns the parent directory of a file path', () => {
      expect(dirname('/home/user/project/main.ts')).toBe('/home/user/project');
    });

    it('returns "/" for a top-level file', () => {
      expect(dirname('/file.txt')).toBe('/');
    });

    it('returns "/" when the only component is removed', () => {
      expect(dirname('/')).toBe('/');
    });

    it('returns the parent for a path without a leading slash', () => {
      expect(dirname('a/b/c.ts')).toBe('a/b');
    });

    it('handles a single segment path (no slash) by returning "/"', () => {
      expect(dirname('file.txt')).toBe('/');
    });
  });
});
