import { randomUUID } from './randomUUID'
import { useChatStore, type ChatTabConfig } from '../stores/useChatStore'
import { captureChatIdentity, isChatIdentityCurrent } from './chatIdentity'
import { useAuthStore } from '../stores/useAuthStore'
import type { ChatSubmissionOptions } from './chatSubmissionTarget'

type Submit = (message: string, options: ChatSubmissionOptions) => Promise<boolean>
type Prepare = (messages: string[]) => { message: string; isAutoNotification: boolean }
let submit: Submit | undefined
let prepare: Prepare | undefined
let notify: ((message: string) => void) | undefined
let subscribed = false
let scheduled = false
const pending = new Set<string>()
const accepted = new Map<string, number>()
const ACCEPTED_RECEIPT_TTL_MS = 2 * 60 * 1000

type QueueReceipt = NonNullable<ChatTabConfig['queuedSubmission']>

function matchesReceipt(queue: string[], receipt: QueueReceipt): boolean {
  const prefix = receipt.queuePrefix ?? receipt.messages
  return prefix.every((message, index) => queue[index] === message)
}

// withoutDelivered removes one occurrence of each delivered message, so other
// entries added, edited or reordered meanwhile stay queued and nothing the
// server accepted is ever shown again as pending.
export function withoutDelivered(queue: string[], delivered: string[]): string[] {
  const remaining = [...queue]
  for (const message of delivered) {
    const index = remaining.indexOf(message)
    if (index >= 0) remaining.splice(index, 1)
  }
  return remaining
}

function receiptKey(identity: number, sessionId: string, receipt: QueueReceipt): string {
  return `${identity}:${sessionId}:${JSON.stringify(receipt.messages)}`
}

function wasRecentlyAccepted(key: string, now = Date.now()): boolean {
  for (const [candidate, acceptedAt] of accepted) {
    if (now - acceptedAt > ACCEPTED_RECEIPT_TTL_MS) accepted.delete(candidate)
  }
  return accepted.has(key)
}

async function deliverQueueReceipt(tabId: string, receipt: QueueReceipt, send: Submit, build: Prepare, verifyIdle: boolean): Promise<boolean> {
  const identity = captureChatIdentity()
  const store = useChatStore.getState()
  const sessionId = receipt.sessionId
  // A conversation can be projected into more than one UI tab. The backend
  // submission belongs to the session, so those projections must share one
  // delivery lane as well.
  const laneKey = `${identity}:${sessionId}`
  const acceptedKey = receiptKey(identity, sessionId, receipt)
  if (pending.has(laneKey)) return false
  pending.add(laneKey)
  const owns = () => isChatIdentityCurrent(identity) && useChatStore.getState().getTab(tabId)?.sessionId === sessionId
  try {
    // Persist the exact snapshot before the first await. New queue entries may
    // be appended while runtime verification or delivery is in progress.
    store.setTabConfig(tabId, { queuedSubmission: receipt, isQueueProcessing: true })
    if (verifyIdle) await store.getActiveSessions(true)
    if (!owns()) return false
    const fresh = useChatStore.getState().getTab(tabId)
    if (!fresh || (verifyIdle && fresh.isStreaming)) return false
    if (!matchesReceipt(fresh.config.queuedMessages || [], receipt)) {
      if (receipt.accepted) {
        // Already delivered: drop exactly what was sent, keep everything else.
        store.setTabConfig(tabId, { queuedMessages: withoutDelivered(fresh.config.queuedMessages || [], receipt.messages), queuedSubmission: undefined, queueError: undefined })
        return true
      }
      // Not sent yet and the queue changed: discard this snapshot; the next
      // drain takes a fresh one.
      store.setTabConfig(tabId, { queuedSubmission: undefined })
      return false
    }
    const built = build(receipt.messages)
    const alreadyAccepted = receipt.accepted || wasRecentlyAccepted(acceptedKey)
    const deliveryAccepted = alreadyAccepted || !built.message.trim() || await send(built.message, {
      sourceTabId: tabId, sourceSessionId: sessionId, identity,
      submissionId: receipt.id, isAutoNotification: built.isAutoNotification,
      queuedDelivery: true,
    })
    // Remember server acceptance before checking whether this particular UI
    // projection still owns the tab. A second projection must not resend an
    // accepted message merely because the first one was closed or replaced.
    if (deliveryAccepted) accepted.set(acceptedKey, Date.now())
    if (!owns()) return false
    if (!deliveryAccepted) throw new Error('Queued delivery was not confirmed. The message remains queued for retry.')
    // Save acknowledgement before consuming the local queue. Even if another
    // view edited it, retry must never dispatch this accepted receipt again.
    const acknowledged = { ...receipt, accepted: true }
    store.setTabConfig(tabId, { queuedSubmission: acknowledged })
    const current = useChatStore.getState().getTabConfig(tabId)?.queuedMessages || []
    const remaining = !matchesReceipt(current, receipt)
      ? withoutDelivered(current, receipt.messages)
      : receipt.queueIndex === undefined
        ? current.slice(receipt.messages.length)
        : current.filter((_message, index) => index !== receipt.queueIndex)
    store.setTabConfig(tabId, { queuedMessages: remaining, queuedSubmission: undefined, queueError: undefined })
    return true
  } catch (error) {
    if (!owns()) return false
    const message = error instanceof Error ? error.message : 'Queued delivery failed; message kept.'
    store.setTabConfig(tabId, { queueError: message })
    notify?.(message)
    return false
  } finally {
    pending.delete(laneKey)
    if (owns()) store.setTabConfig(tabId, { isQueueProcessing: false })
  }
}

