import { describe, expect, it } from 'vitest';
import {
  buildLineBreaks,
  getHighlightDelayMs,
  lineColumnFromOffset,
  updateLineBreaksForEdit,
} from '../components/Editor/editorBuffer.js';

describe('editorBuffer', () => {
  it('matches a full rebuild for inserted multi-line text', () => {
    const original = 'alpha\nbeta\ngamma';
    const start = original.indexOf('beta');
    const inserted = 'beta\nbeta-2\n';
    const nextValue = `${original.slice(0, start)}${inserted}${original.slice(start + 'beta'.length)}`;

    const nextBreaks = updateLineBreaksForEdit(
      buildLineBreaks(original),
      start,
      start + 'beta'.length,
      inserted,
    );

    expect(nextBreaks).toEqual(buildLineBreaks(nextValue));
  });

  it('matches a full rebuild for deleting a newline range', () => {
    const original = 'one\ntwo\nthree\nfour';
    const start = original.indexOf('\ntwo');
    const end = original.indexOf('\nfour');
    const nextValue = `${original.slice(0, start)}${original.slice(end)}`;

    const nextBreaks = updateLineBreaksForEdit(
      buildLineBreaks(original),
      start,
      end,
      '',
    );

    expect(nextBreaks).toEqual(buildLineBreaks(nextValue));
  });

  it('resolves line and column from cached line breaks', () => {
    const text = 'red\nblue\ngreen';
    const breaks = buildLineBreaks(text);

    expect(lineColumnFromOffset(breaks, text.indexOf('blue'))).toEqual({ line: 2, column: 1 });
    expect(lineColumnFromOffset(breaks, text.length)).toEqual({ line: 3, column: 6 });
  });

  it('backs off highlight work for larger buffers', () => {
    expect(getHighlightDelayMs(4 * 1024)).toBe(0);
    expect(getHighlightDelayMs(48 * 1024)).toBe(40);
    expect(getHighlightDelayMs(256 * 1024)).toBe(110);
  });
});
