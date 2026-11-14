import type { PollingEvent } from '../types'

export type NativeGoal = {
  objective: string
  status: 'active' | 'paused' | 'complete' | 'blocked' | 'usage_limited' | 'budget_limited'
  tokens_used?: number
  token_budget?: number
  time_used_seconds?: number
}
export type NativeGoalUpdate = { provider: string; native_session_id: string; goal: NativeGoal | null; reason?: string }

export function nativeGoalUpdate(event: PollingEvent): NativeGoalUpdate | undefined {
  if (event.type !== 'native_goal_updated') return undefined
  const outer = event.data as Record<string, unknown> | undefined
  const data = (outer?.data && typeof outer.data === 'object' ? outer.data : outer) as Record<string, unknown> | undefined
  const update = data?.update as NativeGoalUpdate | undefined
  if (!update || typeof update.provider !== 'string' || typeof update.native_session_id !== 'string') return undefined
  if (update.goal === null) return update
  const goal = update.goal
  if (!goal || typeof goal.objective !== 'string' || !goal.objective.trim() || !['active', 'paused', 'complete', 'blocked', 'usage_limited', 'budget_limited'].includes(goal.status)) return undefined
  return update
}

// One mutable goal row per chat, not one card per native progress update.
// Invalid frames must never erase a valid goal; an explicit native clear does.
export function collapseNativeGoals(events: PollingEvent[]): PollingEvent[] {
  const out: PollingEvent[] = []
  const positions = new Map<string, number>()
  for (const event of events) {
    if (event.type !== 'native_goal_updated') { out.push(event); continue }
    if (!nativeGoalUpdate(event)) continue
    const key = event.session_id || ''
    const index = positions.get(key)
    if (index === undefined) { positions.set(key, out.length); out.push(event) }
    else out[index] = { ...event, id: out[index].id }
  }
  return out.filter(event => event.type !== 'native_goal_updated' || nativeGoalUpdate(event)?.goal !== null)
}
