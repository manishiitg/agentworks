import { useEffect, useMemo, useState } from 'react'
import { ChevronLeft, X } from 'lucide-react'
import type { PollingEvent } from '../../services/api-types'
import { agentApi } from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { collectChatHelpers, helperProviderName, orderChatHelpers, type ChatHelper, type ChatHelperState } from '../../utils/chatHelpers'
import { startChatMessage } from '../../utils/startChatMessage'
import { SubagentChatPanel } from '../events/SubagentChatPanel'
import ModalPortal from '../ui/ModalPortal'

/** Codex helpers whose state the server is asked for, newest first. */
const CODEX_STATE_LIMIT = 8

/**
 * The chat's helpers with each Codex helper's state read from the server: a
 * Codex spawn step says nothing about whether its helper still runs (#3245).
 */
export function useChatHelpers(sessionId: string | undefined, events: PollingEvent[]): ChatHelper[] {
  const collected = useMemo(() => collectChatHelpers(events), [events])
  const [codex, setCodex] = useState<Record<string, ChatHelperState>>({})
  const askFor = useMemo(() => collected.filter(helper => helper.stateFromServer && helper.toolCallId)
    .slice(-CODEX_STATE_LIMIT).map(helper => helper.toolCallId as string), [collected])
  const pending = askFor.filter(id => codex[id] === undefined || codex[id] === 'running')
  const pendingKey = pending.join('|')

  useEffect(() => { setCodex({}) }, [sessionId])
  useEffect(() => {
    if (!sessionId || !pendingKey) return
    const identity = captureChatIdentity()
    let disposed = false
    const ids = pendingKey.split('|')
    const load = async () => {
      const next: Record<string, ChatHelperState> = {}
      await Promise.all(ids.map(async id => {
        try {
          const found = await agentApi.getSessionSubagent(sessionId, id)
          next[id] = found.running ? 'running' : found.transcript?.status === 'failed' ? 'failed' : 'done'
        } catch (failure) {
          // Gone (404): its conversation was cleared; keep it listed without a state.
          if ((failure as { response?: { status?: number } }).response?.status === 404) next[id] = 'unknown'
        }
      }))
      if (!disposed && isChatIdentityCurrent(identity)) setCodex(previous => ({ ...previous, ...next }))
    }
    void load()
    const timer = setInterval(() => { void load() }, 5000)
    return () => { disposed = true; clearInterval(timer) }
  }, [sessionId, pendingKey])

  return useMemo(() => collected.map(helper => helper.stateFromServer && helper.toolCallId && codex[helper.toolCallId] && codex[helper.toolCallId] !== helper.state
    ? { ...helper, state: codex[helper.toolCallId] } : helper), [collected, codex])
}

function ProviderBadge({ provider }: { provider: string }) {
  return <span className="inline-flex h-4 shrink-0 items-center rounded border border-border bg-muted px-1.5 text-[10px] font-semibold text-foreground">{helperProviderName(provider)}</span>
}
function StateDot({ state }: { state: ChatHelperState }) {
  const color = state === 'running' ? 'animate-pulse bg-primary' : state === 'done' ? 'bg-success' : state === 'failed' ? 'bg-destructive' : 'bg-muted-foreground/50'
  return <span className={`h-2 w-2 shrink-0 rounded-full ${color}`} aria-hidden="true" />
}

const stateWord = (state: ChatHelperState) => state === 'running' ? 'Running' : state === 'done' ? 'Done' : state === 'failed' ? 'Failed' : state === 'stopped' ? 'Stopped' : 'Finished or unknown'
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

/** One chip per helper running now, above the message box; a small "Agents" chip when none run (#3245). */
export function ChatAgentsStrip({ sessionId, helpers }: { sessionId: string; helpers: ChatHelper[] }) {
  const [open, setOpen] = useState<{ key: string | null } | null>(null)
  const { running } = orderChatHelpers(helpers)
  const now = useNow(running.length > 0)
  if (!helpers.length) return null
  const shown = running.slice(0, 3)
  const more = running.length - shown.length
  const chip = 'inline-flex max-w-[16rem] shrink-0 items-center gap-1.5 rounded-full border border-border bg-background px-2.5 py-0.5 text-xs hover:border-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
  return <>
    <div data-testid="chat-agents-strip" className="flex items-center gap-1.5 overflow-x-auto px-3 pt-1.5 [scrollbar-width:none]">
      {shown.map(helper => <button key={helper.key} type="button" className={`${chip} hidden sm:inline-flex`} title={`${helper.name}${helper.activity ? `: ${helper.activity}` : ''}`} onClick={() => setOpen({ key: helper.key })}>
        <StateDot state="running" /><ProviderBadge provider={helper.provider} />
        <span className="truncate text-muted-foreground">{helper.activity || helper.name}</span>
        {elapsed(helper, now) && <span className="shrink-0 text-muted-foreground">· {elapsed(helper, now)}</span>}
      </button>)}
      {running.length > 0 && <button type="button" className={`${chip} sm:hidden`} onClick={() => setOpen({ key: null })}><StateDot state="running" />{running.length} running</button>}
      <button type="button" className={`${chip} font-medium ${running.length ? 'hidden sm:inline-flex' : ''}`} onClick={() => setOpen({ key: null })} title="See every agent in this chat">
        {more > 0 ? `+${more} more` : running.length ? 'All agents' : `Agents (${helpers.length})`}
      </button>
    </div>
    {open && <ChatAgentsPanel sessionId={sessionId} helpers={helpers} initialKey={open.key} onClose={() => setOpen(null)} />}
  </>
}

