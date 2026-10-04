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
    closeDialog: (dialog: string) => { if (dialog === 'presetCreate') state.showPresetCreate = false },
    addToast: vi.fn(),
    refreshPresets: async () => {}, getActivePreset: () => state.activePreset,
    getPresetsForMode: () => state.workflowPresets, isPresetActive: () => false,
    setWorkspaceMinimized: noop, setModeCategory: noop, getAgentModeFromCategory: () => 'workflow',
  }
  const store = Object.assign((selector?: (value: any) => any) => selector ? selector(state) : state, { getState: () => state })
  return { state, store, renderedTours: [] as string[] }
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
vi.mock('../utils/workflowPermissions', () => ({ hasWorkflowCreateAccess: () => mocks.state.canCreate, isWorkflowReadOnly: () => false }))
vi.mock('../hooks/useGlobalSchedulerPaused', () => ({ useGlobalSchedulerPaused: () => false }))
vi.mock('./PresetModal', () => ({ default: ({ isOpen, editingPreset, fixedWorkflowKind }: any) => isOpen ? <div data-testid="preset-modal">{editingPreset ? 'edit' : 'create'}:{fixedWorkflowKind}</div> : null }))
vi.mock('./GlobalActivityMonitor', () => ({ GlobalActivityMonitor: () => null }))
vi.mock('./ProductSurfaceSwitcher', () => ({ ProductSurfaceSwitcher: () => null }))
vi.mock('./WorkspaceTopBarControls', () => ({ default: () => null }))
vi.mock('./branding/RuntimeBrandLogo', () => ({ RuntimeBrandLogo: () => null }))
vi.mock('./topbar/McpControl', () => ({ default: () => null }))
vi.mock('./topbar/UsersControl', () => ({ default: () => null }))
vi.mock('./topbar/ProvidersControl', () => ({ default: () => null }))
vi.mock('./topbar/GlobalActivityButton', () => ({ GlobalActivityButton: () => <button aria-label="Activity" /> }))
vi.mock('../stores/useProductSurfaceStore', () => ({ useProductSurfaceStore: mocks.store }))
vi.mock('./workflow/WorkflowWalkthrough', () => ({ default: ({ isOpen, surface, onClose }: any) => {
  if (!isOpen) return null
  mocks.renderedTours.push(surface)
  return <button data-testid="tour" onClick={onClose}>{surface}</button>
} }))

import { ModePresetBar } from './ModePresetBar'
import { TooltipProvider } from './ui/tooltip'
import { dismissWorkflowWalkthrough, markLLMDiscoveryOnboardingCleared, markLLMDiscoveryOnboardingOpen } from '../utils/onboarding'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let host: HTMLDivElement
let root: Root
const originalStorage = Object.getOwnPropertyDescriptor(window, 'localStorage')
const render = async (props: React.ComponentProps<typeof ModePresetBar> = {}) => {
  await act(async () => root.render(<TooltipProvider><ModePresetBar {...props} /></TooltipProvider>))
}
const tour = () => host.querySelector('[data-testid="tour"]')

beforeEach(() => {
  const values = new Map<string, string>()
  Object.defineProperty(window, 'localStorage', { configurable: true, value: {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => { values.set(key, value) },
  } })
  delete (window as any).__llmDiscoveryOnboardingState
  delete window.electronAPI
  Object.assign(mocks.state, { workflowPresetsLoaded: false, activePreset: null, workflowPresets: [], showLLMModal: false, loading: false, showPresetCreate: false, canCreate: true, productSurface: 'agentworks' })
  mocks.renderedTours.length = 0
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

it('never renders the temporary empty tour while a saved automation loads', async () => {
  markLLMDiscoveryOnboardingCleared()
  await render()
  expect(tour()).toBeNull()
  mocks.state.loading = true
  await render()
  expect(tour()).toBeNull()
  Object.assign(mocks.state, { workflowPresetsLoaded: true, loading: false, activePreset: { id: 'saved', label: 'Saved automation' } })
  await render()
  expect(tour()?.textContent).toBe('automation')
  expect(mocks.renderedTours).not.toContain('empty-automation')
})

it.each(['browser', 'electron'])('keeps a dismissed restored tour hidden in %s', async client => {
  if (client === 'electron') {
    window.electronAPI = { isWalkthroughDismissed: key => key === 'agentworks_automation_walkthrough_v3_dismissed' }
  } else {
    dismissWorkflowWalkthrough('automation')
  }
  markLLMDiscoveryOnboardingCleared()
  await render()
  Object.assign(mocks.state, { workflowPresetsLoaded: true, activePreset: { id: 'saved', label: 'Saved automation' } })
  await render()
  expect(tour()).toBeNull()
  expect(mocks.renderedTours).toEqual([])
})

it('waits through pending and open provider onboarding, then opens the empty tour', async () => {
  mocks.state.workflowPresetsLoaded = true
  await render()
  expect(tour()).toBeNull()
  await act(async () => markLLMDiscoveryOnboardingOpen())
  expect(tour()).toBeNull()
  await act(async () => markLLMDiscoveryOnboardingCleared())
  expect(tour()?.textContent).toBe('empty-automation')
})

it.each(['crew', 'code'] as const)('uses %s project readiness independently of automation manifests', async surface => {
  markLLMDiscoveryOnboardingCleared()
  await render({ walkthroughSurface: surface, walkthroughReady: false, reduced: true })
  expect(tour()).toBeNull()
  await render({ walkthroughSurface: surface, walkthroughReady: true, reduced: true })
  expect(tour()?.textContent).toBe(surface)
})

it('allows manual help during startup', async () => {
  await render()
  await act(async () => window.dispatchEvent(new Event('open-workflow-walkthrough')))
  expect(tour()?.textContent).toBe('empty-automation')
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


it.each([
  ['agentworks', true, true], ['work', false, true], ['relays', false, false],
  ['code', false, false], ['mcp-gateway', false, false],
])('keeps product actions separate from shared controls for %s', async (surface, activity, schedules) => {
  markLLMDiscoveryOnboardingCleared()
  mocks.state.productSurface = surface
  await render({ reduced: surface !== 'agentworks' })
  const product = host.querySelector('[data-product-navigation-section="product-actions"]')
  const global = host.querySelector('[data-product-navigation-section="global-actions"]')!
  expect(Boolean(product?.querySelector('[aria-label="Activity"]'))).toBe(activity)
  expect(Boolean(product?.querySelector('[data-tour="global-schedules"]'))).toBe(schedules)
  expect(global.querySelector('[aria-label="Activity"]')).toBeNull()
  expect(global.querySelector('[data-tour="global-schedules"]')).toBeNull()
})
