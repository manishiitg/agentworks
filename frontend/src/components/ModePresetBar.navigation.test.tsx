// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => {
  const noop = () => {}
  const state: Record<string, any> = {
    selectedModeCategory: 'workflow', toolList: [], workflowPresets: [],
    workflowPresetsLoaded: false, loading: false, activePreset: null,
    showWorkflowsOverview: false, showSchedulesOverview: false, adminPage: null,
    showLLMModal: false, workflows: [], activeSessionsCache: [], chatTabs: {},
    activeTabId: null, showPresetSettings: false, showPresetCreate: false, canCreate: true,
    productCreateSurface: null,
    consumeProductCreate: (surface: string) => { if (state.productCreateSurface !== surface) return false; state.productCreateSurface = null; return true },
    closeDialog: (dialog: string) => { if (dialog === 'presetCreate') state.showPresetCreate = false },
    addToast: vi.fn(),
    refreshPresets: async () => {}, getActivePreset: () => state.activePreset,
    getPresetsForMode: () => state.workflowPresets, isPresetActive: () => false,
    setWorkspaceMinimized: noop, setModeCategory: noop, getAgentModeFromCategory: () => 'workflow',
  }
  const store = Object.assign((selector?: (value: any) => any) => selector ? selector(state) : state, { getState: () => state })
  return { state, store }
})
vi.mock('../stores/useAuthStore', () => ({ useAuthStore: mocks.store }))
vi.mock('../stores/useModeStore', () => ({ useModeStore: mocks.store }))
vi.mock('../stores/useGlobalPresetStore', () => ({ useGlobalPresetStore: mocks.store, usePresetApplication: mocks.store, usePresetManagement: mocks.store }))
vi.mock('../stores/useAppStore', () => ({ useAppStore: mocks.store }))
vi.mock('../stores/useMCPStore', () => ({ useMCPStore: mocks.store }))
vi.mock('../stores/useCommandDialogStore', () => ({ useCommandDialogStore: mocks.store }))
vi.mock('../stores/useWorkspaceStore', () => ({ useWorkspaceStore: mocks.store }))
vi.mock('../stores/useWorkflowManifestStore', () => ({ useWorkflowManifestStore: mocks.store }))
vi.mock('../stores', () => ({ useChatStore: mocks.store, useLLMStore: mocks.store }))
vi.mock('../services/api', () => ({ agentApi: {}, workflowManifestApi: {} }))
vi.mock('../utils/workflowSessionRestore', () => ({ openWorkflowPresetPage: vi.fn() }))
vi.mock('../utils/workflowPermissions', () => ({ hasWorkflowCreateAccess: () => mocks.state.canCreate, hasProductCreateAccess: () => mocks.state.canCreate, isWorkflowReadOnly: () => false }))
vi.mock('../hooks/useGlobalSchedulerPaused', () => ({ useGlobalSchedulerPaused: () => false }))
vi.mock('./PresetModal', () => ({ default: ({ isOpen, editingPreset, fixedWorkflowKind }: any) => isOpen ? <div data-testid="preset-modal">{editingPreset ? 'edit' : 'create'}:{fixedWorkflowKind}</div> : null }))
vi.mock('./workspace/WorkspaceChatsSidebar', () => ({ default: () => null }))
vi.mock('./GlobalActivityMonitor', () => ({ GlobalActivityMonitor: () => null }))
vi.mock('./ProductSurfaceSwitcher', () => ({ ProductSurfaceSwitcher: ({ menuOnly }: { menuOnly?: boolean }) => menuOnly ? <span data-testid="combined-picker-products">Products</span> : <button aria-label="Standalone product picker" /> }))
vi.mock('./WorkspaceTopBarControls', () => ({ default: () => null }))
vi.mock('./branding/RuntimeBrandLogo', () => ({ RuntimeBrandLogo: () => null }))
vi.mock('./topbar/McpControl', () => ({ default: () => null }))
vi.mock('./topbar/UsersControl', () => ({ default: () => null }))
vi.mock('./topbar/ProvidersControl', () => ({ default: () => null }))
vi.mock('../stores/useProductSurfaceStore', () => ({ useProductSurfaceStore: mocks.store }))