/** Every helper this chat started, from any coding app, beside the chat (#3245). */
export function ChatAgentsPanel({ sessionId, helpers, initialKey, onClose }: { sessionId: string; helpers: ChatHelper[]; initialKey: string | null; onClose: () => void }) {
  const [selected, setSelected] = useState<string | null>(initialKey)
  const [conversation, setConversation] = useState<ChatHelper | null>(null)
  const { running, finished } = orderChatHelpers(helpers)
  const now = useNow(running.length > 0)
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
        {!helpers.length && <p className="px-6 py-8 text-center text-sm text-muted-foreground">No helpers in this chat yet. Ask the agent to split the work, for example "have Codex write the tests while you build the page".</p>}
        {running.length > 0 && <p className="px-4 pb-1 pt-2 text-[11px] uppercase tracking-wide text-muted-foreground">Running now · {running.length}</p>}
        {running.map(row)}
        {finished.length > 0 && <p className="px-4 pb-1 pt-3 text-[11px] uppercase tracking-wide text-muted-foreground">Finished · {finished.length}</p>}
        {finished.map(row)}
      </div>}
      {helper && <HelperDetail sessionId={sessionId} helper={helper} now={now} onOpen={() => setConversation(helper)} onAsked={onClose} />}
    </aside>
    {conversation?.toolCallId && <SubagentChatPanel sessionId={sessionId} toolCallId={conversation.toolCallId} title={conversation.name} onClose={() => setConversation(null)} />}
  </ModalPortal>
}

function HelperDetail({ sessionId, helper, now, onOpen, onAsked }: { sessionId: string; helper: ChatHelper; now: number; onOpen: () => void; onAsked: () => void }) {
  const [stopping, setStopping] = useState(false)
  const time = elapsed(helper, now)
  const stop = async () => {
    if (!helper.stopId || stopping) return
    setStopping(true)
    try { await agentApi.stopSessionBackgroundWork(sessionId, helper.stopId) } catch {
      useChatStore.getState().addToast(`Could not stop "${helper.name}".`, 'error')
    } finally { setStopping(false) }
  }
  const ask = () => {
    startChatMessage(sessionId, `About the helper "${helper.name}"${helper.delegated ? ` (${helperProviderName(helper.provider)} job${helper.jobId ? ` ${helper.jobId}` : ''})` : ''}: `)
    onAsked()
  }
  return <>
    <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3 text-sm">
      <dl className="grid grid-cols-[5rem_1fr] gap-x-3 gap-y-1.5 text-xs">
        <dt className="text-muted-foreground">App</dt>
        <dd className="flex items-center gap-1.5"><ProviderBadge provider={helper.provider} />{helper.model && <span className="text-muted-foreground">{helper.model}</span>}{helper.delegated && <span className="text-muted-foreground">· handed over as a job</span>}</dd>
        <dt className="text-muted-foreground">Status</dt>
        <dd className="flex items-center gap-1.5"><StateDot state={helper.state} />{stateWord(helper.state)}{helper.state === 'running' && helper.activity ? ` · ${helper.activity}` : ''}</dd>
        {(time || helper.startedAt) && <><dt className="text-muted-foreground">Time</dt>
          <dd>{[time, helper.state !== 'running' && helper.startedAt ? `started ${ago(helper.startedAt, now)}` : ''].filter(Boolean).join(' · ')}</dd></>}
      </dl>
      {helper.task && <><h3 className="mb-1 mt-4 text-xs font-medium">Task</h3><p className="whitespace-pre-wrap break-words text-[13px]">{helper.task}</p></>}
      {helper.result && <><h3 className="mb-1 mt-4 text-xs font-medium">{helper.state === 'failed' ? 'What went wrong' : 'Result'}</h3>
        <p className={`whitespace-pre-wrap break-words text-[13px] ${helper.state === 'failed' ? 'text-destructive' : ''}`}>{helper.result}</p></>}
      {!helper.toolCallId && <p className="mt-4 rounded-md bg-muted px-3 py-2 text-xs text-muted-foreground">
        {helper.delegated ? `${helperProviderName(helper.provider)} ran this as a separate job, so only its task, progress and result show here. Opening its full conversation is coming later.` : "This helper's conversation can't be opened. You can still ask the agent what it found."}
      </p>}
    </div>
    <footer className="flex shrink-0 items-center gap-2 border-t border-border px-4 py-2.5">
      {helper.toolCallId && <button type="button" onClick={onOpen} className="rounded-md bg-primary px-2.5 py-1 text-xs text-primary-foreground hover:opacity-90">Open its conversation</button>}
      <button type="button" onClick={ask} className={helper.toolCallId ? 'rounded-md border border-border px-2.5 py-1 text-xs hover:bg-muted' : 'rounded-md bg-primary px-2.5 py-1 text-xs text-primary-foreground hover:opacity-90'}>Ask the agent about it</button>
      {helper.state === 'running' && helper.stopId && <button type="button" disabled={stopping} onClick={() => void stop()} className="ml-auto rounded-md border border-border px-2.5 py-1 text-xs hover:bg-muted disabled:opacity-60">{stopping ? 'Stopping…' : 'Stop'}</button>}
    </footer>
  </>
}
