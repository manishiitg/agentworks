import { Hourglass } from 'lucide-react'
import type { PulseAutonomy } from '../../services/api-types'
import { AUTONOMY_ALWAYS_ASKS, AUTONOMY_LEVELS, autonomyLevelIndex } from './pulseAutonomy'

/** Pulse's autonomy ladder: what Pulse may get done without asking the owner. */
export function AutonomySlider({ autonomy, saving, onChange }: {
  autonomy: PulseAutonomy
  saving: boolean
  onChange?: (next: PulseAutonomy) => void
}) {
  const index = autonomyLevelIndex(autonomy)
  const level = AUTONOMY_LEVELS[index]
  return <section aria-label="Pulse autonomy" className="mb-3 rounded-lg border bg-background px-3 py-2">
    <div className="flex items-center justify-between gap-3">
      <label htmlFor="pulse-autonomy" className="flex items-center gap-1.5 text-xs font-semibold"><Hourglass className="h-3.5 w-3.5 text-primary" />Pulse autonomy</label>
      <span className="text-[11px] font-medium text-primary">{level.label} · level {index} of {AUTONOMY_LEVELS.length - 1}</span>
    </div>
    <input id="pulse-autonomy" type="range" min={0} max={AUTONOMY_LEVELS.length - 1} step={1} value={index}
      disabled={!onChange || saving} aria-valuetext={level.label}
      className="reasoning-effort-slider reasoning-effort-slider--compact mt-2 w-full disabled:opacity-50"
      style={{ ['--reasoning-effort-fill' as string]: `${(index / (AUTONOMY_LEVELS.length - 1)) * 100}%` }}
      onChange={event => onChange?.(AUTONOMY_LEVELS[Number(event.target.value)].value)} />
    <div className="mt-0.5 flex justify-between text-[10px] text-muted-foreground">{AUTONOMY_LEVELS.map((item, i) =>
      <span key={item.label} className={i <= index ? 'text-foreground' : undefined}>{item.label}</span>)}</div>
    <p className="mt-1 text-[11px] leading-4 text-foreground">{level.summary}</p>
    <p className="text-[10px] leading-4 text-muted-foreground">{AUTONOMY_ALWAYS_ASKS}</p>
  </section>
}
