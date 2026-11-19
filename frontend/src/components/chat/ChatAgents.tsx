import { useEffect, useMemo, useState } from 'react'
import { create } from 'zustand'
import { Bot, ChevronLeft, ChevronRight, X } from 'lucide-react'
import type { PollingEvent } from '../../services/api-types'
import { agentApi } from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { collectChatHelpers, helperProviderName, linkedChatHelpers, mergeHelperEvents, orderChatHelpers, type ChatHelper, type ChatHelperState, type ChatLinkedHelper } from '../../utils/chatHelpers'
import { usePersistentTab } from '../../hooks/usePersistentTab'
import { WorkspaceContextRow } from '../workspace/WorkspaceContextCard'
import { startChatMessage } from '../../utils/startChatMessage'
import { openAgentTab } from '../../utils/openAgentTab'
import { openAppProduct } from '../../utils/appNavigation'
import { SubagentChatPanel } from '../events/SubagentChatPanel'
import ModalPortal from '../ui/ModalPortal'

/** Codex helpers whose state the server is asked for, newest first. */
const CODEX_STATE_LIMIT = 8

/**
 * The chat's helpers with each Codex helper's state read from the server: a
 * Codex spawn step says nothing about whether its helper still runs (#3245).
 */
/**
 * The chat's helper list, shared by the chips above the message box and the
 * side pane's "Agents in this chat" section; the chips pick a helper there.
 */
interface ChatHelpersState {
  lists: Record<string, ChatHelper[]>
  selected: Record<string, string | null>
  select: (sessionId: string, key: string | null) => void
}
export const useChatHelpersStore = create<ChatHelpersState>(set => ({
  lists: {},
  selected: {},
  select: (sessionId, key) => set(state => ({ selected: { ...state.selected, [sessionId]: key } })),
}))

/**
 * The server's lists for the chat (#3245): its saved helper events (the whole
 * chat) and the tasks it handed to its other tabs. Read again when the live
 * list gains a helper, and every 20 s while a helper runs.
 */
function useSavedHelpers(sessionId: string | undefined, liveCount: number, liveRunning: boolean): { events: PollingEvent[]; linked: ChatLinkedHelper[] } {
  const [saved, setSaved] = useState<{ sessionId?: string; events: PollingEvent[]; linked: ChatLinkedHelper[] }>({ events: [], linked: [] })
  const linkedRunning = saved.sessionId === sessionId && saved.linked.some(item => item.state !== 'done' && item.state !== 'blocked' && item.state !== 'needs_you')
  const running = liveRunning || linkedRunning
  useEffect(() => {
    if (!sessionId) return
    const identity = captureChatIdentity()
    let disposed = false
    const load = async () => {
      const [events, linked] = await Promise.all([
        agentApi.getSessionHelperEvents(sessionId).then(found => found.events).catch(() => undefined),
        agentApi.getSessionLinkedHelpers(sessionId).catch(() => undefined),
      ])
      if (disposed || !isChatIdentityCurrent(identity)) return
      // A failed read keeps what was read before; the live events still show recent helpers.
      setSaved(previous => {
        const same = previous.sessionId === sessionId
        return { sessionId, events: events ?? (same ? previous.events : []), linked: linked ?? (same ? previous.linked : []) }
      })
    }
    void load()
    if (!running) return () => { disposed = true }
    const timer = setInterval(() => { void load() }, 20000)
    return () => { disposed = true; clearInterval(timer) }
  }, [sessionId, liveCount, running])
  return saved.sessionId === sessionId ? saved : { events: [], linked: [] }
}

