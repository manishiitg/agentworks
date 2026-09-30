// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

vi.mock('../../services/api', () => ({
  agentApi: { getWorkflowBackup: vi.fn() },
  getApiBaseUrl: () => '',
  getAuthToken: () => '',
}))
vi.mock('./AskAIButton', () => ({
  AskAIButton: (props: Record<string, unknown>) => (
    <button type="button" data-testid="ask-ai" data-message={String(props.message)}>{String(props.label ?? 'Ask AI')}</button>
  ),
}))

import { agentApi } from '../../services/api'
import WorkflowBackupView from './WorkflowBackupView'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => vi.clearAllMocks())

const LONG_BACKEND_SUMMARY = 'Pulse finalizer backup: committed and pushed 34 files with mirrors, ledgers, notes, learnings, and logs.'

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
    const setup = [...host.querySelectorAll('[data-testid="ask-ai"]')].find(node => node.getAttribute('data-message') === '/backup')
    expect(setup?.textContent).toBe('Set up')
  } finally {
    await act(async () => root.unmount()); host.remove()
  }
})
