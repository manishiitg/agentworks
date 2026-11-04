// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

vi.mock('../../services/api', () => ({
  agentApi: { getWorkflowBackup: vi.fn() },
  workflowManifestApi: { getWorkflowManifest: vi.fn().mockResolvedValue({ manifest: { kind: 'workflow' } }), updateWorkflowManifest: vi.fn() },
  getApiBaseUrl: () => '',
  getAuthToken: () => '',
}))

import { agentApi } from '../../services/api'
import WorkflowBackupView from './WorkflowBackupView'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => vi.clearAllMocks())

const LONG_BACKEND_SUMMARY = 'Autopilot finalizer backup: committed and pushed 34 files with mirrors, ledgers, notes, learnings, and logs.'

it('shows one short line instead of the backend paragraph', async () => {
  vi.mocked(agentApi.getWorkflowBackup).mockResolvedValue({
    effective_state: 'healthy',
    config: { enabled: true, destinations: [] },
    status: { summary: LONG_BACKEND_SUMMARY },
    supported: [],
    tracked_files_count: 12,
  } as never)
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(<WorkflowBackupView workspacePath="Workflow/test" />))
  try {
    expect(host.textContent).toContain('Healthy')
    expect(host.textContent).toContain('Backed up. Everything is protected.')
    expect(host.textContent).not.toContain(LONG_BACKEND_SUMMARY)
  } finally {
    await act(async () => root.unmount()); host.remove()
  }
})
