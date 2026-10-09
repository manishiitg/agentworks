// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import UsersAdminPanel from './UsersAdminPanel'
const api = vi.hoisted(() => ({ listAdminUsers: vi.fn(), updateAdminUser: vi.fn() }))
vi.mock('../../services/api', () => ({ authApi: api }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (selector: (state: unknown) => unknown) => selector({ user: { id: 'admin' } }) }))
vi.mock('../../products/productSurfaceConfig', async importOriginal => ({
  ...await importOriginal<typeof import('../../products/productSurfaceConfig')>(),
  enabledProductSurfaces: () => ['agentworks', 'relays', 'work', 'code', 'mcp-gateway'],
}))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

describe('Shared user roles and product access', () => {
  let host: HTMLDivElement
  let root: Root
  beforeEach(() => {
    api.listAdminUsers.mockResolvedValue({ products: ['agentworks', 'relays', 'work', 'code', 'mcp-gateway'], users: [{
      id: 'creator', username: 'creator@example.com', email: 'creator@example.com', role: 'creator', admin: false, can_create: true, can_edit: true, products: ['work'], provider: 'google', invited: true,
    }] })
  })
  afterEach(async () => { if (root) await act(async () => root.unmount()); host?.remove(); vi.clearAllMocks() })
  async function render(vaultOnly = false) {
    host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host)
    await act(async () => root.render(<UsersAdminPanel vaultOnly={vaultOnly} />))
  }
  async function openRole(label: string) {
    const trigger = host.querySelector(`[aria-label="${label}"]`)!
    await act(async () => trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })))
  }
  it('offers the four roles on each account, and no way to add one: DevOps adds people on the server', async () => {
    await render()
    await openRole('Role for creator@example.com')
    expect(Array.from(document.querySelectorAll('[role="option"]')).map(option => option.querySelector('span.block')?.textContent)).toEqual(['Viewer', 'Editor', 'Creator', 'Admin'])
    await act(async () => (document.querySelector('[role="option"]') as HTMLElement).click())
    expect(api.updateAdminUser).toHaveBeenCalledWith('creator', expect.objectContaining({ role: 'viewer' }))
    expect(Array.from(host.querySelectorAll('button')).some(button => button.textContent?.trim() === 'Add user')).toBe(false)
    expect(host.querySelector('[aria-label="Email"]')).toBeNull()
  })
  it('exposes no platform role/product grants in Vault', async () => {
    await render(true)
    expect(host.querySelector('[aria-label="Role for creator@example.com"]')).toBeNull()
    expect(host.querySelector('[aria-label="Code reviewer for creator@example.com"]')).toBeNull()
    for (const product of ['Goals', 'Code', 'Relays', 'Crew', 'Vault']) {
      expect(host.querySelector(`[aria-label="${product} for creator@example.com"]`)).toBeNull()
    }
    expect(api.updateAdminUser).not.toHaveBeenCalled()
  })
})
