import snapshot from './mcpOfferSnapshot.json'
import type { McpOffer, McpOfferCatalog, McpOfferKind } from './mcpOfferMetadata'
export { FREE_TOOLS_CATALOG_URL, offerNeedsReview } from './mcpOfferMetadata'
export type { McpOffer, McpOfferCatalog, McpOfferKind } from './mcpOfferMetadata'

const SOURCE_URL = 'https://raw.githubusercontent.com/manishiitg/free-agent-tools/main/src/free_agent_tools/catalog.json'
const STORAGE_KEY = 'agentworks.public-mcp-offers.v1'
const REFRESH_MS = 5 * 60_000

const record = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value)
const kinds: McpOfferKind[] = ['recurring', 'trial', 'grant', 'open_source', 'unconfirmed', 'paid']
function date(value: unknown): string | null {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}$/.test(value)) return null
  const parsed = new Date(value + 'T00:00:00Z')
  return Number.isFinite(parsed.getTime()) && parsed.toISOString().slice(0, 10) === value ? value : null
}
function httpsUrl(value: unknown): string | null {
  if (typeof value !== 'string') return null
  try {
    const url = new URL(value)
    return url.protocol === 'https:' && !url.username && !url.password ? url.href : null
  } catch { return null }
}

/** Public pricing evidence never adds a connector or changes connection authority. */
export function parseMcpOffers(value: unknown): McpOfferCatalog | null {
  if (!record(value) || value.schema_version !== 1 || !Array.isArray(value.providers) ||
      typeof value.freshness_days !== 'number' || !Number.isInteger(value.freshness_days) || value.freshness_days < 1 || value.freshness_days > 30) return null
  const offers: Record<string, McpOffer> = Object.create(null)
  for (const provider of value.providers) {
    if (!record(provider) || !record(provider.mcp) || typeof provider.mcp.agentworks_connector !== 'string' ||
        !provider.mcp.agentworks_connector.trim() || !record(provider.offer) || typeof provider.name !== 'string' ||
        typeof provider.offer.summary !== 'string' || !kinds.includes(provider.offer.kind as McpOfferKind) ||
        !Array.isArray(provider.offer.conditions) || !provider.offer.conditions.every(item => typeof item === 'string') ||
        !Array.isArray(provider.sources)) continue
    const source = provider.sources.find(item => record(item) && item.type === 'official' && httpsUrl(item.url))
    if (!record(source)) continue
    const key = provider.mcp.agentworks_connector.trim().toLowerCase()
    // Duplicate mappings are ambiguous; never select one provider's price by accident.
    if (Object.hasOwn(offers, key)) return null
    offers[key] = {
      name: provider.name, kind: provider.offer.kind as McpOfferKind,
      summary: provider.offer.summary, conditions: provider.offer.conditions as string[],
      sourceUrl: httpsUrl(source.url)!, lastVerified: date(provider.last_verified), lastReviewed: date(provider.last_reviewed),
      status: provider.status === 'verified' ? 'verified' : 'needs_review',
    }
  }
  return { freshnessDays: value.freshness_days, offers }
}

let current = parseMcpOffers(snapshot)!
let lastAttempt = 0
let pending: Promise<McpOfferCatalog> | null = null
let storageRead = false
export function cachedMcpOffers(): McpOfferCatalog {
  if (!storageRead) {
    storageRead = true
    try {
      const saved = localStorage.getItem(STORAGE_KEY)
      if (saved) current = parseMcpOffers(JSON.parse(saved)) ?? current
    } catch { /* Public metadata is optional; private connection setup remains usable. */ }
  }
  return current
}

export function refreshMcpOffers(force = false): Promise<McpOfferCatalog> {
  cachedMcpOffers()
  if (pending) return pending
  if (!force && Date.now() - lastAttempt < REFRESH_MS) return Promise.resolve(current)
  lastAttempt = Date.now()
  pending = (async () => {
    const controller = new AbortController()
    const timer = setTimeout(() => controller.abort(), 5000)
    try {
      // Do not send app authentication, cookies, account IDs or searches to this public feed.
      const response = await fetch(SOURCE_URL, { credentials: 'omit', referrerPolicy: 'no-referrer', signal: controller.signal })
      if (!response.ok) return current
      const text = await response.text()
      if (text.length > 2_000_000) return current
      const raw: unknown = JSON.parse(text)
      const parsed = parseMcpOffers(raw)
      if (!parsed) return current
      current = parsed
      try { localStorage.setItem(STORAGE_KEY, text) } catch { /* Storage can be disabled. */ }
      return current
    } catch { return current } finally { clearTimeout(timer) }
  })().finally(() => { pending = null })
  return pending
}
