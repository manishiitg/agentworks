import React from 'react'
import { Layers, Loader2 } from 'lucide-react'
import type { PollingEvent } from '../../../services/api-types'
import { contextCompactionInfo, contextCompactionText } from '../../../../shared/session/transcript/contextCompaction'

// One quiet line per compaction (PLAT-553). The transcript collapses a start
// and its end into the same row position, so this only ever swaps its text.
export const ContextCompactionEventDisplay: React.FC<{ event: PollingEvent }> = ({ event }) => {
  const info = contextCompactionInfo(event)
  if (!info) return null
  const live = info.phase === 'start' && !info.stale
  const failed = info.phase === 'end' && (info.outcome === 'failed' || info.outcome === 'aborted')
  const label = contextCompactionText(info)
  const title = [info.provider, info.trigger && `trigger: ${info.trigger}`].filter(Boolean).join(' · ')
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
    </div>
  )
}