export function resetChatQueueDeliveryReceiptsForTests(): void {
  pending.clear()
  accepted.clear()
}

/** Selection is presentation only. The snapshot owns its session and receipt. */
export async function drainChatQueue(tabId: string, send: Submit, build: Prepare): Promise<void> {
  const tab = useChatStore.getState().getTab(tabId)
  // With a session, the server's durable conversation-turn dispatcher orders
  // queued input behind a running turn, so delivery never waits on this tab's
  // own (possibly stale) streaming state.
  if (!tab?.sessionId || tab.config?.queueError || !tab.config?.queuedMessages?.length) return
  const saved = tab.config.queuedSubmission
  const receipt = saved?.sessionId === tab.sessionId ? saved : {
    id: randomUUID(), sessionId: tab.sessionId, messages: [...tab.config.queuedMessages],
  }
  await deliverQueueReceipt(tabId, receipt, send, build, false)
}

/** Manual Send now/Steer uses the same receipt and lock as background delivery. */
export async function sendQueuedChatMessage(tabId: string, index: number, message: string, send: Submit): Promise<boolean> {
  const tab = useChatStore.getState().getTab(tabId)
  if (!tab?.sessionId || tab.config?.queuedMessages?.[index] !== message) return false
  const saved = tab.config.queuedSubmission
  if (saved && (saved.sessionId !== tab.sessionId || saved.queueIndex !== index || saved.messages[0] !== message)) return false
  const receipt: QueueReceipt = saved ?? {
    id: randomUUID(), sessionId: tab.sessionId, messages: [message],
    queueIndex: index, queuePrefix: tab.config.queuedMessages.slice(0, index + 1),
  }
  return deliverQueueReceipt(tabId, receipt, send, messages => ({ message: messages[0], isAutoNotification: false }), false)
}

function schedule() {
  if (scheduled) return
  scheduled = true
  queueMicrotask(() => {
    scheduled = false
    if (!submit || !prepare) return
    const auth = useAuthStore.getState()
    if (!auth.isMultiUserModeChecked || auth.isLoading || (auth.isMultiUserMode && !auth.isAuthenticated)) return
    for (const tab of Object.values(useChatStore.getState().chatTabs)) {
      if (tab.sessionId && tab.config?.queuedMessages?.length && !tab.config.queueError) {
        void drainChatQueue(tab.tabId, submit, prepare)
      }
    }
  })
}

/** The store, not a selected ChatArea, owns the worker. Replacing the view only
 * updates its dispatch adapter; identity checks fence outstanding operations. */
export function configureChatQueueController(send: Submit, build: Prepare, onError: (message: string) => void) {
  submit = send
  prepare = build
  notify = onError
  if (!subscribed) {
    subscribed = true
    useChatStore.subscribe(schedule)
    useAuthStore.subscribe(schedule)
  }
  schedule()
}
