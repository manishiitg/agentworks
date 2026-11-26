import { useEffect, useMemo, useState } from 'react'
import { Loader2, Square, X } from 'lucide-react'
import type { PollingEvent } from '../../services/api-types'
import { agentApi } from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { getWorkspaceScopedStorageKey } from '../../stores/useWorkspaceConnectionStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { collectChatHelpers, helperProviderName, linkedChatHelpers, mergeHelperEvents, orderChatHelpers, type ChatHelper, type ChatHelperState, type ChatLinkedHelper } from '../../utils/chatHelpers'
import { openAgentTab } from '../../utils/openAgentTab'
import { openAppProduct } from '../../utils/appNavigation'
import { SubagentChatPanel } from '../events/SubagentChatPanel'
import { useSessionBackgroundWork } from '../../hooks/useSessionBackgroundWork'
import { backgroundWorkLabel } from '../BackgroundWorkPill'
import { openStepInspection } from '../../utils/stepInspection'
import type { SessionBackgroundWorkItem } from '../../services/api-types'

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

const MAX_HIDDEN_SESSIONS = 50
const legacyHiddenKey = (sessionId: string) => `agentworks.chat-agents.hidden.${sessionId}`
const hiddenMapKey = () => getWorkspaceScopedStorageKey('agentworks.chat-agents.hidden')

function readHiddenMap(): Record<string, string[]> {
  try {
    const raw = JSON.parse(localStorage.getItem(hiddenMapKey()) || '{}')
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return {}
    return raw as Record<string, string[]>
  } catch { return {} }
}

/** Helpers the person removed from the list with ×, kept per chat in this browser (bounded & workspace-scoped). */
function readHidden(sessionId: string): Set<string> {
  try {
    const map = readHiddenMap()
    const scoped = map[sessionId]
    if (Array.isArray(scoped)) return new Set(scoped.filter((item): item is string => typeof item === 'string'))
    const legacy = JSON.parse(localStorage.getItem(legacyHiddenKey(sessionId)) || '[]')
    return new Set(Array.isArray(legacy) ? legacy.filter((item): item is string => typeof item === 'string') : [])
  } catch { return new Set() }
}

function writeHidden(sessionId: string, next: Set<string>): void {
  try {
    const map = readHiddenMap()
    delete map[sessionId]
    map[sessionId] = [...next].slice(-200)
    const keys = Object.keys(map)
    while (keys.length > MAX_HIDDEN_SESSIONS) {
      const oldest = keys.shift()
      if (oldest) delete map[oldest]
    }
    localStorage.setItem(hiddenMapKey(), JSON.stringify(map))
    localStorage.removeItem(legacyHiddenKey(sessionId))
  } catch { /* private mode: hidden for this view only */ }
}

/** Lines shown before "+N more". */
const STRIP_SHOWN = 3

// Clock skew between the app and the agent's own times.
const TURN_SLACK_MS = 5_000

function inCurrentTurn(helper: ChatHelper, turnStartedAt?: number): boolean {
  if (turnStartedAt === undefined) return false
  return helper.startedAt === undefined || helper.startedAt >= turnStartedAt - TURN_SLACK_MS
}

/**
 * Above the message box (#3245): one line per helper at work, shown in full
 * with its actions on the line, open its conversation (or its tab) and stop
 * (owner, 19 Nov). Finished helpers are not shown.
 */
