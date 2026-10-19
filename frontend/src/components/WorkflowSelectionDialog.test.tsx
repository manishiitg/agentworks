// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import WorkflowSelectionDialog from './WorkflowSelectionDialog'
import { workflowManifestApi } from '../services/api'
import { loadProductProjects } from '../platform/chat/productProjects'
import { listFunctionReferences } from '../api/functionReferences'

vi.mock('../api/functionReferences', () => ({ listFunctionReferences: vi.fn() }))
vi.mock('../services/api', () => ({ workflowManifestApi: { listWorkflowManifests: vi.fn() } }))
vi.mock('../platform/chat/productProjects', () => ({ loadProductProjects: vi.fn() }))
vi.mock('../stores/useAuthStore', () => ({ useAuthStore: (selector: (state: { user: { id: string } }) => unknown) => selector({ user: { id: 'reader' } }) }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
beforeEach(() => { vi.mocked(listFunctionReferences).mockReset().mockResolvedValue([]) })
afterEach(() => { cleanups.splice(0).forEach(fn => fn()); vi.clearAllMocks() })
const allowed = { success: true, total: 1, workflows: [{ workspace_path: 'Workflow/shared', manifest: { id: 'shared', label: 'Shared automation' } }] }
async function mount(onSelectWorkflow = vi.fn(), onClose = vi.fn()) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  const render = async (isOpen: boolean, searchQuery = '') => { await act(async () => root.render(<WorkflowSelectionDialog isOpen={isOpen} onClose={onClose} onSelectWorkflow={onSelectWorkflow} searchQuery={searchQuery} position={{ bottom: 0, left: 0 }} />)) }
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  await render(true)
  return { host, render }
}
it('shows only the fresh server list and drops revoked workflows when reopened', async () => {
  vi.mocked(loadProductProjects).mockResolvedValue([])
  vi.mocked(workflowManifestApi.listWorkflowManifests).mockResolvedValueOnce(allowed as Awaited<ReturnType<typeof workflowManifestApi.listWorkflowManifests>>).mockResolvedValueOnce({ success: true, total: 0, workflows: [] })
  const { host, render } = await mount()
  expect(host.textContent).toContain('Shared automation')
  await render(false); await render(true)
  expect(host.textContent).not.toContain('Shared automation')
  expect(host.textContent).toContain('No accessible workflows or Crews')
  expect(workflowManifestApi.listWorkflowManifests).toHaveBeenCalledTimes(2)
})
it('does not reuse stale results when checking permissions fails', async () => {
  vi.mocked(loadProductProjects).mockResolvedValue([])
  vi.mocked(workflowManifestApi.listWorkflowManifests).mockResolvedValueOnce(allowed as Awaited<ReturnType<typeof workflowManifestApi.listWorkflowManifests>>).mockRejectedValueOnce(new Error('offline'))
  const { host, render } = await mount()
  await render(false); await render(true)
  expect(host.textContent).not.toContain('Shared automation')
  expect(host.textContent).toContain('Unable to load accessible references')
})

it('moves one row per arrow key in the search input and closes once', async () => {
  vi.mocked(loadProductProjects).mockResolvedValue([])
  const workflows = ['latency-report', 'aws-project', 'automation-testing'].map(label => ({ workspace_path: `Workflow/${label}`, manifest: { id: label, label } }))
  vi.mocked(workflowManifestApi.listWorkflowManifests).mockResolvedValueOnce({ success: true, total: 3, workflows } as Awaited<ReturnType<typeof workflowManifestApi.listWorkflowManifests>>)
  const onSelect = vi.fn(); const onClose = vi.fn()
  const { host } = await mount(onSelect, onClose)
  const input = host.querySelector('input')!
  const press = async (key: string) => { await act(async () => { input.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true })) }) }
  await press('ArrowDown'); await press('Enter')
  expect(onSelect).toHaveBeenLastCalledWith(expect.objectContaining({ label: 'aws-project' }))
  await press('ArrowDown'); await press('Enter')
  expect(onSelect).toHaveBeenLastCalledWith(expect.objectContaining({ label: 'automation-testing' }))
  await press('ArrowUp'); await press('Enter')
  expect(onSelect).toHaveBeenLastCalledWith(expect.objectContaining({ label: 'aws-project' }))
  await press('Escape')
  expect(onClose).toHaveBeenCalledTimes(1)
})