export function useChatHelpers(sessionId: string | undefined, events: PollingEvent[]): ChatHelper[] {
  const live = useMemo(() => collectChatHelpers(events), [events])
  const saved = useSavedHelpers(sessionId, live.length, live.some(helper => helper.state === 'running'))
  const collected = useMemo(() => [
    ...(saved.events.length ? collectChatHelpers(mergeHelperEvents(saved.events, events)) : live),
    ...linkedChatHelpers(saved.linked),
  ], [saved, events, live])
  const [codex, setCodex] = useState<Record<string, ChatHelperState>>({})
  // A running Codex helper the server can stop on its own (#3245).
  const [codexStop, setCodexStop] = useState<Record<string, string | undefined>>({})
  const askFor = useMemo(() => collected.filter(helper => helper.stateFromServer && helper.toolCallId)
    .slice(-CODEX_STATE_LIMIT).map(helper => helper.toolCallId as string), [collected])
  const pending = askFor.filter(id => codex[id] === undefined || codex[id] === 'running')
  const pendingKey = pending.join('|')

  useEffect(() => { setCodex({}); setCodexStop({}) }, [sessionId])
  useEffect(() => {
    if (!sessionId || !pendingKey) return
    const identity = captureChatIdentity()
    let disposed = false
    const ids = pendingKey.split('|')
    const load = async () => {
      const next: Record<string, ChatHelperState> = {}
      const stops: Record<string, string | undefined> = {}
      await Promise.all(ids.map(async id => {
        try {
          const found = await agentApi.getSessionSubagent(sessionId, id)
          next[id] = found.running ? 'running' : found.transcript?.status === 'failed' ? 'failed' : 'done'
          stops[id] = found.running ? found.stop_id : undefined
        } catch (failure) {
          // Gone (404): its conversation was cleared; keep it listed without a state.
          if ((failure as { response?: { status?: number } }).response?.status === 404) next[id] = 'unknown'
        }
      }))
      if (!disposed && isChatIdentityCurrent(identity)) {
        setCodex(previous => ({ ...previous, ...next }))
        setCodexStop(previous => ({ ...previous, ...stops }))
      }
    }
    void load()
    const timer = setInterval(() => { void load() }, 5000)
    return () => { disposed = true; clearInterval(timer) }
  }, [sessionId, pendingKey])

  const helpers = useMemo(() => collected.map(helper => {
    if (!helper.stateFromServer || !helper.toolCallId) return helper
    const state = codex[helper.toolCallId], stopId = codexStop[helper.toolCallId]
    return state && (state !== helper.state || stopId !== helper.stopId) ? { ...helper, state, stopId } : helper
  }), [collected, codex, codexStop])
  useEffect(() => {
    if (!sessionId) return
    useChatHelpersStore.setState(state => ({ lists: { ...state.lists, [sessionId]: helpers } }))
  }, [sessionId, helpers])
  return helpers
}

function ProviderBadge({ provider }: { provider: string }) {
  return <span className="inline-flex h-4 shrink-0 items-center rounded border border-border bg-muted px-1.5 text-[10px] font-semibold text-foreground">{helperProviderName(provider)}</span>
}
function StateDot({ state }: { state: ChatHelperState }) {
  const color = state === 'running' ? 'animate-pulse bg-primary' : state === 'done' ? 'bg-success' : state === 'failed' ? 'bg-destructive' : 'bg-muted-foreground/50'
  return <span className={`h-2 w-2 shrink-0 rounded-full ${color}`} aria-hidden="true" />
}

const stateWord = (state: ChatHelperState) => state === 'running' ? 'Running' : state === 'waiting' ? 'Needs you' : state === 'done' ? 'Done' : state === 'failed' ? 'Failed' : state === 'stopped' ? 'Stopped' : 'Finished or unknown'
function duration(ms: number): string {
  const seconds = Math.max(0, Math.floor(ms / 1000))
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  return minutes < 60 ? `${minutes}m ${String(seconds % 60).padStart(2, '0')}s` : `${Math.floor(minutes / 60)}h ${minutes % 60}m`
}
function ago(at: number, now: number): string {
  const minutes = Math.floor((now - at) / 60000)
  if (minutes < 1) return 'just now'
  if (minutes < 60) return `${minutes} min ago`
  const hours = Math.floor(minutes / 60)
  return hours < 24 ? `${hours} h ago` : new Date(at).toLocaleDateString([], { day: 'numeric', month: 'short' })
}
const elapsed = (helper: ChatHelper, now: number): string | undefined => helper.state === 'running' && helper.startedAt !== undefined
  ? duration(now - helper.startedAt) : helper.durationMs !== undefined ? duration(helper.durationMs)
    : helper.startedAt !== undefined && helper.endedAt !== undefined ? duration(helper.endedAt - helper.startedAt) : undefined

