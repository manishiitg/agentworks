import { useEffect, useRef, useState, type ReactNode } from 'react'
import { ArrowLeft, Bot, Loader2, Send } from 'lucide-react'
import api from '../../services/api'
import { MarkdownRenderer } from '../../components/ui/MarkdownRenderer'

type SharedAgent = { chat_id: string; name: string; purpose?: string; shared?: boolean }
type ChatLine = { seq: number; role: string; text: string; sender?: string; at?: string }
type ChatPage = { messages?: ChatLine[]; next_after?: number; working?: boolean; has_earlier?: boolean; first_seq?: number }

/** Run recipients use the actual selected agent. Its owner keeps all controls. */
export function SharedCrewWorkspace({ projectId, enabled, children }: { projectId: string; enabled: boolean; children: ReactNode }) {
  const [agents, setAgents] = useState<SharedAgent[]>([])
  const [open, setOpen] = useState(false)
  const [selected, setSelected] = useState<SharedAgent | null>(null)
  const [listError, setListError] = useState('')
  useEffect(() => {
    if (!enabled) return
    const controller = new AbortController()
    void api.post<{ chats: SharedAgent[] }>('/api/crew-agents', { action: 'chats', workspace_id: `work:${projectId}` }, { signal: controller.signal, skipSessionContext: true })
      .then(({ data }) => { if (!controller.signal.aborted) { setAgents((data.chats || []).filter(agent => agent.shared)); setListError('') } })
      .catch(() => { if (!controller.signal.aborted) { setAgents([]); setListError('Could not load shared agents. Reopen this project to try again.') } })
    return () => controller.abort()
  }, [enabled, projectId, open])
  if (!enabled) return <>{children}</>
  return <div className="flex h-full min-h-0 flex-col">
    <nav aria-label="Shared Crew" className="flex shrink-0 items-center gap-1 border-b border-border px-3 py-2">
      <button type="button" aria-current={!open ? 'page' : undefined} onClick={() => setOpen(false)} className={`rounded-md px-3 py-1.5 text-sm ${!open ? 'bg-muted font-medium' : 'text-muted-foreground hover:bg-muted'}`}>Dashboard</button>
      <button type="button" aria-current={open ? 'page' : undefined} onClick={() => setOpen(true)} className={`rounded-md px-3 py-1.5 text-sm ${open ? 'bg-muted font-medium' : 'text-muted-foreground hover:bg-muted'}`}>Agents{agents.length ? ` · ${agents.length}` : ''}</button>
    </nav>
    {!open ? <div className="min-h-0 flex-1">{children}</div> : selected ? <SharedAgentConversation key={selected.chat_id} projectId={projectId} agent={selected} onBack={() => setSelected(null)} /> : <div className="min-h-0 flex-1 overflow-y-auto p-4 sm:p-6">
      <p className="mb-4 text-sm text-muted-foreground">Choose an agent to join its existing conversation. Your messages appear with your name.</p>
      {listError && <p role="alert" className="text-sm text-destructive">{listError}</p>}
      {!listError && !agents.length && <p className="text-sm text-muted-foreground">No agents have been shared with you in this Crew.</p>}
      <div className="mx-auto grid max-w-3xl gap-3 sm:grid-cols-2">{agents.map(agent => <button type="button" key={agent.chat_id} onClick={() => setSelected(agent)} className="flex gap-3 rounded-xl border border-border p-4 text-left hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <Bot aria-hidden="true" className="mt-0.5 h-5 w-5 shrink-0 text-primary" /><span className="min-w-0"><span className="block text-sm font-semibold">{agent.name}</span><span className="mt-1 block text-sm leading-5 text-muted-foreground">{agent.purpose}</span></span>
      </button>)}</div>
    </div>}
  </div>
}

