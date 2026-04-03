// Tool registry: central place to register and look up tools.
// Currently tools are defined inline in ai/context.ts for the Claude API.
// This module provides runtime tool metadata for the UI.

export interface ToolMeta {
  name: string;
  description: string;
  category: 'filesystem' | 'git' | 'terminal' | 'search' | 'editor';
}

export const TOOL_REGISTRY: ToolMeta[] = [
  { name: 'read_file', description: 'Read file contents', category: 'filesystem' },
  { name: 'write_file', description: 'Write file contents', category: 'filesystem' },
  { name: 'list_directory', description: 'List directory tree', category: 'filesystem' },
  { name: 'shell', description: 'Execute shell command', category: 'terminal' },
  { name: 'search_files', description: 'Search with ripgrep', category: 'search' },
  { name: 'git_status', description: 'Get git status', category: 'git' },
  { name: 'git_diff', description: 'Get git diff', category: 'git' },
  { name: 'git_commit', description: 'Create git commit', category: 'git' },
  { name: 'open_file', description: 'Open file in editor', category: 'editor' },
];

export function getTool(name: string): ToolMeta | undefined {
  return TOOL_REGISTRY.find(t => t.name === name);
}
