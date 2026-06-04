# packages/shared - Shared TypeScript Types

Shared type definitions used across Engine client and server packages.

## Session & Message Types

**`Session`**  
Project session metadata passed between client and server.
- `id` — session identifier
- `projectPath` — workspace directory path
- `branchName` — git branch name
- `createdAt` — ISO timestamp
- `updatedAt` — ISO timestamp
- `projectDirection` — optional project context summary
- `summary` — session summary
- `messageCount` — total messages in session

**`Message`**  
Chat message in a session.
- `id` — message identifier
- `sessionId` — parent session ID
- `role` — 'user' or 'assistant'
- `content` — message text
- `toolCalls` — optional array of tool invocations
- `createdAt` — ISO timestamp

**`ToolCall`**  
AI tool invocation within a message.
- `id` — tool call identifier
- `name` — tool name
- `input` — tool input object
- `result` — optional tool result
- `isError` — optional error flag

## File System Types

**`FileNode`**  
File tree node representation.
- `name` — file or directory name
- `path` — absolute or relative path
- `type` — 'file' or 'directory'
- `children` — optional child nodes
- `loaded` — optional loaded state flag
- `hasChildren` — optional flag for directories
- `size` — optional file size in bytes
- `modified` — optional modification timestamp

**`FileContent`**  
File buffer with syntax information.
- `path` — file path
- `content` — file content string
- `language` — language for syntax highlighting
- `size` — content size in bytes

## Request & Search Types

**`ApprovalRequest`**  
Pending user approval for dangerous operations.
- `id` — request identifier
- `sessionId` — session performing the request
- `kind` — 'shell' or 'git_commit'
- `title` — approval title
- `message` — approval message
- `command` — command to be approved

**`SearchResult`**  
Code search result.
- `path` — file path
- `line` — line number
- `column` — optional column number
- `preview` — result preview text

## Configuration Types

**`RuntimeConfig`**  
Merged environment and storage configuration.
- `githubToken` — GitHub API token
- `githubOwner` — GitHub repository owner
- `githubRepo` — GitHub repository name
- `anthropicKey` — Anthropic API key
- `openaiKey` — OpenAI API key
- `modelProvider` — AI model provider selection
- `ollamaBaseUrl` — Ollama base URL for local models
- `model` — selected model name
- `activeTeam` — active team identifier
- `clonesDir` — directory for cloned projects
- `contextMaxTokens` — maximum context tokens
- `contextRecentWindow` — recent context window size
- `listDirectoryMaxChars` — directory listing character limit

## Usage

Import types from this package in client and server code:

```typescript
import type { Session, Message, FileNode, RuntimeConfig } from '@engine/shared';
```

All types are TypeScript interfaces and types with no runtime implementation. This package contains only type definitions.
