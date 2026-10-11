// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import snapshot from '../../api/mcpOfferSnapshot.json'
import { parseMcpOffers } from '../../api/mcpOffers'
import { TooltipProvider } from '../ui/tooltip'
import { McpConnectionsPanel, type McpCatalogRow, type McpConnectionRow } from './McpConnectionsPanel'

vi.mock('../../hooks/useMcpOffers', () => ({ useMcpOffers: () => ({ catalog: parseMcpOffers(snapshot), refresh: () => {} }) }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let cleanup = () => {}
afterEach(() => { cleanup(); vi.useRealTimers() })
const catalog = [
  { id: 'exa', name: 'Exa', connect: { label: 'Add connection', ariaLabel: 'Add Exa connection', run: vi.fn() } },
  { id: 'unstructured', name: 'Unstructured' },
  { id: 'browserbase', name: 'Browserbase' },
  { id: 'custom', name: 'Exa-custom' },
]
async function mount(view: 'available' | 'connected' = 'available', initialCatalog: McpCatalogRow[] = catalog) {
  vi.useFakeTimers({ toFake: ['Date'] }); vi.setSystemTime('2026-10-11T12:00:00Z')
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanup = () => { act(() => root.unmount()); host.remove() }
  const render = (next: 'available' | 'connected', nextCatalog = initialCatalog, servers: McpConnectionRow[] = [{ id: 'account', name: 'My Exa account', status: 'Connected' }]) => act(async () => root.render(<TooltipProvider><McpConnectionsPanel view={next} catalog={nextCatalog} servers={servers} showTools={false} /></TooltipProvider>))
  await render(view)
  if (view === 'available') await act(async () => { await import('./McpOfferBadge') })
  return { host, render }
}
it('reveals provider eligibility on click, restores focus on Escape, and preserves connection setup', async () => {
  const { host } = await mount()
  const badge = host.querySelector<HTMLButtonElement>('[aria-label="Exa Free tier details"]')!
  expect(host.querySelector('[aria-label="Exa-custom Free tier details"]')).toBeNull()
  expect(document.querySelector('[role="dialog"]')).toBeNull()
  await act(async () => badge.click())
  const dialog = document.querySelector('[role="dialog"]')!
  expect(dialog.textContent).toContain('No payment method required')
  expect(dialog.textContent).toContain('Provider API offer; MCP limits can differ')
  expect(dialog.querySelector('a')?.getAttribute('href')).toBe('https://exa.ai/pricing')
  await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
  expect(document.querySelector('[role="dialog"]')).toBeNull()
  expect(document.activeElement).toBe(badge)
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Add Exa connection"]')!.click())
  expect(catalog[0].connect!.run).toHaveBeenCalledOnce()
})
it('separates trials from current free tiers and hides pricing controls on connected MCPs', async () => {
  const { host, render } = await mount()
  const filter = host.querySelector<HTMLSelectElement>('[aria-label="Filter by offer"]')!
  await act(async () => { filter.value = 'recurring'; filter.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(host.querySelector('section[aria-label="Available servers"]')?.textContent).toContain('Exa')
  expect(host.querySelector('section[aria-label="Available servers"]')?.textContent).not.toContain('Unstructured')
  expect(host.querySelector('section[aria-label="Available servers"]')?.textContent).not.toContain('Browserbase')
  await act(async () => { filter.value = 'trial'; filter.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(host.querySelector('section[aria-label="Available servers"]')?.textContent).toContain('Unstructured')
  expect(host.querySelector('[aria-label="Exa Free tier details"]')).toBeNull()
  await render('connected')
  expect(host.textContent).toContain('My Exa account')
  expect(host.querySelector('[aria-label="Filter by offer"]')).toBeNull()
  expect(host.querySelector('a[href="https://agentworkshq.com/free-tools/"]')).toBeNull()
})
it('removes offer evidence after connection, including custom-labeled Vault accounts, without trapping the active filter', async () => {
  const before = [catalog[0]]
  const { host, render } = await mount('available', before)
  const filter = host.querySelector<HTMLSelectElement>('[aria-label="Filter by offer"]')!
  await act(async () => { filter.value = 'recurring'; filter.dispatchEvent(new Event('change', { bubbles: true })) })
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Exa Free tier details"]')!.click())
  expect(document.querySelector('[role="dialog"]')).not.toBeNull()
  await render('available', [{ ...catalog[0], addedCount: 1 }])
  expect(document.querySelector('[role="dialog"]')).toBeNull()
  expect(host.querySelector('[aria-label="Exa Free tier details"]')).toBeNull()
  expect(host.querySelector('[aria-label="Filter by offer"]')).toBeNull()
  expect(host.querySelector('a[href="https://agentworkshq.com/free-tools/"]')).toBeNull()
  expect(host.textContent).toContain('1 added')
  expect(host.querySelector('[aria-label="Add Exa connection"]')).not.toBeNull()
  await render('available', before, [{ id: 'vault-account', name: 'Research account', provider: 'exa', source: 'Vault', status: 'Available automatically' }])
  expect(host.querySelector('[aria-label="Exa Free tier details"]')).toBeNull()
  expect(host.querySelector('[aria-label="Add Exa connection"]')).not.toBeNull()
  await render('available', before, [])
  expect(host.querySelector('[aria-label="Exa Free tier details"]')).not.toBeNull()
})
