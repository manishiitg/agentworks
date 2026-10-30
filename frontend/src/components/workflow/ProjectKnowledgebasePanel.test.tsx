// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ProjectKnowledgebasePanel } from './ProjectKnowledgebasePanel'
import { knowledgebaseApi } from '../../services/knowledgebaseApi'
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (selector: (state: { user: null }) => unknown) => selector({ user: null }) }))
vi.mock('../../services/knowledgebaseApi', () => ({
  knowledgebaseApi: { bootstrap: vi.fn(), project: vi.fn(), setProjectAccess: vi.fn(), folders: vi.fn(), entries: vi.fn() },
  knowledgebaseError: (error: Error) => error.message,
}))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: any) => select({ activeWorkspaceId: 'local' }) }))
let root: Root
let container: HTMLDivElement
beforeEach(() => {
  vi.resetAllMocks()
  vi.stubEnv('VITE_DEPLOYMENT_MODE', 'server')
  window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['work', 'knowledgebase'] }
  vi.mocked(knowledgebaseApi.bootstrap).mockResolvedValue({} as never)
  vi.mocked(knowledgebaseApi.folders).mockResolvedValue({ folders: [{ path: 'guides', name: 'Team guides', effective_role: 'reader' }] })
  vi.mocked(knowledgebaseApi.entries).mockResolvedValue({ entries: [] })
  container = document.createElement('div'); document.body.append(container); root = createRoot(container)
})

it.each(['bootstrap', 'project'] as const)('reports unavailable Brain access when %s returns 404 and recovers on refresh', async endpoint => {
  const missing = Object.assign(new Error('Brain not found'), { response: { status: 404 } })
  vi.mocked(knowledgebaseApi.project).mockResolvedValue({ manifest_version: 'v1', brain_access: 'read', can_manage: false })
  vi.mocked(knowledgebaseApi[endpoint]).mockRejectedValueOnce(missing)
  await act(async () => root.render(<ProjectKnowledgebasePanel workspacePath="Workflow/payments"/>))
  expect(container.querySelector('[aria-label="Brain"]')).not.toBeNull()
  expect(container.querySelector('[aria-label="Knowledge available to you"]')).toBeNull()
  expect(container.querySelector('[role="alert"]')?.textContent).toContain('Brain could not be loaded for this project')
  expect(container.querySelector('[role="radiogroup"]')).toBeNull()
  expect(knowledgebaseApi.setProjectAccess).not.toHaveBeenCalled()
  await act(async () => (container.querySelector('[aria-label="Refresh Brain"]') as HTMLButtonElement).click())
  expect(container.querySelector('[role="alert"]')).toBeNull()
  expect(container.textContent).toContain('Team guides')
  expect(container.textContent).toContain('Available to you')
  expect(container.textContent).not.toContain('Share knowledge with your team')
  const radios = [...container.querySelectorAll('input[type="radio"]')] as HTMLInputElement[]
  expect(radios.find(radio => radio.checked)?.value).toBe('read')
  expect(radios.every(radio => radio.disabled)).toBe(true)
  expect(container.textContent).toContain('Only an owner of this project can change Brain access.')
})
afterEach(async () => { await act(async () => root.unmount()); container.remove(); delete window.__APP_RUNTIME_CONFIG__; vi.unstubAllEnvs() })

// PLAT-628: one Brain setting per project (Off, Read, Read & write) and no folder bindings; an old project saved
// with "folders" is shown as Read & write, which is what the server applies.
it('offers off, read and read & write only, and shows a legacy folders project as read & write', async () => {
  vi.mocked(knowledgebaseApi.project).mockResolvedValue({ manifest_version: 'v1', brain_access: 'folders', can_manage: true })
  vi.mocked(knowledgebaseApi.setProjectAccess).mockResolvedValue({ brain_access: 'read', manifest_version: 'v2' })
  await act(async () => root.render(<ProjectKnowledgebasePanel workspacePath="Workflow/payments"/>))
  expect(container.textContent).toContain('Team guides')
  expect(container.textContent).toContain('Available to you')
  expect(container.textContent).not.toContain('Share knowledge with your team')
  const radios = [...container.querySelectorAll('input[type="radio"]')] as HTMLInputElement[]
  expect(radios.map(radio => radio.value)).toEqual(['write', 'read', 'off'])
  expect(radios.find(radio => radio.checked)?.value).toBe('write')
  await act(async () => radios[1].click())
  expect(knowledgebaseApi.setProjectAccess).toHaveBeenCalledWith(expect.objectContaining({ workspace_path: 'Workflow/payments', mode: 'read', expected_manifest_version: 'v1' }))
})

it('offers the next library page instead of claiming an empty folder', async () => {
  vi.mocked(knowledgebaseApi.project).mockResolvedValue({ manifest_version: 'v1', brain_access: 'read', can_manage: false })
  vi.mocked(knowledgebaseApi.folders).mockResolvedValue({ folders: [] })
  vi.mocked(knowledgebaseApi.entries).mockResolvedValueOnce({ entries: [{ folder_path: 'nested' } as never], next_cursor: 'next' }).mockResolvedValue({ entries: [{ entry_id: 'note', folder_path: '', path: 'handbook.md', title: 'Team handbook', type: 'note' } as never] })
  await act(async () => root.render(<ProjectKnowledgebasePanel workspacePath="Crew/example"/>))
  expect(container.textContent).not.toContain('No knowledge in this folder')
  const more = [...container.querySelectorAll('button')].find(button => button.textContent === 'Show more')!
  await act(async () => more.click())
  expect(container.textContent).toContain('Team handbook')
  expect(knowledgebaseApi.setProjectAccess).not.toHaveBeenCalled()
})
