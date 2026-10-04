// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi, afterAll } from 'vitest'
vi.hoisted(() => vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => {}, removeItem: () => {} }))

vi.mock('../stores/useLLMStore', () => ({ useLLMStore: { getState: () => ({ setShowLLMModal: vi.fn() }) } }))
vi.mock('../stores/useGlobalPresetStore', () => ({ useGlobalPresetStore: { getState: () => ({ getActivePreset: () => null }) } }))
vi.mock('./EmployeeDashboard', () => ({ EmployeeDashboard: () => <input aria-label="Find updates" /> }))
import ActivityPage from './ActivityPage'
import { useAppStore } from '../stores/useAppStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterAll(() => vi.unstubAllGlobals())

describe('Activity page', () => {
  it('shows the updates dashboard with no schedules tab', async () => {
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      useProductSurfaceStore.setState({ productSurface: 'agentworks' })
      useAppStore.setState({ showWorkflowsOverview: true, activityWorkflowPath: 'Workflow/demo' })
      await act(async () => root.render(<ActivityPage />))
      expect(host.querySelector('h1')?.textContent).toBe('Activity')
      expect(host.querySelector('[aria-label="Find updates"]')).not.toBeNull()
      expect(host.querySelector('[role="tablist"]')).toBeNull()
      expect(host.querySelector('#activity-tab-schedules')).toBeNull()
      const back = Array.from(host.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Back to Goals')!
      await act(async () => back.click())
      expect(useAppStore.getState().showWorkflowsOverview).toBe(false)
      expect(useAppStore.getState().activityWorkflowPath).toBeNull()
    } finally { await act(async () => root.unmount()); host.remove() }
  })
})
