import { useCallback, useEffect, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Clock, Loader2 } from 'lucide-react'
import { agentApi } from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { clientMessageEventId, isProvisionalUserMessage, userMessageMetadata } from '../../utils/clientMessageIdentity'
import { getDisplaySafeUserMessageContent } from '../../utils/chatMessageContent'
import { askAIDisplayText } from '../../utils/askAIMessage'
import type { PendingQueuedMessage } from '../../../shared/session/types'

/** This is the server queue, never a second browser dispatch worker. */
export function DurableChatQueue({ sessionId, tabId, busy, canSteer }: {
  sessionId: string; tabId: string; busy: boolean; canSteer: boolean
}) {
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
      store.setTabEvents(sessionId, events.filter(event => !removedIds.has(event.id) || !isProvisionalUserMessage(event) || userMessageMetadata(event).delivery_status !== 'queued_for_turn'))
    }
  }, [query.data, sessionId, owns])

  const change = async (message: PendingQueuedMessage, steer: boolean) => {
    if (!message.message_id || action || !owns()) return
    setAction(message.message_id)
    setError('')
    try {
      if (steer) await agentApi.steerQueuedMessage(sessionId, message.message_id)
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
      } else {
        const store = useChatStore.getState()
        const id = clientMessageEventId(message.client_message_id)
        store.setTabEvents(sessionId, store.getTabEvents(sessionId).filter(event => event.id !== id || !isProvisionalUserMessage(event)))
      }
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
  if (!messages.length && !error && !query.isError) return null
  return <div className="mb-2 space-y-1.5" aria-label="Queued messages" aria-live="polite">
    {(error || query.isError) && <p role="alert" className="text-xs text-amber-500">{error || 'Could not refresh queued messages.'}
      <button type="button" className="ml-2 underline" onClick={() => void query.refetch()}>Retry</button>
    </p>}
    {messages.map(message => {
      const pending = Boolean(message.steering || action === message.message_id)
      const Icon = pending ? Loader2 : Clock
      const text = askAIDisplayText(getDisplaySafeUserMessageContent(message.content))
      return <div key={message.message_id || message.client_message_id} className="rounded-lg border border-amber-500/25 bg-amber-500/5 px-3 py-2">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <span className="flex items-center gap-2 text-xs font-medium text-amber-500">
            <Icon className={`h-3.5 w-3.5 ${pending ? 'animate-spin' : ''}`} />
            {message.delivery_error ? 'Delivery unconfirmed' : message.steering ? 'Steering…' : `Queued · next turn${message.queue_position ? ` · ${message.queue_position}` : ''}`}
          </span>
          {message.message_id && <div className="flex items-center gap-3 text-xs">
            {canSteer && !message.delivery_error && <button type="button" disabled={pending || Boolean(action)} onClick={() => void change(message, true)} className="text-primary hover:underline disabled:opacity-40">Steer now</button>}
            <button type="button" disabled={pending || Boolean(action)} onClick={() => void change(message, false)} className="text-muted-foreground hover:text-foreground disabled:opacity-40">Remove</button>
          </div>}
        </div>
        {text.length > 300 ? <details className="mt-1 text-sm text-foreground">
          <summary className="cursor-pointer break-words">{text.slice(0, 150)}…</summary>
          <p className="mt-1 max-h-48 overflow-y-auto whitespace-pre-wrap break-words">{text}</p>
        </details> : <p className="mt-1 whitespace-pre-wrap break-words text-sm text-foreground">{text}</p>}
        <p className="mt-1 text-[11px] text-muted-foreground">{message.delivery_error || (message.steering ? 'Delivering to the current task' : 'Sends automatically when the current turn finishes')}</p>
      </div>
    })}
  </div>
}