function useNow(active: boolean) {
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    if (!active) return
    setNow(Date.now())
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [active])
  return now
}

/** The side pane's section for this chat when it is on screen. */
const visibleSection = (sessionId: string): HTMLElement | null => {
  const section = document.querySelector<HTMLElement>(`[data-chat-agents-section="${CSS.escape(sessionId)}"]`)
  return section && section.offsetParent !== null ? section : null
}

/**
 * Above the message box, each helper by app and name (#3245): the running ones
 * with what they are doing and for how long. Finished helpers are not shown
 * (owner, 19 Nov). A chip opens the
 * helper in the side pane, or in a panel of its own when the side pane is not
 * showing.
 */
export function ChatAgentsStrip({ sessionId, helpers }: { sessionId: string; helpers: ChatHelper[] }) {
  const [open, setOpen] = useState<{ key: string | null } | null>(null)
  const { running } = orderChatHelpers(helpers)
  const now = useNow(running.length > 0)
  // Only helpers at work (owner, 19 Nov).
  if (!running.length) return null
  const shown = running.slice(0, 3)
  const more = running.length - shown.length
  const openHelper = (key: string | null) => {
    const section = visibleSection(sessionId)
    if (!section) { setOpen({ key }); return }
    useChatHelpersStore.getState().select(sessionId, key)
    section.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  }
  const chip = 'inline-flex min-w-0 max-w-[20rem] shrink-0 items-center gap-1.5 rounded-full border border-border bg-background px-2.5 py-0.5 text-xs hover:border-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
  return <>
    <div data-testid="chat-agents-strip" className="flex items-center gap-1.5 overflow-x-auto px-3 pt-1.5 [scrollbar-width:none]">
      {shown.map(helper => {
        const detail = [helper.activity, elapsed(helper, now)].filter(Boolean).join(' · ')
        return <button key={helper.key} type="button" className={`${chip} hidden sm:inline-flex`} title={`${helperProviderName(helper.provider)}: ${helper.name}${detail ? ` · ${detail}` : ''}`} onClick={() => openHelper(helper.key)}>
          <StateDot state={helper.state} /><ProviderBadge provider={helper.provider} />
          <span className="min-w-[6rem] truncate font-medium">{helper.name}</span>
          {detail && <span className="min-w-0 max-w-[9rem] shrink-[2] truncate text-muted-foreground">· {detail}</span>}
        </button>
      })}
      {<button type="button" className={`${chip} sm:hidden`} onClick={() => openHelper(null)}><StateDot state="running" />{running.length} running</button>}
      {more > 0 && <button type="button" className={`${chip} hidden text-muted-foreground sm:inline-flex`} onClick={() => openHelper(null)} title="See every agent in this chat">+{more} more</button>}
    </div>
    {open && <ChatAgentsPanel sessionId={sessionId} helpers={helpers} initialKey={open.key} onClose={() => setOpen(null)} />}
  </>
}

const sectionStates = ['open', 'closed'] as const

/** Helpers still at work or waiting on the person; finished ones are not listed (owner, 19 Nov). */
function activeHelpers(list: ChatHelper[]): ChatHelper[] {
  const { running, finished } = orderChatHelpers(list)
  return [...running, ...finished.filter(item => item.state === 'waiting')]
}

/**
 * "Agents in this chat" in the side pane (#3245): only the helpers at work (or
 * waiting on the person), hidden when none are; a helper opens in place with
 * Back to the list.
 */
