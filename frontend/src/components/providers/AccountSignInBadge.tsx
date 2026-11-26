import { useCallback, useEffect, useState } from 'react'
import { llmConfigService } from '../../services/llm-config-api'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'

/** How many AI accounts you can see need a new sign-in. The server counts; this only reads it. */
export function useAccountSignInNeeded(enabled = true): number {
  const [count, setCount] = useState(0)
  const refresh = useCallback(() => {
    if (!enabled) {
      setCount(0)
      return
    }
    void llmConfigService.getSignInNeeded().then(setCount).catch(() => setCount(0))
  }, [enabled])
  useEffect(refresh, [refresh])
  useLiveRefetch(refresh, { enabled, kinds: ['account_limits'], fallbackMs: 120_000, minIntervalMs: 5000, refreshOnFocus: true })
  return enabled ? count : 0
}

export function accountSignInLabel(count: number): string {
  if (count <= 0) return ''
  return count === 1 ? '1 account needs sign-in' : `${count} accounts need sign-in`
}
