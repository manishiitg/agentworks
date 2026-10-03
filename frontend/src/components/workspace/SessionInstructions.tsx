import { useEffect, useRef, useState } from 'react'
import { FileText, Loader2, X } from 'lucide-react'
import api from '../../services/api'
import ModalPortal from '../ui/ModalPortal'

// A virtual, read-only workspace entry: generated CLI instructions live in a
// private runtime, not among the project's editable files.
export function SessionInstructions({ sessionId }: { sessionId: string }) {
  const [open, setOpen] = useState(false)
  const [content, setContent] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const closeRef = useRef<HTMLButtonElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  useEffect(() => { setOpen(false); setContent(''); setError('') }, [sessionId])
  useEffect(() => {
    if (!open) return
    let cancelled = false
    setLoading(true); setContent(''); setError('')
    closeRef.current?.focus()
    api.get<{ content: string }>(`/api/sessions/${encodeURIComponent(sessionId)}/instructions`)
      .then(response => { if (!cancelled) setContent(response.data.content) })
      .catch((failure: { response?: { status?: number } }) => { if (!cancelled) setError(failure.response?.status === 404 ? 'No prompt snapshot is available. Send a message in this chat, then reopen this view.' : 'Unable to read this chat’s instructions. Check your access or try again.') })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [open, sessionId])
  const close = () => { setOpen(false); triggerRef.current?.focus() }
  return <>
    <button ref={triggerRef} type="button" onClick={() => setOpen(true)} aria-label="View AGENTS.md system prompt"
      className="flex w-full items-center gap-2 rounded px-3 py-1.5 text-left text-xs text-muted-foreground hover:bg-muted hover:text-foreground">
      <FileText className="h-4 w-4 shrink-0" /><span>AGENTS.md</span><span className="ml-auto text-[10px]">System prompt · read only</span>
    </button>
    {open && <ModalPortal><div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" onClick={close}>
      <section role="dialog" aria-modal="true" aria-label="AGENTS.md system prompt" tabIndex={-1}
        className="flex max-h-[85vh] w-full max-w-4xl flex-col rounded-lg border border-border bg-background shadow-xl"
        onClick={event => event.stopPropagation()}
        onKeyDown={event => {
          if (event.key === 'Escape') { event.preventDefault(); close() }
          // This read-only dialog has a single interactive control.
          if (event.key === 'Tab') { event.preventDefault(); closeRef.current?.focus() }
        }}>
        <header className="flex items-center justify-between border-b border-border px-4 py-3">
          <h2 className="text-sm font-semibold">AGENTS.md · System prompt</h2>
          <button ref={closeRef} type="button" aria-label="Close system prompt" onClick={close} className="rounded p-1 hover:bg-muted"><X className="h-4 w-4" /></button>
        </header>
        <p className="px-4 py-3 text-xs text-muted-foreground">Read-only snapshot of this chat’s last prepared system prompt. Generated instructions stay in its private runtime; this is not an editable project file.</p>
        {loading ? <p className="px-4 pb-4 text-sm text-muted-foreground"><Loader2 className="mr-2 inline h-4 w-4 animate-spin" />Loading instructions…</p>
          : error ? <p role="status" className="px-4 pb-4 text-sm text-muted-foreground">{error}</p>
          : <pre className="min-h-0 overflow-auto whitespace-pre-wrap break-words border-t border-border p-4 font-mono text-xs text-foreground">{content}</pre>}
      </section>
    </div></ModalPortal>}
  </>
}
