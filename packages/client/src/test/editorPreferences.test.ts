import { describe, it, expect } from 'vitest';
import {
  normalizeEditorPreferences,
  DEFAULT_EDITOR_PREFERENCES,
  editorFontOptions,
} from '../editorPreferences.js';

describe('normalizeEditorPreferences', () => {
  describe('defaults', () => {
    it('NullInput_FullDefaultsReturned', () => {
      expect(normalizeEditorPreferences(null)).toEqual(DEFAULT_EDITOR_PREFERENCES);
    });

    it('UndefinedInput_FullDefaultsReturned', () => {
      expect(normalizeEditorPreferences(undefined)).toEqual(DEFAULT_EDITOR_PREFERENCES);
    });

    it('EmptyObject_FullDefaultsReturned', () => {
      expect(normalizeEditorPreferences({})).toEqual(DEFAULT_EDITOR_PREFERENCES);
    });
  });

  describe('fontFamily', () => {
    it('KnownFontValue_AcceptedVerbatim', () => {
      const { value } = editorFontOptions[1]; // IBM Plex Mono
      const result = normalizeEditorPreferences({ fontFamily: value });
      expect(result.fontFamily).toBe(value);
    });

    it('UnrecognisedFontString_DefaultUsed', () => {
      const result = normalizeEditorPreferences({ fontFamily: 'Comic Sans MS' });
      expect(result.fontFamily).toBe(DEFAULT_EDITOR_PREFERENCES.fontFamily);
    });

    it('EmptyFontString_DefaultUsed', () => {
      const result = normalizeEditorPreferences({ fontFamily: '' });
      expect(result.fontFamily).toBe(DEFAULT_EDITOR_PREFERENCES.fontFamily);
    });
  });

  describe('fontSize clamping', () => {
    it('BelowMinimumFontSize_ClampedTo11', () => {
      expect(normalizeEditorPreferences({ fontSize: 5 }).fontSize).toBe(11);
    });

    it('AboveMaximumFontSize_ClampedTo20', () => {
      expect(normalizeEditorPreferences({ fontSize: 99 }).fontSize).toBe(20);
    });

    it('ValidFontSize13_Accepted', () => {
      expect(normalizeEditorPreferences({ fontSize: 13 }).fontSize).toBe(13);
    });

    it('NonIntegerFontSize_Rounded', () => {
      expect(normalizeEditorPreferences({ fontSize: 13.7 }).fontSize).toBe(14);
    });

    it('NaNFontSize_DefaultUsed', () => {
      expect(normalizeEditorPreferences({ fontSize: NaN }).fontSize).toBe(DEFAULT_EDITOR_PREFERENCES.fontSize);
    });
  });

  describe('lineHeight clamping', () => {
    it('BelowMinimumLineHeight_ClampedTo1_35', () => {
      expect(normalizeEditorPreferences({ lineHeight: 0.5 }).lineHeight).toBeGreaterThanOrEqual(1.35);
    });

    it('AboveMaximumLineHeight_ClampedTo2_05', () => {
      expect(normalizeEditorPreferences({ lineHeight: 5 }).lineHeight).toBeLessThanOrEqual(2.05);
    });

    it('LineHeight1_6_Accepted', () => {
      expect(normalizeEditorPreferences({ lineHeight: 1.6 }).lineHeight).toBe(1.6);
    });
  });

  describe('tabSize validation', () => {
    it('TabSize2_Accepted', () => {
      expect(normalizeEditorPreferences({ tabSize: 2 }).tabSize).toBe(2);
    });

    it('TabSize4_Accepted', () => {
      expect(normalizeEditorPreferences({ tabSize: 4 }).tabSize).toBe(4);
    });

    it('TabSize8_Accepted', () => {
      expect(normalizeEditorPreferences({ tabSize: 8 }).tabSize).toBe(8);
    });

    it('TabSize3NotAllowed_DefaultUsed', () => {
      expect(normalizeEditorPreferences({ tabSize: 3 }).tabSize).toBe(DEFAULT_EDITOR_PREFERENCES.tabSize);
    });

    it('TabSize0_DefaultUsed', () => {
      expect(normalizeEditorPreferences({ tabSize: 0 }).tabSize).toBe(DEFAULT_EDITOR_PREFERENCES.tabSize);
    });
  });

  describe('markdownViewMode validation', () => {
    it('TextMode_Accepted', () => {
      expect(normalizeEditorPreferences({ markdownViewMode: 'text' }).markdownViewMode).toBe('text');
    });

    it('PreviewMode_Accepted', () => {
      expect(normalizeEditorPreferences({ markdownViewMode: 'preview' }).markdownViewMode).toBe('preview');
    });

    it('SplitMode_Accepted', () => {
      expect(normalizeEditorPreferences({ markdownViewMode: 'split' }).markdownViewMode).toBe('split');
    });

    it('SyntacticalMode_Accepted', () => {
      expect(normalizeEditorPreferences({ markdownViewMode: 'syntactical' }).markdownViewMode).toBe('syntactical');
    });

    it('UnknownMarkdownMode_DefaultUsed', () => {
      // @ts-expect-error intentionally invalid input
      expect(normalizeEditorPreferences({ markdownViewMode: 'wysiwyg' }).markdownViewMode).toBe(DEFAULT_EDITOR_PREFERENCES.markdownViewMode);
    });
  });

  describe('wordWrap coercion', () => {
    it('TruthyValue_CoercedToTrue', () => {
      expect(normalizeEditorPreferences({ wordWrap: true }).wordWrap).toBe(true);
    });

    it('FalsyValue_CoercedToFalse', () => {
      expect(normalizeEditorPreferences({ wordWrap: false }).wordWrap).toBe(false);
    });
  });

  describe('partial inputs', () => {
    it('SpecifiedFieldOnly_DefaultsUsedForRest', () => {
      const result = normalizeEditorPreferences({ tabSize: 4 });
      expect(result.tabSize).toBe(4);
      expect(result.fontSize).toBe(DEFAULT_EDITOR_PREFERENCES.fontSize);
      expect(result.wordWrap).toBe(DEFAULT_EDITOR_PREFERENCES.wordWrap);
    });
  });
});