function SharedAgentConversation({ projectId, agent, onBack }: { projectId: string; agent: SharedAgent; onBack: () => void }) {
  const [messages, setMessages] = useState<ChatLine[]>([])
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [loading, setLoading] = useState(true)
  const [working, setWorking] = useState(false)
  const [error, setError] = useState('')
  const [refresh, setRefresh] = useState(0)
  const [visible, setVisible] = useState(document.visibilityState !== 'hidden')
  const [earlier, setEarlier] = useState(false)
  const [loadingEarlier, setLoadingEarlier] = useState(false)
  const bottom = useRef<HTMLDivElement>(null)
  const scope = { workspace_id: `work:${projectId}`, chat_id: agent.chat_id }
  useEffect(() => {
    const update = () => setVisible(document.visibilityState !== 'hidden')
    document.addEventListener('visibilitychange', update)
    return () => document.removeEventListener('visibilitychange', update)
  }, [])
  useEffect(() => {
    if (!visible) return
    const controller = new AbortController()
    let after = 0
    let first = true
    const read = async () => {
      try {
        while (!controller.signal.aborted) {
          const { data } = await api.post<ChatPage>('/api/crew-agents', { action: 'read_chat', workspace_id: `work:${projectId}`, chat_id: agent.chat_id, after, wait_seconds: first ? 0 : 25 }, { signal: controller.signal, timeout: 35000, skipSessionContext: true })
          if (controller.signal.aborted) return
          const incoming = data.messages || []
          if (first) { setMessages(incoming); setEarlier(data.has_earlier === true) }
          else if (incoming.length) setMessages(current => [...current, ...incoming.filter(line => !current.some(existing => existing.seq === line.seq))])
          after = data.next_after || after
          first = false
          setWorking(data.working === true)
          setLoading(false)
          setError('')
        }
      } catch {
        if (!controller.signal.aborted) { setMessages([]); setError('This conversation could not be loaded. Its sharing may have changed.'); setLoading(false) }
      }
    }
    void read()
    return () => controller.abort()
  }, [projectId, agent.chat_id, visible, refresh])
  useEffect(() => { bottom.current?.scrollIntoView?.({ block: 'end' }) }, [messages.length])
  const send = async () => {
    if (sending || !draft.trim()) return
    setSending(true)
    try {
      await api.post('/api/crew-agents', { action: 'ask', ...scope, message: draft.trim(), submission_id: crypto.randomUUID() }, { skipSessionContext: true })
      setDraft(''); setRefresh(value => value + 1)
    } catch { setError('Your message was not accepted. Check that this agent is still shared with you.') }
    finally { setSending(false) }
  }
  const loadEarlier = async () => {
    if (!messages.length || loadingEarlier) return
    setLoadingEarlier(true)
    try {
      const { data } = await api.post<ChatPage>('/api/crew-agents', { action: 'read_chat', ...scope, before_seq: messages[0].seq }, { skipSessionContext: true })
      setMessages(current => [...(data.messages || []).filter(line => !current.some(existing => existing.seq === line.seq)), ...current]); setEarlier(data.has_earlier === true)
    } catch { setError('Earlier messages could not be loaded.') }
    finally { setLoadingEarlier(false) }
  }
  return <section className="flex min-h-0 flex-1 flex-col" aria-label={`${agent.name} conversation`}>
    <header className="flex shrink-0 items-start gap-3 border-b border-border px-4 py-3"><button type="button" aria-label="Back to shared agents" onClick={onBack} className="rounded p-1 hover:bg-muted"><ArrowLeft className="h-4 w-4" /></button><div><h2 className="text-sm font-semibold">{agent.name}</h2><p className="text-xs text-muted-foreground">{agent.purpose}</p></div></header>
    <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4 sm:px-6" role="log" aria-label="Agent conversation">
      {loading && <p role="status" className="text-sm text-muted-foreground">Loading conversation…</p>}
      {earlier && <button type="button" disabled={loadingEarlier} onClick={() => void loadEarlier()} className="mb-4 text-xs text-primary underline">{loadingEarlier ? 'Loading…' : 'Earlier messages'}</button>}
      {messages.map(line => <article key={line.seq} className={`mx-auto mb-5 max-w-3xl ${line.role === 'tool' ? 'text-xs text-muted-foreground' : ''}`}>
        <p className="mb-1 text-xs font-medium text-muted-foreground">{line.role === 'assistant' ? agent.name : line.role === 'tool' ? 'Tool' : line.sender || 'Owner'}</p>
        {line.role === 'tool' ? <p>{line.text}</p> : <MarkdownRenderer content={line.text} className="text-sm" untrustedContent compactImages />}
      </article>)}
      {working && <p role="status" className="mx-auto flex max-w-3xl items-center gap-2 text-xs text-muted-foreground"><Loader2 className="h-3 w-3 animate-spin" />Working…</p>}<div ref={bottom} />
    </div>
    <form className="shrink-0 space-y-2 border-t border-border p-3" onSubmit={event => { event.preventDefault(); void send() }}>
      {error && <p role="alert" className="text-xs text-destructive">{error} <button type="button" onClick={() => { setLoading(true); setRefresh(value => value + 1) }} className="underline">Try again</button></p>}
      <div className="flex items-end gap-2"><textarea aria-label={`Message ${agent.name}`} placeholder={`Ask ${agent.name}…`} value={draft} onChange={event => setDraft(event.target.value)} maxLength={20000} rows={2} className="max-h-40 min-h-16 min-w-0 flex-1 resize-y rounded-lg border border-border bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" />
        <button type="submit" disabled={sending || !draft.trim() || loading} aria-label="Send message" className="rounded-lg bg-primary p-2.5 text-primary-foreground disabled:opacity-50"><Send className="h-4 w-4" /></button></div>
      <p className="text-[11px] text-muted-foreground">Your message joins this agent's shared conversation.</p>
    </form>
  </section>
}
