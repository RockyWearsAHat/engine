/**
 * editorSyntax.test.ts
 *
 * Behaviors: the AI controller opens files in the editor and needs to
 * determine the correct syntax highlighting language. These tests verify
 * that file extensions and language hints are correctly resolved.
 */
import { describe, expect, it } from 'vitest';
import { resolveSyntaxLanguage, highlightCode } from '../components/Editor/editorSyntax.js';

describe('resolveSyntaxLanguage — file extension mapping', () => {
  it('resolves TypeScript files to typescript', () => {
    expect(resolveSyntaxLanguage('/project/src/main.ts')).toBe('typescript');
  });

  it('resolves TSX files to tsx', () => {
    expect(resolveSyntaxLanguage('/project/src/App.tsx')).toBe('tsx');
  });

  it('resolves JavaScript files to javascript', () => {
    expect(resolveSyntaxLanguage('/project/src/index.js')).toBe('javascript');
  });

  it('resolves JSX files to jsx', () => {
    expect(resolveSyntaxLanguage('/project/src/App.jsx')).toBe('jsx');
  });

  it('resolves Go source files', () => {
    expect(resolveSyntaxLanguage('/project/server/main.go')).toBe('go');
  });

  it('resolves Rust source files', () => {
    expect(resolveSyntaxLanguage('/project/src/lib.rs')).toBe('rust');
  });

  it('resolves Python files', () => {
    expect(resolveSyntaxLanguage('/project/scripts/build.py')).toBe('python');
  });

  it('resolves shell scripts', () => {
    expect(resolveSyntaxLanguage('/project/scripts/deploy.sh')).toBe('bash');
  });

  it('resolves JSON config files', () => {
    expect(resolveSyntaxLanguage('/project/package.json')).toBe('json');
  });

  it('resolves YAML CI configs', () => {
    expect(resolveSyntaxLanguage('/project/.github/workflows/ci.yml')).toBe('yaml');
    expect(resolveSyntaxLanguage('/project/config.yaml')).toBe('yaml');
  });

  it('resolves markdown documentation files', () => {
    expect(resolveSyntaxLanguage('/project/README.md')).toBe('markdown');
  });

  it('resolves TOML config files like Cargo.toml', () => {
    expect(resolveSyntaxLanguage('/project/Cargo.toml')).toBe('toml');
  });

  it('resolves SQL files', () => {
    expect(resolveSyntaxLanguage('/project/schema.sql')).toBe('sql');
  });

  it('resolves CSS and SCSS files', () => {
    expect(resolveSyntaxLanguage('/project/styles.css')).toBe('css');
    expect(resolveSyntaxLanguage('/project/styles.scss')).toBe('css');
  });

  it('resolves HTML files to markup', () => {
    expect(resolveSyntaxLanguage('/project/index.html')).toBe('markup');
  });

  it('resolves GraphQL files', () => {
    expect(resolveSyntaxLanguage('/project/schema.graphql')).toBe('graphql');
    expect(resolveSyntaxLanguage('/project/query.gql')).toBe('graphql');
  });

  it('falls back to plain for unknown extensions', () => {
    expect(resolveSyntaxLanguage('/project/binary.exe')).toBe('plain');
    expect(resolveSyntaxLanguage('/project/data.bin')).toBe('plain');
  });

  it('falls back to plain for files with no extension', () => {
    expect(resolveSyntaxLanguage('/project/Makefile')).toBe('plain');
    expect(resolveSyntaxLanguage('/project/Dockerfile')).toBe('plain');
  });
});

describe('resolveSyntaxLanguage — language hint overrides extension', () => {
  it('uses the language hint when provided', () => {
    expect(resolveSyntaxLanguage('/project/file.txt', 'typescript')).toBe('typescript');
  });

  it('resolves ts alias from language hint', () => {
    expect(resolveSyntaxLanguage('/project/file', 'ts')).toBe('typescript');
  });

  it('resolves typescriptreact alias', () => {
    expect(resolveSyntaxLanguage('/project/Comp', 'typescriptreact')).toBe('tsx');
  });

  it('resolves javascriptreact alias', () => {
    expect(resolveSyntaxLanguage('/project/Comp', 'javascriptreact')).toBe('jsx');
  });

  it('treats shell/zsh hints as bash', () => {
    expect(resolveSyntaxLanguage('/project/script', 'zsh')).toBe('bash');
    expect(resolveSyntaxLanguage('/project/script', 'shell')).toBe('bash');
  });

  it('maps rs hint to rust', () => {
    expect(resolveSyntaxLanguage('/project/lib', 'rs')).toBe('rust');
  });

  it('maps yml hint to yaml', () => {
    expect(resolveSyntaxLanguage('/project/config', 'yml')).toBe('yaml');
  });

  it('maps xml hint to markup', () => {
    expect(resolveSyntaxLanguage('/project/data.xml', 'xml')).toBe('markup');
  });

  it('maps plaintext/text hints to plain', () => {
    expect(resolveSyntaxLanguage('/project/notes', 'plaintext')).toBe('plain');
    expect(resolveSyntaxLanguage('/project/notes', 'text')).toBe('plain');
  });

  it('falls back to extension when hint is empty', () => {
    expect(resolveSyntaxLanguage('/project/main.go', '')).toBe('go');
  });

  it('is case-insensitive for language hints', () => {
    expect(resolveSyntaxLanguage('/project/main', 'TypeScript')).toBe('typescript');
    expect(resolveSyntaxLanguage('/project/main', 'YAML')).toBe('yaml');
  });
});

describe('highlightCode', () => {
  it('escapes < and & when no grammar matches the language', () => {
    const result = highlightCode('<hello> & "world"', 'plain');
    expect(result).toContain('&lt;');
    expect(result).toContain('&amp;');
    expect(result).not.toContain('<hello');
  });

  it('returns highlighted HTML for a known language', () => {
    const result = highlightCode('const x = 1;', 'javascript');
    // Prism wraps keywords in spans — verify it produced HTML
    expect(result).toMatch(/<span/);
    expect(result).toContain('const');
  });

  it('handles empty input', () => {
    const result = highlightCode('', 'typescript');
    expect(result).toBe('');
  });
});
