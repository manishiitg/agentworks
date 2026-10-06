// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ProjectKnowledgebasePanel } from './ProjectKnowledgebasePanel'
import { knowledgebaseApi } from '../../services/knowledgebaseApi'
vi.mock('../../services/knowledgebaseApi', () => ({
  knowledgebaseApi: { bootstrap: vi.fn(), project: vi.fn(), setProjectAccess: vi.fn() },
  knowledgebaseError: (error: Error) => error.message,
}))
vi.mock('./AskAIButton', () => ({ AskAIButton: ({ label }: { label: string }) => <button>{label}</button> }))
let root: Root
let container: HTMLDivElement
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(knowledgebaseApi.bootstrap).mockResolvedValue({} as never)
  container = document.createElement('div'); document.body.append(container); root = createRoot(container)
})
afterEach(async () => { await act(async () => root.unmount()); container.remove() })

// PLAT-628: one Brain setting per project (Off, Read, Read & write) and no folder bindings; an old project saved
// with "folders" is shown as Read & write, which is what the server applies.
it('offers off, read and read & write only, and shows a legacy folders project as read & write', async () => {
  vi.mocked(knowledgebaseApi.project).mockResolvedValue({ manifest_version: 'v1', brain_access: 'folders', can_manage: true })
  vi.mocked(knowledgebaseApi.setProjectAccess).mockResolvedValue({ brain_access: 'read', manifest_version: 'v2' })
  await act(async () => root.render(<ProjectKnowledgebasePanel workspacePath="Workflow/payments"/>))
  const radios = [...container.querySelectorAll('input[type="radio"]')] as HTMLInputElement[]
  expect(radios.map(radio => radio.value)).toEqual(['write', 'read', 'off'])
  expect(radios.find(radio => radio.checked)?.value).toBe('write')
  await act(async () => radios[1].click())
  expect(knowledgebaseApi.setProjectAccess).toHaveBeenCalledWith(expect.objectContaining({ workspace_path: 'Workflow/payments', mode: 'read', expected_manifest_version: 'v1' }))
})
