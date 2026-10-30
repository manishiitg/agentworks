// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

vi.mock('../../services/api', () => ({
  agentApi: { getWorkflowPublish: vi.fn(), getWorkflowPublishSecret: vi.fn(), getShareTunnelStatus: vi.fn() },
  workflowManifestApi: { getWorkflowManifest: vi.fn().mockResolvedValue({ manifest: { kind: 'workflow' } }), updateWorkflowManifest: vi.fn() },
  getApiBaseUrl: () => '',
  getAuthToken: () => '',
}))

import { agentApi } from '../../services/api'
import WorkflowPublishView from './WorkflowPublishView'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => vi.clearAllMocks())

const LONG_BACKEND_SUMMARY = 'Publish pass deployed 34 files with mirrors, ledgers, notes, learnings, and logs.'

it('shows one short line instead of the backend paragraph', async () => {
  vi.mocked(agentApi.getShareTunnelStatus).mockResolvedValue({ active: false })
  vi.mocked(agentApi.getWorkflowPublish).mockResolvedValue({
    effective_state: 'published',
    config: { enabled: true, destinations: [] },
    status: { summary: LONG_BACKEND_SUMMARY },
    supported: [],
  } as never)
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(<WorkflowPublishView workspacePath="Workflow/test" />))
  try {
    expect(host.textContent).toContain('Published. Your site is up to date.')
    expect(host.textContent).not.toContain(LONG_BACKEND_SUMMARY)
  } finally {
    await act(async () => root.unmount()); host.remove()
  }
})

it('shows the active tunnel URL with its whole-server warning', async () => {
  vi.mocked(agentApi.getShareTunnelStatus).mockResolvedValue({
    active: true, public_url: 'https://bright-otter-42.trycloudflare.com', expires_at: '2026-09-21T01:00:00Z',
  })
  vi.mocked(agentApi.getWorkflowPublish).mockResolvedValue({
    effective_state: 'published', config: { enabled: true, destinations: [] }, status: {}, supported: [],
  } as never)
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(<WorkflowPublishView workspacePath="Workflow/test" />))
  try {
    expect(host.textContent).toContain('https://bright-otter-42.trycloudflare.com')
    expect(host.textContent).toContain('exposes the whole server')
  } finally {
    await act(async () => root.unmount()); host.remove()
  }
})

it('hides the internet share section when the status endpoint refuses', async () => {
  vi.mocked(agentApi.getShareTunnelStatus).mockRejectedValue(new Error('forbidden'))
  vi.mocked(agentApi.getWorkflowPublish).mockResolvedValue({
    effective_state: 'published', config: { enabled: true, destinations: [] }, status: {}, supported: [],
  } as never)
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(<WorkflowPublishView workspacePath="Workflow/test" />))
  try {
    expect(host.textContent).not.toContain('Internet share')
  } finally {
    await act(async () => root.unmount()); host.remove()
  }
})
