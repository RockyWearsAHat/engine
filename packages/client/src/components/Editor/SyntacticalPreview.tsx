import { useMemo } from 'react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { bridge } from '../../bridge.js';
import { highlightCode } from './editorSyntax.js';

function codeLanguage(className?: string): string | null {
  const match = className?.match(/language-([a-z0-9+#-]+)/i);
  return match?.[1]?.toLowerCase() ?? null;
}

function isExternalHref(href: string): boolean {
  return /^(https?:)?\/\//i.test(href) || href.startsWith('mailto:');
}

/** Maps markdown syntax to a short human-readable annotation */
export function syntaxLabel(tag: string, props?: Record<string, unknown>): string | null {
  switch (tag) {
    case 'h1': return '# heading 1';
    case 'h2': return '## heading 2';
    case 'h3': return '### heading 3';
    case 'h4': return '#### heading 4';
    case 'h5': return '##### heading 5';
    case 'h6': return '###### heading 6';
    case 'strong': return '**bold**';
    case 'em': return '*italic*';
    case 'del': return '~~strikethrough~~';
    case 'blockquote': return '> blockquote';
    case 'ul': return '- unordered list';
    case 'ol': return '1. ordered list';
    case 'hr': return '---';
    case 'a': return `[link](${(props?.href as string) ?? ''})`;
    case 'table': return '| table |';
    case 'code-block': return '```code block```';
    case 'code-inline': return '`inline code`';
    default: return null;
  }
}

function SyntaxAnnotation({ label }: { label: string }) {
  return (
    <span className="syntactical-annotation" title={label}>
      {label}
    </span>
  );
}

function AnnotatedWrapper({
  tag,
  children,
  props,
}: {
  tag: string;
  children: React.ReactNode;
  props?: Record<string, unknown>;
}) {
  const label = syntaxLabel(tag, props);
  /* istanbul ignore start */
  if (!label) return <>{children}</>;
  /* istanbul ignore stop */
  return (
    <span className="syntactical-line">
      {children}
      <SyntaxAnnotation label={label} />
    </span>
  );
}

export default function SyntacticalPreview({ value }: { value: string }) {
  const components = useMemo<Components>(() => ({
    h1: ({ children, ...props }) => (
      <AnnotatedWrapper tag="h1">
        <h1 {...props}>{children}</h1>
      </AnnotatedWrapper>
    ),
    h2: ({ children, ...props }) => (
      <AnnotatedWrapper tag="h2">
        <h2 {...props}>{children}</h2>
      </AnnotatedWrapper>
    ),
    h3: ({ children, ...props }) => (
      <AnnotatedWrapper tag="h3">
        <h3 {...props}>{children}</h3>
      </AnnotatedWrapper>
    ),
    h4: ({ children, ...props }) => (
      <AnnotatedWrapper tag="h4">
        <h4 {...props}>{children}</h4>
      </AnnotatedWrapper>
    ),
    h5: ({ children, ...props }) => (
      <AnnotatedWrapper tag="h5">
        <h5 {...props}>{children}</h5>
      </AnnotatedWrapper>
    ),
    h6: ({ children, ...props }) => (
      <AnnotatedWrapper tag="h6">
        <h6 {...props}>{children}</h6>
      </AnnotatedWrapper>
    ),
    strong: ({ children, ...props }) => (
      <AnnotatedWrapper tag="strong">
        <strong {...props}>{children}</strong>
      </AnnotatedWrapper>
    ),
    em: ({ children, ...props }) => (
      <AnnotatedWrapper tag="em">
        <em {...props}>{children}</em>
      </AnnotatedWrapper>
    ),
    del: ({ children, ...props }) => (
      <AnnotatedWrapper tag="del">
        <del {...props}>{children}</del>
      </AnnotatedWrapper>
    ),
    blockquote: ({ children, ...props }) => (
      <AnnotatedWrapper tag="blockquote">
        <blockquote {...props}>{children}</blockquote>
      </AnnotatedWrapper>
    ),
    ul: ({ children, ...props }) => (
      <AnnotatedWrapper tag="ul">
        <ul {...props}>{children}</ul>
      </AnnotatedWrapper>
    ),
    ol: ({ children, ...props }) => (
      <AnnotatedWrapper tag="ol">
        <ol {...props}>{children}</ol>
      </AnnotatedWrapper>
    ),
    hr: (props) => (
      <AnnotatedWrapper tag="hr">
        <hr {...props} />
      </AnnotatedWrapper>
    ),
    a: ({ href, children, ...props }) => (
      <AnnotatedWrapper tag="a" props={{ href }}>
        <a
          {...props}
          href={href}
          onClick={(event) => {
            if (!href || !isExternalHref(href)) return;
            event.preventDefault();
            void bridge.openExternal(href);
          }}
        >
          {children}
        </a>
      </AnnotatedWrapper>
    ),
    table: ({ children, ...props }) => (
      <AnnotatedWrapper tag="table">
        <table {...props}>{children}</table>
      </AnnotatedWrapper>
    ),
    code: ({ className: blockClassName, children, ...props }) => {
      const rawSource = String(children);
      const source = rawSource.replace(/\n$/, '');
      const language = codeLanguage(blockClassName);
      const inlineCode = !language && !rawSource.includes('\n');

      if (inlineCode) {
        return (
          <AnnotatedWrapper tag="code-inline">
            <code {...props} className={blockClassName}>{children}</code>
          </AnnotatedWrapper>
        );
      }

      const block = language ? (
        <pre className={`markdown-code-block language-${language}`}>
          <code
            {...props}
            className={`language-${language}`}
            dangerouslySetInnerHTML={{ __html: highlightCode(source, language) }}
          />
        </pre>
      ) : (
        <pre className="markdown-code-block">
          <code {...props}>{source}</code>
        </pre>
      );

      return (
        <AnnotatedWrapper tag="code-block">
          {block}
        </AnnotatedWrapper>
      );
    },
  }), []);

  return (
    <div className="markdown-preview syntactical-preview">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {value.trim().length > 0 ? value : '_Empty markdown file._'}
      </ReactMarkdown>
    </div>
  );
}
