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

export default function MarkdownPreview({
  value,
  className = '',
}: {
  value: string;
  className?: string;
}) {
  const components = useMemo<Components>(() => ({
    a: ({ href, children, ...props }) => (
      <a
        {...props}
        href={href}
        onClick={(event) => {
          if (!href || !isExternalHref(href)) {
            return;
          }
          event.preventDefault();
          void bridge.openExternal(href);
        }}
      >
        {children}
      </a>
    ),
    code: ({ className: blockClassName, children, ...props }) => {
      const rawSource = String(children);
      const source = rawSource.replace(/\n$/, '');
      const language = codeLanguage(blockClassName);
      const inlineCode = !language && !rawSource.includes('\n');

      if (inlineCode) {
        return (
          <code {...props} className={blockClassName}>
            {children}
          </code>
        );
      }

      if (!language) {
        return (
          <pre className="markdown-code-block">
            <code {...props}>{source}</code>
          </pre>
        );
      }

      return (
        <pre className={`markdown-code-block language-${language}`}>
          <code
            {...props}
            className={`language-${language}`}
            dangerouslySetInnerHTML={{ __html: highlightCode(source, language) }}
          />
        </pre>
      );
    },
  }), []);

  return (
    <div 
      className={`markdown-preview ${className}`.trim()}
    >
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {value.trim().length > 0 ? value : '_Empty markdown file._'}
      </ReactMarkdown>
    </div>
  );
}
