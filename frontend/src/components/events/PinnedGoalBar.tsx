import { useEffect, useState } from 'react'
import ConfirmationDialog from '../ui/ConfirmationDialog'
import { currentNativeGoal, type NativeGoalUpdate } from '../../../shared/session/transcript/nativeGoal'
import type { PollingEvent } from '../../services/api-types'

const STATUS: Record<string, string> = { active: 'Active', paused: 'Paused', complete: 'Done', blocked: 'Blocked', usage_limited: 'Usage limit reached', budget_limited: 'Budget limit reached' }
const tone = (status: string) => status === 'active' ? 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300' : status === 'complete' ? 'bg-muted text-muted-foreground' : status === 'paused' ? 'bg-amber-500/15 text-amber-700 dark:text-amber-300' : 'bg-red-500/15 text-red-700 dark:text-red-300'

/** The chat's goal, pinned above the conversation while one exists. Status, objective, time and tokens come from the
 * server's goal state (the same events the transcript carries); Pause, Resume and Clear send the same /goal command a
 * person types, so the server's own goal control decides who may use them. */
export function PinnedGoalBar({ events, onCommand }: { events: PollingEvent[] | undefined; onCommand?: (command: string) => void }) {
  const update: NativeGoalUpdate | undefined = currentNativeGoal(events)
  const [open, setOpen] = useState(false)
  const goal = update?.goal
  const identity = JSON.stringify([update?.provider, update?.native_session_id, goal?.objective])
  const [clearing, setClearing] = useState<string | null>(null)
  useEffect(() => { setClearing(null) }, [identity])
  if (!goal) return null
  const seconds = typeof goal.time_used_seconds === 'number' && Number.isFinite(goal.time_used_seconds) && goal.time_used_seconds >= 0 ? Math.floor(goal.time_used_seconds) : undefined
  const tokens = typeof goal.tokens_used === 'number' && Number.isFinite(goal.tokens_used) && goal.tokens_used >= 0 ? goal.tokens_used.toLocaleString() : undefined
  const percent = typeof goal.percent_complete === 'number' && Number.isFinite(goal.percent_complete) ? Math.min(100, Math.max(0, Math.round(goal.percent_complete))) : undefined
  // Muse reports what it works on now and next (#4628).
  const work = [goal.current_work?.trim() ? `Now: ${goal.current_work.trim()}` : '', goal.next_work?.trim() ? `Next: ${goal.next_work.trim()}` : ''].filter(Boolean).join(' · ')
  const stalled = goal.status === 'paused' && update?.reason === 'native_continuation_not_started'
  // Antigravity and Claude Code have no pause or resume: Stop ends the /goal turn and Start again sends the goal
  // again (#4604, #4626).
  const agy = update?.provider === 'agy-cli' || update?.provider === 'claude-code'
  const resumable = goal.status === 'paused' || goal.status === 'blocked' || goal.status === 'usage_limited'
  const button = 'shrink-0 rounded-full border border-border px-2.5 py-0.5 text-xs hover:bg-muted'
  const objective = goal.objective.replace(/\s+/g, ' ').trim()
  const characters = Array.from(objective)
  const preview = characters.slice(0, 120).join('') + (characters.length > 120 ? '…' : '')
  return <section aria-label="Agent goal" className="shrink-0 border-b border-border bg-background px-3 py-1.5 text-xs">
    <div className="flex min-w-0 items-center gap-2">
      <span className="shrink-0 font-medium">Goal</span>
      <span role="status" className={`shrink-0 rounded-full px-2 py-0.5 ${tone(goal.status)}`}>{stalled ? 'Paused: Codex did not continue' : agy && goal.status === 'paused' ? 'Stopped' : agy && goal.status === 'active' ? 'Working' : STATUS[goal.status]}</span>
      {percent !== undefined && <span role="progressbar" aria-label="Goal progress" aria-valuenow={percent} aria-valuemin={0} aria-valuemax={100} title={`${percent}% done`} className="h-1.5 w-16 shrink-0 overflow-hidden rounded-full bg-muted"><span className="block h-full bg-emerald-500" style={{ width: `${percent}%` }} /></span>}
      <button type="button" aria-expanded={open} aria-label="Show the whole goal" onClick={() => setOpen(value => !value)} className={`min-w-0 flex-1 text-left ${open ? 'whitespace-pre-wrap break-words' : 'truncate'}`}>{goal.objective}</button>
      <span className="hidden shrink-0 text-muted-foreground sm:inline">{[seconds === undefined ? '' : `${Math.floor(seconds / 60)}m ${seconds % 60}s`, tokens ? `${tokens} tokens` : ''].filter(Boolean).join(' · ')}</span>
      {onCommand && goal.status === 'active' && <button type="button" className={button} onClick={() => onCommand('/goal pause')}>{agy ? 'Stop' : 'Pause'}</button>}
      {onCommand && resumable && <button type="button" className={button} onClick={() => onCommand('/goal resume')}>{agy ? 'Start again' : 'Resume'}</button>}
      {onCommand && <button type="button" className={button} onClick={() => setClearing(identity)}>Clear</button>}
    </div>
    {(goal.status === 'blocked' || (goal.status === 'active' && update?.provider === 'claude-code')) && goal.last_check && <p className="mt-1 line-clamp-2 text-muted-foreground">{goal.status === 'active' ? `Not done yet: ${goal.last_check}` : goal.last_check}</p>}
    {work && goal.status !== 'complete' && <p className="mt-1 truncate text-muted-foreground" title={work}>{work}</p>}
    <ConfirmationDialog isOpen={Boolean(onCommand) && clearing === identity} onClose={() => setClearing(null)}
      onConfirm={() => { if (clearing === identity) { setClearing(null); onCommand?.('/goal clear') } }}
      title="Clear this goal?" message={`The agent stops working toward “${preview}”.`}
      confirmText="Clear goal" cancelText="Cancel" type="warning" />
  </section>
}
