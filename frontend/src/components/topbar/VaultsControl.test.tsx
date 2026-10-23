// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
vi.hoisted(() => {
  const values = new Map<string, string>()
  vi.stubGlobal('localStorage', { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => values.set(key, value), removeItem: (key: string) => values.delete(key) })
})
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: vi.fn() }))
import { useAuthStore } from '../../stores/useAuthStore'
import { TooltipProvider } from '../ui/tooltip'
const navigation = vi.hoisted(() => ({ open: vi.fn(async () => 'own-chat') }))
vi.mock('../../utils/projectVaultNavigation', () => ({ openLastProjectVaultChat: navigation.open }))
import VaultsControl from './VaultsControl'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => { document.body.innerHTML = ''; delete (window as { __APP_RUNTIME_CONFIG__?: unknown }).__APP_RUNTIME_CONFIG__ })

async function render(user: unknown) {
  ;(window as { __APP_RUNTIME_CONFIG__?: unknown }).__APP_RUNTIME_CONFIG__ = { gatewayUrl: 'https://vault.example.com', enabledProductSurfaces: ['work', 'code', 'mcp-gateway', 'knowledgebase'] }
  vi.mocked(useAuthStore).mockImplementation(((selector?: (s: unknown) => unknown) => (selector ? selector({ user }) : { user })) as unknown as typeof useAuthStore)
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  await act(async () => root.render(<TooltipProvider><VaultsControl /></TooltipProvider>))
  return host
}

// Personal account management is available independently of shared Vault access.
describe('My vaults entry', () => {
  it('shows for an account that may open Vault', async () => {
    const host = await render({ id: 'a', can_create: true, allowed_products: ['code', 'mcp-gateway'] })
    expect(host.querySelector('[aria-label="My vaults"]')).not.toBeNull()
  })
  it('is available without shared Vault access and stays hidden from Run accounts', async () => {
    expect((await render({ id: 'b', can_create: true, allowed_products: ['code'] })).querySelector('[aria-label="My vaults"]')).not.toBeNull()
    document.body.innerHTML = ''
    expect((await render({ id: 'c', can_create: false, allowed_products: ['code', 'mcp-gateway'] })).querySelector('[aria-label="My vaults"]')).toBeNull()
  })
})

it('opens the last real project chat instead of a separate Vault page', async () => {
  const host = await render({ id: 'personal', can_create: true, allowed_products: ['mcp-gateway'] })
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="My vaults"]')!.click())
  expect(navigation.open).toHaveBeenCalledOnce()
})
