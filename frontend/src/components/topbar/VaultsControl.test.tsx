// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: vi.fn() }))
import { useAuthStore } from '../../stores/useAuthStore'
import { TooltipProvider } from '../ui/tooltip'
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

// A Code-only account (products ["code"]) saw "My vaults" in the left menu because the entry only checked can_create (Citymall, 2026-10-09).
describe('My vaults entry', () => {
  it('shows for an account that may open Vault', async () => {
    const host = await render({ id: 'a', can_create: true, allowed_products: ['code', 'mcp-gateway'] })
    expect(host.querySelector('[aria-label="My vaults"]')).not.toBeNull()
  })
  it('is hidden from an account whose products leave Vault out, and from read-only accounts', async () => {
    expect((await render({ id: 'b', can_create: true, allowed_products: ['code'] })).querySelector('[aria-label="My vaults"]')).toBeNull()
    document.body.innerHTML = ''
    expect((await render({ id: 'c', can_create: false, allowed_products: ['code', 'mcp-gateway'] })).querySelector('[aria-label="My vaults"]')).toBeNull()
  })
})