it('lists Crew projects with identity and selects their guarded workspace path', async () => {
  vi.mocked(workflowManifestApi.listWorkflowManifests).mockResolvedValueOnce(allowed as Awaited<ReturnType<typeof workflowManifestApi.listWorkflowManifests>>)
  vi.mocked(loadProductProjects).mockResolvedValueOnce([{
    schemaVersion: 1,
    product: 'work',
    id: 'crew-1',
    title: 'Release operations',
    description: '',
    identity: { name: 'Harbor', icon: '⚓' },
    templates: [],
    sessionId: 'work:project:crew-1',
    workspacePath: 'Chats/Work/projects/release-operations-crew-1',
    createdAt: '2026-09-18T00:00:00Z',
    updatedAt: '2026-09-18T00:00:00Z',
    selectedServers: [],
    selectedSkills: [],
    selectedSecrets: [],
    selectedGlobalSecrets: [],
    workflowContextPaths: [],
    selectionConfigInitialized: true,
    secretSelectionInitialized: true,
    runtimeConfigInitialized: true,
  }])
  const onSelect = vi.fn()
  const { host } = await mount(onSelect)

  expect(host.textContent).toContain('Shared automation')
  expect(host.textContent).toContain('Harbor')
  expect(host.textContent).not.toContain('Release operations')
  expect(host.textContent).not.toContain('Chats/Work/projects/')
  expect(host.textContent).toContain('crew')
  const crewRow = [...host.querySelectorAll('[class*="cursor-pointer"]')].find(row => row.textContent?.includes('Harbor'))
  expect(crewRow).toBeDefined()
  expect(crewRow!.getAttribute('title')).toContain('Release operations')
  expect(crewRow!.getAttribute('title')).toContain('Chats/Work/projects/release-operations-crew-1')
  await act(async () => crewRow!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true })))
  expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({
    presetId: 'crew:crew-1',
    label: 'Harbor',
    workspacePath: 'Chats/Work/projects/release-operations-crew-1',
    kind: 'crew',
  }))
})

it('closes itself when the typed reference matches nothing (e.g. a ticket number)', async () => {
  vi.mocked(loadProductProjects).mockResolvedValue([])
  vi.mocked(workflowManifestApi.listWorkflowManifests).mockResolvedValue(allowed as Awaited<ReturnType<typeof workflowManifestApi.listWorkflowManifests>>)
  const onClose = vi.fn()
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => root.render(<WorkflowSelectionDialog isOpen onClose={onClose} onSelectWorkflow={vi.fn()} searchQuery="1764" position={{ bottom: 0, left: 0 }} />))
  expect(onClose).toHaveBeenCalledTimes(1)
  expect(host.textContent).not.toContain('No references found')
  // A matching query keeps it open.
  onClose.mockClear()
  await act(async () => root.render(<WorkflowSelectionDialog isOpen onClose={onClose} onSelectWorkflow={vi.fn()} searchQuery="shared" position={{ bottom: 0, left: 0 }} />))
  expect(host.textContent).toContain('Shared automation')
  expect(onClose).not.toHaveBeenCalled()
})

it('shows fresh workflow references while Crew discovery is still pending, then retains only authorized successes on failure', async () => {
  let rejectCrews!: (reason: Error) => void
  vi.mocked(workflowManifestApi.listWorkflowManifests).mockResolvedValueOnce(allowed as Awaited<ReturnType<typeof workflowManifestApi.listWorkflowManifests>>)
  vi.mocked(loadProductProjects).mockReturnValueOnce(new Promise((_, reject) => { rejectCrews = reject }))
  const onSelect = vi.fn()
  const { host } = await mount(onSelect)
  expect(host.textContent).toContain('Shared automation')
  expect(host.textContent).toContain('Loading more references')
  const row = [...host.querySelectorAll('[class*="cursor-pointer"]')].find(row => row.textContent?.includes('Shared automation'))!
  await act(async () => row.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true })))
  expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ workspacePath: 'Workflow/shared' }))
  await act(async () => rejectCrews(new Error('access discovery unavailable')))
  expect(host.textContent).toContain('Shared automation')
  expect(host.textContent).toContain('Some references could not be loaded')
})

