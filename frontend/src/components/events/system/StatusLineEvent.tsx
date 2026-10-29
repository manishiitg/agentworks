import React from 'react'
import { Activity, Terminal } from 'lucide-react'
import { formatTokens } from '../../../utils/formatTokens'

export interface StatusLineEvent {
  provider?: string
  model?: string
  tmux_session?: string
  input_tokens?: number
  output_tokens?: number
  cache_creation_input_tokens?: number
  cache_read_input_tokens?: number
  total_input_tokens?: number
  total_output_tokens?: number
  cost_usd?: number
  timestamp?: string
  metadata?: Record<string, unknown>
}

interface StatusLineEventDisplayProps {
  event: StatusLineEvent
  compact?: boolean
}


function formatCost(value?: number): string | null {
  if (!value || value <= 0) return null
  return value < 0.01 ? `$${value.toFixed(4)}` : `$${value.toFixed(2)}`
}

const shownTokens = (value?: number) => (value && value > 0 ? formatTokens(value) : null)

export const StatusLineEventDisplay: React.FC<StatusLineEventDisplayProps> = ({ event, compact = false }) => {
  const inputTokens = shownTokens(event.total_input_tokens ?? event.input_tokens)
  const outputTokens = shownTokens(event.total_output_tokens ?? event.output_tokens)
  const cacheReadTokens = shownTokens(event.cache_read_input_tokens)
  const cacheWriteTokens = shownTokens(event.cache_creation_input_tokens)
  const cost = formatCost(event.cost_usd)
  const time = event.timestamp ? new Date(event.timestamp).toLocaleTimeString() : null

  return (
    <div className={`rounded-md border border-slate-200 bg-slate-50 text-slate-700 dark:border-slate-800 dark:bg-slate-900/30 dark:text-slate-300 ${compact ? 'p-2' : 'p-2.5'}`}>
      <div className="flex min-w-0 items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          <Activity className="h-3.5 w-3.5 shrink-0 text-slate-500 dark:text-slate-400" />
          <div className="min-w-0 truncate text-xs font-medium">
            Status line
            {event.provider && <span className="font-normal text-slate-500 dark:text-slate-400"> · {event.provider}</span>}
            {event.model && <span className="font-normal text-slate-500 dark:text-slate-400"> · {event.model}</span>}
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-2 text-[10px] text-slate-500 dark:text-slate-400">
          {inputTokens && <span>in {inputTokens}</span>}
          {outputTokens && <span>out {outputTokens}</span>}
          {cost && <span>{cost}</span>}
          {time && <span>{time}</span>}
        </div>
      </div>

      {(event.tmux_session || cacheReadTokens || cacheWriteTokens) && (
        <div className="mt-1.5 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-[10px] text-slate-500 dark:text-slate-400">
          {event.tmux_session && (
            <span className="inline-flex min-w-0 items-center gap-1">
              <Terminal className="h-3 w-3 shrink-0" />
              <span className="truncate font-mono">{event.tmux_session}</span>
            </span>
          )}
          {cacheReadTokens && <span>cache read {cacheReadTokens}</span>}
          {cacheWriteTokens && <span>cache write {cacheWriteTokens}</span>}
        </div>
      )}
    </div>
  )
}
