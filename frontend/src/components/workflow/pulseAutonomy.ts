import type { PulseAutonomy } from '../../services/api-types'

// The autonomy ladder (owner, 2026-10-08): six levels, each adding one kind of
// action Pulse may get done without asking. Stored as pulse.autonomy.level; the
// run/outward/change switches are written beside it for older readers. Keep in
// step with stepworkflow.AutonomyLadder.
const sw = (on: boolean) => (on ? 'auto' : 'ask') as 'auto' | 'ask'
function valueFor(level: number): PulseAutonomy {
  return { level, run: sw(level >= 1), outward: sw(level >= 4), change: sw(level >= 2) }
}

export const AUTONOMY_LEVELS: Array<{ label: string; summary: string; value: PulseAutonomy }> = [
  { label: 'Advise', summary: 'Pulse only recommends. Nothing happens without you.', value: valueFor(0) },
  { label: 'Measure', summary: 'Runs steps to measure the goal and recover missed runs, and sets up goal metrics.', value: valueFor(1) },
  { label: 'Fix', summary: 'Also fixes broken steps (bugs, wrong inputs), reversibly.', value: valueFor(2) },
  { label: 'Tune', summary: 'Also improves prompts and step settings.', value: valueFor(3) },
  { label: 'Publish', summary: "Also publishes and posts through the workflow's own steps and accounts.", value: valueFor(4) },
  { label: 'Reshape', summary: "Also changes schedules and the plan's structure (never deletes).", value: valueFor(5) },
]

export const AUTONOMY_ALWAYS_ASKS = 'Always asks you: spending money, deleting, soul.md, new kinds of outreach, schedules you paused.'

// autonomyLevelIndex is the stored level, or for an older setting the highest
// level its switches fully allow (never more than they granted).
export function autonomyLevelIndex(autonomy: PulseAutonomy): number {
  if (typeof autonomy.level === 'number') return Math.max(0, Math.min(AUTONOMY_LEVELS.length - 1, autonomy.level))
  if (autonomy.run !== 'auto') return 0
  if (autonomy.change !== 'auto') return 1
  if (autonomy.outward !== 'auto') return 3
  return 5
}

/** Fired when a focus area changes outside the focus card, so it reloads. */
export const PULSE_FOCUS_AREAS_CHANGED_EVENT = 'pulse-focus-areas-changed'
