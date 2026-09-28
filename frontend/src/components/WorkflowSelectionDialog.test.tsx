// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import WorkflowSelectionDialog from './WorkflowSelectionDialog'
import { workflowManifestApi } from '../services/api'
import { loadProductProjects } from '../platform/chat/productProjects'

vi.mock('../services/api', () => ({ workflowManifestApi: { listWorkflowManifests: vi.fn() } }))
vi.mock('../platform/chat/productProjects', () => ({ loadProductProjects: vi.fn() }))
vi.mock('../stores/useAuthStore', () => ({ useAuthStore: (selector: (state: { user: { id: string } }) => unknown) => selector({ user: { id: 'reader' } }) }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(fn => fn()); vi.clearAllMocks() })
const allowed = { success: true, total: 1, workflows: [{ workspace_path: 'Workflow/shared', manifest: { id: 'shared', label: 'Shared automation' } }] }
async function mount(onSelectWorkflow = vi.fn(), onClose = vi.fn()) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  const render = async (isOpen: boolean) => { await act(async () => root.render(<WorkflowSelectionDialog isOpen={isOpen} onClose={onClose} onSelectWorkflow={onSelectWorkflow} searchQuery="" position={{ bottom: 0, left: 0 }} />)) }
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
  const workflows = ['rts-latency', 'rts-aws', 'automation-testing'].map(label => ({ workspace_path: `Workflow/${label}`, manifest: { id: label, label } }))
  vi.mocked(workflowManifestApi.listWorkflowManifests).mockResolvedValueOnce({ success: true, total: 3, workflows } as Awaited<ReturnType<typeof workflowManifestApi.listWorkflowManifests>>)
  const onSelect = vi.fn(); const onClose = vi.fn()
  const { host } = await mount(onSelect, onClose)
  const input = host.querySelector('input')!
  const press = async (key: string) => { await act(async () => { input.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true })) }) }
  await press('ArrowDown'); await press('Enter')
  expect(onSelect).toHaveBeenLastCalledWith(expect.objectContaining({ label: 'rts-aws' }))
  await press('ArrowDown'); await press('Enter')
  expect(onSelect).toHaveBeenLastCalledWith(expect.objectContaining({ label: 'automation-testing' }))
  await press('ArrowUp'); await press('Enter')
  expect(onSelect).toHaveBeenLastCalledWith(expect.objectContaining({ label: 'rts-aws' }))
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
  expect(host.textContent).toContain('Release operations')
  expect(host.textContent).toContain('crew')
  const crewRow = [...host.querySelectorAll('[class*="cursor-pointer"]')].find(row => row.textContent?.includes('Harbor'))
  expect(crewRow).toBeDefined()
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
