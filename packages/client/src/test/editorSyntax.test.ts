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
  it('TypeScriptFile_ResolvesToTypescript', () => {
    expect(resolveSyntaxLanguage('/project/src/main.ts')).toBe('typescript');
  });

  it('TsxFile_ResolvesToTsx', () => {
    expect(resolveSyntaxLanguage('/project/src/App.tsx')).toBe('tsx');
  });

  it('JavaScriptFile_ResolvesToJavascript', () => {
    expect(resolveSyntaxLanguage('/project/src/index.js')).toBe('javascript');
  });

  it('JsxFile_ResolvesToJsx', () => {
    expect(resolveSyntaxLanguage('/project/src/App.jsx')).toBe('jsx');
  });

  it('GoSourceFile_Resolved', () => {
    expect(resolveSyntaxLanguage('/project/server/main.go')).toBe('go');
  });

  it('RustSourceFile_Resolved', () => {
    expect(resolveSyntaxLanguage('/project/src/lib.rs')).toBe('rust');
  });

  it('PythonFile_Resolved', () => {
    expect(resolveSyntaxLanguage('/project/scripts/build.py')).toBe('python');
  });

  it('ShellScript_Resolved', () => {
    expect(resolveSyntaxLanguage('/project/scripts/deploy.sh')).toBe('bash');
  });

  it('JsonConfigFile_Resolved', () => {
    expect(resolveSyntaxLanguage('/project/package.json')).toBe('json');
  });

  it('YamlCiConfig_Resolved', () => {
    expect(resolveSyntaxLanguage('/project/.github/workflows/ci.yml')).toBe('yaml');
    expect(resolveSyntaxLanguage('/project/config.yaml')).toBe('yaml');
  });

  it('MarkdownDocFile_Resolved', () => {
    expect(resolveSyntaxLanguage('/project/README.md')).toBe('markdown');
  });

  it('TomlConfigFile_Resolved', () => {
    expect(resolveSyntaxLanguage('/project/Cargo.toml')).toBe('toml');
  });

  it('SqlFile_Resolved', () => {
    expect(resolveSyntaxLanguage('/project/schema.sql')).toBe('sql');
  });

  it('CssAndScssFiles_Resolved', () => {
    expect(resolveSyntaxLanguage('/project/styles.css')).toBe('css');
    expect(resolveSyntaxLanguage('/project/styles.scss')).toBe('css');
  });

  it('HtmlFile_ResolvesToMarkup', () => {
    expect(resolveSyntaxLanguage('/project/index.html')).toBe('markup');
  });

  it('GraphQLFile_Resolved', () => {
    expect(resolveSyntaxLanguage('/project/schema.graphql')).toBe('graphql');
    expect(resolveSyntaxLanguage('/project/query.gql')).toBe('graphql');
  });

  it('UnknownExtension_FallsBackToPlain', () => {
    expect(resolveSyntaxLanguage('/project/binary.exe')).toBe('plain');
    expect(resolveSyntaxLanguage('/project/data.bin')).toBe('plain');
  });

  it('NoExtension_FallsBackToPlain', () => {
    expect(resolveSyntaxLanguage('/project/Makefile')).toBe('plain');
    expect(resolveSyntaxLanguage('/project/Dockerfile')).toBe('plain');
  });
});

describe('resolveSyntaxLanguage — language hint overrides extension', () => {
  it('LanguageHintProvided_HintOverridesExtension', () => {
    expect(resolveSyntaxLanguage('/project/file.txt', 'typescript')).toBe('typescript');
  });

  it('TsAliasHint_ResolvedToTypescript', () => {
    expect(resolveSyntaxLanguage('/project/file', 'ts')).toBe('typescript');
  });

  it('TypescriptreactAlias_Resolved', () => {
    expect(resolveSyntaxLanguage('/project/Comp', 'typescriptreact')).toBe('tsx');
  });

  it('JavascriptreactAlias_Resolved', () => {
    expect(resolveSyntaxLanguage('/project/Comp', 'javascriptreact')).toBe('jsx');
  });

  it('ShellZshHint_TreatedAsBash', () => {
    expect(resolveSyntaxLanguage('/project/script', 'zsh')).toBe('bash');
    expect(resolveSyntaxLanguage('/project/script', 'shell')).toBe('bash');
  });

  it('RsHint_MappedToRust', () => {
    expect(resolveSyntaxLanguage('/project/lib', 'rs')).toBe('rust');
  });

  it('YmlHint_MappedToYaml', () => {
    expect(resolveSyntaxLanguage('/project/config', 'yml')).toBe('yaml');
  });

  it('XmlHint_MappedToMarkup', () => {
    expect(resolveSyntaxLanguage('/project/data.xml', 'xml')).toBe('markup');
  });

  it('PlaintextTextHint_MappedToPlain', () => {
    expect(resolveSyntaxLanguage('/project/notes', 'plaintext')).toBe('plain');
    expect(resolveSyntaxLanguage('/project/notes', 'text')).toBe('plain');
  });

  it('EmptyHint_FallsBackToExtension', () => {
    expect(resolveSyntaxLanguage('/project/main.go', '')).toBe('go');
  });

  it('LanguageHintCaseInsensitive_Resolved', () => {
    expect(resolveSyntaxLanguage('/project/main', 'TypeScript')).toBe('typescript');
    expect(resolveSyntaxLanguage('/project/main', 'YAML')).toBe('yaml');
  });
});

describe('highlightCode', () => {
  it('UnregisteredLanguage_HtmlEscaped', () => {
    const result = highlightCode('<hello> & "world"', 'xyz-unknown-lang-99999');
    expect(result).toBe('&lt;hello&gt; &amp; "world"');
  });

  it('NoMatchingGrammar_LtAndAmpEscaped', () => {
    const result = highlightCode('<hello> & "world"', 'plain');
    expect(result).toContain('&lt;');
    expect(result).toContain('&amp;');
    expect(result).not.toContain('<hello');
  });

  it('KnownLanguage_HighlightedHtmlReturned', () => {
    const result = highlightCode('const x = 1;', 'javascript');
    // Prism wraps keywords in spans — verify it produced HTML
    expect(result).toMatch(/<span/);
    expect(result).toContain('const');
  });

  it('EmptyInput_EmptyOutputReturned', () => {
    const result = highlightCode('', 'typescript');
    expect(result).toBe('');
  });
});
