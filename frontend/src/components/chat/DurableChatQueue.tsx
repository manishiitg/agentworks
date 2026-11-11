import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { agentApi } from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { clientMessageEventId, isProvisionalUserMessage, pendingQueuedProvisionals, userMessageMetadata } from '../../utils/clientMessageIdentity'
import { useQueuedMessageControlsStore } from '../../stores/useQueuedMessageControlsStore'
import type { PendingQueuedMessage } from '../../../shared/session/types'

export interface DurableChatQueueHandle { recallLatest: () => boolean }

/** A queued bubble the server has handed to the agent stays visible as being sent until its durable user row replaces it. */
function markDelivering(sessionId: string, ids: Set<string>) {
  const store = useChatStore.getState()
  const events = store.getTabEvents(sessionId)
  let changed = false
  const next = events.map(event => {
    if (!ids.has(event.id) || !isProvisionalUserMessage(event) || userMessageMetadata(event).delivery_status === 'delivering') return event
    changed = true
    const outer = event.data as unknown as Record<string, unknown>
    const inner = (outer.data ?? {}) as Record<string, unknown>
    return { ...event, data: { ...outer, data: { ...inner, metadata: { ...userMessageMetadata(event), confirmation: undefined, delivery_status: 'delivering' } } } } as typeof event
  })
  if (changed) store.setTabEvents(sessionId, next)
}

/** This is the server queue, never a second browser dispatch worker. */
export const DurableChatQueue = forwardRef<DurableChatQueueHandle, {
  sessionId: string; tabId: string; busy: boolean
  canEdit?: boolean; onEdit?: (content: string, attachments?: string[]) => void
}>(function DurableChatQueue({ sessionId, tabId, busy, canEdit = true, onEdit }, ref) {
  const [identity] = useState(captureChatIdentity)
  const [action, setAction] = useState<string | null>(null)
  const [error, setError] = useState('')
  const previous = useRef<PendingQueuedMessage[]>([])
  const client = useQueryClient()
  const key = ['chat-queued-messages', identity, sessionId] as const
  const owns = useCallback(() => isChatIdentityCurrent(identity) && useChatStore.getState().getTab(tabId)?.sessionId === sessionId, [identity, tabId, sessionId])
  const query = useQuery({
    queryKey: key,
    queryFn: async () => {
      const messages = await agentApi.getQueuedMessages(sessionId)
      if (!owns()) throw new Error('Conversation changed')
      return messages
    },
    enabled: isChatIdentityCurrent(identity),
    refetchInterval: state => busy || state.state.data?.length ? 2000 : 10000,
    refetchOnWindowFocus: true,
  })
  useEffect(() => {
    if (!query.data || !owns()) return
    // Server deliveries have no sending browser to create their pending card.
    // Project the authoritative queue through the same identity-based echo path.
    const store = useChatStore.getState()
    const pending = pendingQueuedProvisionals(query.data, store.getTabEvents(sessionId), sessionId)
    if (pending.length) store.addTabEvents(sessionId, pending)
    const currentIds = new Set(query.data.map(message => message.client_message_id))
    const removedIds = new Set(previous.current.filter(message => !message.steering && !currentIds.has(message.client_message_id))
      .map(message => clientMessageEventId(message.client_message_id)))
    previous.current = query.data
    const startedIds = new Set(query.data.filter(message => message.started).map(message => clientMessageEventId(message.client_message_id)))
    if (startedIds.size) markDelivering(sessionId, startedIds)
    if (removedIds.size) {
      const store = useChatStore.getState()
      const events = store.getTabEvents(sessionId)
      // A removed queue row can no longer remain a queued provisional bubble.
      // A durable user echo is always retained.
      store.setTabEvents(sessionId, events.filter(event => !removedIds.has(event.id) || !isProvisionalUserMessage(event) || userMessageMetadata(event).delivery_status !== 'queued_for_turn' || userMessageMetadata(event).confirmation === 'failed'))
    }
  }, [query.data, sessionId, owns])

  const change = async (message: PendingQueuedMessage, steer: boolean, retry = false, edit = false) => {
    if (message.can_manage !== true || !message.message_id || action || !owns() || (edit && message.attachment_pending)) return
    setAction(message.message_id)
    setError('')
    try {
      let steered = false
      if (retry) await agentApi.retryQueuedMessage(sessionId, message.message_id)
      else if (steer) steered = await agentApi.steerQueuedMessage(sessionId, message.message_id)
      else await agentApi.removeQueuedMessage(sessionId, message.message_id)
      if (!owns()) return
      if (steered) {
        markDelivering(sessionId, new Set([clientMessageEventId(message.client_message_id)]))
      } else if (!steer && !retry) {
        const store = useChatStore.getState()
        const id = clientMessageEventId(message.client_message_id)
        store.setTabEvents(sessionId, store.getTabEvents(sessionId).filter(event => event.id !== id || !isProvisionalUserMessage(event)))
      }
      if (edit) onEdit?.(message.content, message.attachment_paths)
      await client.invalidateQueries({ queryKey: key })
    } catch {
      if (owns()) {
        setError('Could not update this queued message. Refreshing its status…')
        await client.invalidateQueries({ queryKey: key })
      }
    } finally {
      if (owns()) setAction(null)
    }
  }
  // A started message already sits in the chat as a sending bubble until its turn's user row replaces it.
  const messages = (query.data ?? []).filter(message => !message.started)
  useImperativeHandle(ref, () => ({ recallLatest: () => {
    if (!canEdit || !onEdit || !owns()) return false
    const message = [...messages].reverse().find(message => message.can_manage === true && message.message_id && !message.steering && !message.delivery_error && !message.attachment_pending)
    if (!message) return false
    if (!action) void change(message, false, false, true)
    return true
  } }))
  // The queued message's own row in the chat shows its controls (Edit, Steer now, Retry, remove); there is no separate bar.
  const publish = useQueuedMessageControlsStore(state => state.set)
  const changeRef = useRef(change)
  changeRef.current = change
  const data = query.data
  useEffect(() => {
    const byId: Record<string, PendingQueuedMessage> = {}
    for (const message of data ?? []) if (!message.started && message.client_message_id) byId[message.client_message_id] = message
    publish(sessionId, {
      messages: byId,
      busyMessageId: action,
      canEdit,
      editable: Boolean(onEdit),
      act: (message, kind) => void changeRef.current(message, kind === 'steer', kind === 'retry', kind === 'edit'),
    })
  }, [data, action, canEdit, onEdit, sessionId, publish])
  useEffect(() => () => publish(sessionId, undefined), [sessionId, publish])
  if (!error && !query.isError) return null
  return <p role="alert" className="mb-1 text-xs text-amber-500">{error || 'Could not refresh queued messages.'}
    <button type="button" className="ml-2 underline" onClick={() => void query.refetch()}>Retry</button>
  </p>
})
