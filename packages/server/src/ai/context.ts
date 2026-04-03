import Anthropic from '@anthropic-ai/sdk';
import { randomUUID } from 'node:crypto';
import * as db from '../db/index.js';
import * as fsOps from '../fs/index.js';
import * as gitOps from '../git/index.js';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import type { ToolCall } from '@myeditor/shared';

const execFileAsync = promisify(execFile);

const MODEL = process.env['ANTHROPIC_MODEL'] ?? 'claude-opus-4-5';

export interface ChatContext {
  projectPath: string;
  sessionId: string;
  onChunk: (content: string, done: boolean) => void;
  onToolCall: (name: string, input: unknown) => void;
  onToolResult: (name: string, result: unknown, isError: boolean) => void;
  onError: (error: string) => void;
}

const TOOLS: Anthropic.Tool[] = [
  {
    name: 'read_file',
    description: 'Read the contents of a file at the given path.',
    input_schema: {
      type: 'object',
      properties: { path: { type: 'string', description: 'Absolute path to the file' } },
      required: ['path'],
    },
  },
  {
    name: 'write_file',
    description: 'Write content to a file (creates file and parent dirs if needed).',
    input_schema: {
      type: 'object',
      properties: {
        path: { type: 'string', description: 'Absolute path to write to' },
        content: { type: 'string', description: 'Content to write' },
      },
      required: ['path', 'content'],
    },
  },
  {
    name: 'list_directory',
    description: 'List files and directories at a path, up to 4 levels deep.',
    input_schema: {
      type: 'object',
      properties: { path: { type: 'string', description: 'Absolute directory path' } },
      required: ['path'],
    },
  },
  {
    name: 'shell',
    description: 'Execute a shell command and return stdout + stderr. Use for running tests, builds, installs, etc.',
    input_schema: {
      type: 'object',
      properties: {
        command: { type: 'string', description: 'Shell command to run' },
        cwd: { type: 'string', description: 'Working directory (optional, defaults to project root)' },
      },
      required: ['command'],
    },
  },
  {
    name: 'search_files',
    description: 'Search for a pattern in files using ripgrep. Returns matching lines with file paths and line numbers.',
    input_schema: {
      type: 'object',
      properties: {
        pattern: { type: 'string', description: 'Regex pattern to search for' },
        directory: { type: 'string', description: 'Directory to search in (optional, defaults to project root)' },
        file_pattern: { type: 'string', description: 'Glob pattern to filter files (e.g. "*.ts")' },
      },
      required: ['pattern'],
    },
  },
  {
    name: 'git_status',
    description: 'Get the current git status: branch, staged/unstaged/untracked files.',
    input_schema: { type: 'object', properties: {} },
  },
  {
    name: 'git_diff',
    description: 'Get git diff for current changes (staged + unstaged).',
    input_schema: {
      type: 'object',
      properties: { path: { type: 'string', description: 'Specific file path to diff (optional)' } },
    },
  },
  {
    name: 'git_commit',
    description: 'Stage all changes and create a git commit.',
    input_schema: {
      type: 'object',
      properties: { message: { type: 'string', description: 'Commit message' } },
      required: ['message'],
    },
  },
  {
    name: 'open_file',
    description: 'Open a file in the editor UI so the user can see it.',
    input_schema: {
      type: 'object',
      properties: { path: { type: 'string', description: 'Absolute path to the file to open' } },
      required: ['path'],
    },
  },
];

export async function executeTool(
  name: string,
  input: Record<string, unknown>,
  projectPath: string,
): Promise<{ result: string; isError: boolean }> {
  try {
    switch (name) {
      case 'read_file': {
        const fc = await fsOps.readFile(input['path'] as string);
        return { result: fc.content, isError: false };
      }
      case 'write_file': {
        await fsOps.writeFile(input['path'] as string, input['content'] as string);
        return { result: `File written: ${input['path']}`, isError: false };
      }
      case 'list_directory': {
        const tree = await fsOps.getTree(input['path'] as string);
        return { result: formatTree(tree), isError: false };
      }
      case 'shell': {
        const cwd = (input['cwd'] as string | undefined) ?? projectPath;
        try {
          const { stdout, stderr } = await execFileAsync('bash', ['-c', input['command'] as string], {
            cwd,
            maxBuffer: 1024 * 1024 * 4,
            timeout: 30000,
          });
          const out = [stdout, stderr].filter(Boolean).join('\n').trim();
          return { result: out || '(no output)', isError: false };
        } catch (err: unknown) {
          const e = err as { stdout?: string; stderr?: string; message?: string };
          const out = [e.stdout, e.stderr, e.message].filter(Boolean).join('\n').trim();
          return { result: out || 'Command failed', isError: true };
        }
      }
      case 'search_files': {
        const result = await fsOps.searchFiles(
          input['pattern'] as string,
          (input['directory'] as string | undefined) ?? projectPath,
          input['file_pattern'] as string | undefined
        );
        return { result, isError: false };
      }
      case 'git_status': {
        const status = await gitOps.getStatus(projectPath);
        return { result: JSON.stringify(status, null, 2), isError: false };
      }
      case 'git_diff': {
        const diff = await gitOps.getDiff(projectPath, input['path'] as string | undefined);
        return { result: diff, isError: false };
      }
      case 'git_commit': {
        const hash = await gitOps.commit(projectPath, input['message'] as string);
        return { result: `Committed: ${hash}`, isError: false };
      }
      case 'open_file': {
        return { result: `Opening ${input['path']}`, isError: false };
      }
      default:
        return { result: `Unknown tool: ${name}`, isError: true };
    }
  } catch (err: unknown) {
    return { result: String(err), isError: true };
  }
}

