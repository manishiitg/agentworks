import { lazy, Suspense, useEffect, useMemo, useState, type ReactNode } from 'react'
import { ArrowLeft, Bot } from 'lucide-react'
import api from '../../services/api'
import type { CodingAgentQuestionAnswerHandler } from '../../utils/codingAgentQuestions'
import type { PollingEvent } from '../../services/api-types'
// This module is also used by the eager sidebar and report page. Load the
// full chat only when a shared conversation is opened (PLAT-856-509).
const ChatArea = lazy(() => import('../../components/ChatArea'))
import { useChatStore } from '../../stores/useChatStore'

export type SharedCrewScope = { workspace_id: string } | { workspace: string }
export type SharedAgent = { chat_id: string; name: string; purpose?: string; shared?: boolean; watchOnly?: boolean }
type ChatLine = { seq: number; role: string; text: string; sender?: string; at?: string }
type ChatPage = { messages?: ChatLine[]; events?: PollingEvent[]; next_after?: number; working?: boolean; has_earlier?: boolean; first_seq?: number }

/** Run recipients use the actual selected agent. Its owner keeps all controls. */
export function SharedCrewWorkspace({ projectId, workspacePath, enabled, children }: { projectId?: string; workspacePath?: string; enabled: boolean; children: ReactNode }) {
  const scope = useMemo<SharedCrewScope>(() => projectId ? { workspace_id: `work:${projectId}` } : { workspace: workspacePath || '' }, [projectId, workspacePath])
  const [agents, setAgents] = useState<SharedAgent[]>([])
  const [open, setOpen] = useState(false)
  const [selected, setSelected] = useState<SharedAgent | null>(null)
  const [listError, setListError] = useState('')
  useEffect(() => {
    if (!enabled) return
    const controller = new AbortController()
    void api.post<{ chats: SharedAgent[] }>('/api/crew-agents', { action: 'chats', ...scope }, { signal: controller.signal, skipSessionContext: true })
      .then(({ data }) => { if (!controller.signal.aborted) { setAgents((data.chats || []).filter(agent => agent.shared)); setListError('') } })
      .catch(() => { if (!controller.signal.aborted) { setAgents([]); setListError('Could not load shared agents. Reopen this project to try again.') } })
    return () => controller.abort()
  }, [enabled, scope, open])
  if (!enabled || (!agents.length && !selected)) return <>{children}</>
  return <div className="flex h-full min-h-0 flex-col">
    <nav aria-label="Shared Crew" className="flex shrink-0 items-center gap-1 border-b border-border px-3 py-2">
      <button type="button" aria-current={!open ? 'page' : undefined} onClick={() => setOpen(false)} className={`rounded-md px-3 py-1.5 text-sm ${!open ? 'bg-muted font-medium' : 'text-muted-foreground hover:bg-muted'}`}>Dashboard</button>
      <button type="button" aria-current={open ? 'page' : undefined} onClick={() => setOpen(true)} className={`rounded-md px-3 py-1.5 text-sm ${open ? 'bg-muted font-medium' : 'text-muted-foreground hover:bg-muted'}`}>Agents{agents.length ? ` · ${agents.length}` : ''}</button>
    </nav>
    {!open ? <div className="min-h-0 flex-1">{children}</div> : selected ? <SharedAgentConversation key={selected.chat_id} scope={scope} agent={selected} onBack={() => setSelected(null)} /> : <div className="min-h-0 flex-1 overflow-y-auto p-4 sm:p-6">
      <p className="mb-4 text-sm text-muted-foreground">Choose an agent to join its existing conversation. Your messages appear with your name.</p>
      {listError && <p role="alert" className="text-sm text-destructive">{listError}</p>}
      {!listError && !agents.length && <p className="text-sm text-muted-foreground">No agents have been shared with you in this Crew.</p>}
      <div className="mx-auto grid max-w-3xl gap-3 sm:grid-cols-2">{agents.map(agent => <button type="button" key={agent.chat_id} onClick={() => setSelected(agent)} className="flex gap-3 rounded-xl border border-border p-4 text-left hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <Bot aria-hidden="true" className="mt-0.5 h-5 w-5 shrink-0 text-primary" /><span className="min-w-0"><span className="block text-sm font-semibold">{agent.name}</span><span className="mt-1 block text-sm leading-5 text-muted-foreground">{agent.purpose}</span></span>
      </button>)}</div>
    </div>}
  </div>
}

