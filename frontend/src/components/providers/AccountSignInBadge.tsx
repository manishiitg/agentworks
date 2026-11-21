import { useCallback, useEffect, useState } from 'react'
import { llmConfigService } from '../../services/llm-config-api'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { openSubscriptionLimits } from '../../utils/subscriptionLimitsNavigation'

/** Header badge: how many AI accounts you can see need a new sign-in. The server counts; this only shows it. */
export function AccountSignInBadge() {
  const [count, setCount] = useState(0)
  const refresh = useCallback(() => {
    void llmConfigService.getSignInNeeded().then(setCount).catch(() => setCount(0))
  }, [])
  useEffect(refresh, [refresh])
  useLiveRefetch(refresh, { kinds: ['account_limits'], fallbackMs: 120_000, minIntervalMs: 5000, refreshOnFocus: true })
  if (count === 0) return null
  return <button type="button" onClick={openSubscriptionLimits} title="Open Accounts and usage" className="flex h-8 items-center rounded-full border border-destructive/40 px-2 text-xs text-destructive hover:bg-destructive/10">
    {count === 1 ? '1 account needs sign-in' : `${count} accounts need sign-in`}
  </button>
}
