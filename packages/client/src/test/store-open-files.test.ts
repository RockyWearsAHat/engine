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

  it('does not churn state when a dirty file is marked dirty again', () => {
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

  it('does not churn state when a clean file is marked saved again', () => {
    useStore.getState().openFile('/workspace/editor.ts', 'const value = 1;\n', 'typescript', 17);
    send.mockClear();
    const cleanOpenFiles = useStore.getState().openFiles;

    useStore.getState().markFileSaved('/workspace/editor.ts');

    expect(useStore.getState().openFiles).toBe(cleanOpenFiles);
    expect(send).not.toHaveBeenCalled();
  });
});
