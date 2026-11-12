import { Fragment, useEffect, useRef, useState } from 'react'
import { ChevronRight, Lock } from 'lucide-react'
import { liveFeed } from '../../services/liveFeed'
import { useChatStore } from '../../stores/useChatStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useCrewAgentsStore } from '../../components/chat/AgentCard'
import { openAgentTab } from '../../utils/openAgentTab'
import { workConversationAgent } from '../../utils/workConversationKey'
import { openCrewAccess } from './MacSharedAgents'
import { teamFailure, teamRequest, type TeamTarget } from './teamApi'

type ActivityEnd = { person: string; agent: string; you?: boolean; label: string; chat_key: string; watch_key?: string; tab?: string }
type ActivityMessage = { id: string; sentence: string; topic?: string; from: ActivityEnd; to: ActivityEnd; sent_at: string; status: 'answered' | 'waiting' | 'no_answer' | 'mac_off'; status_line: string; answered_at?: string; question?: string; answer?: string; hidden_line?: string }
type ActivityPost = { id: string; person: string; agent: string; line: string; why?: string; schedule?: string; at: string }
type ActivityView = { decisions: ActivityPost[]; findings: ActivityPost[]; now: { person: string; agent: string; you?: boolean; state: 'running' | 'waiting'; line: string; doing?: string; end: ActivityEnd }[]; idle: ActivityEnd[]; messages: ActivityMessage[]; other: { kind: string; path: string; line: string }[]; next_cursor?: string; refresh_after_ms?: number; foot: string }

const pill: Record<ActivityMessage['status'], string> = {
  answered: 'border-blue-500/25 bg-blue-500/10 text-blue-600 dark:text-blue-300',
  waiting: 'border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-400',
  no_answer: 'border-destructive/30 bg-destructive/10 text-destructive',
  mac_off: 'border-border bg-muted text-muted-foreground',
}
const clock = (at: string) => new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
function day(at: string) {
  const date = new Date(at), today = new Date(), yesterday = new Date()
  yesterday.setDate(today.getDate() - 1)
  return date.toDateString() === today.toDateString() ? 'Today' : date.toDateString() === yesterday.toDateString() ? 'Yesterday' : date.toLocaleDateString([], { day: 'numeric', month: 'short', year: 'numeric' })
}

/** Use the admitted tab binding, never a person's display name, to open an agent. */
function tabFor(projectId: string, end: ActivityEnd) {
  return Object.values(useChatStore.getState().chatTabs).find(tab => {
    const meta = tab.metadata, key = meta?.agentProfileConversationKey
    if (meta?.agentProfileProjectId !== projectId || !key) return false
    return key === end.chat_key || key === end.watch_key || key.endsWith(`:team:${end.chat_key}`) ||
      end.you && !meta.isViewOnly && meta.agentProfileTabCanWrite !== false && !!end.tab && (meta.agentProfileIsMainChat ? 'main' : workConversationAgent(key)) === end.tab
  })?.tabId
}
function openAgent(projectId: string, end: ActivityEnd) {
  const tab = tabFor(projectId, end)
  if (tab) { openAgentTab(tab); return }
  const crew = useCrewAgentsStore.getState()
  const closed = crew.projectId === projectId ? crew.closed.find(agent => agent.key === end.chat_key || agent.key === end.watch_key || end.you && !!end.tab && workConversationAgent(agent.key) === end.tab) : undefined
  if (closed) { void closed.open(); return }
  useChatStore.getState().addToast('This agent is not open here yet. Open it from the agent chips.', 'error')
}

