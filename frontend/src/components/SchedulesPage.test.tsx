// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'

const { captured } = vi.hoisted(() => ({ captured: { props: null as null | { embedded: boolean; active: boolean; onClose: () => void; entityType?: string; productProfileId?: string } } }))
vi.mock('./scheduler/WorkflowScheduleRunsPanel', () => ({ default: (props: { embedded: boolean; active: boolean; onClose: () => void; entityType?: string; productProfileId?: string }) => {
  captured.props = props
  return <div data-testid="schedules-panel" />
} }))
vi.mock('./scheduler/GlobalTriggersView', () => ({ default: ({ kind, onOpen }: { kind: 'workflow' | 'crew'; onOpen: (owner: { id: string; label: string; kind: 'workflow' | 'crew' }) => void }) =>
  <button data-testid={`${kind}-triggers`} onClick={() => onOpen({ id: 'crew-one', label: 'Crew One', kind })}>Open</button> }))
const { storeState } = vi.hoisted(() => ({ storeState: { setShowSchedulesOverview: vi.fn() } }))
vi.mock('../stores/useAppStore', () => ({ useAppStore: (selector: (state: unknown) => unknown) => selector({ showSchedulesOverview: true, setShowSchedulesOverview: storeState.setShowSchedulesOverview }) }))
vi.mock('../stores/useLLMStore', () => ({ useLLMStore: (selector: (state: unknown) => unknown) => selector({ showLLMModal: false }) }))
const { surfaceState } = vi.hoisted(() => ({ surfaceState: { productSurface: 'agentworks', setProductSurface: vi.fn(), setSelectedWorkProjectId: vi.fn(), setPendingWorkView: vi.fn() } }))
vi.mock('../stores/useProductSurfaceStore', () => ({ useProductSurfaceStore: Object.assign((selector: (state: unknown) => unknown) => selector(surfaceState), { getState: () => surfaceState }) }))
vi.mock('../stores/useWorkflowStore', () => ({ useWorkflowStore: { getState: () => ({ openWorkspaceView: vi.fn() }) } }))
vi.mock('../stores/useGlobalPresetStore', () => ({ useGlobalPresetStore: { getState: () => ({ workflowPresets: [], activePresetIds: {} }) } }))
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

  it('switches between Crew schedules and both trigger lists', async () => {
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<SchedulesPage />))
      const clickTab = async (label: string) => {
        const button = Array.from(host.querySelectorAll<HTMLButtonElement>('[role="tab"]')).find(tab => tab.textContent === label)
        await act(async () => button?.click())
      }
      await clickTab('Crew schedules')
      expect(captured.props?.entityType).toBe('product')
      expect(captured.props?.productProfileId).toBe('work')
      await clickTab('Workflow triggers')
      expect(host.querySelector('[data-testid="workflow-triggers"]')).not.toBeNull()
      await clickTab('Crew triggers')
      expect(host.querySelector('[data-testid="crew-triggers"]')).not.toBeNull()
      await act(async () => host.querySelector<HTMLButtonElement>('[data-testid="crew-triggers"]')?.click())
      expect(surfaceState.setSelectedWorkProjectId).toHaveBeenCalledWith('crew-one')
      expect(surfaceState.setPendingWorkView).toHaveBeenCalledWith('triggers')
      expect(surfaceState.setProductSurface).toHaveBeenCalledWith('work')
    } finally { await act(async () => root.unmount()); host.remove() }
  })

  it('opens on Crew schedules when launched from Crew', async () => {
    surfaceState.productSurface = 'work'
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<SchedulesPage />))
      expect(host.querySelector('[role="tab"][aria-selected="true"]')?.textContent).toBe('Crew schedules')
      expect(captured.props?.entityType).toBe('product')
    } finally {
      await act(async () => root.unmount()); host.remove(); surfaceState.productSurface = 'agentworks'
    }
  })
})
