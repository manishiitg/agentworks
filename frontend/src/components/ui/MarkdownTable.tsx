import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Check, Copy } from 'lucide-react'
import { copyToClipboard } from '../../utils/textUtils'

export function MarkdownTable({ children }: { children: ReactNode }) {
  const table = useRef<HTMLTableElement>(null)
  const reset = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const [status, setStatus] = useState<'idle' | 'copied' | 'failed'>('idle')
  useEffect(() => () => clearTimeout(reset.current), [])

  const copy = async () => {
    if (!table.current) return
    const text = Array.from(table.current.rows, row =>
      Array.from(row.cells, cell => (cell.textContent ?? '').replace(/\s+/g, ' ').trim()).join('\t'),
    ).join('\n')
    setStatus(await copyToClipboard(text) ? 'copied' : 'failed')
    clearTimeout(reset.current)
    reset.current = setTimeout(() => setStatus('idle'), 1500)
  }

  return (
    <div className="my-6 min-w-0 overflow-hidden rounded-lg border border-border shadow-sm">
      <div className="overflow-x-auto">
        {/* Use the message width, growing only if the columns' minimum widths cannot fit. */}
        <div className="relative w-full min-w-min">
          <button
            type="button"
            onClick={() => void copy()}
            aria-label={status === 'copied' ? 'Table copied' : status === 'failed' ? 'Retry copying table' : 'Copy table'}
            title={status === 'copied' ? 'Copied' : status === 'failed' ? 'Copy failed — try again' : 'Copy table'}
            className="absolute right-2 top-2 z-10 inline-flex h-7 w-7 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline focus-visible:outline-2"
          >
            {status === 'copied' ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
            <span className="sr-only" aria-live="polite">{status === 'copied' ? 'Copied' : status === 'failed' ? 'Try again' : 'Copy'}</span>
          </button>
          <table ref={table} className="w-full table-auto border-collapse whitespace-normal [&_:is(th,td,a)]:[overflow-wrap:anywhere] [&_thead_th:last-child]:pr-12">{children}</table>
        </div>
      </div>
    </div>
  )
}
