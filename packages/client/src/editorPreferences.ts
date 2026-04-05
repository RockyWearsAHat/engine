export type MarkdownViewMode = 'text' | 'preview';

export interface EditorPreferences {
  fontFamily: string;
  fontSize: number;
  lineHeight: number;
  tabSize: number;
  wordWrap: boolean;
  markdownViewMode: MarkdownViewMode;
}

export const editorFontOptions = [
  { label: 'JetBrains Mono', value: '"JetBrains Mono", "IBM Plex Mono", Menlo, Monaco, monospace' },
  { label: 'IBM Plex Mono', value: '"IBM Plex Mono", "JetBrains Mono", Menlo, Monaco, monospace' },
  { label: 'Fira Code', value: '"Fira Code", "JetBrains Mono", Menlo, Monaco, monospace' },
  { label: 'Menlo', value: 'Menlo, Monaco, "JetBrains Mono", monospace' },
] as const;

export const editorFontSizeOptions = [11, 12, 13, 14, 16, 18] as const;

export const editorLineHeightOptions = [
  { label: 'Compact', value: 1.45 },
  { label: 'Balanced', value: 1.6 },
  { label: 'Airy', value: 1.8 },
] as const;

export const editorTabSizeOptions = [2, 4, 8] as const;

export const DEFAULT_EDITOR_PREFERENCES: EditorPreferences = {
  fontFamily: editorFontOptions[0].value,
  fontSize: 13,
  lineHeight: 1.6,
  tabSize: 2,
  wordWrap: false,
  markdownViewMode: 'text',
};

export function normalizeEditorPreferences(
  input: Partial<EditorPreferences> | null | undefined,
): EditorPreferences {
  const fontOptions = new Map<string, string>(editorFontOptions.map(option => [option.value, option.value]));
  const fontSize = Number.isFinite(input?.fontSize) ? Number(input?.fontSize) : DEFAULT_EDITOR_PREFERENCES.fontSize;
  const lineHeight = Number.isFinite(input?.lineHeight)
    ? Number(input?.lineHeight)
    : DEFAULT_EDITOR_PREFERENCES.lineHeight;
  const requestedTabSize = Number.isFinite(input?.tabSize) ? Number(input?.tabSize) : DEFAULT_EDITOR_PREFERENCES.tabSize;

  return {
    fontFamily: fontOptions.get(input?.fontFamily ?? '') ?? DEFAULT_EDITOR_PREFERENCES.fontFamily,
    fontSize: Math.min(20, Math.max(11, Math.round(fontSize))),
    lineHeight: Math.min(2.05, Math.max(1.35, Math.round(lineHeight * 100) / 100)),
    tabSize: editorTabSizeOptions.includes(requestedTabSize as 2 | 4 | 8)
      ? (requestedTabSize as 2 | 4 | 8)
      : DEFAULT_EDITOR_PREFERENCES.tabSize,
    wordWrap: Boolean(input?.wordWrap),
    markdownViewMode: input?.markdownViewMode === 'preview' ? 'preview' : 'text',
  };
}
