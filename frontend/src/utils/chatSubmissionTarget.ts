export interface ChatSubmissionOptions {
  deliveryMode?: 'steer' | 'queue'
  isAutoNotification?: boolean
  sourceTabId?: string
  sourceSessionId?: string
  sourceComposerId?: string
  identity?: number
  submissionId?: string
  submittedAtPerformanceMS?: number
  submittedAtClientTime?: string
  optimisticUserEventId?: string
  onRejected?: (failure: { message: string; code?: string }) => void
  queuedDelivery?: boolean
  builderHandoff?: { tabId?: string; sessionId?: string }
}

// Only submissions already pending from the same composer share its newly
// created conversation. Returning to Builder mounts a different composer.
const pendingBuilders = new Map<string, { target: NonNullable<ChatSubmissionOptions['builderHandoff']>; count: number }>()
export function acquireBuilderSubmission(key: string) {
  let batch = pendingBuilders.get(key)
  if (!batch) {
    batch = { target: {}, count: 0 }
    pendingBuilders.set(key, batch)
  }
  batch.count++
  const owned = batch
  return { target: owned.target, release: () => {
    if (--owned.count === 0 && pendingBuilders.get(key) === owned) pendingBuilders.delete(key)
  } }
}

// Only an authoritative reconciled rejection proves that a retry can use a
// fresh receipt. Network failures, conflicts and uncertainty retain identity.
// turn_running is authoritative: the backend answered before the lane, so the
// message was provably never sent or queued.
export function isConfirmedUndeliveredSubmission(error: unknown): boolean {
  if (typeof error !== 'object' || error === null || !('response' in error)) return false
  const response = (error as { response?: { status?: number; data?: { error?: string } } }).response
  return response?.status === 409 && (response.data?.error === 'delivery_not_sent' || response.data?.error === 'turn_running')
}
