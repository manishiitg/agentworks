// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import WorkflowFunctionsView from './WorkflowFunctionsView'
import { useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import { workflowWebhooksApi } from '../../api/workflowWebhooks'

vi.mock('../../api/workflowWebhooks', () => ({ workflowWebhooksApi: { list: vi.fn(), save: vi.fn(), delete: vi.fn(), relayReleases: vi.fn() } }))
vi.mock('../../hooks/useCanWriteWorkflow', () => ({ useCanWriteWorkflow: vi.fn(() => true) }))
vi.mock('../../services/api', () => ({ getApiBaseUrl: vi.fn(() => 'http://localhost:8000') }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const webhook = { id: 'hook', name: 'GitHub PRs', enabled: true, auth_mode: 'github' as const, path: '/api/hooks/workflow/hook', route_selections: {}, group_names: [] }
const fn = { id: 'fn-1', name: 'Review PR', enabled: true, auth_mode: '' as never, path: '', route_selections: { router: 'review' }, group_names: ['default'], kind: 'function' as const,
  function: { name: 'review_pr', description: 'Review one pull request', inputs: [{ name: 'PR_NUMBER', type: 'integer' as const, required: true }, { name: 'REVIEW_DEPTH' }] } }
const cleanups: (() => void)[] = []
beforeEach(() => {
  vi.mocked(useCanWriteWorkflow).mockReturnValue(true)
  vi.mocked(workflowWebhooksApi.list).mockResolvedValue({ triggers: [webhook, fn], groups: ['default'], routes: [{ step_id: 'router', step_title: 'Review or skip', route_id: 'review', route_name: 'Review' }] })
  vi.mocked(workflowWebhooksApi.save).mockResolvedValue(fn)
  vi.mocked(workflowWebhooksApi.relayReleases).mockResolvedValue({ active_version: 'v1', releases: [{ version: 'v1', workspace_path: 'Workflow/.relay_releases/test/v1', hash: 'abc', published_at: '2026-09-28T00:00:00Z', functions: ['review_pr'], output_step_id: 'answer', file_count: 3 }] })
})
afterEach(() => { cleanups.splice(0).forEach(clean => clean()); vi.clearAllMocks() })

async function mount(onAsk?: (message: string) => void, relayMode = false) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(<WorkflowFunctionsView workspacePath="Workflow/gate" relayMode={relayMode} relayWorkflowID={relayMode ? 'relay-1' : undefined} onAsk={onAsk} />))
  await act(async () => { await Promise.resolve() })
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  return host
}

it('lists typed functions and the built-in ask, not webhooks', async () => {
  const host = await mount()
  const card = host.querySelector('[data-testid="workflow-function-review_pr"]')!
  expect(card.textContent).toContain('PR_NUMBER: integer, REVIEW_DEPTH: string?')
  expect(card.textContent).toContain('Review or skip → Review')
  expect(host.querySelector('[data-testid="workflow-function-ask"]')?.textContent).toContain('Run mode')
  expect(host.textContent).not.toContain('GitHub PRs')
})

it('pauses a function and routes edits to the Builder chat', async () => {
  const onAsk = vi.fn()
  const host = await mount(onAsk)
  const buttons = [...host.querySelectorAll('button')]
  await act(async () => { buttons.find(button => button.textContent === 'Pause')!.click() })
  expect(workflowWebhooksApi.save).toHaveBeenCalledWith(expect.objectContaining({ id: 'fn-1', enabled: false, kind: 'function', workspace_path: 'Workflow/gate' }), 'fn-1')
  await act(async () => { buttons.find(button => button.textContent === 'Edit in chat')!.click() })
  expect(onAsk).toHaveBeenCalledWith(expect.stringContaining('review_pr'))
})

it('offers to add a function when there is none', async () => {
  vi.mocked(workflowWebhooksApi.list).mockResolvedValue({ triggers: [webhook], groups: [], routes: [] })
  const onAsk = vi.fn()
  const host = await mount(onAsk)
  const add = [...host.querySelectorAll('button')].find(button => button.textContent === 'Add a function in chat')!
  await act(async () => { add.click() })
  expect(onAsk).toHaveBeenCalled()
})

it('shows the durable Relay endpoint without the continuing ask function', async () => {
  const host = await mount(undefined, true)
  expect(host.querySelector('[data-testid="workflow-function-ask"]')).toBeNull()
  expect(host.textContent).toContain('External API request')
  expect(host.textContent).toContain('/api/relays/relay-1/runs')
  expect(host.querySelector('[data-testid="relay-release-status"]')?.textContent).toContain('Published v1')
  expect(host.querySelector('[data-testid="workflow-function-review_pr"]')?.textContent).not.toContain('groups default')
})

it('keeps reader API instructions visible while restricting authoring actions', async () => {
  vi.mocked(useCanWriteWorkflow).mockReturnValue(false)
  const host = await mount(vi.fn(), true)
  expect(host.textContent).toContain('External API request')
  expect(host.textContent).toContain('Anyone with access')
  expect(host.textContent).not.toContain('Publish in chat')
  expect(host.textContent).not.toContain('Pause')
})

it('shows a broken active release instead of claiming draft only', async () => {
  vi.mocked(workflowWebhooksApi.relayReleases).mockResolvedValue({ active_version: 'v2', active_error: 'invalid metadata', releases: [{ version: 'v2', workspace_path: 'Workflow/.relay_releases/test/v2', hash: '', published_at: '', functions: [], output_step_id: '', file_count: 0, error: 'invalid metadata' }] })
  const host = await mount(undefined, true)
  expect(host.textContent).toContain('Unavailable v2')
  expect(host.textContent).toContain('v2 (unavailable)')
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('invalid metadata')
})
