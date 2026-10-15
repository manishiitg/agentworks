import React, { useEffect, useState } from 'react'
import { Check, CheckCheck, CircleAlert, Clock } from 'lucide-react'
import { deliveryTickState, deliveryTickTitle } from './deliveryTickState'
import { recordChatSubmissionTelemetry } from '../../../utils/chatDeliveryTelemetry'

const TICK_ICON = {
  queued: Clock,
  confirmed: CheckCheck,
  unflushed: Clock,
  failed: CircleAlert,
  fast: Check,
} as const

// Icon delivery receipt for a live-input user row. Shared by the terminal
// transcript and the event-card view; renders nothing without delivery
// metadata so plain query rows are untouched. Receipts stay visible.
export const DeliveryTick: React.FC<{ metadata: Record<string, unknown> | undefined }> = ({ metadata }) => {
  const state = deliveryTickState(metadata)
  if (!state) return null
  const Icon = TICK_ICON[state]
  // Debug-level visibility: near-invisible grays. Failed keeps only a
  // faint red whisper.
  const tone = state === 'confirmed'
    ? 'text-slate-300 dark:text-slate-500'
    : state === 'queued'
      ? 'text-amber-400 dark:text-amber-500'
      : state === 'unflushed'
      ? 'text-stone-300 dark:text-stone-600'
      : state === 'failed'
        ? 'text-red-300/70 dark:text-red-400/50'
        : 'text-slate-300 dark:text-slate-600'
  return (
    <span data-testid="delivery-tick" data-state={state} title={deliveryTickTitle(metadata, state)} className={`shrink-0 select-none leading-none ${tone}`} aria-label={deliveryTickTitle(metadata, state)}>
      <Icon className="h-3 w-3" aria-hidden="true" />
    </span>
  )
}

// A failed live input is lost unless the user notices a faint icon and
// retypes it. Say so next to the message and offer a one-click resend
// through the chat's normal send path.
export const DeliveryFailedResend: React.FC<{ metadata: Record<string, unknown> | undefined; text: string; onResend?: (text: string) => void; sessionId?: string }> = ({ metadata, text, onResend, sessionId }) => {
  const [resent, setResent] = useState(false)
  const failed = deliveryTickState(metadata) === 'failed'
  const messageId = typeof metadata?.message_id === 'string' ? metadata.message_id : ''
  const clientMessageId = typeof metadata?.client_message_id === 'string' ? metadata.client_message_id : ''
  // Content-free: lets a screenshot of "Not delivered" be matched to the server's [DELIVERY] line by id.
  useEffect(() => {
    if (!failed || !sessionId) return
    recordChatSubmissionTelemetry('receipt_shown', sessionId, clientMessageId || messageId, { eventId: messageId, deliveryStatus: 'failed' })
  }, [failed, sessionId, clientMessageId, messageId])
  if (deliveryTickState(metadata) !== 'failed' || !onResend || !text.trim()) return null
  if (resent) return <span className="text-muted-foreground">Resent</span>
  return (
    <span data-testid="delivery-failed-resend" className="text-red-500/90 dark:text-red-400/80">
      Not delivered ·{' '}
      <button
        type="button"
        className="underline underline-offset-2 hover:text-red-600 dark:hover:text-red-300"
        onClick={() => { setResent(true); onResend(text) }}
      >
        Resend
      </button>
    </span>
  )
}
