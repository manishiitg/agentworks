import React, { useState } from 'react'
import { Loader2, RotateCcw } from 'lucide-react'
import type { SkippedWhilePaused } from '../../../services/api-types'
import { timeAgo } from './helpers'

type PauseCatchUpPanelProps = {
  items: SkippedWhilePaused[]
  running: boolean
  canRun: boolean
  onRun: (scheduleIds: string[]) => void
  onDismiss: () => void
}

// After all schedules are resumed: the runs the pause skipped. Nothing runs on
// its own, since a burst of late sends or posts can be wrong; the owner picks
// what to catch up, and each chosen schedule runs once.
export const PauseCatchUpPanel: React.FC<PauseCatchUpPanelProps> = ({ items, running, canRun, onRun, onDismiss }) => {
  const [selected, setSelected] = useState<string[]>([])
  if (items.length === 0) return null
  const total = items.reduce((sum, item) => sum + item.count, 0)
  const toggle = (id: string) => setSelected(prev => (prev.includes(id) ? prev.filter(x => x !== id) : [...prev, id]))

  return (
    <section className="rounded-xl border border-primary/30 bg-primary/5 px-4 py-3" aria-label="Runs skipped while paused">
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="text-sm font-medium text-foreground">
            {total} scheduled run{total === 1 ? ' was' : 's were'} skipped while schedules were paused
          </div>
          <div className="mt-0.5 text-xs text-muted-foreground">
            Choose any to run now. Each chosen schedule runs once, even if it missed several times.
          </div>
        </div>
        <button type="button" onClick={onDismiss} className="shrink-0 rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground">
          Dismiss
        </button>
      </div>
      <ul className="mt-3 space-y-1.5">
        {items.map(item => (
          <li key={item.schedule_id}>
            <label className="flex cursor-pointer items-center gap-2 text-xs">
              <input
                type="checkbox"
                checked={selected.includes(item.schedule_id)}
                onChange={() => toggle(item.schedule_id)}
                disabled={!canRun || running}
                aria-label={`Run ${item.schedule_name || item.schedule_id} now`}
              />
              <span className="font-medium text-foreground">{item.schedule_name || item.schedule_id}</span>
              <span className="text-muted-foreground">
                {item.workflow_label || item.workspace_path} · skipped {item.count}× · last {timeAgo(item.latest_scheduled_for)}
              </span>
            </label>
          </li>
        ))}
      </ul>
      {canRun ? (
        <button
          type="button"
          onClick={() => onRun(selected)}
          disabled={running || selected.length === 0}
          className="mt-3 inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-xs font-semibold text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
        >
          {running ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RotateCcw className="h-3.5 w-3.5" />}
          Run selected now
        </button>
      ) : null}
    </section>
  )
}
