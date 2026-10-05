import { deliveryTickState } from '../events/system/deliveryTickState'
import { providerShortLabel } from '../../utils/chatHistoryRuntimeLabel'

/** A changed provider applies from the next message: the running turn finishes on the old one. */
export function providerChangePending(turnRunning: boolean, runningProvider: string | undefined, selectedProvider: string | undefined): boolean {
  return Boolean(turnRunning && runningProvider && selectedProvider && runningProvider !== selectedProvider)
}

/** Under the provider picker of a chat that has a turn running. Renders nothing otherwise. */
export function ProviderChangeNotice({ turnRunning, runningProvider, selectedProvider }: {
  turnRunning: boolean
  runningProvider?: string
  selectedProvider?: string
}) {
  if (!providerChangePending(turnRunning, runningProvider, selectedProvider)) return null
  return (
    <p data-testid="provider-change-notice" className="mt-3 break-words text-xs text-amber-700 dark:text-amber-400">
      Applies from your next message. The current turn finishes on {providerShortLabel(runningProvider!)}.
    </p>
  )
}

/** Under a queued message whose send chose a different provider than the running turn. */
export function QueuedProviderLabel({ metadata }: { metadata: Record<string, unknown> | undefined }) {
  const pending = metadata?.pending_provider
  if (typeof pending !== 'string' || !pending || deliveryTickState(metadata) !== 'queued') return null
  return (
    <span data-testid="queued-provider-label" className="break-words">
      then runs on {providerShortLabel(pending)}
    </span>
  )
}
