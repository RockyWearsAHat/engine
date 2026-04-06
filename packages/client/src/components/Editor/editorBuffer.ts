export interface EditorCursorLocation {
  line: number;
  column: number;
}

const IMMEDIATE_HIGHLIGHT_MAX_BYTES = 24 * 1024;
const SOFT_DEBOUNCE_HIGHLIGHT_MAX_BYTES = 128 * 1024;

export function buildLineBreaks(text: string): number[] {
  const breaks: number[] = [];
  for (let i = 0; i < text.length; i += 1) {
    if (text.charCodeAt(i) === 10) {
      breaks.push(i);
    }
  }
  return breaks;
}

export function updateLineBreaksForEdit(
  lineBreaks: number[],
  start: number,
  end: number,
  insertedText: string,
): number[] {
  const nextBreaks: number[] = [];
  let index = 0;

  while (index < lineBreaks.length && lineBreaks[index] < start) {
    nextBreaks.push(lineBreaks[index]);
    index += 1;
  }

  for (let i = 0; i < insertedText.length; i += 1) {
    if (insertedText.charCodeAt(i) === 10) {
      nextBreaks.push(start + i);
    }
  }

  const delta = insertedText.length - (end - start);
  while (index < lineBreaks.length && lineBreaks[index] < end) {
    index += 1;
  }
  while (index < lineBreaks.length) {
    nextBreaks.push(lineBreaks[index] + delta);
    index += 1;
  }

  return nextBreaks;
}

export function lineColumnFromOffset(
  lineBreaks: number[],
  offset: number,
): EditorCursorLocation {
  let low = 0;
  let high = lineBreaks.length;

  while (low < high) {
    const mid = (low + high) >>> 1;
    if (lineBreaks[mid] < offset) {
      low = mid + 1;
    } else {
      high = mid;
    }
  }

  const previousBreak = low === 0 ? -1 : lineBreaks[low - 1];
  return {
    line: low + 1,
    column: offset - previousBreak,
  };
}

export function getHighlightDelayMs(textSize: number): number {
  if (textSize <= IMMEDIATE_HIGHLIGHT_MAX_BYTES) {
    return 0;
  }
  if (textSize <= SOFT_DEBOUNCE_HIGHLIGHT_MAX_BYTES) {
    return 40;
  }
  return 110;
}