export function ChatAgentsStrip({ sessionId, tabId, helpers, turnStartedAt }: { sessionId: string; tabId?: string; helpers: ChatHelper[]; turnStartedAt?: number }) {
  // Everything else running for this chat (background commands, workflow runs, steps, calls) sits here too, so it
  // is in one place with its Stop (owner, 25 Nov: no "N running" in the menu). A helper already listed is not repeated.
  const work = useSessionBackgroundWork(tabId, true)
  const [all, setAll] = useState(false)
  const [conversation, setConversation] = useState<ChatHelper | null>(null)
  const [hidden, setHidden] = useState<Set<string>>(() => readHidden(sessionId))
  // A helper inside the chat's turn (no stop of its own) cannot outlive the
  // turn: one still marked running from an earlier turn lost its finish (an
  // app restart mid-run). Show it only while the turn it started in is the
  // current one, so sending a new message does not bring old ones back (owner, 19 Nov).
  const active = useMemo(() => activeHelpers(helpers).filter(helper => !hidden.has(helper.key)
    && (!helper.key.startsWith('native:') || !!helper.stopId || inCurrentTurn(helper, turnStartedAt))), [helpers, hidden, turnStartedAt])
  const now = useNow(active.length > 0)
  useEffect(() => { setAll(false); setConversation(null); setHidden(readHidden(sessionId)) }, [sessionId])
  const hide = (key: string) => setHidden(previous => {
    const next = new Set(previous).add(key)
    writeHidden(sessionId, next)
    return next
  })
  const helperStops = new Set(helpers.map(helper => helper.stopId).filter(Boolean))
  const others = work.items.filter(item => !helperStops.has(item.id) && !hidden.has(`work:${item.id}`))
  if (!active.length && !others.length && !conversation) return null
  const shown = all ? active : active.slice(0, STRIP_SHOWN)
  const more = active.length - shown.length
  return <>
    {(active.length > 0 || others.length > 0) && <div data-testid="chat-agents-strip" className="max-h-48 space-y-1 overflow-y-auto px-3 pt-1.5">
      {shown.map(helper => <HelperLine key={helper.key} sessionId={sessionId} helper={helper} now={now} onOpen={() => setConversation(helper)} onHide={() => hide(helper.key)} />)}
      {others.map(item => <WorkLine key={item.id} item={item} now={work.now} stopping={work.isStopping(item)} onStop={() => void work.stopItem(item)} onHide={() => hide(`work:${item.id}`)} />)}
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

function HelperLine({ sessionId, helper, now, onOpen, onHide }: { sessionId: string; helper: ChatHelper; now: number; onOpen: () => void; onHide: () => void }) {
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
    <span className="ml-auto flex shrink-0 items-center gap-1.5">
      {canOpen && <button type="button" onClick={open} className={action}>{linked ? 'Open its tab' : 'Open conversation'}</button>}
      {canStop && <button type="button" disabled={stopping} onClick={() => void stop()} className={action}>{stopping ? 'Stopping…' : 'Stop'}</button>}
      <button type="button" onClick={onHide} aria-label={`Remove "${helper.name}" from this list`} title="Remove from this list (does not stop it)" className="rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><X className="h-3.5 w-3.5" aria-hidden="true" /></button>
    </span>
  </div>
}

/** One line for other background work (a background command, a workflow run, a step), the same shape as a helper's. */
function WorkLine({ item, now, stopping, onStop, onHide }: { item: SessionBackgroundWorkItem; now: number; stopping: boolean; onStop: () => void; onHide: () => void }) {
  const label = backgroundWorkLabel(item)
  const started = Date.parse(item.started_at)
  const seconds = Number.isFinite(started) && !item.started_at.startsWith('0001-') ? Math.max(0, Math.floor((now - started) / 1000)) : -1
  const time = seconds < 0 ? '' : seconds < 60 ? `${seconds}s` : seconds < 3600 ? `${Math.floor(seconds / 60)}m ${seconds % 60}s` : `${Math.floor(seconds / 3600)}h ${Math.floor(seconds / 60) % 60}m`
  // Only the name and how long it has run: a background command's status is its command text, which goes in the
  // hover text with the full label.
  const detail = time
  const hover = [label, item.status].filter(Boolean).join('\n')
  const action = 'rounded-md border border-border px-2 py-0.5 text-[11px] hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60'
  // One line, like a helper's: a background command's label can be the whole command (a long heredoc filled the
  // chat). The full text is on hover; Inspect opens the details.
  return <div data-testid="chat-work-line" title={hover} className="flex min-w-0 items-center gap-2 rounded-lg border border-border bg-background px-2.5 py-1 text-xs">
    <StateDot state="running" />
    <span className="min-w-0 flex-1 truncate font-medium">{item.label.replace(/\s+/g, ' ')}</span>
    {detail && <span className="shrink-0 text-muted-foreground">· {detail}</span>}
    <span className="ml-auto flex shrink-0 items-center gap-1.5">
      {item.inspection && <button type="button" onClick={() => openStepInspection(item.inspection!)} className={action}>Inspect</button>}
      {item.can_stop && <button type="button" disabled={stopping} onClick={onStop} aria-label={`Stop ${label}`} className={`${action} inline-flex items-center gap-1`}>
        {stopping ? <Loader2 className="h-3 w-3 animate-spin" aria-hidden="true" /> : <Square className="h-2.5 w-2.5" fill="currentColor" aria-hidden="true" />}{stopping ? 'Stopping…' : 'Stop'}
      </button>}
      <button type="button" onClick={onHide} aria-label={`Remove "${label}" from this list`} title="Remove from this list (does not stop it)" className="rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><X className="h-3.5 w-3.5" aria-hidden="true" /></button>
    </span>
  </div>
}
