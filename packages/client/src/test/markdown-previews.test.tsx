import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { syntaxLabel } from '../components/Editor/SyntacticalPreview.js';

vi.mock('../bridge.js', () => ({
  bridge: { openExternal: vi.fn() },
}));

vi.mock('../components/Editor/editorSyntax.js', () => ({
  highlightCode: vi.fn((source: string, language: string) => `<span data-lang="${language}">${source}</span>`),
}));

const { default: MarkdownPreview } = await import('../components/Editor/MarkdownPreview.js');
const { default: SyntacticalPreview } = await import('../components/Editor/SyntacticalPreview.js');
const { bridge } = await import('../bridge.js');
const { highlightCode: highlightCodeMock } = await import('../components/Editor/editorSyntax.js');
const openExternalMock = vi.mocked(bridge.openExternal);

describe('Markdown preview behaviors', () => {
  it('ExternalLinks_OpenedThroughBridgeAndInternalIgnored', () => {
    openExternalMock.mockClear();
    const { container } = render(
      <MarkdownPreview value={'[ext](https://example.com)\n[rel](/docs/readme.md)'} />,
    );

    const links = container.querySelectorAll('a');
    fireEvent.click(links[0] as HTMLAnchorElement);
    fireEvent.click(links[1] as HTMLAnchorElement);

    expect(openExternalMock).toHaveBeenCalledWith('https://example.com');
    expect(openExternalMock).toHaveBeenCalledTimes(1);
  });

  it('BlankMarkdown_EmptyPlaceholderRendered', () => {
    render(<MarkdownPreview value={''} />);
    expect(screen.getByText(/empty markdown file/i)).toBeTruthy();
  });

  it('HighlightedCodeBlocksAndInlineCode_Rendered', () => {
    highlightCodeMock.mockClear();
    const { container } = render(
      <MarkdownPreview value={'Inline `x`\n```ts\nconst x = 1;\n```'} />,
    );

    expect(screen.getByText('x').tagName).toBe('CODE');
    expect(highlightCodeMock).toHaveBeenCalledWith('const x = 1;', 'ts');
    expect(container.querySelector('pre.language-ts')).not.toBeNull();
  });

  it('CodeBlockNoLanguageTag_PlainPreCodeRendered', () => {
    const { container } = render(
      <MarkdownPreview value={'```\nraw block\n```'} />,
    );
    expect(container.querySelector('pre')).not.toBeNull();
    expect(screen.getByText('raw block')).toBeTruthy();
  });
});

describe('Syntactical preview behaviors', () => {
  it('SyntaxAnnotations_HeadingsLinksAndCodeShown', () => {
    openExternalMock.mockClear();
    highlightCodeMock.mockClear();
    const { container } = render(
      <SyntacticalPreview value={'# Title\n[link](https://example.com)\n`inline`\n```js\nconst y = 2;\n```'} />,
    );

    expect(screen.getByText('# heading 1')).toBeTruthy();
    expect(screen.getByText(/\[link\]\(https:\/\/example.com\)/i)).toBeTruthy();
    expect(screen.getByText('`inline code`')).toBeTruthy();
    expect(screen.getByText('```code block```')).toBeTruthy();
    expect(highlightCodeMock).toHaveBeenCalledWith('const y = 2;', 'js');

    const link = container.querySelector('a') as HTMLAnchorElement;
    fireEvent.click(link);
    expect(openExternalMock).toHaveBeenCalledWith('https://example.com');
  });

  it('H2ToH6StrongEmDel_AnnotationsRendered', () => {
    render(
      <SyntacticalPreview value={
        '## h2\n### h3\n#### h4\n##### h5\n###### h6\n**bold**\n*italic*\n~~strike~~'
      } />,
    );
    expect(screen.getByText('## heading 2')).toBeTruthy();
    expect(screen.getByText('### heading 3')).toBeTruthy();
    expect(screen.getByText('#### heading 4')).toBeTruthy();
    expect(screen.getByText('##### heading 5')).toBeTruthy();
    expect(screen.getByText('###### heading 6')).toBeTruthy();
    expect(screen.getByText('**bold**')).toBeTruthy();
    expect(screen.getByText('*italic*')).toBeTruthy();
    expect(screen.getByText('~~strikethrough~~')).toBeTruthy();
  });

  it('BlockquoteUlOlHrTable_AnnotationsRendered', () => {
    const { container } = render(
      <SyntacticalPreview value={
        '> blockquote\n\n- item\n\n1. ordered\n\n---\n\n| col |\n|-----|\n| val |'
      } />,
    );
    expect(screen.getByText('> blockquote')).toBeTruthy();
    expect(screen.getByText('- unordered list')).toBeTruthy();
    expect(screen.getByText('1. ordered list')).toBeTruthy();
    expect(screen.getByText('---')).toBeTruthy();
    expect(screen.getByText('| table |')).toBeTruthy();
    expect(container.querySelector('table')).not.toBeNull();
  });

  it('InternalLink_BridgeOpenExternalNotCalled', () => {
    openExternalMock.mockClear();
    const { container } = render(
      <SyntacticalPreview value={'[rel](/docs/readme.md)'} />,
    );
    const link = container.querySelector('a') as HTMLAnchorElement;
    fireEvent.click(link);
    expect(openExternalMock).not.toHaveBeenCalled();
  });

  it('ImageTag_RenderedAsImgElement', () => {
    const { container } = render(<SyntacticalPreview value={'![alt text](https://example.com/img.png)'} />);
    expect(container.querySelector('img[alt="alt text"]')).not.toBeNull();
  });

  it('BlankValue_EmptyPlaceholderRendered', () => {
    render(<SyntacticalPreview value={''} />);
    expect(screen.getByText(/empty markdown file/i)).toBeTruthy();
  });

  it('CodeBlockNoLanguage_Rendered', () => {
    const { container } = render(
      <SyntacticalPreview value={'```\nraw\n```'} />,
    );
    expect(container.querySelector('pre.markdown-code-block')).not.toBeNull();
    expect(screen.getByText('```code block```')).toBeTruthy();
  });
});

describe('syntaxLabel utility', () => {
  it('UnknownTag_ReturnsNull', () => {
    expect(syntaxLabel('aside')).toBeNull();
    expect(syntaxLabel('p')).toBeNull();
  });
  it('LinkHref_Interpolated', () => {
    expect(syntaxLabel('a', { href: 'https://test.com' })).toBe('[link](https://test.com)');
  });
  it('LinkNoHref_FallsBackToEmptyString', () => {
    expect(syntaxLabel('a')).toBe('[link]()');
  });
});