it('ignores slow results from a previous opening after fresh discovery has revoked access', async () => {
  let finishOld!: (result: Awaited<ReturnType<typeof workflowManifestApi.listWorkflowManifests>>) => void
  vi.mocked(workflowManifestApi.listWorkflowManifests)
    .mockReturnValueOnce(new Promise(resolve => { finishOld = resolve }))
    .mockResolvedValueOnce({ success: true, total: 0, workflows: [] })
  vi.mocked(loadProductProjects).mockResolvedValue([])
  const { host, render } = await mount()
  await render(false)
  await render(true)
  expect(host.textContent).toContain('No accessible workflows or Crews')
  await act(async () => finishOld(allowed as Awaited<ReturnType<typeof workflowManifestApi.listWorkflowManifests>>))
  expect(host.textContent).not.toContain('Shared automation')
})

it('keeps the highlighted Crew when a slow workflow list inserts rows ahead of it', async () => {
  let finishWorkflows!: (result: Awaited<ReturnType<typeof workflowManifestApi.listWorkflowManifests>>) => void
  vi.mocked(workflowManifestApi.listWorkflowManifests).mockReturnValueOnce(new Promise(resolve => { finishWorkflows = resolve }))
  vi.mocked(loadProductProjects).mockResolvedValueOnce(['alpha', 'beta'].map(id => ({
    schemaVersion: 1, product: 'work', id, title: id, description: '', templates: [],
    sessionId: `work:${id}`, workspacePath: `Chats/Work/projects/${id}`,
    createdAt: '', updatedAt: '', selectedServers: [], selectedSkills: [],
    selectedSecrets: [], selectedGlobalSecrets: [], workflowContextPaths: [],
    selectionConfigInitialized: true, secretSelectionInitialized: true, runtimeConfigInitialized: true,
  })))
  const onSelect = vi.fn()
  const { host } = await mount(onSelect)
  const input = host.querySelector('input')!
  await act(async () => input.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true })))
  await act(async () => finishWorkflows(allowed as Awaited<ReturnType<typeof workflowManifestApi.listWorkflowManifests>>))
  await act(async () => input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })))
  expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ presetId: 'crew:beta', label: 'beta' }))
})

it('shows functions inline without stealing focus and selects the highlighted project or function', async () => {
  vi.mocked(loadProductProjects).mockResolvedValue([])
  vi.mocked(workflowManifestApi.listWorkflowManifests).mockResolvedValue(allowed as Awaited<ReturnType<typeof workflowManifestApi.listWorkflowManifests>>)
  vi.mocked(listFunctionReferences).mockResolvedValue([
    { name: 'export_report', description: 'Export this month’s report' },
    { name: 'send_report', description: 'Send the report to the team' },
  ])
  const onSelect = vi.fn()
  const { host } = await mount(onSelect)
  const input = host.querySelector('input')!
  const press = async (key: string) => { await act(async () => input.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }))) }
  expect(host.querySelector('[aria-label^="Browse functions"]')).toBeNull()
  expect(listFunctionReferences).toHaveBeenCalledWith('workflow:shared')
  expect(host.textContent).toContain('export_report')
  expect(host.querySelector('[title="Export this month’s report"]')).not.toBeNull()
  expect(document.activeElement).toBe(input)
  await press('Enter')
  expect(onSelect).toHaveBeenLastCalledWith(expect.objectContaining({ presetId: 'workflow:shared' }))
  expect(onSelect.mock.lastCall?.[0].functionName).toBeUndefined()
  await press('ArrowDown'); await press('Enter')
  expect(onSelect).toHaveBeenLastCalledWith(expect.objectContaining({ functionName: 'export_report' }))
  await press('ArrowDown'); await press('Enter')
  expect(onSelect).toHaveBeenLastCalledWith(expect.objectContaining({ functionName: 'send_report' }))
  await press('ArrowUp'); await press('Enter')
  expect(onSelect).toHaveBeenLastCalledWith(expect.objectContaining({ functionName: 'export_report' }))
  expect(document.activeElement).toBe(input)
  expect(listFunctionReferences).toHaveBeenCalledTimes(1)
})

