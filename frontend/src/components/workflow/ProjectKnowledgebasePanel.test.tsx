// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ProjectKnowledgebasePanel } from './ProjectKnowledgebasePanel'
import { knowledgebaseApi } from '../../services/knowledgebaseApi'
vi.mock('../../services/knowledgebaseApi', () => ({
  knowledgebaseApi: { bootstrap: vi.fn(), project: vi.fn(), projectFolders: vi.fn(), bindProject: vi.fn(), setProjectAccess: vi.fn() },
  knowledgebaseError: (error: Error) => error.message,
}))
vi.mock('./AskAIButton', () => ({ AskAIButton: ({ label }: { label: string }) => <button>{label}</button> }))
let root: Root
let container: HTMLDivElement
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(knowledgebaseApi.bootstrap).mockResolvedValue({} as never)
  vi.mocked(knowledgebaseApi.project).mockResolvedValue({ manifest_version: 'v1', brain_access: 'folders', shared_knowledgebase: [], can_manage: true })
  vi.mocked(knowledgebaseApi.projectFolders).mockResolvedValue({ items: [{ folder_id: 'folder_payments', path: 'Engineering/Payments', name: 'Payments', effective_role: 'editor' }] })
  vi.mocked(knowledgebaseApi.bindProject).mockResolvedValue({ manifest_version: 'v2', brain_access: 'folders', shared_knowledgebase: [], can_manage: true })
  container = document.createElement('div'); document.body.append(container); root = createRoot(container)
})
afterEach(async () => { await act(async () => root.unmount()); container.remove() })
async function render(disabled = false) { await act(async () => root.render(<ProjectKnowledgebasePanel workspacePath="Workflow/payments" disabled={disabled}/>)) }
async function click(label: string) { const control = container.querySelector(`[aria-label="${label}"]`) as HTMLElement; expect(control).not.toBeNull(); await act(async () => control.click()) }
it('uses current manifest and immutable folder ID for selection', async () => {
  await render(); await click('Use Engineering/Payments')
  expect(knowledgebaseApi.bindProject).toHaveBeenCalledWith(expect.objectContaining({ action: 'bind_project', workspace_path: 'Workflow/payments', folder_id: 'folder_payments', alias: 'kb_engineering_payments', access: 'read', expected_manifest_version: 'v1', request_id: expect.any(String) }))
  expect(container.textContent).toContain('Ask AI to set up')
})
it('changes selected access and detaches through the same endpoint', async () => {
  vi.mocked(knowledgebaseApi.project).mockResolvedValue({ manifest_version: 'v1', shared_knowledgebase: [{ alias: 'payments', folder_id: 'folder_payments', access: 'read' }], can_manage: true })
  await render()
  const select = container.querySelector('select')!
  await act(async () => { select.value = 'write'; select.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(knowledgebaseApi.bindProject).toHaveBeenLastCalledWith(expect.objectContaining({ alias: 'payments', access: 'write', expected_manifest_version: 'v1' }))
  await click('Use Engineering/Payments')
  expect(knowledgebaseApi.bindProject).toHaveBeenLastCalledWith(expect.objectContaining({ action: 'unbind_project', alias: 'payments' }))
})
it('keeps selection unchanged and displays audience/version errors', async () => {
  vi.mocked(knowledgebaseApi.bindProject).mockRejectedValue(new Error('An output audience member cannot read the shared folder.'))
  await render(); await click('Use Engineering/Payments')
  expect(container.querySelector('[role="alert"]')?.textContent).toContain('audience member')
  expect(container.querySelector('[role="checkbox"]')?.getAttribute('aria-checked')).toBe('false')
})
it('disables mutations for readers and project non-owners', async () => {
  vi.mocked(knowledgebaseApi.project).mockResolvedValue({ manifest_version: 'v1', brain_access: 'folders', shared_knowledgebase: [], can_manage: false })
  await render()
  expect((container.querySelector('[role="checkbox"]') as HTMLButtonElement).disabled).toBe(true)
  await click('Use Engineering/Payments')
  expect(knowledgebaseApi.bindProject).not.toHaveBeenCalled()
})
it('loads every page and excludes breadcrumb-only folders', async () => {
  vi.mocked(knowledgebaseApi.projectFolders).mockResolvedValueOnce({ items: [{ folder_id: 'folder_breadcrumb', path: 'Engineering', name: 'Engineering', breadcrumb_only: true }], next_cursor: 'more' }).mockResolvedValueOnce({ items: [{ folder_id: 'folder_child', path: 'Engineering/Payments', name: 'Payments', effective_role: 'reader' }] })
  await render()
  expect(knowledgebaseApi.projectFolders).toHaveBeenCalledWith('more')
  expect(container.querySelector('[aria-label="Use Engineering"]')).toBeNull()
  expect(container.querySelector('[aria-label="Use Engineering/Payments"]')).not.toBeNull()
})

// Owner decision: one Brain setting (Off, Read, Read & write) per project; folders only matter for read & write.
it('sets the Brain access mode and shows folders only for read & write', async () => {
  vi.mocked(knowledgebaseApi.project).mockResolvedValue({ manifest_version: 'v1', brain_access: 'off', shared_knowledgebase: [], can_manage: true })
  vi.mocked(knowledgebaseApi.setProjectAccess).mockResolvedValue({ brain_access: 'read', manifest_version: 'v2' })
  await render()
  expect(container.querySelector('[aria-label="Use Engineering/Payments"]')).toBeNull()
  const read = container.querySelector('input[type="radio"][value="read"]') as HTMLInputElement
  await act(async () => read.click())
  expect(knowledgebaseApi.setProjectAccess).toHaveBeenCalledWith(expect.objectContaining({ workspace_path: 'Workflow/payments', mode: 'read', expected_manifest_version: 'v1' }))
})
