import type { PollingEvent } from '../services/api-types'

export type CompactMessageGroup = { kind: 'message_group'; key: string; category: 'interrupted' | 'sender'; label: string; events: PollingEvent[] }
const object = (value: unknown): Record<string, unknown> => value && typeof value === 'object' ? value as Record<string, unknown> : {}

// Classification uses server provenance/outcome fields, never envelope prose.
function descriptor(event: PollingEvent) {
  const outer = object(event.data), payload = outer.data ? object(outer.data) : outer
  const meta = object(payload.metadata)
  if (['unified_completion', 'agent_end'].includes(event.type || '') && meta.retained_turn_outcome === 'interrupted' && meta.final_response_missing === true && meta.stopped !== true && !meta.partial_response) {
    return { category: 'interrupted' as const, label: 'Interrupted', identity: JSON.stringify([event.session_id, 'interrupted']) }
  }
  if (event.type !== 'user_message' || meta.source !== 'agent_message' || typeof meta.sender_label !== 'string' || typeof meta.display_content !== 'string') return null
  // A clock, uncertain delivery or an actionable failure must remain visible.
  if (meta.confirmation !== 'confirmed' && meta.delivery_status && !['sent_to_cli', 'next_turn_started'].includes(String(meta.delivery_status))) return null
  if (meta.confirmation !== 'confirmed' && (meta.delivery_status === 'unconfirmed' || ['failed', 'accepted_but_unflushed'].includes(String(meta.confirmation)))) return null
  const sender = typeof meta.sender_agent === 'string' && meta.sender_agent ? meta.sender_agent : typeof meta.agent_message_inbox === 'string' ? meta.agent_message_inbox : ''
  if (!sender || !meta.sender_label.trim()) return null
  return { category: 'sender' as const, label: meta.sender_label, identity: JSON.stringify([event.session_id, meta.sender_user_id, sender, meta.sender_channel, meta.sender_label]) }
}

/** One linear display pass; original events, order and data are retained. */
export function groupTranscriptMessages<T extends { kind: string; key: string; event?: PollingEvent }>(items: T[], protectedKey?: string): (T | CompactMessageGroup)[] {
  const out: (T | CompactMessageGroup)[] = []
  let run: T[] = [], kind: ReturnType<typeof descriptor> = null
  const flush = () => {
    if (run.length > 1 && kind) out.push({ kind: 'message_group', key: run[0].key, category: kind.category, label: kind.label, events: run.map(item => item.event!) })
    else out.push(...run)
    run = []; kind = null
  }
  for (const item of items) {
    // An exact message link/reading anchor stays individually addressable.
    const next = item.kind === 'event' && item.event && item.key !== protectedKey && item.event.id !== protectedKey ? descriptor(item.event) : null
    if (!next || next.identity !== kind?.identity) flush()
    if (!next) { out.push(item); continue }
    kind = next; run.push(item)
  }
  flush()
  return out
}
