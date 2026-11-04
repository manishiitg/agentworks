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
    <div className="my-6 min-w-0 overflow-hidden rounded-lg border border-gray-200 shadow-sm dark:border-gray-700">
      <div className="overflow-x-auto">
        <div className="relative w-max min-w-full">
          <button
            type="button"
            onClick={() => void copy()}
            aria-label={status === 'copied' ? 'Table copied' : status === 'failed' ? 'Retry copying table' : 'Copy table'}
            title={status === 'copied' ? 'Copied' : status === 'failed' ? 'Copy failed — try again' : 'Copy table'}
            className="absolute right-2 top-2 z-10 inline-flex h-7 w-7 items-center justify-center rounded text-gray-500 hover:bg-gray-200 hover:text-gray-900 focus-visible:outline focus-visible:outline-2 dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-gray-100"
          >
            {status === 'copied' ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
            <span className="sr-only" aria-live="polite">{status === 'copied' ? 'Copied' : status === 'failed' ? 'Try again' : 'Copy'}</span>
          </button>
            <table ref={table} className="min-w-full border-collapse [&_thead_th:last-child]:pr-12">{children}</table>
        </div>
      </div>
    </div>
  )
}
