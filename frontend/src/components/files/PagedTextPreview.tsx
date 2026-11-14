import { useEffect, useState } from 'react'
import { readPreviewPage, type PreviewPage } from './readPreviewPage'

/** A bounded page replaces the previous one; visiting a large log never accumulates it in memory. */
export function PagedTextPreview({ path, first, readPage = readPreviewPage }: { path: string; first: PreviewPage; readPage?: typeof readPreviewPage }) {
  const [page, setPage] = useState(first)
  const [history, setHistory] = useState<number[]>([])
  const [offset, setOffset] = useState(first.offset)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  useEffect(() => {
    if (offset === first.offset) { setPage(first); setLoading(false); setError(''); return }
    const controller = new AbortController()
    setLoading(true); setError('')
    readPage(path, offset, controller.signal).then(setPage).catch(error => {
      if (!controller.signal.aborted) setError(error instanceof Error ? error.message : 'Could not read this page.')
    }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [path, offset, first, readPage])
  return <div className="space-y-3">
    <div className="flex items-center gap-3 text-xs"><span>Bytes {page.offset.toLocaleString()}–{page.next.toLocaleString()} of {page.total.toLocaleString()} · Plain text preview</span>
      <button disabled={loading || !history.length} onClick={() => { setOffset(history[history.length - 1]); setHistory(history.slice(0, -1)) }}>Previous</button>
      <button disabled={loading || page.next >= page.total} onClick={() => { setHistory([...history, page.offset]); setOffset(page.next) }}>Next</button>
      {loading && <span>Loading…</span>}
    </div>
    {error && <p role="alert" className="text-destructive">{error}</p>}
    <pre className="max-h-[70vh] overflow-auto whitespace-pre-wrap break-words text-xs">{page.text}</pre>
  </div>
}
