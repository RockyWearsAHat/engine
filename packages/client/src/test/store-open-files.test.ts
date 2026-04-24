import { beforeEach, describe, expect, it, vi } from 'vitest';

const { send } = vi.hoisted(() => ({
  send: vi.fn(),
}));

vi.mock('../ws/client.js', () => ({
  wsClient: {
    send,
  },
}));

import { useStore } from '../store/index.js';

describe('open file dirty state transitions', () => {
  beforeEach(() => {
    send.mockReset();
    useStore.setState({ openFiles: [], activeFilePath: null });
  });

  it('AlreadyDirtyFile_StateUnchangedAndNoSend', () => {
    useStore.getState().openFile('/workspace/editor.ts', 'const value = 1;\n', 'typescript', 17);
    send.mockClear();

    useStore.getState().markFileDirty('/workspace/editor.ts');
    const dirtyOpenFiles = useStore.getState().openFiles;

    expect(dirtyOpenFiles[0]?.dirty).toBe(true);

    send.mockClear();
    useStore.getState().markFileDirty('/workspace/editor.ts');

    expect(useStore.getState().openFiles).toBe(dirtyOpenFiles);
    expect(send).not.toHaveBeenCalled();
  });

  it('AlreadyCleanFile_StateUnchangedAndNoSend', () => {
    useStore.getState().openFile('/workspace/editor.ts', 'const value = 1;\n', 'typescript', 17);
    send.mockClear();
    const cleanOpenFiles = useStore.getState().openFiles;

    useStore.getState().markFileSaved('/workspace/editor.ts');

    expect(useStore.getState().openFiles).toBe(cleanOpenFiles);
    expect(send).not.toHaveBeenCalled();
  });
});

describe('store_markFileDirty_onlyTargetFileMarkedDirty', () => {
  beforeEach(() => {
    send.mockReset();
    useStore.setState({ openFiles: [], activeFilePath: null });
  });

  it('store_markFileDirty_otherOpenFilesUnchanged', () => {
    useStore.getState().openFile('/a.ts', 'a', 'typescript', 1);
    useStore.getState().openFile('/b.ts', 'b', 'typescript', 1);

    useStore.getState().markFileDirty('/a.ts');

    expect(useStore.getState().openFiles.find(f => f.path === '/a.ts')?.dirty).toBe(true);
    expect(useStore.getState().openFiles.find(f => f.path === '/b.ts')?.dirty).toBe(false);
  });

  it('store_markFileSaved_otherOpenFilesUnchanged', () => {
    useStore.getState().openFile('/a.ts', 'a', 'typescript', 1);
    useStore.getState().openFile('/b.ts', 'b', 'typescript', 1);
    useStore.getState().markFileDirty('/a.ts');
    useStore.getState().markFileDirty('/b.ts');

    useStore.getState().markFileSaved('/a.ts');

    expect(useStore.getState().openFiles.find(f => f.path === '/a.ts')?.dirty).toBe(false);
    expect(useStore.getState().openFiles.find(f => f.path === '/b.ts')?.dirty).toBe(true);
  });

  it('store_openFile_reopeningSamePathUpdatesContent', () => {
    useStore.getState().openFile('/editor.ts', 'first content', 'typescript', 100);
    expect(useStore.getState().openFiles).toHaveLength(1);

    useStore.getState().openFile('/editor.ts', 'updated content', 'typescript', 200);
    expect(useStore.getState().openFiles).toHaveLength(1);
    expect(useStore.getState().openFiles[0]?.content).toBe('updated content');
    expect(useStore.getState().openFiles[0]?.size).toBe(200);
  });
});
