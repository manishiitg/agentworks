// Context compaction rows (PLAT-553).
//
// A coding CLI reports compaction as a `context_compaction` event with phase
// "start" (Claude, Pi, Muse announce one) and "end" (every CLI that records
// it). The transcript shows ONE row per compaction, at the position of its
// first event: "Compacting context…" while only the start is known, then the
// same row turns into "Compacted context (1m 39s) · 384k → 92k tokens". The
// row never moves, so the end replacing the start causes no layout jump.

import type { PollingEvent } from '../types'

export const CONTEXT_COMPACTION_EVENT = 'context_compaction'

export interface ContextCompactionInfo {
  phase: 'start' | 'end'
  id: string
  provider: string
  trigger: string
  outcome: string
  tokensBefore: number
  tokensAfter: number
  durationMs: number
  /** Start with no end, followed by a later turn: the CLI never reported a result. */
  stale: boolean
}

function payloadOf(event: PollingEvent): Record<string, unknown> {
  const outer = event.data as unknown
  if (!outer || typeof outer !== 'object') return {}
  const nested = (outer as { data?: unknown }).data
  return (nested && typeof nested === 'object' ? nested : outer) as Record<string, unknown>
}

const text = (value: unknown): string => (typeof value === 'string' ? value.trim() : '')
const count = (value: unknown): number => (typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0)

export function contextCompactionInfo(event: PollingEvent): ContextCompactionInfo | null {
  if (event.type !== CONTEXT_COMPACTION_EVENT) return null
  const payload = payloadOf(event)
  return {
    phase: text(payload.phase) === 'start' ? 'start' : 'end',
    id: text(payload.compaction_id),
    provider: text(payload.provider),
    trigger: text(payload.trigger),
    outcome: text(payload.outcome),
    tokensBefore: count(payload.tokens_before),
    tokensAfter: count(payload.tokens_after),
    durationMs: count(payload.duration_ms),
    stale: payload.stale === true,
  }
}

// Events that mean the turn holding an open compaction start is over.
const TURN_BOUNDARY_TYPES = new Set(['user_message', 'agent_end', 'unified_completion', 'agent_error', 'conversation_error'])

/**
 * One event per compaction, at the first event's position: the newest end for
 * its id, else the start. An end without an id closes the newest open start.
 * A start left open by a finished turn is marked stale.
 */
export function collapseContextCompactions(events: PollingEvent[]): PollingEvent[] {
  if (!events.some(event => event.type === CONTEXT_COMPACTION_EVENT)) return events
  const groupOf = new Map<PollingEvent, number>()
  const latest: PollingEvent[] = []
  const hasEnd: boolean[] = []
  const closedByTurn: boolean[] = []
  const groupById = new Map<string, number>()
  const open: number[] = []
  events.forEach(event => {
    if (TURN_BOUNDARY_TYPES.has(event.type || '')) {
      for (const group of open) closedByTurn[group] = true
      open.length = 0
      return
    }
    const info = contextCompactionInfo(event)
    if (!info) return
    let group = info.id ? groupById.get(info.id) : undefined
    if (group === undefined && info.phase === 'end' && !info.id && open.length > 0) group = open[open.length - 1]
    if (group === undefined) {
      group = latest.length
      latest.push(event)
      hasEnd.push(false)
      closedByTurn.push(false)
      if (info.id) groupById.set(info.id, group)
    }
    groupOf.set(event, group)
    if (info.phase === 'end') {
      latest[group] = event
      hasEnd[group] = true
      const at = open.indexOf(group)
      if (at >= 0) open.splice(at, 1)
    } else if (!hasEnd[group] && !open.includes(group)) {
      open.push(group)
    }
  })
  const emitted = new Set<number>()
  const out: PollingEvent[] = []
  for (const event of events) {
    const group = groupOf.get(event)
    if (group === undefined) {
      out.push(event)
      continue
    }
    if (emitted.has(group)) continue
    emitted.add(group)
    const representative = latest[group]
    if (!hasEnd[group] && closedByTurn[group]) {
      const outer = (representative.data && typeof representative.data === 'object' ? representative.data : {}) as Record<string, unknown>
      const nested = outer.data && typeof outer.data === 'object' ? { ...(outer.data as Record<string, unknown>), stale: true } : undefined
      out.push({ ...representative, data: (nested ? { ...outer, data: nested } : { ...outer, stale: true }) as PollingEvent['data'] })
    } else {
      out.push(representative)
    }
  }
  return out
}

export function formatCompactTokens(value: number): string {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1).replace(/\.0$/, '')}M`
  if (value >= 1_000) return `${Math.round(value / 1_000)}k`
  return String(Math.round(value))
}

export function formatCompactionDuration(ms: number): string {
  const seconds = Math.round(ms / 1000)
  if (seconds < 60) return `${Math.max(seconds, 1)}s`
  const minutes = Math.floor(seconds / 60)
  const rest = seconds % 60
  return rest ? `${minutes}m ${rest}s` : `${minutes}m`
}

/** The row text; unknown parts are omitted. */
export function contextCompactionText(info: ContextCompactionInfo): string {
  if (info.phase === 'start') {
    return info.stale ? 'Context compaction started (no result recorded)' : 'Compacting context…'
  }
  const failed = info.outcome === 'failed' || info.outcome === 'aborted'
  let head = failed ? (info.outcome === 'aborted' ? 'Context compaction aborted' : 'Context compaction failed') : 'Compacted context'
  if (info.durationMs > 0) head += ` (${formatCompactionDuration(info.durationMs)})`
  const parts = [head]
  if (!failed && info.tokensBefore > 0 && info.tokensAfter > 0) parts.push(`${formatCompactTokens(info.tokensBefore)} → ${formatCompactTokens(info.tokensAfter)} tokens`)
  else if (info.tokensBefore > 0) parts.push(`${formatCompactTokens(info.tokensBefore)} tokens before`)
  return parts.join(' · ')
}
