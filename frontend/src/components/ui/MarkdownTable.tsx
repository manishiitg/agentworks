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
      <div className="flex justify-end bg-gray-50 px-2 py-1 dark:bg-gray-800">
        <button
          type="button"
          onClick={() => void copy()}
          aria-label={status === 'copied' ? 'Table copied' : 'Copy table'}
          className="inline-flex items-center gap-1 rounded px-2 py-1 text-xs text-gray-600 hover:bg-gray-200 focus-visible:outline focus-visible:outline-2 dark:text-gray-300 dark:hover:bg-gray-700"
        >
          {status === 'copied' ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
          <span aria-live="polite">{status === 'copied' ? 'Copied' : status === 'failed' ? 'Try again' : 'Copy'}</span>
        </button>
      </div>
      <div className="overflow-x-auto">
        <table ref={table} className="min-w-full border-collapse">{children}</table>
      </div>
    </div>
  )
}
