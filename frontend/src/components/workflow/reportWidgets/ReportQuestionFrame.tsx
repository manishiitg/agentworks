import { useEffect, useId, useRef, useState, type ComponentProps } from 'react'
import { Copy, MessageCircle, X } from 'lucide-react'
import { useChatStore } from '../../../stores/useChatStore'
import ModalPortal from '../../ui/ModalPortal'
import { HtmlReportFrame } from './HtmlWidgetFrame'
import { useReportDataApi } from './reportEmbedContext'
import { reportContentQuestion, type ReportContentMenu } from './reportContentQuestion'

type Props = Omit<ComponentProps<typeof HtmlReportFrame>, 'onContentContextMenu'> & { documentPath: string; allowQuestions?: boolean }
const suggestions = ['Where did this come from?', "Why hasn't this changed?"]

/** Platform-owned question UI: existing HTML dashboards need no modifications. */
export function ReportQuestionFrame({ documentPath, allowQuestions = true, ...frameProps }: Props) {
  const dataApi = useReportDataApi()
  const [menu, setMenu] = useState<ReportContentMenu | null>(null)
  const [item, setItem] = useState<ReportContentMenu | null>(null)
  const [question, setQuestion] = useState(suggestions[0])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const dialogRef = useRef<HTMLDialogElement>(null)
  const sendingRef = useRef(false)
  const requestIdRef = useRef('')
  const titleId = useId()
  const questionId = useId()

  useEffect(() => {
    if (!menu) return
    menuRef.current?.querySelector<HTMLButtonElement>('button')?.focus()
    const dismiss = (event: Event) => {
      if (!menuRef.current?.contains(event.target as Node)) setMenu(null)
    }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') setMenu(null) }
    // Scroll can move the clicked item out from under a viewport-positioned menu.
    window.addEventListener('pointerdown', dismiss, true)
    window.addEventListener('scroll', dismiss, true)
    window.addEventListener('keydown', escape)
    return () => {
      window.removeEventListener('pointerdown', dismiss, true)
      window.removeEventListener('scroll', dismiss, true)
      window.removeEventListener('keydown', escape)
    }
  }, [menu])

  useEffect(() => {
    if (item) dialogRef.current?.showModal()
  }, [item])

  const ask = () => {
    setItem(menu)
    setMenu(null)
    setQuestion(suggestions[0])
    setError(null)
    requestIdRef.current = `dashboard-question:${crypto.randomUUID()}`
  }
  const send = async () => {
    if (!dataApi || !item || !question.trim() || sendingRef.current) return
    sendingRef.current = true
    setBusy(true)
    setError(null)
    try {
      const message = reportContentQuestion(question, frameProps.title, documentPath, dataApi.workspacePath, item.context)
      const receipt = await dataApi.sendChatMessage(message, { requestId: requestIdRef.current })
      useChatStore.getState().addToast(receipt.queuedBehindRunningTurn ? 'Question queued behind the current agent turn.' : 'Question queued for the agent.', 'info')
      setItem(null)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not send your question. Try again.')
    } finally {
      sendingRef.current = false
      setBusy(false)
    }
  }
  const copy = async () => {
    if (!menu) return
    try {
      await navigator.clipboard.writeText(menu.context.text)
      setMenu(null)
    } catch {
      useChatStore.getState().addToast('Could not copy this text. Select it and use your browser’s Copy action.', 'error')
    }
  }

  return <>
    <HtmlReportFrame {...frameProps} onContentContextMenu={dataApi && allowQuestions ? setMenu : undefined} />
    {menu && <ModalPortal>
      <div className="fixed inset-0 z-[10000]" onPointerDown={() => setMenu(null)}>
      <div ref={menuRef} role="menu" aria-label="Dashboard item actions" onPointerDown={event => event.stopPropagation()} className="fixed rounded-md border border-border bg-background p-1 text-foreground shadow-lg"
        style={{ left: Math.max(8, Math.min(menu.x, window.innerWidth - 224)), top: Math.max(8, Math.min(menu.y, window.innerHeight - 104)) }}>
        <button type="button" role="menuitem" onClick={ask} className="flex w-full items-center gap-2 rounded px-3 py-2 text-sm hover:bg-muted focus:bg-muted focus:outline-none">
          <MessageCircle className="h-4 w-4" /> Ask agent about this
        </button>
        <button type="button" role="menuitem" onClick={() => void copy()} className="flex w-full items-center gap-2 rounded px-3 py-2 text-sm hover:bg-muted focus:bg-muted focus:outline-none"><Copy className="h-4 w-4" /> Copy text</button>
      </div>
      </div>
    </ModalPortal>}
    {item && <ModalPortal>
      <dialog ref={dialogRef} onCancel={event => { if (busy) event.preventDefault(); else setItem(null) }} onClick={event => {
        const rect = event.currentTarget.getBoundingClientRect()
        if (!busy && event.target === event.currentTarget && (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom)) setItem(null)
      }}
        aria-labelledby={titleId} className="m-auto max-h-[90vh] w-[calc(100%_-_2rem)] max-w-lg overflow-y-auto rounded-xl border border-border bg-background p-5 text-foreground shadow-xl backdrop:bg-black/50">
        <div className="mb-3 flex items-center justify-between gap-3">
          <h2 id={titleId} className="font-semibold">Ask about this dashboard item</h2>
          <button type="button" onClick={() => setItem(null)} aria-label="Close question" disabled={busy} className="rounded p-1 hover:bg-muted"><X className="h-4 w-4" /></button>
        </div>
        <p className="mb-2 text-xs text-muted-foreground">{frameProps.title}{item.context.column ? ` · ${item.context.column}` : ''}</p>
        <blockquote className="mb-3 max-h-32 overflow-y-auto whitespace-pre-wrap break-words rounded-md border border-border bg-muted/40 p-3 text-sm">{item.context.text}</blockquote>
        {item.context.nearbyText !== item.context.text && <p className="mb-3 line-clamp-3 text-xs text-muted-foreground">{item.context.nearbyText}</p>}
        <form onSubmit={event => { event.preventDefault(); void send() }}>
          <label htmlFor={questionId} className="text-sm">Your question</label>
          <textarea id={questionId} autoFocus value={question} onChange={event => setQuestion(event.target.value)} maxLength={2000} disabled={busy}
            className="mt-1 min-h-24 w-full rounded-md border border-border bg-background p-2 text-sm" />
          <div className="mt-2 flex flex-wrap gap-2">
            {suggestions.map(suggestion => <button type="button" key={suggestion} onClick={() => setQuestion(suggestion)} disabled={busy} className="rounded-md border border-border px-2 py-1 text-xs hover:bg-muted">{suggestion}</button>)}
          </div>
          {error && <p role="alert" className="mt-3 text-sm text-destructive">{error}</p>}
          <div className="mt-4 flex justify-end gap-2">
            <button type="button" onClick={() => setItem(null)} disabled={busy} className="rounded-md border border-border px-3 py-2 text-sm">Cancel</button>
            <button type="submit" disabled={busy || !question.trim()} className="rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground disabled:opacity-50">{busy ? 'Sending…' : 'Send to agent'}</button>
          </div>
        </form>
      </dialog>
    </ModalPortal>}
  </>
}