import { TopBarEntitySelector } from './topbar/TopBarEntitySelector'
import { ModePresetBar } from './ModePresetBar'
import { TooltipProvider } from './ui/tooltip'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let host: HTMLDivElement
let root: Root
const originalStorage = Object.getOwnPropertyDescriptor(window, 'localStorage')
const render = async (props: React.ComponentProps<typeof ModePresetBar> = {}) => {
  await act(async () => root.render(<TooltipProvider><ModePresetBar {...props} /></TooltipProvider>))
}

beforeEach(() => {
  const values = new Map<string, string>()
  Object.defineProperty(window, 'localStorage', { configurable: true, value: {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => { values.set(key, value) },
  } })
  delete (window as any).__llmDiscoveryOnboardingState
  delete window.electronAPI
  Object.assign(mocks.state, { selectedModeCategory: 'workflow', workflowPresetsLoaded: false, activePreset: null, workflowPresets: [], showWorkflowsOverview: false, showSchedulesOverview: false, adminPage: null, showLLMModal: false, loading: false, showPresetCreate: false, productCreateSurface: null, canCreate: true, productSurface: 'agentworks' })
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
  delete window.electronAPI
  delete (window as any).__llmDiscoveryOnboardingState
  if (originalStorage) Object.defineProperty(window, 'localStorage', originalStorage)
  else Reflect.deleteProperty(window, 'localStorage')
})

it('keeps the workspace free of Help and tours, including old manual launch events', async () => {
  await render()
  await act(async () => {
    window.dispatchEvent(new Event('open-workflow-walkthrough'))
    window.dispatchEvent(new Event('open-providers-walkthrough'))
  })
  expect(host.querySelector('[data-testid="tour"]')).toBeNull()
  expect(host.querySelector('[data-contextual-guide]')).toBeNull()
  expect(host.textContent).not.toContain('Help & walkthrough')
})

it('opens the existing creation dialog for an intro request, even with an active preset', async () => {
  mocks.state.activePreset = { id: 'existing', label: 'Existing automation' }
  mocks.state.showPresetCreate = true
  await render()
  expect(host.querySelector('[data-testid="preset-modal"]')?.textContent).toBe('create:workflow')
  expect(mocks.state.showPresetCreate).toBe(false)
})

it('rejects intro creation requests when the account cannot create', async () => {
  mocks.state.canCreate = false
  mocks.state.showPresetCreate = true
  await render()
  expect(host.querySelector('[data-testid="preset-modal"]')).toBeNull()
  expect(mocks.state.showPresetCreate).toBe(false)
})


it.each(['agentworks', 'relays'])('opens the existing %s creation form for a pending Ctrl+K request', async surface => {
  mocks.state.productSurface = surface
  mocks.state.productCreateSurface = surface
  mocks.state.activePreset = { id: 'existing', label: 'Existing automation' }
  await render()
  expect(host.querySelector('[data-testid="preset-modal"]')?.textContent).toBe(surface === 'relays' ? 'create:relay' : 'create:workflow')
  expect(mocks.state.productCreateSurface).toBeNull()
})


// Owner, 2026-10-29: the project picker only switches projects; no schedules action or product grid.
it.each(['agentworks', 'work'] as const)('shows only projects in the %s header picker', async productSurface => {
  Object.assign(mocks.state, { productSurface, setShowLLMModal: vi.fn(), setAdminPage: vi.fn(), setShowWorkflowsOverview: vi.fn(), setShowSchedulesOverview: vi.fn() })
  await render(productSurface === 'work' ? { productControl: <TopBarEntitySelector placeholder="Choose a Crew" open={false} onToggle={() => {}} onClose={() => {}}><span>Existing Crews</span></TopBarEntitySelector> } : {})
  expect(host.querySelector('[data-product-settings] [data-product-picker-menu]')).not.toBeNull()
  expect(host.querySelector('[data-product-picker-menu] [data-tour="global-schedules"]')).toBeNull()
  expect(host.querySelector('[data-product-picker-menu] [data-product-switch-menu]')).toBeNull()
})

it('omits global Goal activity from the header and automation picker', async () => {
  await render()
  expect(host.querySelector('[aria-label="Goal activity"]')).toBeNull()
  expect(host.querySelector('[aria-label^="Needs you:"]')).toBeNull()
})
