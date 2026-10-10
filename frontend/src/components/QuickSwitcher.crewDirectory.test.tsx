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
  loadWorkSessionsIncludingShared: vi.fn(async () => [
    { id: 'crew-own', title: 'project-a-flow-tester', identity: { name: 'project-a Flow Tester' } },
    { id: 'crew-shared', title: 'qa', identity: { name: 'QA Bot' }, shared: { ownerId: 'u2', ownerUsername: 'yoav' } },
  ]),
}))

// llm-config-api resolves the API base URL at import time; stub it so this
// component test does not depend on the service modules' init order.
vi.mock('../services/llm-config-api', () => {
  const service = new Proxy({}, { get: () => vi.fn(async () => ({})) })
  return { llmConfigService: service, default: service }
})

import QuickSwitcher from './QuickSwitcher'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(fn => fn()) })

it('lists every accessible Crew, not only open or running ones, and opens one', async () => {
  useGlobalPresetStore.setState({ workflowPresetsLoaded: true, workflowPresets: [] })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  const onClose = vi.fn()
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => { root.render(<QuickSwitcher isOpen onClose={onClose} />) })
  await act(async () => { await Promise.resolve() })
  expect(host.textContent).toContain('project-a Flow Tester')
  expect(host.textContent).toContain('QA Bot')
  expect(host.textContent).toContain('shared by yoav')
  const row = [...host.querySelectorAll('div')].find(div => div.textContent?.startsWith('QA Bot') && div.getAttribute('class')?.includes('cursor-pointer'))
    ?? [...host.querySelectorAll('.cursor-pointer')].find(div => div.textContent?.includes('QA Bot'))
  await act(async () => { row!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })) })
  expect(useProductSurfaceStore.getState().selectedWorkProjectId).toBe('crew-shared')
  expect(useProductSurfaceStore.getState().productSurface).toBe('work')
  expect(onClose).toHaveBeenCalled()
})
