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

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanup: (() => Promise<void>)[] = []
const originalScroll = HTMLElement.prototype.scrollIntoView
beforeEach(() => {
  HTMLElement.prototype.scrollIntoView = vi.fn()
  window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['agentworks', 'relays', 'work'], localServerProducts: true }
  useAuthStore.setState({ user: { id: 'alice', allowed_products: null, can_create: true } as never, isMultiUserMode: true })
  useProductSurfaceStore.setState({ productSurface: 'work' })
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
  return host
}

it('opens Home with focus intent from the expanded menu', async () => {
  const host = await renderMenu()
  const request = vi.fn()
  window.addEventListener('open-welcome-home', request)
  try {
    await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
    expect(window.location.pathname).toBe('/home')
    expect((request.mock.calls[0][0] as CustomEvent).detail).toEqual({ focusPrompt: true })
    expect(useCommandDialogStore.getState().productCreateSurface).toBeNull()
    expect(document.querySelector('[role="menu"]')).toBeNull()
    expect(openProduct).not.toHaveBeenCalled()
  } finally { window.removeEventListener('open-welcome-home', request) }
})

it('keeps the existing create-access gate and rechecks stale clicks', async () => {
  const host = await renderMenu()
  const button = host.querySelector<HTMLButtonElement>('button')!
  window.history.replaceState({}, '', '/crew')
  await act(async () => {
    useAuthStore.setState({ user: { id: 'alice', can_create: false } as never })
    button.click()
  })
  expect(host.querySelector('button')).toBeNull()
  expect(window.location.pathname).toBe('/crew')
  expect(useCommandDialogStore.getState().productCreateSurface).toBeNull()
})
