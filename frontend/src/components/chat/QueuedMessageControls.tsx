import React from 'react'
import { Clock, Loader2 } from 'lucide-react'
import { useQueuedMessageControlsStore } from '../../stores/useQueuedMessageControlsStore'

const ordinal = (n: number) => `${n}${n % 100 >= 11 && n % 100 <= 13 ? 'th' : ['th', 'st', 'nd', 'rd'][n % 10] ?? 'th'}`

/** Edit, Steer now, Retry and remove on a message that is waiting in the server queue, next to its clock and time. */
export const QueuedMessageControls: React.FC<{ sessionId?: string; metadata?: Record<string, unknown> }> = ({ sessionId, metadata }) => {
  const clientId = typeof metadata?.client_message_id === 'string' ? metadata.client_message_id : ''
  const controls = useQueuedMessageControlsStore(state => sessionId ? state.bySession[sessionId] : undefined)
  const message = clientId ? controls?.messages[clientId] : undefined
  if (!controls || !message?.message_id || message.can_manage !== true) return null
  const refreshing = Boolean(message.refreshing && !message.delivery_error)
  const pending = Boolean(message.steering || controls.busyMessageId === message.message_id)
  const disabled = pending || Boolean(controls.busyMessageId)
  const reason = message.delivery_error || message.queue_detail || (refreshing
    ? 'This Crew changed (for example a new function), so the chat restarts with it after the current reply, then your message goes in'
    : message.steering ? 'Delivering to the current reply'
      : message.queue_reason === 'starting' ? 'The agent is starting or resuming. Your message sends automatically.'
        : message.queue_reason === 'running' ? 'Waiting for the current reply' : 'Sends next')
  const position = message.queue_position && message.queue_position > 1 ? `${ordinal(message.queue_position)} in line` : ''
  const link = 'text-primary hover:underline disabled:opacity-40 disabled:no-underline'
  const parts: React.ReactNode[] = []
  if (pending) parts.push(<span key="p" className="inline-flex items-center gap-1"><Loader2 aria-hidden="true" className="h-3 w-3 animate-spin" />{message.steering ? 'Steering…' : 'Updating…'}</span>)
  if (controls.editable && !message.delivery_error && !message.steering) parts.push(<button key="e" type="button" className={link} disabled={!controls.canEdit || disabled || message.attachment_pending}
    title={message.attachment_pending ? 'Attachments are still uploading' : controls.canEdit ? 'Move back to the box to edit · ↑' : 'Clear the box to edit this message'}
    onClick={() => controls.act(message, 'edit')}>Edit</button>)
  if (message.delivery_error) parts.push(<button key="r" type="button" className={link} disabled={disabled} onClick={() => controls.act(message, 'retry')}>Retry</button>)
  if (message.can_steer === true && !message.delivery_error && !refreshing && !message.steering) parts.push(<button key="s" type="button" className={link} disabled={disabled} title="Send it into the current reply" onClick={() => controls.act(message, 'steer')}>Steer now</button>)
  parts.push(<button key="x" type="button" aria-label="Remove queued message" title="Remove from the queue" disabled={disabled} onClick={() => controls.act(message, 'remove')} className="text-muted-foreground hover:text-foreground disabled:opacity-40">✕</button>)
  return (
    <span data-testid="queued-message-controls" title={reason} className="inline-flex flex-wrap items-center gap-x-1.5 text-[11px]" aria-live="polite">
      {position && <span className="text-amber-500">{position}</span>}
      {parts.map((part, i) => <React.Fragment key={i}>{i > 0 && <span aria-hidden="true" className="text-border">·</span>}{part}</React.Fragment>)}
    </span>
  )
}

/** One short line under a queued message when it needs attention: delivery unconfirmed, or waiting for the chat to restart with the Crew's latest setup. */
export const QueuedMessageNotice: React.FC<{ sessionId?: string; metadata?: Record<string, unknown> }> = ({ sessionId, metadata }) => {
  const clientId = typeof metadata?.client_message_id === 'string' ? metadata.client_message_id : ''
  const message = useQueuedMessageControlsStore(state => sessionId && clientId ? state.bySession[sessionId]?.messages[clientId] : undefined)
  if (!message) return null
  const text = message.delivery_error ? `Delivery unconfirmed. ${message.delivery_error}` : message.refreshing ? "Sends after the chat restarts with the Crew's latest setup." : ''
  return text ? <p className="mt-0.5 max-w-full text-[11px] text-amber-500">{text}</p> : null
}

/** Incoming queue state stays in the card header, without a separate status row. */
export const QueuedInboxStatus: React.FC<{ sessionId?: string; metadata?: Record<string, unknown> }> = ({ sessionId, metadata }) => {
  const clientId = typeof metadata?.client_message_id === 'string' ? metadata.client_message_id : ''
  const message = useQueuedMessageControlsStore(state => sessionId && clientId ? state.bySession[sessionId]?.messages[clientId] : undefined)
  if (!message) return null
  const reason = message.delivery_error || message.queue_detail || 'Waiting for the current reply'
  return <span title={reason} aria-label={reason} className="inline-flex text-amber-500"><Clock aria-hidden="true" className="h-3.5 w-3.5" /></span>
}
