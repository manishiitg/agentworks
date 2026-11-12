import { Fragment, useEffect, useRef, useState } from 'react'
import { ChevronRight, Clock, Lock } from 'lucide-react'
import { liveFeed } from '../../services/liveFeed'
import { useChatStore } from '../../stores/useChatStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useCrewAgentsStore } from '../../components/chat/AgentCard'
import { openAgentTab } from '../../utils/openAgentTab'
import { workConversationAgent } from '../../utils/workConversationKey'
import { teamFailure, teamRequest, type TeamTarget } from './teamApi'

type ActivityEnd = { person: string; agent: string; you?: boolean; label: string; chat_key: string; watch_key?: string; tab?: string }
type ActivityMessage = { id: string; sentence: string; topic?: string; from: ActivityEnd; to: ActivityEnd; sent_at: string; status: 'answered' | 'waiting' | 'no_answer' | 'mac_off'; status_line: string; answered_at?: string; question?: string; answer?: string; hidden_line?: string }
type ActivityItem = { id: string; kind: 'decision' | 'finding' | 'question'; at: string; person: string; title: string; verb: string; line?: string; why?: string; state?: ActivityMessage['status']; state_line?: string; message?: ActivityMessage }
type ActivityView = { feed: ActivityItem[]; next_cursor?: string; refresh_after_ms?: number }

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
          current = { ...data, feed: older && current ? [...current.feed, ...data.feed] : data.feed }
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
  const [filter, setFilter] = useState<ActivityItem['kind'] | 'all'>('all')
  if (!view) return error ? <div role="alert" className="space-y-2 rounded-xl border border-border p-4 text-sm"><p>Could not load Team activity. {error}</p><button type="button" onClick={reload} className="text-primary hover:underline">Try again</button></div> : <p role="status" className="text-sm text-muted-foreground">Loading Team activity…</p>
  const heading = 'text-[11px] font-semibold uppercase tracking-wide text-muted-foreground'
  const items = view.feed.filter(item => filter === 'all' || item.kind === filter)
  return <section aria-label="Activity" className="space-y-3 text-sm">
    <div role="group" aria-label="Filter activity" className="flex gap-1 text-xs">
      {([['all', 'All'], ['decision', 'Decisions'], ['question', 'Questions'], ['finding', 'Findings']] as const).map(([kind, label]) => <button key={kind} type="button" aria-pressed={filter === kind} onClick={() => setFilter(kind)} className={`rounded-md px-2 py-1 ${filter === kind ? 'bg-muted font-medium' : 'text-muted-foreground hover:bg-muted/50'}`}>{label}</button>)}
    </div>
    {items.length === 0 && <p className="text-xs text-muted-foreground">Nothing yet. Decisions, schedule findings and agent questions show here.</p>}
    {items.map((item, index) => {
      const group = day(item.at)
      const message = item.message
      const readable = Boolean(message?.question), isOpen = readable && open === item.id
      return <Fragment key={`${item.kind}:${item.id}`}>
        {(index === 0 || day(items[index - 1].at) !== group) && <h3 className={`${heading} pt-2`}>{group}</h3>}
        <div className="flex items-start gap-2 py-2">
          <span aria-hidden="true" className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-muted text-[11px] font-medium">{item.kind === 'finding' ? <Clock className="h-3.5 w-3.5" /> : item.person.slice(0, 1).toUpperCase()}</span>
          <div className="min-w-0 flex-1">
            {message ? <button type="button" disabled={!readable} aria-expanded={readable ? isOpen : undefined} onClick={() => setOpen(isOpen ? '' : item.id)} className="flex w-full items-start gap-1 text-left enabled:hover:text-primary">
              <span className="min-w-0 flex-1 text-[13px]"><strong className="font-semibold">{item.title}</strong> <span className="text-muted-foreground">{item.verb}</span></span>
              {readable && <ChevronRight aria-hidden="true" className={`mt-1 h-3 w-3 shrink-0 text-muted-foreground ${isOpen ? 'rotate-90' : ''}`} />}
            </button> : <p className="text-[13px]"><strong className="font-semibold">{item.title}</strong> <span className="text-muted-foreground">{item.verb}</span></p>}
            {item.line && <p className="mt-0.5 truncate text-[13px]">{item.line}</p>}
            {item.why && <p className="mt-0.5 text-xs text-muted-foreground">{item.why}</p>}
            {message && <p className={`mt-0.5 text-xs ${item.state === 'waiting' || item.state === 'no_answer' ? 'text-amber-600 dark:text-amber-400' : 'text-muted-foreground'}`}>{item.state_line}</p>}
            {message?.hidden_line && <p className="mt-1 flex items-center gap-1 text-xs text-muted-foreground"><Lock aria-hidden="true" className="h-3 w-3 shrink-0" />{message.hidden_line}</p>}
            {isOpen && message && <div className="mt-2 space-y-2 text-[13px]"><p className="whitespace-pre-wrap">{message.question}</p>{message.answer && <p className="whitespace-pre-wrap text-muted-foreground">{message.answer}</p>}<button type="button" onClick={() => openAgent(projectId, message.to)} className="text-xs text-primary hover:underline">Open →</button></div>}
          </div>
          <time dateTime={item.at} className="shrink-0 text-[11px] text-muted-foreground">{clock(item.at)}</time>
        </div>
      </Fragment>
    })}
    {view.next_cursor && <button type="button" disabled={olderLoading} onClick={older} className="text-xs text-primary hover:underline disabled:opacity-50">{olderLoading ? 'Loading…' : 'Show older'}</button>}
    {error && <p role="alert" className="text-xs text-destructive">{error} <button type="button" onClick={reload} className="underline">Try again</button></p>}
  </section>
}
