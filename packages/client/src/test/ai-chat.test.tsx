import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import AIChat from '../components/AI/AIChat.js';
import { useStore } from '../store/index.js';

const { sendMock } = vi.hoisted(() => ({
  sendMock: vi.fn(),
}));

vi.mock('../ws/client.js', () => ({
  wsClient: {
    send: sendMock,
  },
}));

describe('AIChat baseline integration', () => {
  beforeEach(() => {
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
      configurable: true,
      value: vi.fn(),
    });
    sendMock.mockClear();
    useStore.setState({
      activeSession: {
        id: 'session-1',
        projectPath: '/tmp/project',
        branchName: 'main',
        createdAt: '',
        updatedAt: '',
        summary: '',
        messageCount: 0,
      },
      chatMessages: [],
      streamingMessageId: null,
    });
  });

  it('sends a chat payload and adds the local user message', () => {
    render(<AIChat />);

    const input = screen.getByPlaceholderText('Ask anything… (⌘↵ to send)');
    fireEvent.change(input, { target: { value: 'hello cave' } });
    fireEvent.click(screen.getByTitle('Send'));

    expect(sendMock).toHaveBeenCalledWith({
      type: 'chat',
      sessionId: 'session-1',
      content: 'hello cave',
    });
    expect(useStore.getState().chatMessages).toEqual([
      expect.objectContaining({
        role: 'user',
        content: 'hello cave',
      }),
    ]);
  });
});
