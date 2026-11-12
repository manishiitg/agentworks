import { providerShortLabel } from '../../../utils/chatHistoryRuntimeLabel'

export type DeliveryTickState = 'sending' | 'queued' | 'fast' | 'confirmed' | 'unflushed' | 'failed' | null

export function deliveryTickState(metadata: Record<string, unknown> | undefined): DeliveryTickState {
  if (!metadata) return null
  const confirmation = metadata.confirmation
  if (confirmation === 'confirmed') return 'confirmed'
  if (confirmation === 'accepted_but_unflushed') return 'unflushed'
  if (confirmation === 'failed') return 'failed'
  if (confirmation === 'queued') return 'queued'
  if (metadata.delivery_status === 'queued_for_injection') return 'queued'
  if (confirmation === 'fast') return 'fast'
  const status = metadata.delivery_status
  if (status === 'queued_for_turn' || status === 'queued_for_injection') return 'queued'
  if (status === 'sent_to_cli' || status === 'next_turn_started') return 'fast'
  if (metadata.provisional === true && !status && !confirmation) return 'sending'
  return null
}

export function deliveryTickTitle(metadata: Record<string, unknown> | undefined, state: Exclude<DeliveryTickState, null>): string {
  const provider = typeof metadata?.provider === 'string' && metadata.provider ? ` ${metadata.provider}` : ''
  const latencyMs = typeof metadata?.latency_ms === 'number' ? metadata.latency_ms : null
  const latency = latencyMs === null ? '' : latencyMs >= 1000 ? ` in ${(latencyMs / 1000).toFixed(1)}s` : ` in ${Math.round(latencyMs)}ms`
  const position = typeof metadata?.queue_position === 'number' ? ` · position ${metadata.queue_position}` : ''
  const pending = typeof metadata?.pending_provider === 'string' ? metadata.pending_provider : ''
  const pendingProviderSuffix = pending ? `, then runs on ${providerShortLabel(pending)}` : ''
  switch (state) {
    case 'sending': return 'Sending message'
    case 'queued': return `Queued behind the active conversation turn${position}${pendingProviderSuffix}`
    case 'confirmed': return `Confirmed in${provider} CLI record${latency}`
    case 'unflushed': return `Sent to${provider} CLI; not confirmed yet`
    case 'failed': return `Delivery to${provider} CLI unconfirmed`
    case 'fast': return `Sent to${provider} CLI`
  }
}

/** Plain receipt text; never promises that a model read or acted on input. */
export function deliveryTickLabel(state: Exclude<DeliveryTickState, null>): string {
  switch (state) {
    case 'sending': return 'Sending…'
    case 'queued': return 'Queued'
    case 'confirmed': return 'Saved in agent chat'
    case 'fast': return 'Sent to agent'
    case 'unflushed': case 'failed': return 'Delivery unconfirmed'
  }
}
