import Prism from 'prismjs';
import 'prismjs/components/prism-markup';
import 'prismjs/components/prism-markup-templating';
import 'prismjs/components/prism-css';
import 'prismjs/components/prism-clike';
import 'prismjs/components/prism-javascript';
import 'prismjs/components/prism-jsx';
import 'prismjs/components/prism-typescript';
import 'prismjs/components/prism-tsx';
import 'prismjs/components/prism-json';
import 'prismjs/components/prism-bash';
import 'prismjs/components/prism-go';
import 'prismjs/components/prism-rust';
import 'prismjs/components/prism-python';
import 'prismjs/components/prism-sql';
import 'prismjs/components/prism-yaml';
import 'prismjs/components/prism-markdown';
import 'prismjs/components/prism-toml';
import 'prismjs/components/prism-graphql';

const syntaxAliases: Record<string, string> = {
  ts: 'typescript',
  typescript: 'typescript',
  typescriptreact: 'tsx',
  tsx: 'tsx',
  js: 'javascript',
  javascript: 'javascript',
  javascriptreact: 'jsx',
  jsx: 'jsx',
  html: 'markup',
  xml: 'markup',
  svg: 'markup',
  plaintext: 'plain',
  text: 'plain',
  css: 'css',
  scss: 'css',
  less: 'css',
  json: 'json',
  yaml: 'yaml',
  yml: 'yaml',
  md: 'markdown',
  markdown: 'markdown',
  py: 'python',
  python: 'python',
  sh: 'bash',
  bash: 'bash',
  shell: 'bash',
  zsh: 'bash',
  go: 'go',
  rs: 'rust',
  rust: 'rust',
  sql: 'sql',
  toml: 'toml',
  graphql: 'graphql',
  gql: 'graphql',
};

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

export function resolveSyntaxLanguage(path: string, language?: string): string {
  const normalizedLanguage = (language ?? '').trim().toLowerCase();
  if (syntaxAliases[normalizedLanguage]) {
    return syntaxAliases[normalizedLanguage];
  }
  /* c8 ignore next */
  const extension = path.split('.').pop()?.trim().toLowerCase() ?? '';
  return syntaxAliases[extension] ?? 'plain';
}

export function highlightCode(text: string, syntaxLanguage: string): string {
  const grammar = Prism.languages[syntaxLanguage];
  if (!grammar) {
    return escapeHtml(text);
  }

  return Prism.highlight(text, grammar, syntaxLanguage);
}
