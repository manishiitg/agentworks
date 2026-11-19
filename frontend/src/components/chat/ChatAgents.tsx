import { useEffect, useMemo, useState } from 'react'
import type { PollingEvent } from '../../services/api-types'
import { agentApi } from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { collectChatHelpers, helperProviderName, linkedChatHelpers, mergeHelperEvents, orderChatHelpers, type ChatHelper, type ChatHelperState, type ChatLinkedHelper } from '../../utils/chatHelpers'
import { openAgentTab } from '../../utils/openAgentTab'
import { openAppProduct } from '../../utils/appNavigation'
import { SubagentChatPanel } from '../events/SubagentChatPanel'

/** Codex helpers whose state the server is asked for, newest first. */
const CODEX_STATE_LIMIT = 8

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

/**
 * The chat's helpers with each Codex helper's state read from the server: a
 * Codex spawn step says nothing about whether its helper still runs (#3245).
 */
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
  return helpers
}

function ProviderBadge({ provider }: { provider: string }) {
  return <span className="inline-flex h-4 shrink-0 items-center rounded border border-border bg-muted px-1.5 text-[10px] font-semibold text-foreground">{helperProviderName(provider)}</span>
}
function StateDot({ state }: { state: ChatHelperState }) {
  const color = state === 'running' ? 'animate-pulse bg-primary' : state === 'done' ? 'bg-success' : state === 'failed' ? 'bg-destructive' : 'bg-muted-foreground/50'
  return <span className={`h-2 w-2 shrink-0 rounded-full ${color}`} aria-hidden="true" />
}

function duration(ms: number): string {
  const seconds = Math.max(0, Math.floor(ms / 1000))
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  return minutes < 60 ? `${minutes}m ${String(seconds % 60).padStart(2, '0')}s` : `${Math.floor(minutes / 60)}h ${minutes % 60}m`
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

/** Helpers still at work or waiting on the person; finished ones are not shown (owner, 19 Nov). */
function activeHelpers(list: ChatHelper[]): ChatHelper[] {
  const { running, finished } = orderChatHelpers(list)
  return [...running, ...finished.filter(item => item.state === 'waiting')]
}

/** Lines shown before "+N more". */
const STRIP_SHOWN = 3

/**
 * Above the message box (#3245): one line per helper at work, shown in full
 * with its actions on the line, open its conversation (or its tab) and stop
 * (owner, 19 Nov). Finished helpers are not shown.
 */
export function ChatAgentsStrip({ sessionId, helpers }: { sessionId: string; helpers: ChatHelper[] }) {
  const [all, setAll] = useState(false)
  const [conversation, setConversation] = useState<ChatHelper | null>(null)
  const active = useMemo(() => activeHelpers(helpers), [helpers])
  const now = useNow(active.length > 0)
  useEffect(() => { setAll(false); setConversation(null) }, [sessionId])
  if (!active.length && !conversation) return null
  const shown = all ? active : active.slice(0, STRIP_SHOWN)
  const more = active.length - shown.length
  return <>
    {active.length > 0 && <div data-testid="chat-agents-strip" className="max-h-48 space-y-1 overflow-y-auto px-3 pt-1.5">
      {shown.map(helper => <HelperLine key={helper.key} sessionId={sessionId} helper={helper} now={now} onOpen={() => setConversation(helper)} />)}
      {(more > 0 || all) && active.length > STRIP_SHOWN && <button type="button" onClick={() => setAll(!all)} className="px-1 text-[11px] text-muted-foreground hover:text-foreground">{all ? 'Show fewer' : `+${more} more`}</button>}
    </div>}
    {conversation?.toolCallId && <SubagentChatPanel sessionId={sessionId} toolCallId={conversation.toolCallId} title={conversation.name} onClose={() => setConversation(null)} />}
  </>
}

/** Opens the other tab a handed-off task went to. */
function openLinkedChat(key: string) {
  const tab = Object.values(useChatStore.getState().chatTabs).find(item => item.metadata?.agentProfileConversationKey === key)
  if (tab) { openAgentTab(tab.tabId); return }
  const [projectId, agent] = key.split(':chat:')
  if (projectId && agent) openAppProduct({ surface: 'work', id: projectId, agent })
}

function HelperLine({ sessionId, helper, now, onOpen }: { sessionId: string; helper: ChatHelper; now: number; onOpen: () => void }) {
  const [stopping, setStopping] = useState(false)
  const linked = helper.linkedChat
  // A handed-off task stops by stopping the other tab's turn; a helper by its own stop id.
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
  // The activity often repeats the task name; show it only when it says something new.
  const activity = helper.activity && helper.activity.trim().toLowerCase() !== helper.name.trim().toLowerCase() ? helper.activity : ''
  const detail = [helper.state === 'waiting' ? 'Needs you' : activity, linked ? `in the tab "${linked.agent}"` : '', elapsed(helper, now)].filter(Boolean).join(' · ')
  const note = helper.state === 'running' && !canStop && helper.provider === 'cursor-cli' ? `${helperProviderName(helper.provider)} can't stop one helper on its own. Stop the chat to stop it.` : undefined
  const action = 'rounded-md border border-border px-2 py-0.5 text-[11px] hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60'
  return <div data-testid="chat-agent-line" title={note} className="flex flex-wrap items-center gap-x-2 gap-y-1 rounded-lg border border-border bg-background px-2.5 py-1 text-xs">
    <StateDot state={helper.state} /><ProviderBadge provider={helper.provider} />
    <span className="min-w-0 break-words font-medium">{helper.name}</span>
    {detail && <span className="min-w-0 break-words text-muted-foreground">· {detail}</span>}
    {(canOpen || canStop) && <span className="ml-auto flex shrink-0 items-center gap-1.5">
      {canOpen && <button type="button" onClick={open} className={action}>{linked ? 'Open its tab' : 'Open conversation'}</button>}
      {canStop && <button type="button" disabled={stopping} onClick={() => void stop()} className={action}>{stopping ? 'Stopping…' : 'Stop'}</button>}
    </span>}
  </div>
}
