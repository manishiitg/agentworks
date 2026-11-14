import type { PendingQueuedMessage } from '../../../shared/session/types'
import React, { useEffect, useState } from 'react'
import { CircleAlert, Clock, Loader2 } from 'lucide-react'
import { useQueuedMessageControlsStore } from '../../stores/useQueuedMessageControlsStore'

/** Only an explicit server receipt clears a stale queue snapshot. */
export function queuedMessageDelivered(metadata?: Record<string, unknown>): boolean {
  // The durable receipt wins even when its original delivery_status remains.
  if (metadata?.confirmation === 'confirmed') return true
  if (metadata?.delivery_status === 'unconfirmed' || metadata?.confirmation === 'failed' || metadata?.confirmation === 'accepted_but_unflushed') return false
  return metadata?.delivery_status === 'sent_to_cli' || metadata?.delivery_status === 'next_turn_started'
}

export function queuedMessageReason(message: PendingQueuedMessage): string {
  return message.delivery_error || message.queue_detail || (message.refreshing || message.queue_reason === 'refreshing'
    ? "Updated setup applies next turn. You can steer into the current reply."
    : message.steering ? 'Delivering to the current reply'
      : message.queue_reason === 'starting' ? 'The agent is starting or resuming. Your message sends automatically.'
        : message.queue_reason === 'running' ? 'Waiting for the current reply' : 'Sends next')
}

const ordinal = (n: number) => `${n}${n % 100 >= 11 && n % 100 <= 13 ? 'th' : ['th', 'st', 'nd', 'rd'][n % 10] ?? 'th'}`

/** The server decides whether age is shown; this component only formats it. */
export function QueuedWaitingAge({ since }: { since?: string }) {
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    if (!since) return
    setNow(Date.now())
    const timer = setInterval(() => setNow(Date.now()), 60_000)
    return () => clearInterval(timer)
  }, [since])
  const started = Date.parse(since || '')
  if (!since || !Number.isFinite(started)) return null
  const minutes = Math.max(0, Math.floor((now - started) / 60_000))
  return <span className="text-amber-500">Waiting {minutes} min</span>
}

/** Edit, Steer now, Retry and remove on a message that is waiting in the server queue, next to its clock and time. */
export const QueuedMessageControls: React.FC<{ sessionId?: string; metadata?: Record<string, unknown> }> = ({ sessionId, metadata }) => {
  const clientId = typeof metadata?.client_message_id === 'string' ? metadata.client_message_id : ''
  const controls = useQueuedMessageControlsStore(state => sessionId ? state.bySession[sessionId] : undefined)
  const message = clientId ? controls?.messages[clientId] : undefined
  if (queuedMessageDelivered(metadata) || !controls || !message?.message_id || (message.can_manage !== true && message.can_dismiss !== true)) return null
  if (message.can_manage !== true) return <button type="button" disabled={controls.busyMessageId === message.message_id}
    onClick={() => controls.act(message, 'remove')} className="text-[11px] text-muted-foreground hover:text-foreground disabled:opacity-40">Dismiss</button>
  const refreshing = Boolean(message.refreshing && !message.delivery_error)
  const pending = Boolean(message.steering || controls.busyMessageId === message.message_id)
  const disabled = pending || Boolean(controls.busyMessageId)
  const reason = queuedMessageReason(message)
  const position = message.queue_position && message.queue_position > 1 ? `${ordinal(message.queue_position)} in line` : ''
  const link = 'text-primary hover:underline disabled:opacity-40 disabled:no-underline'
  const parts: React.ReactNode[] = []
  if (pending) parts.push(<span key="p" className="inline-flex items-center gap-1"><Loader2 aria-hidden="true" className="h-3 w-3 animate-spin" />{message.steering ? 'Steering…' : 'Updating…'}</span>)
  if (controls.editable && !message.delivery_error && !message.steering) parts.push(<button key="e" type="button" className={link} disabled={!controls.canEdit || disabled || message.attachment_pending}
    title={message.attachment_pending ? 'Attachments are still uploading' : controls.canEdit ? 'Move back to the box to edit · ↑' : 'Clear the box to edit this message'}
    onClick={() => controls.act(message, 'edit')}>Edit</button>)
  if (message.delivery_error) parts.push(<button key="r" type="button" className={link} disabled={disabled} onClick={() => controls.act(message, 'retry')}>Retry</button>)
  if (message.can_steer === true && !message.delivery_error && !message.steering) parts.push(<button key="s" type="button" className={link} disabled={disabled} title="Send it into the current reply" onClick={() => controls.act(message, 'steer')}>Steer now</button>)
  parts.push(<button key="x" type="button" aria-label="Remove queued message" title="Remove from the queue" disabled={disabled} onClick={() => controls.act(message, 'remove')} className="text-muted-foreground hover:text-foreground disabled:opacity-40">✕</button>)
  return (
    <span data-testid="queued-message-controls" title={reason} className="inline-flex flex-wrap items-center gap-x-1.5 text-[11px]" aria-live="polite">
      {position && <span className="text-amber-500">{position}</span>}
      <QueuedWaitingAge since={message.waiting_since} />
      {parts.map((part, i) => <React.Fragment key={i}>{i > 0 && <span aria-hidden="true" className="text-border">·</span>}{part}</React.Fragment>)}
    </span>
  )
}

/** Keep actionable delivery failures; ordinary queue reasons live on the clock. */
export const QueuedMessageNotice: React.FC<{ sessionId?: string; metadata?: Record<string, unknown> }> = ({ sessionId, metadata }) => {
  const clientId = typeof metadata?.client_message_id === 'string' ? metadata.client_message_id : ''
  const message = useQueuedMessageControlsStore(state => sessionId && clientId ? state.bySession[sessionId]?.messages[clientId] : undefined)
  if (queuedMessageDelivered(metadata)) return null
  if (!message) return metadata?.delivery_status === 'unconfirmed' ? <p className="mt-0.5 text-[11px] text-amber-500">Delivery unconfirmed. It will not be sent again automatically.</p> : null
  const text = message.delivery_error ? `Delivery unconfirmed. ${message.delivery_error}` : message.queue_reason === 'provider_limit' ? message.queue_detail : ''
  return text ? <p className="mt-0.5 max-w-full text-[11px] text-amber-500">{text}</p> : null
}

/** Incoming queue state stays in the card header, without a separate status row. */
export const QueuedInboxStatus: React.FC<{ sessionId?: string; metadata?: Record<string, unknown> }> = ({ sessionId, metadata }) => {
  const clientId = typeof metadata?.client_message_id === 'string' ? metadata.client_message_id : ''
  const message = useQueuedMessageControlsStore(state => sessionId && clientId ? state.bySession[sessionId]?.messages[clientId] : undefined)
  if (queuedMessageDelivered(metadata)) return null
  if (!message) return metadata?.delivery_status === 'unconfirmed' ? <span title="Delivery unconfirmed" aria-label="Delivery unconfirmed" className="inline-flex items-center gap-1 text-amber-500"><CircleAlert aria-hidden="true" className="h-3.5 w-3.5" /></span> : null
  const reason = queuedMessageReason(message)
  const Icon = message.delivery_error ? CircleAlert : Clock
  return <span title={reason} aria-label={reason} className="inline-flex items-center gap-1 text-amber-500"><Icon aria-hidden="true" className="h-3.5 w-3.5" /><QueuedWaitingAge since={message.waiting_since} /></span>
}
