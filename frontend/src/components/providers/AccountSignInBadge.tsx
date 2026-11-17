import { useCallback, useEffect, useState } from 'react'
import { llmConfigService } from '../../services/llm-config-api'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { openSubscriptionLimits } from '../../utils/subscriptionLimitsNavigation'
import { hasDesktopConnections } from '../../stores/useConnectedWorkStore'
import { useSignInNeededByPlace } from './AccountsByServer'

const badgeClass = 'flex h-8 items-center rounded-full border border-destructive/40 px-2 text-xs text-destructive hover:bg-destructive/10'
const label = (count: number) => count === 1 ? '1 account needs sign-in' : `${count} accounts need sign-in`

/** Header badge: AI accounts that need a new sign-in. Each server counts its own; on the Mac there is one badge per place. */
export function AccountSignInBadge() {
  return hasDesktopConnections() ? <PlaceBadges /> : <ServerBadge />
}

function ServerBadge() {
  const [count, setCount] = useState(0)
  const refresh = useCallback(() => {
    void llmConfigService.getSignInNeeded().then(setCount).catch(() => setCount(0))
  }, [])
  useEffect(refresh, [refresh])
  useLiveRefetch(refresh, { kinds: ['account_limits'], fallbackMs: 120_000, minIntervalMs: 5000, refreshOnFocus: true })
  if (count === 0) return null
  return <button type="button" onClick={() => openSubscriptionLimits()} title="Open Accounts and usage" className={badgeClass}>{label(count)}</button>
}

function PlaceBadges() {
  const places = useSignInNeededByPlace().filter(place => place.count)
  if (places.length === 0) return null
  return <>{places.map(place => <button key={place.id} type="button" onClick={() => openSubscriptionLimits(place.id)} title={`Open Accounts and usage for ${place.name}`} className={badgeClass}>
    {place.name}: {label(place.count ?? 0)}
  </button>)}</>
}
