import { useState } from 'react'
import { useQueuedMessageControlsStore } from '../../stores/useQueuedMessageControlsStore'

/** Same current-chat queue projection/actions as the composer; no extra request or timer. */
export function UsageLimitRecoveryNotice({ sessionId, eventId }: { sessionId: string; eventId: string }) {
  const controls = useQueuedMessageControlsStore(state => state.bySession[sessionId])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  if (!controls) return null // Inspectors do not host the current-chat queue.
  const recovery = controls.recovery
  if (recovery?.event_id && recovery.event_id !== eventId) return null
  const reset = recovery?.retry_at ? new Date(recovery.retry_at) : null
  const message = recovery?.state === 'scheduled'
    ? `Will resume automatically${reset && Number.isFinite(reset.getTime()) ? ` at ${reset.toLocaleString()}` : ' when the limit resets'}.`
    : recovery?.state === 'started' ? 'Automatic continuation started.'
    : recovery?.state === 'not_scheduled' ? 'No automatic continuation is scheduled.'
    : 'Automatic continuation status is unavailable.'
  return <div data-testid="usage-limit-recovery-status" className="mt-2 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
    <span role="status">{message}</span>
    {recovery?.state === 'scheduled' && recovery.can_cancel && controls.cancelRecovery && <button type="button" disabled={busy} className="underline disabled:opacity-50" onClick={async () => {
      setBusy(true); setError('')
      try { await controls.cancelRecovery?.() } catch { setError('Could not cancel automatic continuation. Refresh its status and try again.') }
      finally { setBusy(false) }
    }}>{busy ? 'Cancelling…' : 'Cancel auto-resume'}</button>}
    {error && <span role="alert">{error}</span>}
  </div>
}
