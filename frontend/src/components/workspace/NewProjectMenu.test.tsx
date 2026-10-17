// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

vi.hoisted(() => {
  const memory = new Map<string, string>()
  const storage = { getItem: (key: string) => memory.get(key) ?? null, setItem: (key: string, value: string) => memory.set(key, value), removeItem: (key: string) => memory.delete(key) }
  Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true })
  Object.defineProperty(globalThis, 'sessionStorage', { value: storage, configurable: true })
})
const openProduct = vi.hoisted(() => vi.fn())
vi.mock('../../utils/productWorkspaceNavigation', () => ({ openProductWorkspace: openProduct }))
import { NewProjectMenu } from './NewProjectMenu'
import { useAuthStore } from '../../stores/useAuthStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { useCommandDialogStore } from '../../stores/useCommandDialogStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import type { ProductCreateSurface } from '../../stores/useCommandDialogStore'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanup: (() => Promise<void>)[] = []
const originalScroll = HTMLElement.prototype.scrollIntoView
beforeEach(() => {
  HTMLElement.prototype.scrollIntoView = vi.fn()
  window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['agentworks', 'relays', 'work', 'code', 'video-studio'], localServerProducts: true }
  useAuthStore.setState({ user: { id: 'alice', allowed_products: null, can_create: true } as never, isMultiUserMode: true })
  useProductSurfaceStore.setState({ productSurface: 'code' })
  useWorkspaceConnectionStore.setState({ activeWorkspaceId: 'local' })
  useCommandDialogStore.getState().closeAll()
  openProduct.mockClear()
})
afterEach(async () => {
  for (const fn of cleanup.splice(0)) await fn()
  delete window.__APP_RUNTIME_CONFIG__
  HTMLElement.prototype.scrollIntoView = originalScroll
})
async function renderMenu() {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanup.push(async () => { await act(async () => root.unmount()); host.remove() })
  await act(async () => root.render(<NewProjectMenu />))
  const open = async () => act(async () => {
    const trigger = host.querySelector<HTMLButtonElement>('button')!
    trigger.focus()
    trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
  })
  return { host, open }
}
const choices = () => [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')]

it.each([['Goals', 'agentworks'], ['Crew', 'work'], ['Code', 'code'], ['Flows', 'relays']] as [string, ProductCreateSurface][])
  ('opens the existing %s creation flow from the common entry', async (label, surface) => {
    const { host, open } = await renderMenu()
    expect(host.textContent).toBe('New project')
    expect(useCommandDialogStore.getState().productCreateSurface).toBeNull()
    await open()
    expect(choices().map(item => item.getAttribute('aria-label'))).toEqual(['Goals', 'Flows', 'Crew', 'Code'])
    await act(async () => choices().find(item => item.getAttribute('aria-label') === label)!.click())
    expect(openProduct).toHaveBeenCalledWith(surface)
    expect(useCommandDialogStore.getState().productCreateSurface).toBe(surface)
    expect(document.querySelector('[role="menu"]')).toBeNull()
  })

it('uses enabled products and per-product create permissions, and hides the entry when none remain', async () => {
  window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['agentworks', 'work'] }
  useAuthStore.setState({ user: { id: 'alice', can_create: false, can_create_in: { work: true }, allowed_products: ['agentworks', 'work', 'code'] } as never })
  const { host, open } = await renderMenu()
  await open()
  expect(choices().map(item => item.getAttribute('aria-label'))).toEqual(['Crew'])
  await act(async () => useAuthStore.setState({ user: { id: 'alice', can_create: false } as never }))
  expect(host.querySelector('button')).toBeNull()
  expect(document.querySelector('[role="menu"]')).toBeNull()
})

it('rechecks a stale menu selection before routing or requesting creation', async () => {
  const { open } = await renderMenu()
  await open()
  const code = choices().find(item => item.getAttribute('aria-label') === 'Code')!
  await act(async () => {
    useAuthStore.setState({ user: { id: 'alice', can_create: false } as never })
    code.click()
  })
  expect(openProduct).not.toHaveBeenCalled()
  expect(useCommandDialogStore.getState().productCreateSurface).toBeNull()
})

it('follows a consolidated product catalog without retaining the removed Crew choice', async () => {
  window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['agentworks', 'relays', 'code'] }
  const { open } = await renderMenu()
  await open()
  expect(choices().map(item => item.getAttribute('aria-label'))).toEqual(['Goals', 'Flows', 'Code'])
})

it('closes the product chooser when the account changes', async () => {
  const { open } = await renderMenu()
  await open()
  expect(document.querySelector('[role="menu"]')).not.toBeNull()
  await act(async () => useAuthStore.setState({ user: { id: 'bob', can_create: true } as never }))
  expect(document.querySelector('[role="menu"]')).toBeNull()
  expect(openProduct).not.toHaveBeenCalled()
})