/** A person's own chat with an agent shared with them: the same chat area and message box as the owner's (owner, 4 Nov:
 * "reuse the same chat area and chat input"). Its lines come from read_chat and its messages go out as ask. */
export function SharedAgentConversation({ scope: crewScope, agent, onBack, ownerName }: { scope: SharedCrewScope; agent: SharedAgent; onBack?: () => void; ownerName?: string }) {
  const [tabId, setTabId] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [refresh, setRefresh] = useState(0)
  const [visible, setVisible] = useState(document.visibilityState !== 'hidden')
  const sessionId = `shared-agent:${'workspace_id' in crewScope ? crewScope.workspace_id : crewScope.workspace}:${agent.chat_id}`
  const scope = { ...crewScope, chat_id: agent.chat_id }
  const macOff = `${ownerName || 'Its owner'}'s Mac is off. Try again later.`
  // A new scope object each render must not restart the reader; its contents decide.
  const scopeKey = JSON.stringify(crewScope)
  useEffect(() => {
    const update = () => setVisible(document.visibilityState !== 'hidden')
    document.addEventListener('visibilitychange', update)
    return () => document.removeEventListener('visibilitychange', update)
  }, [])
  // A tab of its own for the chat area, fed by this page; it is never on a tab strip and goes when the page does.
  useEffect(() => {
    let live = true
    let created = ''
    const store = useChatStore.getState()
    const existing = Object.values(store.chatTabs).find(tab => tab.sessionId === sessionId)
    void (existing ? Promise.resolve(existing.tabId) : store.createChatTab(agent.name, { mode: 'multi-agent', agentProfileId: 'shared-agent', agentProfileConversationKey: sessionId, externalTransport: true }, sessionId, { activate: false }))
      .then(id => { created = id; if (live) setTabId(id); else void useChatStore.getState().closeTab(id, false) })
      .catch(() => { if (live) setError('This conversation could not be opened.') })
    return () => { live = false; if (created) void useChatStore.getState().closeTab(created, false) }
  }, [sessionId, agent.name])
  useEffect(() => {
    if (!visible || !tabId) return
    const controller = new AbortController()
    let after = 0
    let lines: ChatLine[] = []
    // The chat's own events when its owner's Mac sends them (the same view as the owner's); plain lines otherwise.
    let events: PollingEvent[] | null = null
    const show = (working: boolean) => {
      const store = useChatStore.getState()
      store.setTabEvents(sessionId, events ?? chatLineEvents(sessionId, lines))
      store.markTabHistoryLoaded(sessionId)
      store.setTabStreaming(tabId, working)
      store.setTabCompleted(tabId, !working)
    }
    const read = async () => {
      try {
        for (let first = true; !controller.signal.aborted; first = false) {
          const { data } = await api.post<ChatPage>('/api/crew-agents', { action: 'read_chat', ...crewScope, chat_id: agent.chat_id, after, wait_seconds: first ? 0 : 25, events: true }, { signal: controller.signal, timeout: 35000, skipSessionContext: true })
          if (controller.signal.aborted) return
          const incoming = data.messages || []
          lines = first ? incoming : [...lines, ...incoming.filter(line => !lines.some(existing => existing.seq === line.seq))]
          if (data.events) {
            const known = new Set((events || []).map(event => event.sequence))
            const fresh = data.events.filter(event => !known.has(event.sequence)).map(event => ({ ...event, session_id: sessionId }))
            events = [...(first ? [] : events || []), ...fresh].sort((a, b) => (a.sequence ?? 0) - (b.sequence ?? 0))
          }
          after = data.next_after || after
          show(data.working === true)
          setError('')
        }
      } catch (cause) {
        if (controller.signal.aborted) return
        // A shared agent on someone's Mac answers only while that Mac is on (the server says 503).
        setError((cause as { response?: { status?: number } })?.response?.status === 503 ? macOff : 'This conversation could not be loaded. Its sharing may have changed.')
      }
    }
    void read()
    return () => controller.abort()
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scopeKey, agent.chat_id, visible, refresh, tabId, sessionId, macOff])
  const send = async (message: string) => {
    if (!message.trim()) return false
    try {
      await api.post('/api/crew-agents', { action: 'ask', ...scope, message: message.trim(), submission_id: crypto.randomUUID() }, { skipSessionContext: true })
      setRefresh(value => value + 1)
      return true
    } catch (cause) {
      setError((cause as { response?: { status?: number } })?.response?.status === 503 ? macOff : 'Your message was not accepted. Check that this agent is still shared with you.')
      return false
    }
  }
  const answerQuestion: CodingAgentQuestionAnswerHandler = async (provider, promptId, answers, auto) => {
    await api.post('/api/crew-agents', {
      action: 'reply_input', ...scope, provider, request_id: promptId, auto,
      answers: answers.map(answer => ({ id: answer.id, selected_labels: answer.selectedLabels, other_text: answer.otherText })),
    }, { skipSessionContext: true })
    setRefresh(value => value + 1)
  }
  return <section className="flex min-h-0 flex-1 flex-col" aria-label={`${agent.name} conversation`}>
    {(onBack || agent.purpose) && <header className="flex shrink-0 items-start gap-3 border-b border-border px-4 py-2.5">{onBack && <button type="button" aria-label="Back to shared agents" onClick={onBack} className="rounded p-1 hover:bg-muted"><ArrowLeft className="h-4 w-4" /></button>}<div className="min-w-0">{onBack && <h2 className="text-sm font-semibold">{agent.name}</h2>}<Purpose text={agent.purpose} /></div></header>}
    {error && <p role="alert" className="shrink-0 px-4 py-2 text-xs text-destructive">{error} <button type="button" onClick={() => setRefresh(value => value + 1)} className="underline">Try again</button></p>}
    <div className="min-h-0 flex-1">{tabId && <Suspense fallback={<p role="status" className="p-4 text-sm text-muted-foreground">Loading chat…</p>}><ChatArea tabId={tabId} inputVariant="product" hideRuntimeStatus hideInput={agent.watchOnly} onSubmitOverride={send} onQuestionAnswerOverride={answerQuestion} composerPlaceholder={`Ask ${agent.name}…`} /></Suspense>}</div>
    {agent.watchOnly
      ? <p className="mx-4 mb-3 shrink-0 rounded-xl bg-muted px-3 py-2.5 text-[13px] text-muted-foreground">👁 You're watching {ownerName ? `${ownerName}'s` : 'this'} {agent.name}. Only {ownerName || 'its owner'} types here. To ask it something, ask from your own agent.</p>
      : <p className="shrink-0 px-4 pb-2 text-[11px] text-muted-foreground">Only you see your messages and its answers.</p>}
  </section>
}

// read_chat lines as the chat area's own events: messages, and each tool as a finished tool row (collapsed by default).
function chatLineEvents(sessionId: string, lines: ChatLine[]): PollingEvent[] {
  return lines.map((line, index) => {
    const id = `${sessionId}:${line.seq}`
    const timestamp = line.at || new Date(0).toISOString()
    const type = line.role === 'tool' ? 'tool_call_end' : line.role === 'assistant' ? 'llm_generation_end' : 'user_message'
    const data: Record<string, unknown> = line.role === 'tool'
      ? { tool_name: line.text.trim().split(/\s/)[0] || 'tool', tool_call_id: id, arguments: '', tool_params: { arguments: '' }, result: '' }
      : line.role === 'assistant' ? { content: line.text } : { content: line.text, role: 'user', ...(line.sender ? { metadata: { source: 'agent_message', sender_label: line.sender } } : {}) }
    return { id, type, timestamp, session_id: sessionId, sequence: index, data: { type, data } } as PollingEvent
  })
}

/** A purpose of one or two lines, with "more" for the rest. */
function Purpose({ text }: { text?: string }) {
  const [open, setOpen] = useState(false)
  if (!text) return null
  return <p className={`text-xs text-muted-foreground ${open ? 'whitespace-pre-wrap' : 'line-clamp-2'}`}>{text}{text.length > 140 && <> <button type="button" onClick={() => setOpen(value => !value)} className="text-primary hover:underline">{open ? 'less' : 'more'}</button></>}</p>
}
