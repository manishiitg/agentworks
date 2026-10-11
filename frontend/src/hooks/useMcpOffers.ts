import { useCallback, useEffect, useState } from 'react'
import type { McpOfferCatalog } from '../api/mcpOfferMetadata'

const empty: McpOfferCatalog = { freshnessDays: 30, offers: Object.create(null) }

/** One shared public feed, loaded only while browsing available MCPs. */
export function useMcpOffers(enabled: boolean) {
  const [catalog, setCatalog] = useState(empty)
  const refresh = useCallback(() => {
    void import('../api/mcpOffers').then(api => api.refreshMcpOffers(true)).then(setCatalog).catch(() => {})
  }, [])
  useEffect(() => {
    if (!enabled) return
    let active = true
    // The feed loader and offline evidence stay out of the startup bundle.
    void import('../api/mcpOffers').then(api => {
      if (!active) return
      setCatalog(api.cachedMcpOffers())
      return api.refreshMcpOffers().then(value => { if (active) setCatalog(value) })
    }).catch(() => { /* Offer metadata is optional; keep connection setup usable. */ })
    return () => { active = false }
  }, [enabled])
  return { catalog, refresh }
}
