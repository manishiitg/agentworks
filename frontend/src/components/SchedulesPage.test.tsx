// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'

const { captured } = vi.hoisted(() => ({ captured: { props: null as null | { embedded: boolean; active: boolean; onClose: () => void; entityType?: string; productProfileId?: string; workflowKind?: string } } }))
vi.mock('./scheduler/WorkflowScheduleRunsPanel', () => ({ default: (props: { embedded: boolean; active: boolean; onClose: () => void; entityType?: string; productProfileId?: string; workflowKind?: string }) => {
  captured.props = props
  return <div data-testid="schedules-panel" />
} }))
vi.mock('./scheduler/GlobalTriggersView', () => ({ default: ({ kind, onOpen }: { kind: 'workflow' | 'crew'; onOpen: (owner: { id: string; label: string; kind: 'workflow' | 'crew' }) => void }) =>
  <button data-testid={`${kind}-triggers`} onClick={() => onOpen({ id: 'crew-one', label: 'Crew One', kind })}>Open</button> }))
const { storeState } = vi.hoisted(() => ({ storeState: { setShowSchedulesOverview: vi.fn(), setShowWorkflowsOverview: vi.fn(), setAdminPage: vi.fn(), setModeCategory: vi.fn(), setActivityWorkflowPath: vi.fn() } }))
vi.mock('../stores/useAppStore', () => ({ useAppStore: Object.assign((selector: (state: unknown) => unknown) => selector({ showSchedulesOverview: true, ...storeState }), { getState: () => storeState }) }))
vi.mock('../stores/useLLMStore', () => ({ useLLMStore: Object.assign((selector: (state: unknown) => unknown) => selector({ showLLMModal: false }), { getState: () => ({ setShowLLMModal: vi.fn() }) }) }))
const { surfaceState } = vi.hoisted(() => ({ surfaceState: { productSurface: 'agentworks', setProductSurface: vi.fn(), setSelectedWorkProjectId: vi.fn(), setPendingWorkView: vi.fn() } }))
vi.mock('../stores/useProductSurfaceStore', () => ({ useProductSurfaceStore: Object.assign((selector: (state: unknown) => unknown) => selector(surfaceState), { getState: () => surfaceState }) }))
vi.mock('../stores/useWorkflowStore', () => ({ useWorkflowStore: { getState: () => ({ openWorkspaceView: vi.fn() }) } }))
vi.mock('../stores/useGlobalPresetStore', () => ({ useGlobalPresetStore: { getState: () => ({ workflowPresets: [], activePresetIds: {}, getActivePreset: () => null }) } }))
vi.mock('../utils/workflowNavigation', () => ({ selectWorkflowPreset: vi.fn() }))
vi.mock('../utils/workflowSessionRestore', () => ({ openWorkflowPresetPage: vi.fn() }))
import SchedulesPage from './SchedulesPage'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

describe('Schedules page', () => {
  it('embeds the active schedules panel and closes back to the workspace', async () => {
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<SchedulesPage />))
      expect(host.querySelector('h1')?.textContent).toBe('Schedules and triggers')
      expect(host.querySelector('[data-testid="schedules-panel"]')).not.toBeNull()
      expect(captured.props?.embedded).toBe(true)
      expect(captured.props?.active).toBe(true)
      await act(async () => captured.props?.onClose())
      expect(storeState.setShowSchedulesOverview).toHaveBeenCalledWith(false)
    } finally { await act(async () => root.unmount()); host.remove() }
  })

  it.each([['agentworks', 'Goals'], ['work', 'Crew'], ['code', 'Code'], ['mcp-gateway', 'Vault']])('returns from Schedules to %s with a labeled page action', async (surface, label) => {
    surfaceState.productSurface = surface
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<SchedulesPage />))
      const back = Array.from(host.querySelectorAll('header button')).find(button => button.textContent?.trim() === `Back to ${label}`) as HTMLButtonElement
      expect(back).toBeDefined()
      await act(async () => back.click())
      expect(storeState.setShowSchedulesOverview).toHaveBeenCalledWith(false)
      expect(storeState.setShowWorkflowsOverview).toHaveBeenCalledWith(false)
      expect(surfaceState.setProductSurface).toHaveBeenCalledWith(surface)
    } finally { await act(async () => root.unmount()); host.remove(); surfaceState.productSurface = 'agentworks' }
  })

  it.each([['agentworks', 'workflow'], ['work', undefined]])('shows only the schedules and triggers for %s', async (surface, kind) => {
    surfaceState.productSurface = surface
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<SchedulesPage />))
      expect(Array.from(host.querySelectorAll('[role="tab"]')).map(tab => tab.textContent)).toEqual(['Schedules', 'Triggers'])
      expect(captured.props?.workflowKind).toBe(kind)
      expect(captured.props?.entityType).toBe(surface === 'work' ? 'product' : 'workflow')
      const triggerTab = Array.from(host.querySelectorAll<HTMLButtonElement>('[role="tab"]')).find(tab => tab.textContent === 'Triggers')!
      await act(async () => triggerTab.click())
      expect(host.querySelector(`[data-testid="${surface === 'work' ? 'crew' : 'workflow'}-triggers"]`)).not.toBeNull()
    } finally { await act(async () => root.unmount()); host.remove(); surfaceState.productSurface = 'agentworks' }
  })

  it('opens on Crew schedules when launched from Crew', async () => {
    surfaceState.productSurface = 'work'
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<SchedulesPage />))
      expect(host.querySelector('[role="tab"][aria-selected="true"]')?.textContent).toBe('Schedules')
      expect(captured.props?.entityType).toBe('product')
    } finally {
      await act(async () => root.unmount()); host.remove(); surfaceState.productSurface = 'agentworks'
    }
  })
})
