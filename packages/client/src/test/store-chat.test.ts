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

describe('chat session reload state', () => {
  beforeEach(() => {
    send.mockReset();
    useStore.setState({
      chatMessages: [],
      streamingMessageId: null,
    });
  });

  it('clears stale streaming state when loaded messages replace the live chat view', () => {
    useStore.getState().startAssistantMessage('assistant-live');

    expect(useStore.getState().streamingMessageId).toBe('assistant-live');

    useStore.getState().setMessages([
      {
        id: 'user-1',
        sessionId: 'session-1',
        role: 'user',
        content: 'hello cave',
        createdAt: '',
        toolCalls: [],
      },
    ]);

    expect(useStore.getState().streamingMessageId).toBeNull();
    expect(useStore.getState().chatMessages).toEqual([
      expect.objectContaining({
        id: 'user-1',
        role: 'user',
        content: 'hello cave',
      }),
    ]);
  });
});
