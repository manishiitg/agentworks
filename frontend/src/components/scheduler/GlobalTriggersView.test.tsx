// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  listWorkflows: vi.fn(),
  listProjects: vi.fn(),
  listWorkflowTriggers: vi.fn(),
  listProductTriggers: vi.fn(),
}))
vi.mock('../../services/api', () => ({ workflowManifestApi: { listWorkflowManifests: mocks.listWorkflows } }))
vi.mock('../../platform/chat/productProjects', () => ({ loadProductProjects: mocks.listProjects }))
vi.mock('../../api/workflowWebhooks', () => ({ workflowWebhooksApi: { list: mocks.listWorkflowTriggers } }))
vi.mock('../../api/productWebhooks', () => ({ productWebhooksApi: { list: mocks.listProductTriggers } }))
import GlobalTriggersView from './GlobalTriggersView'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

beforeEach(() => {
  vi.clearAllMocks()
  mocks.listWorkflows.mockResolvedValue({ workflows: [{ workspace_path: '/workflows/alpha', manifest: { id: 'alpha', label: 'Alpha' } }] })
  mocks.listProjects.mockResolvedValue([{ id: 'crew-one', title: 'Crew One', identity: { name: 'Research Crew' } }])
  mocks.listWorkflowTriggers.mockResolvedValue({ triggers: [
    { id: 'external', name: 'Start research', enabled: true, path: '/api/hook/external', kind: '' },
    { id: 'internal', name: 'Internal', enabled: true, path: '/api/hook/internal', kind: 'internal' },
  ] })
  mocks.listProductTriggers.mockResolvedValue({ triggers: [
    { id: 'crew-hook', name: 'Daily briefing', enabled: false, path: '/api/hook/crew', kind: '' },
  ] })
})

describe('global triggers overview', () => {
  it('lists external workflow triggers and opens their workflow', async () => {
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    const onOpen = vi.fn()
    try {
      await act(async () => root.render(<GlobalTriggersView kind="workflow" onOpen={onOpen} />))
      expect(mocks.listWorkflowTriggers).toHaveBeenCalledWith('/workflows/alpha')
      expect(host.textContent).toContain('Start research')
      expect(host.textContent).not.toContain('Internal')
      await act(async () => host.querySelector<HTMLButtonElement>('tbody button')?.click())
      expect(onOpen).toHaveBeenCalledWith({ id: 'alpha', label: 'Alpha', kind: 'workflow', workspacePath: '/workflows/alpha' })
    } finally { await act(async () => root.unmount()); host.remove() }
  })

  it('loads Crew triggers from owned projects', async () => {
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<GlobalTriggersView kind="crew" onOpen={() => {}} />))
      expect(mocks.listProductTriggers).toHaveBeenCalledWith({ profileId: 'work', projectId: 'crew-one' })
      expect(host.textContent).toContain('Research Crew')
      expect(host.textContent).toContain('Daily briefing')
      expect(host.textContent).toContain('Paused')
    } finally { await act(async () => root.unmount()); host.remove() }
  })
})
