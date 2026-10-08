import { Gauge } from 'lucide-react'
import type { PulsePace } from '../../services/api-types'

// Pulse pace (owner, 2026-10-08): how hard Pulse pushes on the goal. Autonomy
// is what Pulse may do; pace is how fast. Keep in step with pulse_pace.go.
export const PULSE_PACES: Array<{ value: PulsePace; label: string; summary: string }> = [
  { value: 'calm', label: 'Calm', summary: 'Checks every 1-7 days. A failed run waits for the next check. One improvement at a time.' },
  { value: 'steady', label: 'Steady', summary: 'Checks every 6 hours to 3 days. A failed run wakes Pulse once. 1-3 improvements per turn.' },
  { value: 'aggressive', label: 'Aggressive', summary: 'Checks every 1-24 hours, soon after runs that should show a fix. A failed run wakes Pulse at once. Up to 3 improvements per turn, chaining follow-ups.' },
]

export function PaceSelector({ pace, saving, onChange }: { pace: PulsePace; saving: boolean; onChange?: (next: PulsePace) => void }) {
  const current = PULSE_PACES.find(item => item.value === pace) || PULSE_PACES[1]
  return <section aria-label="Pulse pace" className="mb-3 rounded-lg border bg-background px-3 py-2">
    <div className="flex flex-wrap items-center justify-between gap-2">
      <span className="flex items-center gap-1.5 text-xs font-semibold"><Gauge className="h-3.5 w-3.5 text-primary" />Pace on the goal</span>
      <div role="radiogroup" aria-label="Pulse pace" className="flex gap-1 rounded-md border bg-muted/30 p-0.5">
        {PULSE_PACES.map(item => <button key={item.value} type="button" role="radio" aria-checked={item.value === current.value}
          disabled={!onChange || saving} onClick={() => onChange?.(item.value)}
          className={`rounded px-2 py-0.5 text-[11px] font-medium transition-colors disabled:opacity-50 ${item.value === current.value ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}>
          {item.label}</button>)}
      </div>
    </div>
    <p className="mt-1 text-[11px] leading-4 text-muted-foreground">{current.summary}</p>
  </section>
}
