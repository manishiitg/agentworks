// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import snapshot from './mcpOfferSnapshot.json'
import { offerNeedsReview, parseMcpOffers } from './mcpOffers'

afterEach(() => { vi.unstubAllGlobals(); localStorage.clear(); vi.resetModules() })
it('requires current evidence and an exact connector mapping; rejects unsafe evidence and ambiguous mappings', () => {
  const catalog = parseMcpOffers(snapshot)!
  const exa = catalog.offers.exa
  expect(offerNeedsReview(exa, 30, Date.parse('2026-10-11T12:00:00Z'))).toBe(false)
  expect(offerNeedsReview(exa, 30, Date.parse('2026-11-11T00:00:00Z'))).toBe(true)
  expect(offerNeedsReview(exa, 30, Date.parse('2026-10-10T00:00:00Z'))).toBe(true)
  expect(offerNeedsReview(catalog.offers.browserbase, 30)).toBe(true)
  expect(catalog.offers['exa-custom']).toBeUndefined()
  const unsafe = structuredClone(snapshot)
  const provider = unsafe.providers.find(item => item.name === 'Exa')!
  provider.sources.forEach(source => { source.url = 'javascript:alert(1)' })
  expect(parseMcpOffers(unsafe)!.offers.exa).toBeUndefined()
  const duplicate = structuredClone(snapshot)
  duplicate.providers.push(duplicate.providers.find(item => item.name === 'Exa')!)
  expect(parseMcpOffers(duplicate)).toBeNull()
})
it('coalesces credential-free public refreshes and retains evidence when the source is unavailable', async () => {
  vi.resetModules()
  const offers = await import('./mcpOffers')
  const fetch = vi.fn().mockResolvedValue({ ok: true, text: async () => JSON.stringify(snapshot) })
  vi.stubGlobal('fetch', fetch)
  const [first, second] = await Promise.all([offers.refreshMcpOffers(), offers.refreshMcpOffers()])
  expect(first).toBe(second)
  expect(fetch).toHaveBeenCalledOnce()
  expect(fetch.mock.calls[0][1]).toMatchObject({ credentials: 'omit', referrerPolicy: 'no-referrer' })
  fetch.mockRejectedValueOnce(new Error('offline'))
  expect(await offers.refreshMcpOffers(true)).toBe(first)
  expect(first.offers.exa.lastVerified).toBe('2026-10-11')
})