/** Same crew_panel notices as the Team page; older pages use a server message-ID cursor, not a timer. */
function useTeamActivity(target: TeamTarget, projectId: string) {
  const user = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = JSON.stringify([user, server, target.connection, target.project, projectId])
  const [loadedScope, setLoadedScope] = useState('')
  const [view, setView] = useState<ActivityView>()
  const [error, setError] = useState('')
  const [olderLoading, setOlderLoading] = useState(false)
  const loadRef = useRef<(older?: boolean) => void>(() => {})
  useEffect(() => {
    let active = true, request = 0, current: ActivityView | undefined
    let boundary: ReturnType<typeof setTimeout> | undefined
    setView(undefined); setError(''); setOlderLoading(false); setLoadedScope(scope)
    const load = (older = false) => {
      clearTimeout(boundary)
      const cursor = older ? current?.next_cursor : undefined
      if (older && !cursor) return
      const sequence = ++request
      if (older) setOlderLoading(true)
      void teamRequest<ActivityView>(target, `/activity${cursor ? `?before=${encodeURIComponent(cursor)}` : ''}`)
        .then(data => {
          if (!active || sequence !== request) return
          current = { ...data, messages: older && current ? [...current.messages, ...data.messages] : data.messages }
          setView(current); setError('')
          if (data.refresh_after_ms && data.refresh_after_ms > 0) boundary = setTimeout(() => load(), data.refresh_after_ms)
        })
        .catch(reason => { if (active && sequence === request) setError(teamFailure(reason).message) })
        .finally(() => { if (active && sequence === request) setOlderLoading(false) })
    }
    loadRef.current = load
    const refresh = () => load()
    refresh()
    window.addEventListener('agentworks:team-updated', refresh)
    const unlive = liveFeed.subscribe(['crew_panel'], null, refresh, projectId)
    return () => { active = false; clearTimeout(boundary); loadRef.current = () => {}; window.removeEventListener('agentworks:team-updated', refresh); unlive() }
  }, [target.project, target.connection, projectId, scope])
  return { view: loadedScope === scope ? view : undefined, error: loadedScope === scope ? error : '', olderLoading, reload: () => loadRef.current(), older: () => loadRef.current(true) }
}

