// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import WorkflowFunctionsView from './WorkflowFunctionsView'
import { useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import { workflowWebhooksApi } from '../../api/workflowWebhooks'

vi.mock('../../api/workflowWebhooks', () => ({ workflowWebhooksApi: { list: vi.fn(), save: vi.fn(), delete: vi.fn() } }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: vi.fn() }))
vi.mock('../../hooks/useCanWriteWorkflow', () => ({ useCanWriteWorkflow: vi.fn(() => true) }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: () => {} }))
vi.mock('../../services/api', () => ({ getApiBaseUrl: vi.fn(() => 'http://localhost:8000'), getAuthToken: () => '' }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const webhook = { id: 'hook', name: 'GitHub PRs', enabled: true, auth_mode: 'github' as const, path: '/api/hooks/workflow/hook', route_selections: {}, input_preset: undefined }
const fn = { id: 'fn-1', name: 'Review PR', enabled: true, auth_mode: '' as never, path: '', route_selections: { router: 'review' }, input_preset: 'default', kind: 'function' as const,
  input_schema: { properties: { PR_NUMBER: { type: 'integer' }, REVIEW_DEPTH: { type: 'string' } }, required: ['PR_NUMBER'] }, function: { name: 'review_pr', description: 'Review one pull request', inputs: ['PR_NUMBER', 'REVIEW_DEPTH'] } }
const cleanups: (() => void)[] = []
beforeEach(() => {
  vi.mocked(useCanWriteWorkflow).mockReturnValue(true)
  vi.mocked(workflowWebhooksApi.list).mockResolvedValue({ triggers: [webhook, fn], input_presets: ['default'], routes: [{ step_id: 'router', step_title: 'Review or skip', route_id: 'review', route_name: 'Review' }] })
  vi.mocked(workflowWebhooksApi.save).mockResolvedValue(fn)
})
afterEach(() => { cleanups.splice(0).forEach(clean => clean()); vi.clearAllMocks() })

async function mount(onAsk?: (message: string) => void) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(<WorkflowFunctionsView workspacePath="Workflow/gate" onAsk={onAsk} />))
  await act(async () => { await Promise.resolve() })
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  return host
}

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
  vi.mocked(workflowWebhooksApi.list).mockResolvedValue({ triggers: [webhook], input_presets: [], routes: [] })
  const onAsk = vi.fn()
  const host = await mount(onAsk)
  const add = [...host.querySelectorAll('button')].find(button => button.textContent === 'Add a function in chat')!
  await act(async () => { add.click() })
  expect(onAsk).toHaveBeenCalled()
})

// PLAT-746-265: changing the destination preserves the saved function;
// Run people have no authoring control for the owner's chats.
it('saves the chosen chat without changing the callable function', async () => {
  vi.mocked(workflowWebhooksApi.list).mockResolvedValue({ triggers: [fn], input_presets: ['default'], routes: [], chat_choices: [{ id: 'main', label: 'Main chat' }, { id: 'reporting', label: 'Reporting' }] })
  const host = await mount()
  const select = host.querySelector('select[aria-label="Chat for review_pr"]') as HTMLSelectElement
  expect(select.value).toBe('main')
  await act(async () => { select.value = 'reporting'; select.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(workflowWebhooksApi.save).toHaveBeenCalledWith(expect.objectContaining({ chat_id: 'reporting', function: fn.function, kind: 'function', workspace_path: 'Workflow/gate' }), fn.id)
  vi.mocked(useCanWriteWorkflow).mockReturnValue(false)
  const reader = await mount()
  expect(reader.querySelector('select[aria-label="Chat for review_pr"]')).toBeNull()
})
