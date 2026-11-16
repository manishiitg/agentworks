import React from 'react'
import ReactMarkdown, { type Components } from 'react-markdown'
import remarkGfm from 'remark-gfm'

/**
 * Markdown for content that is READ, not interacted with — prompts, system
 * prompts, instructions.
 *
 * Deliberately separate from ConversationMarkdownRenderer, which imports four
 * stores plus services/api to resolve workspace paths into clickable links.
 * That dependency chain is real weight for a block of prompt text, and pulling
 * it into a leaf event component created a circular import (api.ts -> stores ->
 * MarkdownRenderer -> component) that broke at module-init time. Nothing here
 * needs a workspace, so nothing here imports one.
 *
 * Typography is tuned for dense operational text rather than prose: tight
 * leading, restrained heading sizes (a prompt's "##" is a section marker, not a
 * page title), and tables/code that scroll inside their own box so a long line
 * never widens the surrounding card.
 */
export const PlainMarkdown: React.FC<{ content: string; className?: string; components?: Components }> = ({
  content,
  className = '',
  components,
}) => (
  <div className={`text-[12.5px] leading-5 text-neutral-300 ${className}`}>
    <ReactMarkdown
      // Single-tilde strikethrough off: ~ means "approximately" in model
      // text (~2.37L), and pairing those into <del> spans strikes whole
      // paragraphs. ~~ still strikethroughs, like GitHub.
      remarkPlugins={[[remarkGfm, { singleTilde: false }]]}
      components={{
        h1: ({ children }) => (
          <h1 className="mt-3 mb-1 text-[13px] font-semibold text-neutral-100 first:mt-0">{children}</h1>
        ),
        h2: ({ children }) => (
          <h2 className="mt-3 mb-1 text-[12.5px] font-semibold text-neutral-100 first:mt-0">{children}</h2>
        ),
        h3: ({ children }) => (
          <h3 className="mt-2.5 mb-1 text-[12.5px] font-semibold text-neutral-200 first:mt-0">{children}</h3>
        ),
        p: ({ children }) => <p className="my-1.5 first:mt-0 last:mb-0">{children}</p>,
        ul: ({ children }) => <ul className="my-1.5 ml-4 list-disc space-y-0.5">{children}</ul>,
        ol: ({ children }) => <ol className="my-1.5 ml-4 list-decimal space-y-0.5">{children}</ol>,
        li: ({ children }) => <li className="leading-5">{children}</li>,
        strong: ({ children }) => <strong className="font-semibold text-neutral-100">{children}</strong>,
        em: ({ children }) => <em className="italic text-neutral-200">{children}</em>,
        a: ({ children, href }) => (
          <a href={href} target="_blank" rel="noreferrer" className="text-cyan-400 underline underline-offset-2">
            {children}
          </a>
        ),
        blockquote: ({ children }) => (
          <blockquote className="my-1.5 border-l-2 border-neutral-700 pl-2 text-neutral-400">{children}</blockquote>
        ),
        hr: () => <hr className="my-2 border-neutral-800" />,
        code: ({ className: codeClass, children }) => {
          // react-markdown gives fenced blocks a language- class and inline
          // code none; only the block form needs its own scroll container.
          const isBlock = typeof codeClass === 'string' && codeClass.includes('language-')
          if (isBlock) {
            return (
              <code className="block overflow-x-auto whitespace-pre rounded bg-black/50 p-2 font-mono text-[11.5px] leading-4 text-neutral-200">
                {children}
              </code>
            )
          }
          return (
            <code className="rounded bg-black/40 px-1 py-0.5 font-mono text-[11.5px] text-amber-200">{children}</code>
          )
        },
        pre: ({ children }) => <pre className="my-1.5">{children}</pre>,
        table: ({ children }) => (
          <div className="my-1.5 overflow-x-auto">
            <table className="w-full border-collapse text-[11.5px]">{children}</table>
          </div>
        ),
        th: ({ children }) => (
          <th className="border border-neutral-800 bg-neutral-900/60 px-2 py-1 text-left font-semibold text-neutral-200">
            {children}
          </th>
        ),
        td: ({ children }) => <td className="border border-neutral-800 px-2 py-1 align-top">{children}</td>,
        // Callers that own a file store (e.g. Team files) supply `img`/`a`.
        ...components,
      }}
    >
      {content}
    </ReactMarkdown>
  </div>
)
