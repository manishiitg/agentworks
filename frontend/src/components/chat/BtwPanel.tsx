import React, { useEffect, useState } from 'react'
import { Loader2, X } from 'lucide-react'
import { MarkdownRenderer } from '../ui/MarkdownRenderer'
import { btwChatNote, dismissBtw, keepBtwAsTab, openBtwItem, pollBtwItem, useBtwView, type BtwItem } from '../../utils/btwSideQuestion'

const POLL_MS = 1500

// The /btw side-question panel above the message box (approved mockup, 27 Nov). It follows the hidden copy that
// answers the question; the chat underneath keeps working and never sees it.
export const BtwPanel: React.FC<{ tabId: string; onAddToChat: (text: string) => void }> = ({ tabId, onAddToChat }) => {
  const view = useBtwView(tabId)
  const answering = view?.kind === 'item' && view.item.status === 'answering' ? view.item : undefined

  useEffect(() => {
    if (!answering?.sessionId) return
    let stopped = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const tick = async () => {
      let again = true
      try {
        again = await pollBtwItem(tabId, answering)
      } catch {
        // A missed read retries on the next tick.
      }
      if (!stopped && again) timer = setTimeout(() => void tick(), POLL_MS)
    }
    timer = setTimeout(() => void tick(), POLL_MS)
    return () => { stopped = true; if (timer) clearTimeout(timer) }
  }, [tabId, answering?.id, answering?.sessionId, answering?.answer]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!view) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented) return
      if (view.kind === 'item') dismissBtw(tabId, view.item.id)
      else openBtwItem(tabId, null)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [tabId, view])

  if (!view) return null
  if (view.kind === 'history') {
    return (
      <div data-testid="btw-history" className="mx-3 mb-2 max-h-[45vh] overflow-y-auto rounded-xl border border-blue-200 bg-blue-50/60 text-sm dark:border-blue-900 dark:bg-blue-950/30">
        <div className="flex items-center gap-2 px-3 pb-1 pt-2 text-xs text-blue-700 dark:text-blue-300">
          <span className="font-semibold">Side questions in this chat</span>
          <button type="button" aria-label="Close" className="ml-auto rounded p-0.5 text-muted-foreground hover:bg-muted" onClick={() => openBtwItem(tabId, null)}><X className="h-3.5 w-3.5" /></button>
        </div>
        {view.items.map(item => (
          <button key={item.id} type="button" onClick={() => openBtwItem(tabId, item.id)} className="flex w-full gap-2 border-t border-blue-100 px-3 py-1.5 text-left text-[13px] hover:bg-blue-100/60 dark:border-blue-900 dark:hover:bg-blue-900/30">
            <span className="min-w-0 flex-1 truncate">{item.question}</span>
            <span className="shrink-0 text-[11px] text-muted-foreground">{timeLabel(item.at)}</span>
          </button>
        ))}
      </div>
    )
  }
  return <BtwAnswer tabId={tabId} item={view.item} onAddToChat={onAddToChat} />
}

const BtwAnswer: React.FC<{ tabId: string; item: BtwItem; onAddToChat: (text: string) => void }> = ({ tabId, item, onAddToChat }) => {
  const [keeping, setKeeping] = useState(false)
  const done = item.status === 'done'
  return (
    <div data-testid="btw-panel" data-status={item.status} className="mx-3 mb-2 flex max-h-[45vh] flex-col rounded-xl border border-blue-200 bg-blue-50/60 shadow-sm dark:border-blue-900 dark:bg-blue-950/30">
      <div className="flex shrink-0 items-center gap-2 px-3 pb-1 pt-2 text-xs text-blue-700 dark:text-blue-300">
        <span className="font-semibold">By the way</span>
        <span className="text-muted-foreground">· side question · doesn&apos;t interrupt the chat</span>
        <button type="button" aria-label="Dismiss side question" title="Dismiss (Esc)" className="ml-auto rounded p-0.5 text-muted-foreground hover:bg-muted" onClick={() => dismissBtw(tabId, item.id)}><X className="h-3.5 w-3.5" /></button>
      </div>
      <div className="shrink-0 px-3 text-[12.5px] text-muted-foreground">{item.question}</div>
      <div data-testid="btw-answer" className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-3 pb-2 pt-1.5 text-[13.5px]">
        {item.status === 'failed'
          ? <span className="text-red-600 dark:text-red-400">{item.error || 'The side question did not get an answer.'}</span>
          : item.answer
            ? <MarkdownRenderer content={item.answer} />
            : <span className="inline-flex items-center gap-1.5 text-muted-foreground"><Loader2 className="h-3 w-3 animate-spin" />{item.status === 'starting' ? 'Copying this chat…' : 'Answering from this chat’s context…'}</span>}
      </div>
      {(done || item.status === 'failed') && (
        <div className="flex shrink-0 flex-wrap items-center gap-1.5 border-t border-blue-100 px-3 pb-2.5 pt-2 dark:border-blue-900">
          <button type="button" className="rounded-md border border-border bg-background px-2.5 py-0.5 text-xs hover:bg-muted" onClick={() => dismissBtw(tabId, item.id)}>Dismiss</button>
          {done && item.conversationKey && <button type="button" disabled={keeping} aria-busy={keeping} className="rounded-md border border-blue-200 bg-background px-2.5 py-0.5 text-xs text-blue-700 hover:bg-muted disabled:opacity-60 dark:border-blue-900 dark:text-blue-300"
            onClick={async () => { setKeeping(true); try { await keepBtwAsTab(tabId, item.id) } finally { setKeeping(false) } }}>{keeping ? 'Opening…' : 'Open as tab'}</button>}
          {done && <button type="button" className="rounded-md border border-border bg-background px-2.5 py-0.5 text-xs hover:bg-muted" onClick={() => { onAddToChat(btwChatNote(item)); dismissBtw(tabId, item.id) }}>Add to chat</button>}
          <span className="ml-auto text-[11px] text-muted-foreground">Esc to dismiss</span>
        </div>
      )}
    </div>
  )
}

function timeLabel(at: number): string {
  const seconds = Math.round((Date.now() - at) / 1000)
  if (seconds < 60) return 'just now'
  return new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}
