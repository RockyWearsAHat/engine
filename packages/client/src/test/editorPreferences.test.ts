import { describe, it, expect } from 'vitest';
import {
  normalizeEditorPreferences,
  DEFAULT_EDITOR_PREFERENCES,
  editorFontOptions,
} from '../editorPreferences.js';

describe('normalizeEditorPreferences', () => {
  describe('defaults', () => {
    it('returns full defaults when called with null', () => {
      expect(normalizeEditorPreferences(null)).toEqual(DEFAULT_EDITOR_PREFERENCES);
    });

    it('returns full defaults when called with undefined', () => {
      expect(normalizeEditorPreferences(undefined)).toEqual(DEFAULT_EDITOR_PREFERENCES);
    });

    it('returns full defaults when called with empty object', () => {
      expect(normalizeEditorPreferences({})).toEqual(DEFAULT_EDITOR_PREFERENCES);
    });
  });

  describe('fontFamily', () => {
    it('accepts a known font option value verbatim', () => {
      const { value } = editorFontOptions[1]; // IBM Plex Mono
      const result = normalizeEditorPreferences({ fontFamily: value });
      expect(result.fontFamily).toBe(value);
    });

    it('falls back to default for an unrecognised font string', () => {
      const result = normalizeEditorPreferences({ fontFamily: 'Comic Sans MS' });
      expect(result.fontFamily).toBe(DEFAULT_EDITOR_PREFERENCES.fontFamily);
    });

    it('falls back to default for an empty string', () => {
      const result = normalizeEditorPreferences({ fontFamily: '' });
      expect(result.fontFamily).toBe(DEFAULT_EDITOR_PREFERENCES.fontFamily);
    });
  });

  describe('fontSize clamping', () => {
    it('clamps below-minimum fontSize to 11', () => {
      expect(normalizeEditorPreferences({ fontSize: 5 }).fontSize).toBe(11);
    });

    it('clamps above-maximum fontSize to 20', () => {
      expect(normalizeEditorPreferences({ fontSize: 99 }).fontSize).toBe(20);
    });

    it('accepts a valid fontSize of 13', () => {
      expect(normalizeEditorPreferences({ fontSize: 13 }).fontSize).toBe(13);
    });

    it('rounds non-integer fontSize', () => {
      expect(normalizeEditorPreferences({ fontSize: 13.7 }).fontSize).toBe(14);
    });

    it('falls back to default for NaN fontSize', () => {
      expect(normalizeEditorPreferences({ fontSize: NaN }).fontSize).toBe(DEFAULT_EDITOR_PREFERENCES.fontSize);
    });
  });

  describe('lineHeight clamping', () => {
    it('clamps below-minimum lineHeight to 1.35', () => {
      expect(normalizeEditorPreferences({ lineHeight: 0.5 }).lineHeight).toBeGreaterThanOrEqual(1.35);
    });

    it('clamps above-maximum lineHeight to 2.05', () => {
      expect(normalizeEditorPreferences({ lineHeight: 5 }).lineHeight).toBeLessThanOrEqual(2.05);
    });

    it('accepts lineHeight 1.6', () => {
      expect(normalizeEditorPreferences({ lineHeight: 1.6 }).lineHeight).toBe(1.6);
    });
  });

  describe('tabSize validation', () => {
    it('accepts tabSize 2', () => {
      expect(normalizeEditorPreferences({ tabSize: 2 }).tabSize).toBe(2);
    });

    it('accepts tabSize 4', () => {
      expect(normalizeEditorPreferences({ tabSize: 4 }).tabSize).toBe(4);
    });

    it('accepts tabSize 8', () => {
      expect(normalizeEditorPreferences({ tabSize: 8 }).tabSize).toBe(8);
    });

    it('falls back to default for tabSize 3 (not in allowed set)', () => {
      expect(normalizeEditorPreferences({ tabSize: 3 }).tabSize).toBe(DEFAULT_EDITOR_PREFERENCES.tabSize);
    });

    it('falls back to default for tabSize 0', () => {
      expect(normalizeEditorPreferences({ tabSize: 0 }).tabSize).toBe(DEFAULT_EDITOR_PREFERENCES.tabSize);
    });
  });

  describe('markdownViewMode validation', () => {
    it('accepts text', () => {
      expect(normalizeEditorPreferences({ markdownViewMode: 'text' }).markdownViewMode).toBe('text');
    });

    it('accepts preview', () => {
      expect(normalizeEditorPreferences({ markdownViewMode: 'preview' }).markdownViewMode).toBe('preview');
    });

    it('accepts split', () => {
      expect(normalizeEditorPreferences({ markdownViewMode: 'split' }).markdownViewMode).toBe('split');
    });

    it('accepts syntactical', () => {
      expect(normalizeEditorPreferences({ markdownViewMode: 'syntactical' }).markdownViewMode).toBe('syntactical');
    });

    it('falls back to default for unknown markdown mode', () => {
      // @ts-expect-error intentionally invalid input
      expect(normalizeEditorPreferences({ markdownViewMode: 'wysiwyg' }).markdownViewMode).toBe(DEFAULT_EDITOR_PREFERENCES.markdownViewMode);
    });
  });

  describe('wordWrap coercion', () => {
    it('coerces truthy value to true', () => {
      expect(normalizeEditorPreferences({ wordWrap: true }).wordWrap).toBe(true);
    });

    it('coerces falsy value to false', () => {
      expect(normalizeEditorPreferences({ wordWrap: false }).wordWrap).toBe(false);
    });
  });

  describe('partial inputs', () => {
    it('applies only the specified field and uses defaults for the rest', () => {
      const result = normalizeEditorPreferences({ tabSize: 4 });
      expect(result.tabSize).toBe(4);
      expect(result.fontSize).toBe(DEFAULT_EDITOR_PREFERENCES.fontSize);
      expect(result.wordWrap).toBe(DEFAULT_EDITOR_PREFERENCES.wordWrap);
    });
  });
});
