// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { CrewWorkflowHeader } from './CrewWorkflowHeader'
import { fileRequest, workflowTaskDescriptions, workflowTaskNames } from './CrewExecutableEditor'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

async function mount(props: Partial<React.ComponentProps<typeof CrewWorkflowHeader>> = {}) {
  const onEdit = vi.fn()
  const onDelete = vi.fn().mockResolvedValue(undefined)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  await act(async () => root.render(<CrewWorkflowHeader name="Crew tasks" onEdit={onEdit} onDelete={onDelete} {...props} />))
  const button = (label: string) => [...host.querySelectorAll('button')].find(item => item.textContent === label || item.getAttribute('aria-label') === label) as HTMLButtonElement
  const click = (label: string) => act(async () => { button(label).click() })
  return { host, root, onEdit, onDelete, button, click, cleanup: async () => { await act(async () => root.unmount()); host.remove() } }
}

it('shows the workflow name with Edit and Delete', async () => {
  const view = await mount()
  try {
    expect(view.host.querySelector('h3')?.textContent).toBe('Crew tasks')
    expect(view.button('Edit this workflow')).toBeTruthy()
    expect(view.button('Delete this workflow')).toBeTruthy()
  } finally { await view.cleanup() }
})

it('Edit hands over to the chat and shows no rename form', async () => {
  const view = await mount()
  try {
    await view.click('Edit this workflow')
    expect(view.onEdit).toHaveBeenCalledTimes(1)
    expect(view.host.querySelector('input')).toBeNull()
  } finally { await view.cleanup() }
})

it('asks before deleting, and Cancel deletes nothing', async () => {
  const view = await mount()
  try {
    await view.click('Delete this workflow')
    expect(view.host.textContent).toContain('This cannot be undone.')
    expect(view.onDelete).not.toHaveBeenCalled()
    await view.click('Cancel')
    expect(view.onDelete).not.toHaveBeenCalled()
    expect(view.host.textContent).not.toContain('This cannot be undone.')
    await view.click('Delete this workflow')
    await view.click('Delete workflow')
    expect(view.onDelete).toHaveBeenCalledTimes(1)
  } finally { await view.cleanup() }
})

it('shows why a delete failed and keeps the question open', async () => {
  const view = await mount({ onDelete: vi.fn().mockRejectedValue(new Error('Could not remove all of this workflow’s files. Try again.')) })
  try {
    await view.click('Delete this workflow')
    await view.click('Delete workflow')
    expect(view.host.querySelector('[role="alert"]')?.textContent).toContain('Could not remove all')
    expect(view.host.textContent).toContain('This cannot be undone.')
  } finally { await view.cleanup() }
})

it('reads the task names from the task input and ignores anything else', () => {
  const task = (schema?: { enum?: unknown[] }) => [{ name: 'task', value: '', schema }]
  expect(workflowTaskNames(task({ enum: ['a', 'b', '', 3, null] }))).toEqual(['a', 'b'])
  expect(workflowTaskNames(task({}))).toEqual([])
  expect(workflowTaskNames(task())).toEqual([])
  expect(workflowTaskNames([{ name: 'state', value: '', schema: { enum: ['open'] } }])).toEqual([])
  expect(workflowTaskNames(undefined)).toEqual([])
})

it('reads one line per task from the task input and ignores anything else', () => {
  const task = (descriptions?: Record<string, unknown>) => [{ name: 'task', value: '', schema: { enum: ['a', 'b'], descriptions } }]
  expect(workflowTaskDescriptions(task({ a: '  Reads new issues.  ', b: '   ', c: 3 }))).toEqual({ a: 'Reads new issues.' })
  expect(workflowTaskDescriptions(task())).toEqual({})
  expect(workflowTaskDescriptions([{ name: 'state', value: '', schema: { descriptions: { a: 'x' } } }])).toEqual({})
  expect(workflowTaskDescriptions(undefined)).toEqual({})
})

it('shows the whole description under the name, or says there is none', async () => {
  const view = await mount({ description: 'Reads new issues.\nPosts a summary.' })
  try {
    expect(view.host.querySelector('[data-testid="crew-workflow-description"]')?.textContent).toBe('Reads new issues.\nPosts a summary.')
  } finally { await view.cleanup() }
  const bare = await mount()
  try {
    expect(bare.host.querySelector('[data-testid="crew-workflow-description"]')?.textContent).toBe('No description yet.')
  } finally { await bare.cleanup() }
})

it('writes the file request with the workflow, its ID, the path and the task the file belongs to', () => {
  const item = { id: 'wf-1', kind: 'script' as const, workspace_path: 'Workflow/wf-1', manifest: { label: 'GitHub tracker' } }
  const task = fileRequest(item, 'github_issues_tracker.py', ['github-issues-tracker'])
  expect(task).toContain('GitHub tracker')
  expect(task).toContain('wf-1')
  expect(task).toContain('workspace_path: Workflow/wf-1, file: github_issues_tracker.py')
  expect(task).toContain('task “github-issues-tracker”')
  expect(task).toContain('ask me what I want to change')
  expect(fileRequest(item, 'main.py', [])).toContain('main file')
  expect(fileRequest(item, 'helpers/util.py', [])).toContain('one of the workflow')
})
