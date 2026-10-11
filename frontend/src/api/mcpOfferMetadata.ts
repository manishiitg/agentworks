export const FREE_TOOLS_CATALOG_URL = 'https://agentworkshq.com/free-tools/'
export type McpOfferKind = 'recurring' | 'trial' | 'grant' | 'open_source' | 'unconfirmed' | 'paid'
export interface McpOffer {
  name: string
  kind: McpOfferKind
  summary: string
  conditions: string[]
  sourceUrl: string
  lastVerified: string | null
  lastReviewed: string | null
  status: 'verified' | 'needs_review'
}
export interface McpOfferCatalog { freshnessDays: number; offers: Record<string, McpOffer> }

export function offerNeedsReview(offer: McpOffer, freshnessDays: number, now = Date.now()): boolean {
  if (offer.status !== 'verified' || offer.kind === 'unconfirmed' || !offer.lastVerified) return true
  const ageDays = Math.floor(now / 86_400_000) - Date.parse(offer.lastVerified + 'T00:00:00Z') / 86_400_000
  return ageDays < 0 || ageDays > freshnessDays
}
