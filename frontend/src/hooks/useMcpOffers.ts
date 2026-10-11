import { useCallback, useEffect, useState } from 'react'
import { cachedMcpOffers, refreshMcpOffers } from '../api/mcpOffers'

/** One shared public feed, loaded only while browsing available MCPs. */
export function useMcpOffers(enabled: boolean) {
  const [catalog, setCatalog] = useState(cachedMcpOffers)
  const refresh = useCallback(() => { void refreshMcpOffers(true).then(setCatalog) }, [])
  useEffect(() => {
    if (!enabled) return
    let active = true
    void refreshMcpOffers().then(value => { if (active) setCatalog(value) })
    return () => { active = false }
  }, [enabled])
  return { catalog, refresh }
}
