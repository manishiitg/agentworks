// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

vi.mock('../../stores/useWorkflowManifestStore', () => ({
  useWorkflowManifestStore: (selector: (state: unknown) => unknown) => selector({ workflows: [], refreshWorkflows: vi.fn() }),
}))
import { WorkflowReferenceAccess } from './WorkflowReferenceAccess'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root | undefined
afterEach(() => { act(() => root?.unmount()); root = undefined; document.body.innerHTML = '' })

it('lets a Code project attach its person\'s other projects even where the other pickers are hidden, and not itself', async () => {
  const onChange = vi.fn()
  const host = document.createElement('div'); document.body.append(host)
  root = createRoot(host)
  await act(async () => root?.render(<WorkflowReferenceAccess selectedPaths={[]} onChange={onChange} excludeWorkspacePath="Chats/Code/projects/this-1" hideAdd
    extraGroups={[{ label: 'Code projects', description: 'Your other private projects.', placeholder: 'Attach a project…', references: [
      { path: 'Chats/Code/projects/this-1', label: 'This' }, { path: 'Chats/Code/projects/api-2', label: 'API' }] }]} />))
  expect(host.textContent).toContain('Code projects')
  const selects = [...host.querySelectorAll('select')]
  expect(selects).toHaveLength(1) // only the Code projects picker; workflows and Crews stay "ask the assistant"
  expect([...selects[0].options].map(option => option.value)).toEqual(['', 'Chats/Code/projects/api-2'])
  await act(async () => {
    selects[0].value = 'Chats/Code/projects/api-2'
    selects[0].dispatchEvent(new Event('change', { bubbles: true }))
  })
  expect(onChange).toHaveBeenCalledWith(['Chats/Code/projects/api-2'])
})
