import { useEffect, useRef, useState, type ReactElement, type ReactNode } from 'react'
import { openStepInspection } from '../utils/stepInspection'
import { Loader2, Square } from 'lucide-react'
import type { SessionBackgroundWorkItem } from '../services/api-types'
import { useSessionBackgroundWork } from '../hooks/useSessionBackgroundWork'

const KIND_PREFIX: Record<SessionBackgroundWorkItem['kind'], string> = {
  step: 'Step',
  workflow_run: 'Workflow run',
  sub_agent: 'Sub-agent',
  crew_call: 'Call',
  native_helper: '',
  native_command: '',
  native_turn: '',
  claude_task: 'Started by Claude',
}

export function backgroundWorkLabel(item: SessionBackgroundWorkItem): string {
  const prefix = KIND_PREFIX[item.kind] ?? 'Background'
  return `${prefix ? `${prefix}: ` : ''}${item.label}`
}

function elapsed(startedAt: string, now: number): string {
	if (!startedAt || startedAt.startsWith('0001-')) return ''
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

// "N running" for view-only chats and run footers (PLAT-705): background work this chat started —
// steps, workflow runs, sub-agents, calls to other chats or Crews. Each item
// shows what it is doing now and has its own Stop, which ends only that item.
// Editable chats use these same rows inside the send-options menu (PLAT-791).
export function BackgroundWorkPill({ tabId }: { tabId: string }) {
  const [open, setOpen] = useState(false)
  const work = useSessionBackgroundWork(tabId, open)
  const rootRef = useRef<HTMLDivElement>(null)
  useEffect(() => { if (!work.active) setOpen(false) }, [work.active])

  useEffect(() => {
    if (!open) return
    const onPointer = (event: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(event.target as Node)) setOpen(false)
    }
    const onKey = (event: KeyboardEvent) => { if (event.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', onPointer)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onPointer)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  if (!work.active || work.items.length === 0) return null

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
        {work.items.length} running
      </button>
      {open && (
        <div
          role="dialog"
          aria-label="Background work"
          className="absolute bottom-full right-0 z-50 mb-2 w-[min(22rem,calc(100vw-2rem))] rounded-md border border-border bg-popover p-1 text-popover-foreground shadow-lg"
          data-testid="background-work-list"
        >
          <BackgroundWorkList work={work} onOpen={() => setOpen(false)} />
        </div>
      )}
    </div>
  )
}

/** Shared rows; the caller can wrap buttons in its menu's keyboard primitives. */
export function BackgroundWorkList({ work, onOpen, renderAction }: {
  work: ReturnType<typeof useSessionBackgroundWork>
  onOpen?: () => void
  renderAction?: (button: ReactElement, options?: { keepOpen: boolean; disabled: boolean }) => ReactNode
}) {
  return <ul className="max-h-72 overflow-y-auto">
    {work.items.map(item => {
      const card = transcriptCard(item.id)
      const openButton = item.inspection || card ? <button type="button"
        className="underline-offset-2 outline-none hover:text-foreground hover:underline data-[highlighted]:text-foreground data-[highlighted]:underline"
        aria-label={`Open ${backgroundWorkLabel(item)}`}
        onClick={() => { if (item.inspection) openStepInspection(item.inspection); else card?.scrollIntoView({ behavior: 'smooth', block: 'center' }); onOpen?.() }}>{item.inspection ? 'Inspect' : 'Open'}</button> : null
      const stopButton = <button type="button" onClick={() => void work.stopItem(item)} disabled={work.isStopping(item)}
        className="mt-0.5 inline-flex h-6 shrink-0 items-center gap-1 rounded px-1.5 text-[11px] text-muted-foreground outline-none hover:bg-destructive/10 hover:text-destructive data-[highlighted]:bg-destructive/10 data-[highlighted]:text-destructive disabled:opacity-60"
        aria-label={`Stop ${backgroundWorkLabel(item)}`} data-testid="background-work-stop">
        {work.isStopping(item) ? <Loader2 className="h-3 w-3 animate-spin" /> : <Square className="h-3 w-3" fill="currentColor" />}Stop
      </button>
      return (
        <li key={item.id} className="flex items-start gap-2 rounded px-2 py-1.5 hover:bg-muted/60" data-testid="background-work-item">
          <div className="min-w-0 flex-1">
            <div className="truncate text-xs font-medium" title={backgroundWorkLabel(item)}>{backgroundWorkLabel(item)}</div>
            {item.status && (
              <div className="truncate text-[11px] text-muted-foreground" title={item.status} data-testid="background-work-status">{item.status}</div>
            )}
            <div className="flex items-center gap-2 text-[10px] text-muted-foreground">
              <span className="font-mono">{elapsed(item.started_at, work.now)}</span>
              {openButton && (renderAction ? renderAction(openButton) : openButton)}
            </div>
          </div>
          {item.can_stop && (renderAction ? renderAction(stopButton, { keepOpen: true, disabled: work.isStopping(item) }) : stopButton)}
        </li>
      )
    })}
  </ul>
}
