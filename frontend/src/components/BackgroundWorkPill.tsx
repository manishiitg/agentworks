import { useCallback, useEffect, useRef, useState } from 'react'
import { Loader2, Square } from 'lucide-react'
import { agentApi } from '../services/api'
import type { SessionBackgroundWorkItem } from '../services/api-types'
import { useChatStore } from '../stores/useChatStore'
import { runtimeHasBackgroundAgents } from '../utils/runtimeActivity'

const POLL_MS = 5_000
const POLL_OPEN_MS = 3_000

const KIND_PREFIX: Record<SessionBackgroundWorkItem['kind'], string> = {
  step: 'Step',
  workflow_run: 'Workflow run',
  sub_agent: 'Sub-agent',
  crew_call: 'Call',
}

export function backgroundWorkLabel(item: SessionBackgroundWorkItem): string {
  return `${KIND_PREFIX[item.kind] ?? 'Background'}: ${item.label}`
}

function elapsed(startedAt: string, now: number): string {
  const started = Date.parse(startedAt)
  if (!Number.isFinite(started)) return ''
  const seconds = Math.max(0, Math.floor((now - started) / 1000))
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`
}

// The item's start card in this chat's transcript, when it is rendered.
function transcriptCard(itemId: string): Element | null {
  if (typeof document === 'undefined') return null
  const escaped = typeof CSS !== 'undefined' && CSS.escape ? CSS.escape(itemId) : itemId.replace(/"/g, '\\"')
  return document.querySelector(`[data-background-agent-id="${escaped}"]`)
}

// "N running" in the composer (PLAT-705): background work this chat started —
// steps, workflow runs, sub-agents, calls to other chats or Crews. Each item
// shows what it is doing now and has its own Stop, which ends only that item.
// The composer Stop only interrupts the CLI's current turn.
export function BackgroundWorkPill({ tabId }: { tabId: string }) {
  const sessionId = useChatStore(state => state.chatTabs[tabId]?.sessionId ?? null)
  const tabHasBg = useChatStore(state => state.chatTabs[tabId]?.hasRunningBgAgents ?? false)
  const sessionHasBg = useChatStore(state => sessionId
    ? runtimeHasBackgroundAgents(state.activeSessionsCache.find(item => item.session_id === sessionId))
    : false)
  const active = !!sessionId && (tabHasBg || sessionHasBg)
  const [items, setItems] = useState<SessionBackgroundWorkItem[]>([])
  const [open, setOpen] = useState(false)
  const [stopping, setStopping] = useState<Record<string, boolean>>({})
  const [now, setNow] = useState(() => Date.now())
  const rootRef = useRef<HTMLDivElement>(null)

  const refresh = useCallback(async () => {
    if (!sessionId) return
    try {
      const response = await agentApi.getSessionBackgroundWork(sessionId)
      setItems(response.items ?? [])
    } catch {
      setItems([])
    }
  }, [sessionId])

  // Poll only while the chat has background work; faster while the list is open.
  useEffect(() => {
    if (!active) { setItems([]); setOpen(false); return }
    void refresh()
    const timer = setInterval(() => { void refresh() }, open ? POLL_OPEN_MS : POLL_MS)
    return () => clearInterval(timer)
  }, [active, open, refresh])

  useEffect(() => {
    if (!open) return
    setNow(Date.now())
    const timer = setInterval(() => setNow(Date.now()), 1000)
    const onPointer = (event: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(event.target as Node)) setOpen(false)
    }
    const onKey = (event: KeyboardEvent) => { if (event.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', onPointer)
    document.addEventListener('keydown', onKey)
    return () => {
      clearInterval(timer)
      document.removeEventListener('mousedown', onPointer)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  if (!active || items.length === 0) return null

  const stopItem = async (item: SessionBackgroundWorkItem) => {
    if (!sessionId || stopping[item.id]) return
    setStopping(prev => ({ ...prev, [item.id]: true }))
    try {
      await agentApi.stopSessionBackgroundWork(sessionId, item.id)
      setItems(prev => prev.filter(existing => existing.id !== item.id))
    } catch (error) {
      console.error('[BackgroundWorkPill] Failed to stop background work:', error)
      useChatStore.getState().addToast(`Could not stop ${backgroundWorkLabel(item)}.`, 'error')
    } finally {
      setStopping(prev => { const next = { ...prev }; delete next[item.id]; return next })
      void refresh()
    }
  }

  return (
    <div ref={rootRef} className="relative shrink-0">
      <button
        type="button"
        onClick={() => setOpen(value => !value)}
        className="inline-flex h-7 items-center gap-1.5 rounded-full border border-border bg-muted/60 px-2.5 text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground"
        aria-haspopup="dialog"
        aria-expanded={open}
        data-testid="background-work-pill"
        title="Background work started from this chat"
      >
        <span className="inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-blue-500" />
        {items.length} running
      </button>
      {open && (
        <div
          role="dialog"
          aria-label="Background work"
          className="absolute bottom-full right-0 z-50 mb-2 w-[min(22rem,calc(100vw-2rem))] rounded-md border border-border bg-popover p-1 text-popover-foreground shadow-lg"
          data-testid="background-work-list"
        >
          <ul className="max-h-72 overflow-y-auto">
            {items.map(item => {
              const card = transcriptCard(item.id)
              return (
                <li key={item.id} className="flex items-start gap-2 rounded px-2 py-1.5 hover:bg-muted/60" data-testid="background-work-item">
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-xs font-medium" title={backgroundWorkLabel(item)}>{backgroundWorkLabel(item)}</div>
                    {item.status && (
                      <div className="truncate text-[11px] text-muted-foreground" title={item.status} data-testid="background-work-status">{item.status}</div>
                    )}
                    <div className="flex items-center gap-2 text-[10px] text-muted-foreground">
                      <span className="font-mono">{elapsed(item.started_at, now)}</span>
                      {card && (
                        <button
                          type="button"
                          className="underline-offset-2 hover:text-foreground hover:underline"
                          onClick={() => { card.scrollIntoView({ behavior: 'smooth', block: 'center' }); setOpen(false) }}
                        >
                          Open
                        </button>
                      )}
                    </div>
                  </div>
                  {item.can_stop && (
                    <button
                      type="button"
                      onClick={() => void stopItem(item)}
                      disabled={!!stopping[item.id]}
                      className="mt-0.5 inline-flex h-6 shrink-0 items-center gap-1 rounded px-1.5 text-[11px] text-muted-foreground hover:bg-destructive/10 hover:text-destructive disabled:opacity-60"
                      aria-label={`Stop ${backgroundWorkLabel(item)}`}
                      data-testid="background-work-stop"
                    >
                      {stopping[item.id]
                        ? <Loader2 className="h-3 w-3 animate-spin" />
                        : <Square className="h-3 w-3" fill="currentColor" />}
                      Stop
                    </button>
                  )}
                </li>
              )
            })}
          </ul>
        </div>
      )}
    </div>
  )
}