export function ChatAgentsSection({ sessionId }: { sessionId: string }) {
  const helpers = useChatHelpersStore(state => state.lists[sessionId])
  const selected = useChatHelpersStore(state => state.selected[sessionId] ?? null)
  const select = useChatHelpersStore(state => state.select)
  const [state, setState] = usePersistentTab('agentworks.chat-agents-section', 'open', sectionStates)
  const [conversation, setConversation] = useState<ChatHelper | null>(null)
  const list = useMemo(() => helpers ?? [], [helpers])
  const active = useMemo(() => activeHelpers(list), [list])
  const now = useNow(active.length > 0)
  useEffect(() => { if (selected !== null && state !== 'open') setState('open') }, [selected, state, setState])
  // A helper opened from a chip stays shown after it finishes, until Back.
  const helper = selected ? list.find(item => item.key === selected) : undefined
  if (!active.length && !helper && !conversation) return null
  const open = state === 'open'
  const runningCount = active.filter(item => item.state === 'running').length
  const waiting = active.length - runningCount
  const summary = [runningCount ? `${runningCount} running` : '', waiting ? `${waiting} need${waiting === 1 ? 's' : ''} you` : ''].filter(Boolean).join(' · ')
  const row = (item: ChatHelper) => {
    const line = item.state === 'running' ? item.activity || 'Working…' : item.state === 'failed' ? `Failed${item.result ? `: ${item.result}` : ''}` : item.result || ''
    return <button key={item.key} type="button" onClick={() => select(sessionId, item.key)} title={item.name}
      className="flex w-full min-w-0 items-start gap-2 rounded-lg py-1.5 pl-2 pr-2 text-left hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <span className="mt-1"><StateDot state={item.state} /></span>
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-center gap-1.5 text-xs"><ProviderBadge provider={item.provider} /><span className="truncate">{item.name}</span></span>
        {line && line !== item.name && <span className={`block truncate text-[11px] ${item.state === 'failed' ? 'text-destructive' : 'text-muted-foreground'}`}>{line}</span>}
      </span>
      <span className="shrink-0 text-[10px] text-muted-foreground">{item.state === 'running' ? elapsed(item, now) : item.endedAt ?? item.startedAt ? ago(item.endedAt ?? item.startedAt as number, now) : ''}</span>
    </button>
  }
  return <div data-chat-agents-section={sessionId} data-testid="chat-agents-section">
    <div className="flex items-center">
      <div className="min-w-0 flex-1">
        <WorkspaceContextRow icon={Bot} hint="The helper agents this chat has at work, in any coding app" onClick={() => { select(sessionId, null); setState(open ? 'closed' : 'open') }} detail={summary}>Agents in this chat</WorkspaceContextRow>
      </div>
      <button type="button" aria-expanded={open} aria-label={open ? 'Collapse agents' : 'Expand agents'} onClick={() => setState(open ? 'closed' : 'open')}
        className="flex h-8 w-6 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <ChevronRight className={`h-3 w-3 transition-transform ${open ? 'rotate-90' : ''}`} aria-hidden="true" />
      </button>
    </div>
    {open && !helper && <div className="mt-0.5 pl-3">
      {active.map(row)}
      {!active.length && <p className="px-2 py-1 text-[11px] text-muted-foreground">No agents running now.</p>}
    </div>}
    {open && helper && <div className="mt-0.5 rounded-lg border border-border bg-background/40">
      <div className="flex items-center gap-1 border-b border-border px-1 py-1">
        <button type="button" onClick={() => select(sessionId, null)} className="inline-flex items-center gap-0.5 rounded px-1 py-0.5 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground"><ChevronLeft className="h-3 w-3" aria-hidden="true" />All agents</button>
      </div>
      <p className="px-3 pt-2 text-xs font-medium" title={helper.name}>{helper.name}</p>
      <HelperDetail sessionId={sessionId} helper={helper} now={now} compact onOpen={() => setConversation(helper)} onAsked={() => select(sessionId, null)} />
    </div>}
    {conversation?.toolCallId && <SubagentChatPanel sessionId={sessionId} toolCallId={conversation.toolCallId} title={conversation.name} onClose={() => setConversation(null)} />}
  </div>
}

