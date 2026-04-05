import type { MarkdownViewMode } from './editorPreferences.js';

export const EDITOR_STATUS_EVENT = 'engine:editor-status';
export const REQUEST_CLOSE_FILE_EVENT = 'engine:request-close-file';
export const PERFORM_CLOSE_FILE_EVENT = 'engine:perform-close-file';
export const SAVE_FILES_EVENT = 'engine:save-files';

export interface EditorStatusDetail {
  path: string;
  language: string;
  fileSizeLabel: string;
  locationLabel: string;
  syntaxStatus: string;
  wrapLabel: string;
  markdownFileActive: boolean;
  markdownViewMode: MarkdownViewMode;
  canSave: boolean;
  dirty: boolean;
}

export interface CloseFileEventDetail {
  path: string;
}

export interface SaveFilesEventDetail {
  paths: string[];
}
