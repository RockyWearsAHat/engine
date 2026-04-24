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

  it('SetMessagesWhileStreaming_StreamingIdCleared', () => {
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

describe('store_resolveToolCall_nonMatchingMessages', () => {
  beforeEach(() => {
    send.mockReset();
    useStore.setState({ chatMessages: [], streamingMessageId: null });
  });

  it('store_resolveToolCall_nonMatchingMessageLeftUnchanged', () => {
    useStore.getState().startAssistantMessage('msg1');
    useStore.getState().addToolCall('msg1', { id: 'tc1', name: 'read_file', input: {}, pending: true });
    useStore.getState().startAssistantMessage('msg2');

    useStore.getState().resolveToolCall('msg1', 'tc1', 'file content', false, 50);

    const msg2 = useStore.getState().chatMessages.find(m => m.id === 'msg2');
    expect(msg2?.toolCalls).toHaveLength(0);
    expect(msg2?.streaming).toBe(true);
  });

  it('store_resolveToolCall_nonMatchingToolCallLeftPending', () => {
    useStore.getState().startAssistantMessage('msg1');
    useStore.getState().addToolCall('msg1', { id: 'tc1', name: 'read_file', input: {}, pending: true });
    useStore.getState().addToolCall('msg1', { id: 'tc2', name: 'write_file', input: {}, pending: true });

    useStore.getState().resolveToolCall('msg1', 'tc1', 'done', false, 30);

    const msg1 = useStore.getState().chatMessages.find(m => m.id === 'msg1');
    expect(msg1?.toolCalls.find(tc => tc.id === 'tc1')?.pending).toBe(false);
    expect(msg1?.toolCalls.find(tc => tc.id === 'tc2')?.pending).toBe(true);
  });

  it('store_markMessageFailed_setsFailedAndClearsStreamingId', () => {
    useStore.getState().startAssistantMessage('fail-msg');
    expect(useStore.getState().streamingMessageId).toBe('fail-msg');

    useStore.getState().markMessageFailed('fail-msg');

    const msg = useStore.getState().chatMessages.find(m => m.id === 'fail-msg');
    expect(msg?.failed).toBe(true);
    expect(msg?.streaming).toBe(false);
    expect(useStore.getState().streamingMessageId).toBeNull();
  });

  it('store_setMessages_nullToolCallsDefaultsToEmpty', () => {
    useStore.getState().setMessages([
      { id: 'h1', role: 'assistant', content: 'hello', toolCalls: null as unknown as [] },
    ]);

    const msg = useStore.getState().chatMessages.find(m => m.id === 'h1');
    expect(msg?.toolCalls).toEqual([]);
  });
});