/** Every helper this chat started, from any coding app, beside the chat (#3245). */
export function ChatAgentsPanel({ sessionId, helpers, initialKey, onClose }: { sessionId: string; helpers: ChatHelper[]; initialKey: string | null; onClose: () => void }) {
  const [selected, setSelected] = useState<string | null>(initialKey)
  const [conversation, setConversation] = useState<ChatHelper | null>(null)
  const active = useMemo(() => activeHelpers(helpers), [helpers])
  const now = useNow(active.length > 0)
  const helper = selected ? helpers.find(item => item.key === selected) : undefined

  useEffect(() => {
    const close = (event: KeyboardEvent) => { if (event.key === 'Escape' && !conversation) onClose() }
    window.addEventListener('keydown', close)
    return () => window.removeEventListener('keydown', close)
  }, [onClose, conversation])

  const row = (item: ChatHelper) => {
    const line = item.state === 'running' ? item.activity || 'Working…' : item.state === 'failed' ? `Failed${item.result ? `: ${item.result}` : ''}` : item.result || item.task
    return <button key={item.key} type="button" onClick={() => setSelected(item.key)} className="flex w-full items-start gap-2.5 px-4 py-2 text-left hover:bg-muted focus-visible:bg-muted focus-visible:outline-none">
      <span className="mt-1.5"><StateDot state={item.state} /></span>
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5 text-sm font-medium"><ProviderBadge provider={item.provider} /><span className="truncate" title={item.name}>{item.name}</span></span>
        {line && line !== item.name && <span className={`block truncate text-xs ${item.state === 'failed' ? 'text-destructive' : 'text-muted-foreground'}`}>{line}</span>}
      </span>
      <span className="shrink-0 text-[11px] text-muted-foreground">{item.state === 'running' ? elapsed(item, now) : item.startedAt ? ago(item.endedAt ?? item.startedAt, now) : ''}</span>
    </button>
  }

  return <ModalPortal>
    <aside role="dialog" aria-label="Agents in this chat" data-testid="chat-agents-panel" className="fixed inset-y-0 right-0 z-50 flex w-[min(24rem,100vw)] flex-col border-l border-border bg-background shadow-xl">
      <header className="flex shrink-0 items-center gap-2 border-b border-border px-3 py-2.5">
        {helper && <button type="button" aria-label="Back to all agents" onClick={() => setSelected(null)} className="rounded p-1 text-muted-foreground hover:bg-muted"><ChevronLeft className="h-4 w-4" /></button>}
        <h2 className="min-w-0 flex-1 truncate text-sm font-medium" title={helper?.name}>{helper ? helper.name : 'Agents in this chat'}</h2>
        <button type="button" aria-label="Close" onClick={onClose} className="rounded p-1 text-muted-foreground hover:bg-muted"><X className="h-4 w-4" /></button>
      </header>
      {!helper && <div className="min-h-0 flex-1 overflow-y-auto py-2">
        {!active.length && <p className="px-6 py-8 text-center text-sm text-muted-foreground">No agents running now. Ask the agent to split the work, for example "have Codex write the tests while you build the page".</p>}
        {active.map(row)}
      </div>}
      {helper && <HelperDetail sessionId={sessionId} helper={helper} now={now} onOpen={() => setConversation(helper)} onAsked={onClose} />}
    </aside>
    {conversation?.toolCallId && <SubagentChatPanel sessionId={sessionId} toolCallId={conversation.toolCallId} title={conversation.name} onClose={() => setConversation(null)} />}
  </ModalPortal>
}

/** Opens the other tab a handed-off task went to. */
function openLinkedChat(key: string) {
  const tab = Object.values(useChatStore.getState().chatTabs).find(item => item.metadata?.agentProfileConversationKey === key)
  if (tab) { openAgentTab(tab.tabId); return }
  const [projectId, agent] = key.split(':chat:')
  if (projectId && agent) openAppProduct({ surface: 'work', id: projectId, agent })
}

