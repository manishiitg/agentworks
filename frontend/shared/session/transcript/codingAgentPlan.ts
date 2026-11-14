import type { PollingEvent } from '../types'
import { eventTurnID } from '../eventTurnIdentity'

export type CodingAgentPlanItem = { id: string; text: string; status: 'pending' | 'running' | 'completed' }
export type CodingAgentPlan = { plan_id: string; provider: string; items: CodingAgentPlanItem[] }

export function codingAgentPlan(event: PollingEvent): CodingAgentPlan | undefined {
  if (event.type !== 'coding_agent_plan') return undefined
  const outer = event.data as Record<string, unknown> | undefined
  const data = (outer?.data && typeof outer.data === 'object' ? outer.data : outer) as Record<string, unknown> | undefined
  if (!data || typeof data.plan_id !== 'string' || !Array.isArray(data.items)) return undefined
  const items: CodingAgentPlanItem[] = []
  for (const raw of data.items) {
    if (!raw || typeof raw !== 'object') return undefined
    const item = raw as Record<string, unknown>
    if (typeof item.id !== 'string' || typeof item.text !== 'string' || !['pending', 'running', 'completed'].includes(String(item.status))) return undefined
    items.push(item as CodingAgentPlanItem)
  }
  return { plan_id: data.plan_id, provider: typeof data.provider === 'string' ? data.provider : '', items }
}

// A checklist is a mutable snapshot within one accepted user turn, not a new
// transcript row per tool call. Keep the first row's identity and position.
export function collapseCodingAgentPlans(events: PollingEvent[]): PollingEvent[] {
  const out: PollingEvent[] = []
  const positions = new Map<string, number>()
  let user = 'history'
  for (const event of events) {
    if (event.type === 'user_message') user = event.id
    if (event.type !== 'coding_agent_plan') { out.push(event); continue }
    const plan = codingAgentPlan(event)
    if (!plan) continue // Never turn malformed wire data into a cleared list.
    const key = `${event.session_id || ''}:${eventTurnID(event) || user}`
    const index = positions.get(key)
    if (index === undefined) { positions.set(key, out.length); out.push(event) }
    else out[index] = { ...event, id: out[index].id }
  }
  return out.filter(event => event.type !== 'coding_agent_plan' || codingAgentPlan(event)!.items.length > 0)
}
