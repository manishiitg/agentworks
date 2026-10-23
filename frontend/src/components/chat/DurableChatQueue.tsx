import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowUp, Clock, Loader2, X } from 'lucide-react'
import { agentApi } from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { clientMessageEventId, isProvisionalUserMessage, userMessageMetadata } from '../../utils/clientMessageIdentity'
import { getDisplaySafeUserMessageContent } from '../../utils/chatMessageContent'
import { askAIDisplayText } from '../../utils/askAIMessage'
import type { PendingQueuedMessage } from '../../../shared/session/types'

export interface DurableChatQueueHandle { recallLatest: () => boolean }

/** This is the server queue, never a second browser dispatch worker. */
export const DurableChatQueue = forwardRef<DurableChatQueueHandle, {
  sessionId: string; tabId: string; busy: boolean
  canEdit?: boolean; onEdit?: (content: string) => void
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
    const currentIds = new Set(query.data.map(message => message.client_message_id))
    const removedIds = new Set(previous.current.filter(message => !message.steering && !currentIds.has(message.client_message_id))
      .map(message => clientMessageEventId(message.client_message_id)))
    previous.current = query.data
    if (removedIds.size) {
      const store = useChatStore.getState()
      const events = store.getTabEvents(sessionId)
      // A removed/started queue row can no longer remain a queued provisional
      // bubble. A durable user echo is always retained.
      store.setTabEvents(sessionId, events.filter(event => !removedIds.has(event.id) || !isProvisionalUserMessage(event) || userMessageMetadata(event).delivery_status !== 'queued_for_turn' || userMessageMetadata(event).confirmation === 'failed'))
    }
  }, [query.data, sessionId, owns])

  const change = async (message: PendingQueuedMessage, steer: boolean, retry = false, edit = false) => {
    if (!message.message_id || action || !owns()) return
    setAction(message.message_id)
    setError('')
    try {
      if (retry) await agentApi.retryQueuedMessage(sessionId, message.message_id)
      else if (steer) await agentApi.steerQueuedMessage(sessionId, message.message_id)
      else await agentApi.removeQueuedMessage(sessionId, message.message_id)
      if (!owns()) return
      if (steer) {
        const store = useChatStore.getState()
        const id = clientMessageEventId(message.client_message_id)
        store.setTabEvents(sessionId, store.getTabEvents(sessionId).map(event => {
          if (event.id !== id || !isProvisionalUserMessage(event)) return event
          const outer = event.data as unknown as Record<string, unknown>
          const inner = (outer.data ?? {}) as Record<string, unknown>
          return { ...event, data: { ...outer, data: { ...inner, metadata: { ...userMessageMetadata(event), confirmation: undefined, delivery_status: 'delivering' } } } } as typeof event
        }))
      } else if (!retry) {
        const store = useChatStore.getState()
        const id = clientMessageEventId(message.client_message_id)
        store.setTabEvents(sessionId, store.getTabEvents(sessionId).filter(event => event.id !== id || !isProvisionalUserMessage(event)))
      }
      if (edit) onEdit?.(message.content)
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
  const messages = query.data ?? []
  useImperativeHandle(ref, () => ({ recallLatest: () => {
    if (!canEdit || !onEdit || !owns()) return false
    const message = [...messages].reverse().find(message => message.message_id && !message.steering && !message.delivery_error)
    if (!message) return false
    if (!action) void change(message, false, false, true)
    return true
  } }))
  if (!messages.length && !error && !query.isError) return null
  return <div className="mb-1 space-y-1" aria-label="Queued messages" aria-live="polite">
    {(error || query.isError) && <p role="alert" className="text-xs text-amber-500">{error || 'Could not refresh queued messages.'}
      <button type="button" className="ml-2 underline" onClick={() => void query.refetch()}>Retry</button>
    </p>}
    {messages.map(message => {
      const refreshing = Boolean(message.refreshing && !message.delivery_error)
      const pending = Boolean(message.steering || action === message.message_id)
      const Icon = pending || refreshing ? Loader2 : Clock
      const text = askAIDisplayText(getDisplaySafeUserMessageContent(message.content))
      const reason = message.queue_reason
      const description = message.delivery_error || (refreshing ? 'This Crew changed (for example a new function), so the chat restarts with it after the current reply, then your message goes in' : message.steering ? 'Delivering to the current task' : reason === 'starting' ? 'The agent is starting or resuming. Your message will send automatically.' : reason === 'running' ? 'Sends automatically when the current turn finishes' : 'Waiting to send automatically')
      return <div key={message.message_id || message.client_message_id} className="rounded-md border border-amber-500/25 bg-amber-500/5 px-2 py-1.5">
        <div className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-2">
          <span title={description} className="flex items-center gap-1 text-[11px] font-medium text-amber-500">
            <Icon className={`h-3 w-3 shrink-0 ${pending || refreshing ? 'animate-spin' : ''}`} />
            {message.delivery_error ? 'Delivery unconfirmed' : refreshing ? 'Sends after the current reply · chat restarts with the Crew\'s latest setup' : message.steering ? 'Steering…' : `${reason === 'starting' ? 'Agent starting · sends when ready' : reason === 'running' ? 'Sends after the current reply' : 'Queued · sends next'}${message.queue_position && message.queue_position > 1 ? ` · ${message.queue_position} in line` : ''}`}
          </span>
          <details className="group min-w-0 text-xs text-foreground">
            <summary title={text} className="cursor-pointer list-none truncate [&::-webkit-details-marker]:hidden group-open:max-h-32 group-open:overflow-y-auto group-open:whitespace-pre-wrap group-open:break-words">{text}</summary>
          </details>
          {message.message_id && <div className="flex items-center gap-2 whitespace-nowrap text-[11px]">
            {onEdit && !message.delivery_error && !message.steering && <button type="button" aria-label="Edit queued message" title={canEdit ? 'Move back to the composer to edit · ↑' : 'Clear the composer to edit this queued message'} disabled={!canEdit || pending || Boolean(action)} onClick={() => void change(message, false, false, true)} className="inline-flex h-6 items-center gap-1 px-1 justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-40"><ArrowUp aria-hidden="true" className="h-3.5 w-3.5" /><span>Edit</span></button>}
            {message.delivery_error && <button type="button" disabled={pending || Boolean(action)} onClick={() => void change(message, false, true)} className="text-primary hover:underline disabled:opacity-40">Retry</button>}
            {message.can_steer === true && !message.delivery_error && !refreshing && !message.steering && <button type="button" disabled={pending || Boolean(action)} onClick={() => void change(message, true)} className="text-primary hover:underline disabled:opacity-40">Steer now</button>}
            <button type="button" aria-label="Remove queued message" title="Remove queued message" disabled={pending || Boolean(action)} onClick={() => void change(message, false)} className="inline-flex h-6 w-6 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-40"><X aria-hidden="true" className="h-3.5 w-3.5" /></button>
          </div>}
        </div>
        {message.delivery_error && <p className="mt-1 text-[11px] text-muted-foreground">{message.delivery_error}</p>}
      </div>
    })}
  </div>
})