it('uses the same arrow selection when a function button receives keyboard focus', async () => {
  vi.mocked(loadProductProjects).mockResolvedValue([])
  vi.mocked(workflowManifestApi.listWorkflowManifests).mockResolvedValue(allowed as Awaited<ReturnType<typeof workflowManifestApi.listWorkflowManifests>>)
  vi.mocked(listFunctionReferences).mockResolvedValue([{ name: 'first' }, { name: 'second' }])
  const onSelect = vi.fn()
  const { host } = await mount(onSelect)
  const button = host.querySelector<HTMLButtonElement>('[data-testid="reference-functions"] button')!
  await act(async () => button.focus())
  await act(async () => button.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true })))
  await act(async () => button.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })))
  expect(onSelect).toHaveBeenCalledTimes(1)
  expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ functionName: 'second' }))
})

it('discards old function metadata when the picker is closed and access is revoked', async () => {
  let finishFunctions!: (value: Awaited<ReturnType<typeof listFunctionReferences>>) => void
  vi.mocked(loadProductProjects).mockResolvedValue([])
  vi.mocked(workflowManifestApi.listWorkflowManifests).mockResolvedValueOnce(allowed as Awaited<ReturnType<typeof workflowManifestApi.listWorkflowManifests>>).mockResolvedValueOnce({ success: true, total: 0, workflows: [] })
  vi.mocked(listFunctionReferences).mockReturnValueOnce(new Promise(resolve => { finishFunctions = resolve }))
  const { host, render } = await mount()
  expect(listFunctionReferences).toHaveBeenCalledWith('workflow:shared')
  await render(false); await render(true)
  await act(async () => finishFunctions([{ name: 'private_old_function' }]))
  expect(host.textContent).not.toContain('private_old_function')
  expect(host.querySelector('[data-testid="reference-functions"]')).toBeNull()
})

it('searches function names across accessible Crew, Goal and Flow projects without a separate expansion step', async () => {
  vi.mocked(workflowManifestApi.listWorkflowManifests).mockResolvedValue({ success: true, total: 2, workflows: [
    { workspace_path: 'Workflow/goal', manifest: { id: 'goal', label: 'Sales goal' } },
    { workspace_path: 'Workflow/flow', manifest: { id: 'flow', label: 'Invoice flow' } },
  ] } as Awaited<ReturnType<typeof workflowManifestApi.listWorkflowManifests>>)
  vi.mocked(loadProductProjects).mockResolvedValue([{
    schemaVersion: 1, product: 'work', id: 'crew', title: 'Operations', description: '', templates: [],
    sessionId: 'work:crew', workspacePath: 'Chats/Work/projects/crew',
    createdAt: '', updatedAt: '', selectedServers: [], selectedSkills: [],
    selectedSecrets: [], selectedGlobalSecrets: [], workflowContextPaths: [],
    selectionConfigInitialized: true, secretSelectionInitialized: true, runtimeConfigInitialized: true,
  }])
  vi.mocked(listFunctionReferences).mockImplementation(async target => [
    { name: target === 'workflow:goal' ? 'weekly_report' : target === 'workflow:flow' ? 'invoice_report' : 'team_report' },
    { name: 'unrelated_action' },
  ])
  const onSelect = vi.fn(); const onClose = vi.fn()
  const { host, render } = await mount(onSelect, onClose)
  await render(true, 'REPORT')
  const names = () => [...host.querySelectorAll('[data-reference-row]')].map(row => row.getAttribute('aria-label'))
  expect(names()).toEqual(['Invoice flow', 'Invoice flow: invoice_report', 'Operations', 'Operations: team_report', 'Sales goal', 'Sales goal: weekly_report'])
  expect(host.textContent).not.toContain('unrelated_action')
  expect(onClose).not.toHaveBeenCalled()
  const input = host.querySelector('input')!
  const press = async (key: string) => { await act(async () => input.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }))) }
  await press('ArrowDown'); await press('Enter')
  expect(onSelect).toHaveBeenLastCalledWith(expect.objectContaining({ presetId: 'workflow:flow', functionName: 'invoice_report' }))
  await press('ArrowDown'); await press('Enter')
  expect(onSelect).toHaveBeenLastCalledWith(expect.objectContaining({ presetId: 'crew:crew' }))
  expect(onSelect.mock.lastCall?.[0].functionName).toBeUndefined()
  await press('ArrowDown'); await press('Enter')
  expect(onSelect).toHaveBeenLastCalledWith(expect.objectContaining({ presetId: 'crew:crew', functionName: 'team_report' }))
  await press('ArrowLeft'); await press('ArrowRight'); await press('Enter')
  expect(onSelect).toHaveBeenLastCalledWith(expect.objectContaining({ functionName: 'team_report' }))
  expect(listFunctionReferences).toHaveBeenCalledTimes(3)
})

