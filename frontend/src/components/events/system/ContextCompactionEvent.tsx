import React, { lazy, Suspense, useState } from 'react'
import { Layers, Loader2 } from 'lucide-react'
import type { PollingEvent } from '../../../services/api-types'
import { contextCompactionInfo, contextCompactionText } from '../../../../shared/session/transcript/contextCompaction'

const CompactionSummaryPanel = lazy(() => import('../CompactionSummaryPanel').then(module => ({ default: module.CompactionSummaryPanel })))

// The coding programs that save what they kept when compacting (#3236).
const SUMMARY_PROVIDERS = new Set(['claude-code', 'codex-cli'])

// One quiet line per compaction (PLAT-553). The transcript collapses a start
// and its end into the same row position, so this only ever swaps its text.
export const ContextCompactionEventDisplay: React.FC<{ event: PollingEvent }> = ({ event }) => {
  const [open, setOpen] = useState(false)
  const info = contextCompactionInfo(event)
  if (!info) return null
  const live = info.phase === 'start' && !info.stale
  const failed = info.phase === 'end' && (info.outcome === 'failed' || info.outcome === 'aborted')
  const label = contextCompactionText(info)
  const title = [info.provider, info.trigger && `trigger: ${info.trigger}`].filter(Boolean).join(' · ')
  const sessionId = event.session_id || ''
  const canOpen = info.phase === 'end' && !failed && !!sessionId && SUMMARY_PROVIDERS.has(info.provider)
  return (
    <div
      data-testid="context-compaction-event"
      data-phase={info.phase}
      title={title || undefined}
      className={`my-1 flex h-6 min-w-0 items-center gap-2 text-xs ${failed ? 'text-amber-500' : 'text-muted-foreground'}`}
    >
      {live
        ? <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin" aria-hidden="true" />
        : <Layers className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />}
      <span className="truncate">{label}</span>
      {canOpen && <button type="button" onClick={() => setOpen(true)} className="shrink-0 text-primary hover:underline">See summary</button>}
      {open && <Suspense fallback={null}><CompactionSummaryPanel sessionId={sessionId} at={info.endedAt || undefined} onClose={() => setOpen(false)} /></Suspense>}
    </div>
  )
}
