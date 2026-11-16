// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { CrewWorkflowHeader } from './CrewWorkflowHeader'
import { workflowTaskNames } from './CrewExecutableEditor'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

async function mount(props: Partial<React.ComponentProps<typeof CrewWorkflowHeader>> = {}) {
  const onRename = vi.fn().mockResolvedValue(undefined)
  const onDelete = vi.fn().mockResolvedValue(undefined)
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  await act(async () => root.render(<CrewWorkflowHeader name="Crew tasks" onRename={onRename} onDelete={onDelete} {...props} />))
  const button = (label: string) => [...host.querySelectorAll('button')].find(item => item.textContent === label || item.getAttribute('aria-label') === label) as HTMLButtonElement
  const click = (label: string) => act(async () => { button(label).click() })
  const type = (value: string) => act(async () => {
    const input = host.querySelector('input') as HTMLInputElement
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  return { host, root, onRename, onDelete, button, click, type, cleanup: async () => { await act(async () => root.unmount()); host.remove() } }
}

it('shows the workflow name with Rename and Delete', async () => {
  const view = await mount()
  try {
    expect(view.host.querySelector('h3')?.textContent).toBe('Crew tasks')
    expect(view.button('Rename this workflow')).toBeTruthy()
    expect(view.button('Delete this workflow')).toBeTruthy()
  } finally { await view.cleanup() }
})

it('renames with the trimmed name and only when it changed', async () => {
  const view = await mount()
  try {
    await view.click('Rename this workflow')
    expect(view.button('Save').disabled).toBe(true)
    await view.type('   ')
    expect(view.button('Save').disabled).toBe(true)
    await view.type('  Weekly report  ')
    expect(view.button('Save').disabled).toBe(false)
    await view.click('Save')
    expect(view.onRename).toHaveBeenCalledWith('Weekly report')
    expect(view.host.querySelector('input')).toBeNull()
  } finally { await view.cleanup() }
})

it('keeps the edit open and shows the message when the rename is refused', async () => {
  const view = await mount({ onRename: vi.fn().mockRejectedValue(new Error('Enter a workflow name (up to 120 characters).')) })
  try {
    await view.click('Rename this workflow')
    await view.type('Weekly report')
    await view.click('Save')
    expect(view.host.querySelector('[role="alert"]')?.textContent).toContain('up to 120 characters')
    expect(view.host.querySelector('input')).not.toBeNull()
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
