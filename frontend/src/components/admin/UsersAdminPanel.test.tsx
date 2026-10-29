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
  enabledProductSurfaces: () => ['agentworks', 'relays', 'work', 'code', 'mcp-gateway', 'knowledgebase', 'bullpen'],
}))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

describe('Shared user roles and product access', () => {
  let host: HTMLDivElement
  let root: Root
  beforeEach(() => {
    api.listAdminUsers.mockResolvedValue({ products: ['agentworks', 'relays', 'work', 'code'], shared_products: ['bullpen', 'knowledgebase', 'mcp-gateway'], users: [{
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
  it('offers the four roles on each account, and no way to add one: new people are added by your server administrator', async () => {
    await render()
    // One line per person; the editor opens on click. No "viewer" wording (Builder and Run only).
    expect(host.querySelector('[aria-label="Role for creator@example.com"]')).toBeNull()
    await act(async () => (host.querySelector('[aria-label="Details for creator@example.com"]') as HTMLElement).click())
    expect(host.textContent).toContain('Available to everyone: AgentBridge, Brain, Vault')
    for (const name of ['AgentBridge', 'Brain', 'Vault']) expect(host.querySelector(`[aria-label="${name} for creator@example.com"]`)).toBeNull()
    expect(host.querySelector('[aria-label="Crew for creator@example.com"]')).not.toBeNull()
    await openRole('Role for creator@example.com')
    expect(Array.from(document.querySelectorAll('[role="option"]')).map(option => option.querySelector('span.block')?.textContent)).toEqual(['Member', 'Editor', 'Creator', 'Admin'])
    await act(async () => (document.querySelector('[role="option"]') as HTMLElement).click())
    expect(api.updateAdminUser).toHaveBeenCalledWith('creator', expect.objectContaining({ role: 'viewer' }))
    expect(Array.from(host.querySelectorAll('button')).some(button => button.textContent?.trim() === 'Add user')).toBe(false)
    expect(host.querySelector('[aria-label="Email"]')).toBeNull()
    // An SSO account has no password to set, and the identity service behind SSO is not named.
    expect(host.querySelector('button[title="Set a new password"]')).toBeNull()
    expect(host.textContent).toContain('Company sign-in')
  })
  it('shows advanced permissions as a summary and directs updates through admin MCP', async () => {
    api.listAdminUsers.mockResolvedValue({ products: [], shared_products: ['mcp-gateway'], users: [{
      id: 'gw', username: 'gw@example.com', email: 'gw@example.com', role: 'viewer', admin: false, can_create: false, can_edit: true, products: ['mcp-gateway'], provider: 'google', vault_reader: true, token_limits: { daily: 1000 },
    }] })
    await render()
    expect(host.textContent).toContain('New people are added by your server administrator.')
    expect(host.textContent).not.toContain('DevOps')
    await act(async () => (host.querySelector('[aria-label="Details for gw@example.com"]') as HTMLElement).click())
    expect(host.querySelector('[aria-label="Vault audit for gw@example.com"]')).toBeNull()
    expect(host.textContent).toContain('Vault audit')
    expect(host.textContent).toContain('Daily 1k')
    expect(host.textContent).toContain('Use admin MCP')
    expect(host.querySelector('input[aria-label*="token limit"]')).toBeNull()
    expect(host.querySelector('[aria-label="Vault reader for gw@example.com"]')).toBeNull()
  })
  it('keeps password status visible while password resets and deletion use MCP', async () => {
    api.listAdminUsers.mockResolvedValue({ products: ['code'], shared_products: [], users: [{
      id: 'pw', username: 'pw@example.com', email: 'pw@example.com', role: 'creator', admin: false, can_create: true, can_edit: true, products: ['code'], provider: 'password', has_password: true,
    }] })
    await render()
    await act(async () => (host.querySelector('[aria-label="Details for pw@example.com"]') as HTMLElement).click())
    expect(host.textContent).toContain('Password sign-in')
    expect(host.querySelector('button[title="Set a new password"]')).toBeNull()
    expect(host.querySelector('input[type="password"]')).toBeNull()
    expect(host.textContent).toContain('Copy admin request')
    expect(host.textContent).toContain('Disable account')
  })
  it('exposes no platform role/product grants in Vault', async () => {
    await render(true)
    expect(host.querySelector('[aria-label="Role for creator@example.com"]')).toBeNull()
    expect(host.querySelector('[aria-label="Code reviewer for creator@example.com"]')).toBeNull()
    for (const product of ['Goals', 'Code', 'Flows', 'Crew', 'Vault']) {
      expect(host.querySelector(`[aria-label="${product} for creator@example.com"]`)).toBeNull()
    }
    expect(api.updateAdminUser).not.toHaveBeenCalled()
  })
  it('ends a load that times out in an error with Retry instead of an endless spinner, and Retry loads again', async () => {
    api.listAdminUsers.mockRejectedValueOnce(new Error('timeout of 15000ms exceeded'))
    await render()
    expect(host.textContent).toContain('took longer than 15 seconds')
    expect(host.textContent).not.toContain('Loading accounts')
    const retry = Array.from(host.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Retry')!
    await act(async () => retry.click())
    expect(api.listAdminUsers).toHaveBeenCalledTimes(2)
    expect(host.textContent).not.toContain('took longer than 15 seconds')
    expect(host.textContent).toContain('creator@example.com')
  })
})
