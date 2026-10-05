// @vitest-environment happy-dom
import { act, type ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import WorkflowAPITriggersView from './WorkflowAPITriggersView'
import { agentApi } from '../../services/api'
import { workflowWebhooksApi } from '../../api/workflowWebhooks'
import { useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import { TooltipProvider } from '../ui/tooltip'
import { useCapabilitiesStore } from '../../stores/useCapabilitiesStore'

vi.mock('../../api/workflowWebhooks', () => ({ workflowWebhooksApi: { list: vi.fn(), save: vi.fn(), delete: vi.fn() }, apiTriggerURL: (path: string) => `https://agent.example${path}` }))
vi.mock('../../services/api', () => ({ getApiBaseUrl: () => '', getAuthToken: () => null, agentApi: { getGmailInboundRoute: vi.fn().mockResolvedValue({ configured: true, route: null, deliveries: [] }) } }))
vi.mock('../../hooks/useCanWriteWorkflow', () => ({ useCanWriteWorkflow: vi.fn(() => true) }))
vi.mock('../../stores/useWorkflowManifestStore', () => ({ useWorkflowManifestStore: { getState: () => ({ refreshWorkflows: vi.fn().mockResolvedValue(undefined) }) } }))
vi.mock('../../stores/useWorkflowStore', () => ({ useWorkflowStore: { getState: () => ({ openWorkspaceView: vi.fn() }) } }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const trigger = { id: 'trigger-1', name: 'Issues', enabled: true, auth_mode: 'github' as const, path: '/api/hooks/workflow/trigger-1', route_selections: { router: 'issues' }, group_names: ['prod'] }
const cleanups: (() => void)[] = []
beforeEach(() => {
  // Automatic incoming Gmail is a server-deployment surface (PLAT-500); the panel renders only when local_mode is false.
  useCapabilitiesStore.setState({ capabilities: { providers: [], streaming: true, sse: true, agent_modes: [], tracing: { enabled: false, provider: 'noop' }, workspace: {}, servers: [], local_mode: false } })
  vi.mocked(useCanWriteWorkflow).mockReturnValue(true)
  vi.mocked(workflowWebhooksApi.list).mockResolvedValue({ triggers: [trigger], groups: ['prod'], routes: [{ step_id: 'router', step_title: 'Choose work', route_id: 'issues', route_name: 'Process issues' }] })
  vi.mocked(workflowWebhooksApi.save).mockImplementation(async value => ({ ...trigger, ...value }))
})
afterEach(() => { cleanups.splice(0).forEach(clean => clean()); vi.clearAllMocks() })
type MountProps = { onViewRuns?: () => void; deliveryHistory?: ReactNode; hideHeader?: boolean; refreshToken?: number; onCounts?: (counts: { active: number; paused: number }) => void }
async function mount(props: MountProps = {}) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(<TooltipProvider><WorkflowAPITriggersView workspacePath="Workflow/test" {...props} /></TooltipProvider>))
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  return host
}
async function mountRerenderable(initial: MountProps) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  const render = async (props: MountProps) => { await act(async () => root.render(<TooltipProvider><WorkflowAPITriggersView workspacePath="Workflow/test" {...props} /></TooltipProvider>)) }
  await render(initial)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  return { host, render }
}
function button(host: HTMLElement, label: string) {
  const found = [...host.querySelectorAll('button')].find(node => node.textContent === label)
  if (!found) throw new Error(`Missing button ${label}`)
  return found
}
it('shows endpoint, saved route, authentication and group', async () => {
  const host = await mount()
  expect(host.textContent).toContain('https://agent.example/api/hooks/workflow/trigger-1')
  expect(host.textContent).toContain('Choose work → Process issues')
  expect(host.textContent).toContain('GitHub signature')
  expect(host.textContent).toContain('Groups: prod')
  expect(host.textContent).toContain('up to 4 concurrent deliveries')
})
it('shows payload mappings configured by the builder', async () => {
  vi.mocked(workflowWebhooksApi.list).mockResolvedValue({
    triggers: [{ ...trigger, payload_mappings: {
      group: { source: 'env', values: { prod: 'confida-prod' } },
      routes: { 'component-branch': { source: 'component', values: { 'service/review': 'review' } } },
    } }],
    groups: ['confida-prod'],
    routes: [{ step_id: 'router', step_title: 'Choose work', route_id: 'issues', route_name: 'Process issues' }],
  })
  const host = await mount()
  expect(host.textContent).toContain('Payload env → group (prod → confida-prod)')
  expect(host.textContent).toContain('Payload component → component-branch branch (service/review → review)')
})
it('disables a trigger while retaining its route binding', async () => {
  const host = await mount()
  await act(async () => button(host, 'Pause').click())
  expect(workflowWebhooksApi.save).toHaveBeenCalledWith(expect.objectContaining({ workspace_path: 'Workflow/test', enabled: false, route_selections: { router: 'issues' } }), 'trigger-1')
})
it('rotates and displays a secret only until dismissed', async () => {
  vi.mocked(workflowWebhooksApi.save).mockResolvedValue({ ...trigger, secret: 'new-one-time-secret' })
  const host = await mount()
  expect(host.textContent).not.toContain('new-one-time-secret')
  await act(async () => button(host, 'Rotate secret').click())
  expect(workflowWebhooksApi.save).toHaveBeenCalledWith(expect.objectContaining({ rotate_secret: true }), 'trigger-1')
  expect(host.textContent).toContain('new-one-time-secret')
  expect(host.textContent).toContain('Setup pings do not start runs')
  await act(async () => button(host, 'Dismiss secret').click())
  expect(host.textContent).not.toContain('new-one-time-secret')
})
it('lets a reader inspect but not change triggers', async () => {
  vi.mocked(useCanWriteWorkflow).mockReturnValue(false)
  const host = await mount()
  expect(host.textContent).toContain('Process issues')
  expect(host.textContent).not.toContain('Rotate secret')
  expect(host.textContent).not.toContain('Add API trigger')
  expect(host.textContent).not.toContain('Remove')
})
it('directs creation and configuration to the builder chat without a manual form', async () => {
  const host = await mount()
  expect(host.textContent).toContain('by asking Builder')
  expect(host.textContent).not.toContain('Add API trigger')
  expect(host.querySelector('form')).toBeNull()
  expect([...host.querySelectorAll('button')].some(node => node.textContent === 'Edit')).toBe(false)
})
it('renders older webhooks whose optional maps were stored as null', async () => {
  vi.mocked(workflowWebhooksApi.list).mockResolvedValue({
    triggers: [{ ...trigger, route_selections: null, group_names: null, payload_mappings: { routes: { legacy: { source: 'kind', values: null } } } } as unknown as typeof trigger],
    groups: [],
    routes: [],
  })
  const host = await mount()
  expect(host.textContent).toContain('Full workflow')
  expect(host.textContent).toContain('Default access')
})
it('removes only the selected trigger', async () => {
  const host = await mount()
  await act(async () => button(host, 'Remove').click())
  expect(workflowWebhooksApi.delete).toHaveBeenCalledWith('Workflow/test', 'trigger-1')
})

it('keeps delivery history inside Triggers when an embedded feed is available', async () => {
  const onViewRuns = vi.fn()
  const host = await mount({ onViewRuns, deliveryHistory: <div>Recorded webhook deliveries</div> })
  expect(host.textContent).toContain('Recorded webhook deliveries')
  expect(host.textContent).not.toContain('View delivery history')
  expect(onViewRuns).not.toHaveBeenCalled()
})
it('hides its header when embedded in the hub but keeps trigger content', async () => {
  const host = await mount({ hideHeader: true })
  expect(host.textContent).not.toContain('External events that start this workflow.')
  expect(host.querySelector('button[aria-label="Refresh webhooks"]')).toBeNull()
  expect(host.textContent).toContain('Issues')
  expect(host.textContent).toContain('https://agent.example/api/hooks/workflow/trigger-1')
})
it('reloads and reports counts when the hub bumps its refresh token', async () => {
  const onCounts = vi.fn()
  const { render } = await mountRerenderable({ refreshToken: 0, onCounts })
  expect(workflowWebhooksApi.list).toHaveBeenCalledTimes(1)
  expect(onCounts).toHaveBeenLastCalledWith({ active: 1, paused: 0 })
  await render({ refreshToken: 1, onCounts })
  expect(workflowWebhooksApi.list).toHaveBeenCalledTimes(2)
  expect(onCounts).toHaveBeenLastCalledWith({ active: 1, paused: 0 })
})
it('keeps functions out of the webhooks list and points to the Functions tab', async () => {
  const fn = { id: 'fn-1', name: 'Review PR', enabled: true, auth_mode: '' as never, path: '', route_selections: { router: 'issues' }, group_names: ['prod'], kind: 'function' as const,
    function: { name: 'review_pr', inputs: [{ name: 'PR_NUMBER', type: 'integer' as const, required: true }] } }
  vi.mocked(workflowWebhooksApi.list).mockResolvedValue({ triggers: [trigger, fn], groups: ['prod'], routes: [] })
  const host = await mount()
  expect(host.textContent).toContain('Issues')
  expect(host.textContent).not.toContain('review_pr')
  expect(host.textContent).toContain('Automation → Functions')
})

it('shows the Builder Gmail binding as a read-only trigger', async () => {
  vi.mocked(workflowWebhooksApi.list).mockResolvedValue({ triggers: [{ ...trigger, id: 'gmail', kind: 'gmail', path: '', name: 'Mail triage' }], groups: ['prod'], routes: [] })
  vi.mocked(agentApi.getGmailInboundRoute).mockResolvedValue({ configured: true, watch_ready: true, deliveries: [], route: { id: 'gmail', name: 'Mail triage', address: 'owner+agent-id@example.com', connection_id: 'mail', enabled: true, reply: true, workflow_trigger: true, route_selections: { triage: 'support' }, group_names: ['prod'] } })
  const host = await mount()
  expect(host.textContent).toContain('owner+agent-id@example.com')
  expect(host.textContent).toContain('triage → support')
  expect(host.textContent).not.toContain('/api/hooks/workflow/gmail')
  expect(host.querySelector('input, select')).toBeNull()
  expect([...host.querySelectorAll('button')].some(button => ['Pause', 'Enable', 'Remove', 'Rotate secret'].includes(button.textContent || ''))).toBe(false)
})