function HelperDetail({ sessionId, helper, now, compact = false, onOpen, onAsked }: { sessionId: string; helper: ChatHelper; now: number; compact?: boolean; onOpen: () => void; onAsked: () => void }) {
  const [stopping, setStopping] = useState(false)
  const time = elapsed(helper, now)
  const linked = helper.linkedChat
  // A handed-off task stops by stopping the other tab's turn; a Claude helper by its own task id.
  const canStop = helper.state === 'running' && (linked ? !!linked.sessionId && linked.running : !!helper.stopId)
  const canOpen = !!helper.toolCallId || !!linked
  const stop = async () => {
    if (!canStop || stopping) return
    setStopping(true)
    try {
      if (linked?.sessionId) await agentApi.cancelCurrentTurn(linked.sessionId)
      else if (helper.stopId) await agentApi.stopSessionBackgroundWork(sessionId, helper.stopId)
    } catch {
      useChatStore.getState().addToast(`Could not stop "${helper.name}".`, 'error')
    } finally { setStopping(false) }
  }
  const open = () => { if (linked) openLinkedChat(linked.key); else onOpen() }
  const ask = () => {
    startChatMessage(sessionId, `About the helper "${helper.name}"${linked ? ` (the task you handed to "${linked.agent}")` : helper.delegated ? ` (${helperProviderName(helper.provider)} job${helper.jobId ? ` ${helper.jobId}` : ''})` : ''}: `)
    onAsked()
  }
  const primary = 'rounded-md bg-primary px-2.5 py-1 text-xs text-primary-foreground hover:opacity-90'
  const secondary = 'rounded-md border border-border px-2.5 py-1 text-xs hover:bg-muted'
  return <>
    <div className={compact ? 'max-h-80 overflow-y-auto px-3 py-2 text-sm' : 'min-h-0 flex-1 overflow-y-auto px-4 py-3 text-sm'}>
      <dl className="grid grid-cols-[5rem_1fr] gap-x-3 gap-y-1.5 text-xs">
        <dt className="text-muted-foreground">App</dt>
        <dd className="flex flex-wrap items-center gap-1.5"><ProviderBadge provider={helper.provider} />{helper.model && <span className="text-muted-foreground">{helper.model}</span>}{helper.delegated && <span className="text-muted-foreground">· handed over as a job</span>}{linked && <span className="text-muted-foreground">· in the tab "{linked.agent}"</span>}</dd>
        <dt className="text-muted-foreground">Status</dt>
        <dd className="flex items-center gap-1.5"><StateDot state={helper.state} />{stateWord(helper.state)}{helper.state === 'running' && helper.activity ? ` · ${helper.activity}` : ''}</dd>
        {(time || helper.startedAt) && <><dt className="text-muted-foreground">Time</dt>
          <dd>{[time, helper.state !== 'running' && helper.startedAt ? `started ${ago(helper.startedAt, now)}` : ''].filter(Boolean).join(' · ')}</dd></>}
      </dl>
      {helper.task && <><h3 className="mb-1 mt-4 text-xs font-medium">Task</h3><p className="whitespace-pre-wrap break-words text-[13px]">{helper.task}</p></>}
      {helper.result && <><h3 className="mb-1 mt-4 text-xs font-medium">{helper.state === 'failed' ? 'What went wrong' : helper.state === 'waiting' ? 'What it needs' : 'Result'}</h3>
        <p className={`whitespace-pre-wrap break-words text-[13px] ${helper.state === 'failed' ? 'text-destructive' : ''}`}>{helper.result}</p></>}
      {!canOpen && <p className="mt-4 rounded-md bg-muted px-3 py-2 text-xs text-muted-foreground">
        {helper.delegated ? `${helperProviderName(helper.provider)} ran this as a separate job, so only its task, progress and result show here.` : "This helper's conversation can't be opened. You can still ask the agent what it found."}
      </p>}
      {helper.state === 'running' && !canStop && helper.provider === 'cursor-cli' && <p className="mt-2 text-[11px] text-muted-foreground">{helperProviderName(helper.provider)} can't stop one helper on its own. Stop the chat to stop it.</p>}
    </div>
    <footer className={`flex shrink-0 flex-wrap items-center gap-2 border-t border-border ${compact ? 'px-3 py-2' : 'px-4 py-2.5'}`}>
      {canOpen && <button type="button" onClick={open} className={primary}>{linked ? 'Open its tab' : 'Open its conversation'}</button>}
      <button type="button" onClick={ask} className={canOpen ? secondary : primary}>Ask the agent about it</button>
      {canStop && <button type="button" disabled={stopping} onClick={() => void stop()} className={`ml-auto ${secondary} disabled:opacity-60`}>{stopping ? 'Stopping…' : 'Stop'}</button>}
    </footer>
  </>
}
