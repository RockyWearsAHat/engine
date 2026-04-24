import { describe, expect, it } from 'vitest';
import { formatBytes, basename, dirname, randomUUID } from '../utils.js';

describe('utils', () => {
  describe('randomUUID', () => {
    it('Default_NonEmptyStringReturned', () => {
      expect(typeof randomUUID()).toBe('string');
      expect(randomUUID().length).toBeGreaterThan(0);
    });

    it('CalledTwice_UniqueValuesReturned', () => {
      const a = randomUUID();
      const b = randomUUID();
      expect(a).not.toBe(b);
    });

    it('Default_UUIDv4FormatReturned', () => {
      const uuid = randomUUID();
      expect(uuid).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    });
  });

  describe('formatBytes', () => {
    it('Zero_FormattedAsZeroBytes', () => {
      expect(formatBytes(0)).toBe('0B');
    });

    it('OneByte_FormattedAs1B', () => {
      expect(formatBytes(1)).toBe('1B');
    });

    it('1023Bytes_FormattedInBytes', () => {
      expect(formatBytes(1023)).toBe('1023B');
    });

    it('Exactly1KB_FormattedAs1_0KB', () => {
      expect(formatBytes(1024)).toBe('1.0KB');
    });

    it('1536Bytes_FormattedAs1_5KB', () => {
      expect(formatBytes(1536)).toBe('1.5KB');
    });

    it('1023x1024Bytes_StillFormattedInKB', () => {
      expect(formatBytes(1023 * 1024)).toBe('1023.0KB');
    });

    it('Exactly1MB_FormattedAs1_0MB', () => {
      expect(formatBytes(1024 * 1024)).toBe('1.0MB');
    });

    it('2_5MB_FormattedAs2_5MB', () => {
      expect(formatBytes(2.5 * 1024 * 1024)).toBe('2.5MB');
    });
  });

  describe('basename', () => {
    it('StandardUnixPath_FilenameReturned', () => {
      expect(basename('/home/user/project/main.ts')).toBe('main.ts');
    });

    it('SingleSegmentPath_ReturnedAsIs', () => {
      expect(basename('file.txt')).toBe('file.txt');
    });

    it('DeeplyNestedPath_LastSegmentReturned', () => {
      expect(basename('/a/b/c/d/e.go')).toBe('e.go');
    });

    it('EmptyString_EmptyStringReturned', () => {
      expect(basename('')).toBe('');
    });

    it('TrailingSlashPath_EmptyStringReturned', () => {
      expect(basename('/home/user/')).toBe('');
    });
  });

  describe('dirname', () => {
    it('FilePath_ParentDirectoryReturned', () => {
      expect(dirname('/home/user/project/main.ts')).toBe('/home/user/project');
    });

    it('TopLevelFile_RootReturned', () => {
      expect(dirname('/file.txt')).toBe('/');
    });

    it('OnlyComponentRemoved_RootReturned', () => {
      expect(dirname('/')).toBe('/');
    });

    it('PathWithoutLeadingSlash_ParentReturned', () => {
      expect(dirname('a/b/c.ts')).toBe('a/b');
    });

    it('SingleSegmentNoSlash_RootReturned', () => {
      expect(dirname('file.txt')).toBe('/');
    });
  });
});
