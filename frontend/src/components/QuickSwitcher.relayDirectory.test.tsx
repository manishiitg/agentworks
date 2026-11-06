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

vi.mock('../products/work/workSessions', () => ({ loadWorkSessionsIncludingShared: vi.fn(async () => []) }))
vi.mock('../utils/workflowSessionRestore', async importOriginal => ({
  ...await importOriginal<typeof import('../utils/workflowSessionRestore')>(),
  openWorkflowPresetPage: vi.fn(async () => {}),
}))

// llm-config-api resolves the API base URL at import time; stub it so this
// component test does not depend on the service modules' init order.
vi.mock('../services/llm-config-api', () => {
  const service = new Proxy({}, { get: () => vi.fn(async () => ({})) })
  return { llmConfigService: service, default: service }
})

import QuickSwitcher from './QuickSwitcher'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { openWorkflowPresetPage } from '../utils/workflowSessionRestore'
import { useModeStore } from '../stores/useModeStore'
import type { CustomPreset } from '../types/preset'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(fn => fn()) })

it('omits a saved Flow from search while the product is disabled', async () => {
  const preset = { id: 'invoice-relay', label: 'Invoice Relay', workflowKind: 'relay', selectedFolder: { filepath: 'Workflow/invoice-relay' } } as CustomPreset
  useGlobalPresetStore.setState(state => ({ workflowPresetsLoaded: true, workflowPresets: [preset], activePresetIds: { ...state.activePresetIds, workflow: preset.id } }))
  useModeStore.setState({ selectedModeCategory: 'workflow' })
  useProductSurfaceStore.setState({ productSurface: 'work' })
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => root.render(<QuickSwitcher isOpen onClose={vi.fn()} />))
  expect(host.textContent).not.toContain('Invoice Relay')
  expect(openWorkflowPresetPage).not.toHaveBeenCalled()
  expect(useProductSurfaceStore.getState().productSurface).toBe('work')
})