export function TeamActivity({ target, projectId }: { target: TeamTarget; projectId: string }) {
  const { view, error, reload, older, olderLoading } = useTeamActivity(target, projectId)
  const [open, setOpen] = useState('')
  if (!view) return error ? <div role="alert" className="space-y-2 rounded-xl border border-border p-4 text-sm"><p>Could not load Team activity. {error}</p><button type="button" onClick={reload} className="text-primary hover:underline">Try again</button></div> : <p role="status" className="text-sm text-muted-foreground">Loading Team activity…</p>
  const heading = 'text-[11px] font-semibold uppercase tracking-wide text-muted-foreground'
  const agentOpen = (end: ActivityEnd) => <button type="button" onClick={() => openAgent(projectId, end)} className="shrink-0 text-xs text-primary hover:underline">Open →</button>
  return <section aria-label="Activity" className="space-y-4 rounded-xl border border-border px-4 py-3 text-sm">
    {view.other.length > 0 && <section aria-label="Needs attention" className="space-y-2">
      <h3 className={`${heading} text-amber-600`}>Needs attention</h3>
      {view.other.map(item => <div key={item.path} className="flex items-center gap-3 rounded-lg border border-amber-500/30 bg-amber-500/5 px-3 py-2">
        <p className="min-w-0 flex-1 break-words text-[13px]">{item.line}</p>
        <button type="button" onClick={() => openCrewAccess(projectId, '', 'team', 'sync')} className="shrink-0 text-xs text-primary hover:underline">Pick a copy →</button>
      </div>)}
    </section>}
    <section aria-label="Working now" className="space-y-2">
      <h3 className={heading}>Working now</h3>
      {view.now.length === 0 && <p className="text-xs text-muted-foreground">No agent is working right now.</p>}
      {view.now.map(agent => <div key={agent.end.chat_key} className="flex items-center gap-2 rounded-lg border border-border px-3 py-2">
        <span aria-hidden="true" className={`h-2 w-2 shrink-0 rounded-full ${agent.state === 'running' ? 'motion-safe:animate-pulse bg-emerald-500' : 'bg-amber-500'}`} />
        <div className="min-w-0 flex-1"><p className="font-medium">{agent.end.label}</p><p className="text-xs text-muted-foreground">{agent.doing || agent.line}{agent.doing && agent.state === 'waiting' ? ` · ${agent.line}` : ''}</p></div>{agentOpen(agent.end)}
      </div>)}
      {!!view.idle?.length && <details><summary className="cursor-pointer text-xs text-muted-foreground">{view.idle.length} others idle</summary><div className="mt-2 space-y-1">{view.idle.map(agent => <div key={agent.chat_key} className="flex items-center justify-between gap-2 px-3 py-1 text-xs text-muted-foreground"><span>{agent.label}</span>{agentOpen(agent)}</div>)}</div></details>}
    </section>
    {!!view.decisions?.length && <section aria-label="Decisions" className="space-y-2"><h3 className={heading}>Decisions</h3>{view.decisions.map(post => <div key={post.id} className="rounded-lg border border-border px-3 py-2"><p className="text-xs text-muted-foreground">{post.person}'s {post.agent} · {clock(post.at)}</p><p className="mt-1 text-[13px] font-medium">{post.line}</p>{post.why && <p className="mt-1 text-xs text-muted-foreground">Why: {post.why}</p>}</div>)}</section>}
    {!!view.findings?.length && <section aria-label="Schedule findings" className="space-y-2"><h3 className={heading}>Schedule findings</h3>{view.findings.map(post => <div key={post.id} className="rounded-lg border border-border px-3 py-2"><p className="text-xs text-muted-foreground">{post.schedule} · {post.person}'s {post.agent} · {clock(post.at)}</p><p className="mt-1 text-[13px]">{post.line}</p></div>)}</section>}
    <section aria-label="Agents asking each other" className="space-y-2">
      <h3 className={heading}>Agents asking each other</h3>
      {view.messages.length === 0 && <p className="text-xs text-muted-foreground">No messages between the Team's agents yet.</p>}
      {view.messages.map((message, index) => {
        const readable = Boolean(message.question), isOpen = readable && open === message.id
        const group = day(message.sent_at)
        return <Fragment key={message.id}>
          {(index === 0 || day(view.messages[index - 1].sent_at) !== group) && <p className="pt-2 text-xs font-medium text-muted-foreground">{group}</p>}
          <div className="rounded-lg border border-border">
            <button type="button" disabled={!readable} aria-expanded={readable ? isOpen : undefined} onClick={() => setOpen(isOpen ? '' : message.id)} className="flex w-full items-start gap-2 px-3 py-2 text-left enabled:hover:bg-muted/40">
              <ChevronRight aria-hidden="true" className={`mt-1 h-3 w-3 shrink-0 text-muted-foreground ${readable ? '' : 'invisible'} ${isOpen ? 'rotate-90' : ''}`} />
              <span className="min-w-0 flex-1"><span className="block text-[13px]">{message.sentence}</span>{message.topic && <span className="mt-0.5 block truncate text-xs text-muted-foreground">{message.topic}</span>}</span>
              <span className={`shrink-0 rounded-full border px-2 py-0.5 text-[11px] ${pill[message.status]}`}>{message.status_line}</span><span className="text-[11px] text-muted-foreground">{clock(message.sent_at)}</span>
            </button>
            {message.hidden_line && <p className="flex items-center gap-1.5 px-3 pb-2 pl-8 text-xs text-muted-foreground"><Lock aria-hidden="true" className="h-3 w-3 shrink-0" />{message.hidden_line}</p>}
            {isOpen && <div className="space-y-2 px-3 pb-3 pl-8 text-[13px]"><p className="whitespace-pre-wrap rounded-lg bg-muted/40 p-2">{message.question}</p>{message.answer && <p className="ml-4 whitespace-pre-wrap rounded-lg border border-border p-2">{message.answer}</p>}{agentOpen(message.to)}</div>}
          </div>
        </Fragment>
      })}
      {view.next_cursor && <button type="button" disabled={olderLoading} onClick={older} className="text-xs text-primary hover:underline disabled:opacity-50">{olderLoading ? 'Loading…' : 'Show older'}</button>}
    </section>
    {error && <p role="alert" className="text-xs text-destructive">{error} <button type="button" onClick={reload} className="underline">Try again</button></p>}
    <p className="text-xs text-muted-foreground">{view.foot}</p>
  </section>
}
