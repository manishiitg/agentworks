import type { PollingEvent } from '../../services/api-types'
import { nativeGoalUpdate } from '../../../shared/session/transcript/nativeGoal'

export function NativeGoalRow({ event, onSendMessage }: { event: PollingEvent; onSendMessage?: (message: string) => void }) {
  const update = nativeGoalUpdate(event)
  const goal = update?.goal
  if (!goal) return null
  const status = goal.status === 'paused' && update.reason === 'native_continuation_not_started'
    ? 'Paused: Codex did not continue'
    : { active: 'Working', paused: 'Paused', complete: 'Complete', blocked: 'Blocked', usage_limited: 'Usage limit reached', budget_limited: 'Budget limit reached' }[goal.status]
  const count = (value: number | undefined) => typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value.toLocaleString() : '—'
  const seconds = typeof goal.time_used_seconds === 'number' && Number.isFinite(goal.time_used_seconds) && goal.time_used_seconds >= 0 ? Math.floor(goal.time_used_seconds) : undefined
  return <section aria-label="Agent goal" className="my-1 rounded-lg border border-border px-3 py-2 text-xs">
    <div className="flex items-center gap-2"><span className="font-medium">Goal</span><span role="status" className="text-muted-foreground">{status}</span>
      {goal.status === 'paused' && onSendMessage && <button type="button" className="ml-auto text-primary hover:underline" onClick={() => onSendMessage('/goal resume')}>Resume</button>}
    </div>
    <p className="mt-1 whitespace-pre-wrap break-words">{goal.objective}</p>
    <p className="mt-1 text-muted-foreground">Tokens: {count(goal.tokens_used)} · Budget: {count(goal.token_budget)} · Time: {seconds === undefined ? '—' : `${Math.floor(seconds / 60)}m ${seconds % 60}s`}</p>
  </section>
}
