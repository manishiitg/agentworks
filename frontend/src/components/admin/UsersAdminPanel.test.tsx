// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import UsersAdminPanel from './UsersAdminPanel'
const api = vi.hoisted(() => ({ listAdminUsers: vi.fn(), createAdminUser: vi.fn(), updateAdminUser: vi.fn() }))
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
    api.createAdminUser.mockImplementation(async (input: { email: string }) => ({ email: input.email }))
  })
  afterEach(async () => { if (root) await act(async () => root.unmount()); host?.remove(); vi.clearAllMocks() })
  async function render(vaultOnly = false) {
    host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host)
    await act(async () => root.render(<UsersAdminPanel vaultOnly={vaultOnly} />))
  }
  async function inputEmail(value: string) {
    const input = host.querySelector('[aria-label="Email"]') as HTMLInputElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
  }
  async function add() {
    await act(async () => (Array.from(host.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Add user')!).click())
  }
  async function openRole(label: string) {
    const trigger = host.querySelector(`[aria-label="${label}"]`)!
    await act(async () => trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })))
  }
  async function chooseRole(label: string, role: string) {
    await openRole(label)
    const option = Array.from(document.querySelectorAll('[role="option"]')).find(el => el.querySelector('span.block')?.textContent === role) as HTMLElement
    expect(option).toBeDefined()
    await act(async () => option.click())
  }
  it('offers the same four roles for creation and editing, and includes Relays globally', async () => {
    await render()
    const expected = ['Viewer', 'Editor', 'Creator', 'Admin']
    for (const label of ['Role', 'Role for creator@example.com']) {
      await openRole(label)
      expect(Array.from(document.querySelectorAll('[role="option"]')).map(option => option.querySelector('span.block')?.textContent)).toEqual(expected)
      await act(async () => (document.querySelector('[role="option"]') as HTMLElement).click())
    }
    expect(api.updateAdminUser).toHaveBeenCalledWith('creator', expect.objectContaining({ role: 'viewer' }))
    expect(host.querySelector('[aria-label="Relays for the new user"]')).not.toBeNull()
    expect(host.querySelector('[aria-label="Vault for the new user"]')?.getAttribute('data-state')).toBe('unchecked')
    expect(host.querySelector('[aria-label="Vault for the new user"]')?.hasAttribute('disabled')).toBe(false)
  })
  it('adds only Vault consumers and exposes no platform role/product grants in Vault', async () => {
    await render(true)
    expect(host.textContent).toContain('Vault only')
    expect(host.querySelector('[aria-label="Role"]')).toBeNull()
    expect(host.querySelector('[aria-label="Role for creator@example.com"]')).toBeNull()
    expect(host.querySelector('[aria-label="Code reviewer for creator@example.com"]')).toBeNull()
    for (const product of ['Goals', 'Code', 'Relays', 'Crew', 'Vault']) {
      expect(host.querySelector(`[aria-label="${product} for the new user"]`)).toBeNull()
      expect(host.querySelector(`[aria-label="${product} for creator@example.com"]`)).toBeNull()
    }
    for (const email of ['first@example.com', 'second@example.com']) {
      await inputEmail(email); await add()
      expect(api.createAdminUser).toHaveBeenLastCalledWith(expect.objectContaining({
        email, role: 'viewer', admin: false, can_create: false, can_edit: false, products: ['mcp-gateway'],
      }))
    }
    expect(api.updateAdminUser).not.toHaveBeenCalled()
  })
})