export async function chat(ctx: ChatContext, userMessage: string): Promise<void> {
  const client = new Anthropic({ apiKey: process.env['ANTHROPIC_API_KEY'] });

  // Save user message
  const userMsgId = randomUUID();
  db.saveMessage(userMsgId, ctx.sessionId, 'user', userMessage);

  // Load full history (including the message just saved)
  const history = db.getMessages(ctx.sessionId);
  const session = db.getSession(ctx.sessionId);

  // Build Anthropic messages from persisted history (all prior turns)
  const messages: Anthropic.MessageParam[] = history.slice(0, -1).map(m => ({
    role: m.role as 'user' | 'assistant',
    content: m.content,
  }));
  messages.push({ role: 'user', content: userMessage });

  // Build system prompt
  let branchName = 'unknown';
  try {
    branchName = await gitOps.getCurrentBranch(ctx.projectPath);
  } catch { /* no git */ }

  const systemPrompt = [
    'You are the AI assistant for MyEditor — an AI-native code editor.',
    'You ARE the editor. You have full control: you can read files, write files, run commands, search code, and commit changes.',
    '',
    `Project: ${ctx.projectPath}`,
    `Branch: ${branchName}`,
    session?.summary ? `Project context: ${session.summary}` : '',
    '',
    'Key principles:',
    '- Always validate changes by running the code, not just checking syntax',
    '- Use the shell tool to run tests, builds, and observe real output',
    '- When you open a file, the user sees it in the Monaco editor immediately',
    '- Keep the user informed of what you are doing and why',
    '- Be decisive: fix problems completely, not just symptoms',
  ].filter(Boolean).join('\n');

  const allToolCalls: ToolCall[] = [];
  let finalAssistantText = '';

  // Agentic loop: keep going until Claude stops calling tools
  let continueLoop = true;
  while (continueLoop) {
    // Use only the last 50 messages to stay within context limits
    const windowedMessages = messages.slice(-50);

    const stream = client.messages.stream({
      model: MODEL,
      max_tokens: 8192,
      system: systemPrompt,
      messages: windowedMessages,
      tools: TOOLS,
    });

    // Accumulate all content blocks from this response turn
    const contentBlocks: Anthropic.ContentBlockParam[] = [];
    let currentText = '';
    let currentToolUse: { id: string; name: string; inputJson: string } | null = null;

    for await (const event of stream) {
      if (event.type === 'content_block_start') {
        if (event.content_block.type === 'text') {
          currentText = '';
        } else if (event.content_block.type === 'tool_use') {
          currentToolUse = {
            id: event.content_block.id,
            name: event.content_block.name,
            inputJson: '',
          };
        }
      } else if (event.type === 'content_block_delta') {
        if (event.delta.type === 'text_delta') {
          currentText += event.delta.text;
          ctx.onChunk(event.delta.text, false);
        } else if (event.delta.type === 'input_json_delta' && currentToolUse) {
          currentToolUse.inputJson += event.delta.partial_json;
        }
      } else if (event.type === 'content_block_stop') {
        if (currentToolUse) {
          const input = JSON.parse(currentToolUse.inputJson || '{}') as Record<string, unknown>;
          contentBlocks.push({
            type: 'tool_use',
            id: currentToolUse.id,
            name: currentToolUse.name,
            input,
          });
          currentToolUse = null;
        } else if (currentText) {
          contentBlocks.push({ type: 'text', text: currentText });
          finalAssistantText += currentText;
          currentText = '';
        }
      } else if (event.type === 'message_delta') {
        continueLoop = event.delta.stop_reason === 'tool_use';
      }
    }

    // Add complete assistant turn to message history
    messages.push({ role: 'assistant', content: contentBlocks });

    // Execute all tool calls from this turn, collect results
    const toolResults: Anthropic.ToolResultBlockParam[] = [];
    for (const block of contentBlocks) {
      if (block.type !== 'tool_use') continue;

      const input = block.input as Record<string, unknown>;
      ctx.onToolCall(block.name, input);

      const start = Date.now();
      const { result, isError } = await executeTool(block.name, input, ctx.projectPath);
      const durationMs = Date.now() - start;

      db.logToolCall(randomUUID(), ctx.sessionId, block.name, input, result, isError, durationMs);
      ctx.onToolResult(block.name, result, isError);

      allToolCalls.push({ id: block.id, name: block.name, input, result, isError });
      toolResults.push({ type: 'tool_result', tool_use_id: block.id, content: result });
    }

    // Feed tool results back for the next loop iteration
    if (toolResults.length > 0) {
      messages.push({ role: 'user', content: toolResults });
    }
  }

  // Persist the final assistant message
  const assistantMsgId = randomUUID();
  db.saveMessage(
    assistantMsgId,
    ctx.sessionId,
    'assistant',
    finalAssistantText,
    allToolCalls.length > 0 ? allToolCalls : undefined
  );

  ctx.onChunk('', true);
}

function formatTree(node: import('@myeditor/shared').FileNode, indent = 0): string {
  const prefix = '  '.repeat(indent);
  const icon = node.type === 'directory' ? '📁' : '📄';
  let result = `${prefix}${icon} ${node.name}\n`;
  if (node.children) {
    for (const child of node.children) {
      result += formatTree(child, indent + 1);
    }
  }
  return result;
}