it('waits for function discovery before dismissing a query that matches no project name', async () => {
  let finishFunctions!: (value: Awaited<ReturnType<typeof listFunctionReferences>>) => void
  vi.mocked(loadProductProjects).mockResolvedValue([])
  vi.mocked(workflowManifestApi.listWorkflowManifests).mockResolvedValue(allowed as Awaited<ReturnType<typeof workflowManifestApi.listWorkflowManifests>>)
  vi.mocked(listFunctionReferences).mockReturnValue(new Promise(resolve => { finishFunctions = resolve }))
  const onClose = vi.fn()
  const { host, render } = await mount(vi.fn(), onClose)
  await render(true, 'export_report')
  expect(host.textContent).toContain('Searching functions')
  expect(onClose).not.toHaveBeenCalled()
  await act(async () => finishFunctions([{ name: 'export_report' }]))
  expect(host.textContent).toContain('Shared automation')
  expect(host.textContent).toContain('export_report')
  expect(onClose).not.toHaveBeenCalled()
})

it('keeps searching partial results usable when function discovery fails for another project', async () => {
  vi.mocked(loadProductProjects).mockResolvedValue([])
  vi.mocked(workflowManifestApi.listWorkflowManifests).mockResolvedValue({ success: true, total: 2, workflows: [
    { workspace_path: 'Workflow/ok', manifest: { id: 'ok', label: 'Available' } },
    { workspace_path: 'Workflow/revoked', manifest: { id: 'revoked', label: 'Restricted' } },
  ] } as Awaited<ReturnType<typeof workflowManifestApi.listWorkflowManifests>>)
  vi.mocked(listFunctionReferences).mockImplementation(async target => {
    if (target === 'workflow:revoked') throw new Error('forbidden')
    return [{ name: 'export_report' }]
  })
  const onSelect = vi.fn(); const onClose = vi.fn()
  const { host, render } = await mount(onSelect, onClose)
  await render(true, 'export_report')
  expect(host.textContent).toContain('Available')
  expect(host.textContent).not.toContain('Restricted')
  expect(host.textContent).toContain('Some references could not be loaded')
  await act(async () => host.querySelector<HTMLButtonElement>('[data-testid="reference-functions"] button')!.click())
  expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ presetId: 'workflow:ok', functionName: 'export_report' }))
  expect(onClose).not.toHaveBeenCalled()
})

it('limits discovery requests and abandons queued projects when closed', async () => {
  const finishRequests: ((items: Awaited<ReturnType<typeof listFunctionReferences>>) => void)[] = []
  vi.mocked(loadProductProjects).mockResolvedValue([])
  vi.mocked(workflowManifestApi.listWorkflowManifests).mockResolvedValue({ success: true, total: 8, workflows:
    Array.from({ length: 8 }, (_, i) => ({ workspace_path: `Workflow/${i}`, manifest: { id: String(i), label: `Project ${i}` } })),
  } as Awaited<ReturnType<typeof workflowManifestApi.listWorkflowManifests>>)
  vi.mocked(listFunctionReferences).mockImplementation(() => new Promise(resolve => finishRequests.push(resolve)))
  const { host, render } = await mount()
  expect(listFunctionReferences).toHaveBeenCalledTimes(4)
  await render(false)
  await act(async () => finishRequests.forEach(finish => finish([{ name: 'old_private_function' }])))
  expect(listFunctionReferences).toHaveBeenCalledTimes(4)
  expect(host.textContent).not.toContain('old_private_function')
})
