import { useMemo, useState } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

type Cell = { cell_type?: string; source?: unknown; outputs?: Array<{ text?: unknown; data?: Record<string, unknown>; traceback?: unknown }> }
const joined = (value: unknown): string => typeof value === 'string' ? value : Array.isArray(value) ? value.filter(v => typeof v === 'string').join('') : ''

/** Never execute notebook code, HTML, JavaScript or embedded widgets. */
export function NotebookPreview({ content }: { content: string }) {
  const [page, setPage] = useState(0)
  const parsed = useMemo(() => {
    try {
      const value = JSON.parse(content)
      return value.nbformat === 4 && Array.isArray(value.cells) ? value.cells.filter((cell: unknown) => cell !== null && typeof cell === 'object') as Cell[] : null
    } catch { return null }
  }, [content])
  if (!parsed) return <p role="alert">This file is not a supported Jupyter notebook (version 4).</p>
  const pages = Math.max(1, Math.ceil(parsed.length / 25))
  const current = Math.min(page, pages - 1)
  return <div className="space-y-4">
    <div className="flex items-center gap-3 text-sm"><span>{parsed.length} cells · Saved output only</span><button disabled={!current} onClick={() => setPage(current - 1)}>Previous</button><span>{current + 1} / {pages}</span><button disabled={current + 1 >= pages} onClick={() => setPage(current + 1)}>Next</button></div>
    {parsed.slice(current * 25, current * 25 + 25).map((cell, index) => <section key={current * 25 + index} className="rounded border border-border p-3">
      {cell.cell_type === 'markdown' ? <ReactMarkdown remarkPlugins={[remarkGfm]} skipHtml disallowedElements={['a', 'img']} unwrapDisallowed>{joined(cell.source)}</ReactMarkdown> : <pre className="overflow-auto whitespace-pre-wrap text-xs">{joined(cell.source)}</pre>}
      {(Array.isArray(cell.outputs) ? cell.outputs.filter(output => output !== null && typeof output === 'object') : []).map((output, i) => <pre key={i} className="mt-3 overflow-auto whitespace-pre-wrap border-t pt-2 text-xs">{joined(output.text) || joined(output.data?.['text/plain']) || joined(output.traceback) || (output.data ? JSON.stringify(output.data, null, 2) : '')}</pre>)}
    </section>)}
  </div>
}
