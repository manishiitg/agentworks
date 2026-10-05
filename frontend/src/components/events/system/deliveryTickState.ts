import { providerShortLabel } from '../../../utils/chatHistoryRuntimeLabel'

export type DeliveryTickState = 'queued' | 'fast' | 'confirmed' | 'unflushed' | 'failed' | null

export function deliveryTickState(metadata: Record<string, unknown> | undefined): DeliveryTickState {
  if (!metadata) return null
  const confirmation = metadata.confirmation
  if (confirmation === 'confirmed') return 'confirmed'
  if (confirmation === 'accepted_but_unflushed') return 'unflushed'
  if (confirmation === 'failed') return 'failed'
  if (confirmation === 'queued') return 'queued'
  if (confirmation === 'fast') return 'fast'
  const status = metadata.delivery_status
  if (status === 'queued_for_turn') return 'queued'
  if (status === 'sent_to_cli' || status === 'next_turn_started' || status === 'queued_for_injection') return 'fast'
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
    case 'queued': return `Queued behind the active conversation turn${position}${pendingProviderSuffix}`
    case 'confirmed': return `Confirmed in${provider} CLI record${latency}`
    case 'unflushed': return `Held in${provider} CLI queue, awaiting record`
    case 'failed': return `Delivery to${provider} CLI unconfirmed`
    case 'fast': return `Sent to${provider} CLI`
  }
}
