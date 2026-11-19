import { useEffect, useState } from 'react'
import { Loader2, X } from 'lucide-react'
import { agentApi, type ChatCompactionSummary } from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { CHAT_FOCUS_COMPOSER_EVENT } from '../../utils/workspacePaneChat'
import { CHAT_INSERT_QUOTE_EVENT, withInsert, type InsertQuoteDetail } from '../chat/ReplyQuote'
import ModalPortal from '../ui/ModalPortal'
import { PlainMarkdown } from '../ui/PlainMarkdown'

const providerName = (provider: string) => provider === 'codex-cli' ? 'Codex' : provider === 'claude-code' ? 'Claude Code' : provider

function when(at: string): string {
  const date = new Date(at)
  if (Number.isNaN(date.getTime())) return ''
  const today = new Date()
  const time = date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  return date.toDateString() === today.toDateString() ? `Today, ${time}` : `${date.toLocaleDateString([], { day: 'numeric', month: 'short' })}, ${time}`
}

/** Puts a line in this chat's message box (the open composer, else the saved draft); sends nothing. */
function startMessage(sessionId: string, text: string) {
  const store = useChatStore.getState()
  const tabId = Object.entries(store.chatTabs).find(([, tab]) => tab?.sessionId === sessionId)?.[0] || store.activeTabId
  if (!tabId) return
  const detail: InsertQuoteDetail = { tabId, text, plain: true }
  window.dispatchEvent(new CustomEvent<InsertQuoteDetail>(CHAT_INSERT_QUOTE_EVENT, { detail }))
  if (!detail.handled) {
    store.setTabConfig(tabId, { inputText: withInsert(store.getTabConfig(tabId)?.inputText || '', detail) })
    window.setTimeout(() => window.dispatchEvent(new CustomEvent(CHAT_FOCUS_COMPOSER_EVENT)), 30)
  }
}

/** What the agent kept when this chat was compacted, read by the server from the chat's own session (#3236). */
export function CompactionSummaryPanel({ sessionId, at, onClose }: { sessionId: string; at?: string; onClose: () => void }) {
  const [summary, setSummary] = useState<ChatCompactionSummary | null>(null)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    let disposed = false
    setSummary(null); setError('')
    agentApi.getChatCompactionSummary(sessionId, at).then(next => { if (!disposed) setSummary(next) }).catch(failure => {
      if (disposed) return
      const response = (failure as { response?: { status?: number; data?: { error?: unknown } } }).response
      const reason = typeof response?.data?.error === 'string' ? response.data.error : ''
      setError(reason || 'The summary could not be opened just now. Try again in a moment.')
    })
    return () => { disposed = true }
  }, [sessionId, at])

  useEffect(() => {
    const close = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
    window.addEventListener('keydown', close)
    return () => window.removeEventListener('keydown', close)
  }, [onClose])

  const kept = summary?.kind === 'kept_messages'
  const copyText = summary ? (kept ? (summary.messages || []).join('\n\n') : summary.text || '') : ''
  const copy = () => {
    void navigator.clipboard?.writeText(copyText).then(() => { setCopied(true); window.setTimeout(() => setCopied(false), 1200) })
  }
  const ask = () => {
    startMessage(sessionId, summary ? `About what you kept when this chat was summarized (${when(summary.at)}): ` : 'What do you remember from earlier in this chat? ')
    onClose()
  }

  return <ModalPortal>
    <aside role="dialog" aria-label="Chat summary" data-testid="compaction-summary-panel" className="fixed inset-y-0 right-0 z-50 flex w-[min(32rem,100vw)] flex-col border-l border-border bg-background shadow-xl">
      <header className="flex shrink-0 items-start gap-2 border-b border-border px-4 py-3">
        <div className="min-w-0 flex-1">
          <h2 className="text-sm font-medium">{error ? 'Summary not available' : 'What the agent remembers'}</h2>
          {summary && <p className="text-xs text-muted-foreground">{[when(summary.at), providerName(summary.provider)].filter(Boolean).join(' · ')}</p>}
        </div>
        <button type="button" aria-label="Close summary" onClick={onClose} className="rounded p-1 text-muted-foreground hover:bg-muted"><X className="h-4 w-4" /></button>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3 text-sm">
        {!summary && !error && <p role="status" className="flex items-center gap-2 text-xs text-muted-foreground"><Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />Opening the summary…</p>}
        {error && <>
          <p role="status" className="rounded-md bg-muted px-3 py-2 text-xs text-muted-foreground">{error}</p>
        </>}
        {summary && !kept && <PlainMarkdown content={summary.text || ''} className="!text-[13px] !leading-6 !text-foreground [&_*]:!text-inherit [&_code]:!bg-muted [&_li]:!leading-6" />}
        {summary && kept && <>
          <p className="mb-3 rounded-md bg-muted px-3 py-2 text-xs text-muted-foreground">{providerName(summary.provider)} does not keep a written summary. It continues from your earlier messages, listed here.</p>
          {(summary.messages || []).length === 0
            ? <p className="text-xs text-muted-foreground">No earlier messages were kept.</p>
            : (summary.messages || []).map((message, index) => <p key={index} className="my-2 whitespace-pre-wrap break-words border-l-2 border-border pl-3">{message}</p>)}
        </>}
      </div>
      {(summary || error) && <footer className="flex shrink-0 items-center gap-2 border-t border-border px-4 py-2.5">
        {summary && <button type="button" onClick={copy} className="rounded-md bg-primary px-2.5 py-1 text-xs text-primary-foreground hover:opacity-90">{copied ? 'Copied' : 'Copy'}</button>}
        <button type="button" onClick={ask} className="rounded-md border border-border px-2.5 py-1 text-xs hover:bg-muted">Ask the agent about it</button>
        {summary && !kept && <span className="ml-auto text-[11px] text-muted-foreground">Written by the agent for itself</span>}
      </footer>}
    </aside>
  </ModalPortal>
}
