// @vitest-environment happy-dom
import { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

const loading = vi.hoisted(() => {
  const values = new Map<string, string>()
  const storage = { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => { values.set(key, value) }, removeItem: (key: string) => { values.delete(key) }, clear: () => values.clear(), key: () => null, get length() { return values.size } }
  Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true })
  Object.defineProperty(globalThis, 'sessionStorage', { value: storage, configurable: true })
  let release!: () => void
  const ready = new Promise<void>(resolve => { release = resolve })
  return { ready, release }
})
vi.mock('./QuickSwitcher', async importOriginal => {
  await loading.ready
  return importOriginal<typeof import('./QuickSwitcher')>()
})
vi.mock('../products/work/workSessions', () => ({ loadWorkSessionsIncludingShared: vi.fn(async () => []) }))
vi.mock('../services/llm-config-api', () => {
  const service = new Proxy({}, { get: (_, key) => vi.fn(async () => key === 'getProviderConnections' ? [] : {}) })
  return { llmConfigService: service, default: service }
})

import QuickSwitcherHost, { preloadQuickSwitcher } from './QuickSwitcherHost'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { useAuthStore } from '../stores/useAuthStore'
import { useChatStore } from '../stores/useChatStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('closes before the first chunk arrives, then focuses the real search input and handles Escape', async () => {
  window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['code'], gatewayUrl: 'http://localhost:99999' } as never
  useAuthStore.setState({ user: { id: 'viewer', is_admin: true, allowed_products: ['code'] } as never })
  useProductSurfaceStore.setState({ productSurface: 'work' })
  useGlobalPresetStore.setState({ workflowPresetsLoaded: true, workflowPresets: [] })
  useChatStore.setState({ chatTabs: {}, activeTabId: null, activeSessionsCache: [], getActiveSessions: vi.fn(async () => []) })
  const closed = vi.fn()
  let open!: () => void
  function View() {
    const [isOpen, setOpen] = useState(true)
    open = () => setOpen(true)
    return <QuickSwitcherHost isOpen={isOpen} initialQuery="Code" onClose={() => { closed(); setOpen(false) }} />
  }
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<View />))
    expect(host.querySelector('input')).toBeNull()
    await act(async () => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })))
    expect(closed).toHaveBeenCalledOnce()
    await act(async () => { loading.release(); await preloadQuickSwitcher() })
    expect(host.querySelector('input')).toBeNull()
    await act(async () => open())
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 60)) })
    const input = host.querySelector('input')!
    expect(input.value).toBe('Code')
    expect(document.activeElement).toBe(input)
    await act(async () => input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })))
    expect(closed).toHaveBeenCalledTimes(2)
    expect(host.querySelector('input')).toBeNull()
  } finally {
    loading.release()
    act(() => root.unmount()); host.remove()
    delete window.__APP_RUNTIME_CONFIG__
    useAuthStore.setState({ user: null, isMultiUserMode: false })
  }
})
