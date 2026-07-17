import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { cn } from '@/lib/cn'

/**
 * Renders GitHub-flavored markdown (bullets, code, bold, tables, links) styled
 * to the console tokens. Used for agent messages and HITL inquiry text, which
 * arrive as markdown but were previously shown as a raw wall of text.
 */
export function Markdown({ children, className }: { children: string; className?: string }) {
  return (
    <div className={cn('leading-relaxed', className)}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          p: ({ children }) => <p className="mb-2 last:mb-0">{children}</p>,
          ul: ({ children }) => <ul className="mb-2 ml-4 list-disc space-y-0.5 last:mb-0">{children}</ul>,
          ol: ({ children }) => <ol className="mb-2 ml-4 list-decimal space-y-0.5 last:mb-0">{children}</ol>,
          li: ({ children }) => <li className="marker:text-text3">{children}</li>,
          code: ({ children }) => (
            <code className="rounded bg-surface2 px-1 py-0.5 font-mono text-[0.85em] text-text">{children}</code>
          ),
          pre: ({ children }) => (
            <pre className="mb-2 overflow-x-auto rounded-btn border border-border bg-surface2 p-2.5 font-mono text-2xs last:mb-0">
              {children}
            </pre>
          ),
          strong: ({ children }) => <strong className="font-semibold text-text">{children}</strong>,
          em: ({ children }) => <em className="italic">{children}</em>,
          a: ({ href, children }) => (
            <a href={href} target="_blank" rel="noreferrer" className="text-accent underline">
              {children}
            </a>
          ),
          h1: ({ children }) => <h3 className="mb-1 mt-2 text-sm font-bold first:mt-0">{children}</h3>,
          h2: ({ children }) => <h3 className="mb-1 mt-2 text-sm font-bold first:mt-0">{children}</h3>,
          h3: ({ children }) => <h4 className="mb-1 mt-2 text-[13px] font-semibold first:mt-0">{children}</h4>,
          blockquote: ({ children }) => (
            <blockquote className="mb-2 border-l-2 border-border pl-2.5 text-text2 last:mb-0">{children}</blockquote>
          ),
          hr: () => <hr className="my-2 border-border" />,
          table: ({ children }) => (
            <div className="mb-2 overflow-x-auto last:mb-0">
              <table className="border-collapse text-xs">{children}</table>
            </div>
          ),
          th: ({ children }) => (
            <th className="border border-border px-2 py-1 text-left font-semibold">{children}</th>
          ),
          td: ({ children }) => <td className="border border-border px-2 py-1">{children}</td>,
        }}
      >
        {children}
      </ReactMarkdown>
    </div>
  )
}
