import type { PollingEvent } from '../services/api-types'
import type { PendingQueuedMessage } from '../../shared/session/types'

// A submitted message's identity is its submission id (the Idempotency-Key the
// chat journal already requires). The browser's provisional bubble and the
// server's durable user_message share the id `user:<submission id>`, so
// reconciliation is id replacement, never text matching.
export const CLIENT_MESSAGE_EVENT_ID_PREFIX = 'user:'

export function clientMessageEventId(clientMessageId: string): string {
  return CLIENT_MESSAGE_EVENT_ID_PREFIX + clientMessageId
}

function userMessageMetadata(event: PollingEvent): Record<string, unknown> {
  const outer = event.data as unknown as Record<string, unknown> | undefined
  const inner = (outer?.data ?? {}) as Record<string, unknown>
  const metadata = inner.metadata
  return metadata && typeof metadata === 'object' ? metadata as Record<string, unknown> : {}
}

export function readClientMessageId(event: PollingEvent): string {
  if (event.type !== 'user_message') return ''
  const id = userMessageMetadata(event).client_message_id
  return typeof id === 'string' ? id.trim() : ''
}

export function isProvisionalUserMessage(event: PollingEvent): boolean {
  return event.type === 'user_message' && userMessageMetadata(event).provisional === true
}

export function isDurableClientUserMessage(event: PollingEvent): boolean {
  return Boolean(readClientMessageId(event)) && !isProvisionalUserMessage(event)
}

// The durable row is authoritative for content and position. Keep only the
// browser-side receipt facts it cannot know (queue position, a verdict that
// already landed), so ticks survive the replacement.
const RECEIPT_KEYS = ['confirmation', 'confirmed_at', 'proof_source', 'latency_ms', 'queue_position', 'pending_provider'] as const

export function carryProvisionalReceipt(durable: PollingEvent, provisional: PollingEvent): PollingEvent {
  const provisionalMetadata = userMessageMetadata(provisional)
  const durableMetadata = userMessageMetadata(durable)
  const carried: Record<string, unknown> = {}
  for (const key of RECEIPT_KEYS) {
    if (provisionalMetadata[key] !== undefined && durableMetadata[key] === undefined) carried[key] = provisionalMetadata[key]
  }
  if (Object.keys(carried).length === 0) return durable
  const outer = durable.data as unknown as Record<string, unknown>
  const inner = (outer.data ?? {}) as Record<string, unknown>
  return { ...durable, data: { ...outer, data: { ...inner, metadata: { ...durableMetadata, ...carried } } } as PollingEvent['data'] }
}

// reconcileDurableUserEchoes drops every provisional bubble whose durable
// echo is in `incoming`, and returns the incoming rows with the bubbles'
// receipts carried over. The durable row then lands where the server placed
// it (after an answer the CLI was still writing), which is what moves a
// message sent mid-reply below that reply.
export function reconcileDurableUserEchoes(
  current: ReadonlyArray<PollingEvent>,
  incoming: ReadonlyArray<PollingEvent>,
): { current: PollingEvent[]; incoming: PollingEvent[]; replacedIds: string[] } {
  const echoes = new Set(incoming.filter(isDurableClientUserMessage).map(event => event.id).filter((id): id is string => Boolean(id)))
  if (echoes.size === 0) return { current: [...current], incoming: [...incoming], replacedIds: [] }
  const provisionals = new Map<string, PollingEvent>()
  const kept: PollingEvent[] = []
  for (const event of current) {
    if (event.id && echoes.has(event.id) && isProvisionalUserMessage(event)) provisionals.set(event.id, event)
    else kept.push(event)
  }
  if (provisionals.size === 0) return { current: [...current], incoming: [...incoming], replacedIds: [] }
  const merged = incoming.map(event => {
    const provisional = event.id ? provisionals.get(event.id) : undefined
    return provisional && isDurableClientUserMessage(event) ? carryProvisionalReceipt(event, provisional) : event
  })
  return { current: kept, incoming: merged, replacedIds: [...provisionals.keys()] }
}

// On restore, a provisional bubble survives only while its durable row is
// absent from the page (the send is queued or still being delivered).
export function keepUnechoedProvisionals(
  durable: PollingEvent[],
  current: ReadonlyArray<PollingEvent>,
): PollingEvent[] {
  const durableIds = new Set(durable.map(event => event.id).filter((id): id is string => Boolean(id)))
  const pending = current.filter(event => isProvisionalUserMessage(event) && event.id && !durableIds.has(event.id))
  return pending.length === 0 ? durable : [...durable, ...pending]
}

export function userMessageDisplayContent(event: PollingEvent): string {
  const display = userMessageMetadata(event).display_content
  return typeof display === 'string' ? display.trim() : ''
}

// Messages still waiting in the server's turn queue have no durable row yet.
// Restore shows them as queued provisional bubbles (same id as the durable row
// that will replace them) so a reload or another tab does not lose them.
export function pendingQueuedProvisionals(
  pending: PendingQueuedMessage[] | undefined,
  present: PollingEvent[],
  sessionId: string,
): PollingEvent[] {
  if (!pending?.length) return []
  const presentIds = new Set(present.map(event => event.id))
  return pending
    .filter(message => message.client_message_id && !presentIds.has(clientMessageEventId(message.client_message_id)))
    .map(message => {
      const timestamp = message.queued_at || new Date().toISOString()
      return {
        id: clientMessageEventId(message.client_message_id),
        type: 'user_message',
        timestamp,
        session_id: sessionId,
        data: {
          type: 'user_message',
          timestamp,
          data: {
            content: message.content,
            timestamp,
            metadata: {
              client_message_id: message.client_message_id,
              provisional: true,
              delivery_status: 'queued_for_turn',
              ...(message.queue_position ? { queue_position: message.queue_position } : {}),
            },
          },
        },
      } as unknown as PollingEvent
    })
}
