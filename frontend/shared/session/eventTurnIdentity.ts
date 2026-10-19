import type { PollingEvent } from './types'

function record(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === 'object' ? value as Record<string, unknown> : undefined
}

// Only use identity supplied by the server; arrival order is not a turn ID.
export function eventTurnID(event: Pick<PollingEvent, 'data'>): string {
  const outer = record(event.data)
  const inner = record(outer?.data)
  for (const value of [outer?.turn_id, record(inner?.metadata)?.turn_id, inner?.turn_id, record(outer?.metadata)?.turn_id]) {
    if (typeof value === 'string' && value.trim()) return value.trim()
  }
  return ''
}

export function distinctServerTurns(left: string, right: string): boolean {
  return Boolean(left && right && left !== right)
}
