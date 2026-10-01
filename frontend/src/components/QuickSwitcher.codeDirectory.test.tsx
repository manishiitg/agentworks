// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

// Persisted stores need a Storage before they are created at import time.
vi.hoisted(() => {
  const memory = new Map<string, string>()
  const storage = { getItem: (k: string) => memory.get(k) ?? null, setItem: (k: string, v: string) => { memory.set(k, String(v)) }, removeItem: (k: string) => { memory.delete(k) }, clear: () => memory.clear(), key: (i: number) => [...memory.keys()][i] ?? null, get length() { return memory.size } }
  Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true })
  Object.defineProperty(globalThis, 'sessionStorage', { value: storage, configurable: true })
})

vi.mock('../products/work/workSessions', () => ({
  loadWorkSessionsIncludingShared: vi.fn(async (product?: { profileId?: string }) => product?.profileId === 'code'
    ? [
        { id: 'code-own', title: 'billing-api' },
      ]
    : [{ id: 'crew-own', title: 'sde', identity: { name: 'SDE' } }]),
}))

vi.mock('../services/llm-config-api', () => {
  const service = new Proxy({}, { get: () => vi.fn(async () => ({})) })
  return { llmConfigService: service, default: service }
})

import QuickSwitcher from './QuickSwitcher'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { useAuthStore } from '../stores/useAuthStore'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
afterEach(() => {
  cleanups.splice(0).forEach(fn => fn())
  delete (window as Window & { __APP_RUNTIME_CONFIG__?: unknown }).__APP_RUNTIME_CONFIG__
})

async function renderSwitcher(onClose = vi.fn()) {
  useGlobalPresetStore.setState({ workflowPresetsLoaded: true, workflowPresets: [] })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => { root.render(<QuickSwitcher isOpen onClose={onClose} />) })
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
  return host
}

it('lists Code workspaces next to Crews and opens one on the Code surface', async () => {
  (window as Window & { __APP_RUNTIME_CONFIG__?: unknown }).__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['agentworks', 'work', 'code'] }
  const onClose = vi.fn()
  const host = await renderSwitcher(onClose)
  expect(host.textContent).toContain('SDE')
  await act(async () => { await vi.waitFor(() => expect(host.textContent).toContain('billing-api')) })
  expect(host.textContent).not.toContain('shared by')
  const row = [...host.querySelectorAll('.cursor-pointer')].find(div => div.textContent?.includes('billing-api'))
  await act(async () => { row!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })) })
  expect(useProductSurfaceStore.getState().selectedCodeProjectId).toBe('code-own')
  expect(useProductSurfaceStore.getState().productSurface).toBe('code')
  expect(onClose).toHaveBeenCalled()
})

it('lists no Code workspaces where the deployment or the account has no Code', async () => {
  (window as Window & { __APP_RUNTIME_CONFIG__?: unknown }).__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['agentworks', 'work'] }
  let host = await renderSwitcher()
  expect(host.textContent).toContain('SDE')
  expect(host.textContent).not.toContain('billing-api')
  cleanups.splice(0).forEach(fn => fn())

  ;(window as Window & { __APP_RUNTIME_CONFIG__?: unknown }).__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['agentworks', 'work', 'code'] }
  useAuthStore.setState({ user: { id: 'u1', username: 'u1', allowed_products: ['agentworks', 'work'] } as never })
  host = await renderSwitcher()
  expect(host.textContent).not.toContain('billing-api')
  useAuthStore.setState({ user: null } as never)
})
