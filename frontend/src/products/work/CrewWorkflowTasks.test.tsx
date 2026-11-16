// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { CrewWorkflowTasks, groupStepsByTask, runTaskName, taskFileName } from './CrewWorkflowTasks'
import type { RunPythonStep } from '../../services/api-types'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const tasks = ['github-issues-tracker', 'slack-message-notifier']
const step = (id: string, name: string, file: string, status = 'completed'): RunPythonStep => ({ execution_id: id, id, name, source_call: `${file}:fn@${file}:10`, status, started_at: '2026-01-01T00:00:00Z', completed_at: '2026-01-01T00:00:05Z' })

async function mount(latestSteps: RunPythonStep[], onDeleteTask?: (task: string) => void, more: Partial<React.ComponentProps<typeof CrewWorkflowTasks>> = {}) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  await act(async () => root.render(<CrewWorkflowTasks tasks={tasks} latestSteps={latestSteps} onDeleteTask={onDeleteTask} {...more} />))
  return { host, cleanup: async () => { await act(async () => root.unmount()); host.remove() } }
}
const row = (host: HTMLElement, task: string) => host.querySelector(`[data-testid="workflow-task-${task}"]`)

it('names the file a task keeps its steps in', () => {
  expect(taskFileName('slack-message-notifier')).toBe('slack_message_notifier.py')
  expect(taskFileName('  GitHub Issues  ')).toBe('github_issues.py')
})

it('splits the latest run by task file and keeps every other step', () => {
  const { byTask, other } = groupStepsByTask(tasks, [
    step('1', 'Fetch all issues', 'github_issues_tracker.py'),
    step('2', 'Write report', 'src/github_issues_tracker.py'),
    step('3', 'Read incoming message', 'slack_message_notifier.py'),
    step('4', 'Set up', 'main.py'),
    { ...step('5', 'No source call', 'x.py'), source_call: undefined },
  ])
  expect(byTask.get('github-issues-tracker')?.map(item => item.id)).toEqual(['1', '2'])
  expect(byTask.get('slack-message-notifier')?.map(item => item.id)).toEqual(['3'])
  expect(other.map(item => item.id)).toEqual(['4', '5'])
})

it('shows only the task names, marked not run, before any run', async () => {
  const view = await mount([])
  try {
    expect(view.host.querySelector('h3')?.textContent).toBe('Tasks')
    expect([...view.host.querySelectorAll('[data-testid^="workflow-task-"]')].map(item => item.querySelector('span')?.textContent)).toEqual(tasks)
    expect(view.host.textContent).toContain('0 steps · not run yet')
    expect(view.host.textContent).not.toContain('Other steps')
  } finally { await view.cleanup() }
})

it('opens the steps of the task that ran, and says the other was not in the latest run', async () => {
  const view = await mount([step('1', 'Fetch all issues', 'github_issues_tracker.py'), step('2', 'Write report', 'github_issues_tracker.py', 'failed')])
  try {
    expect(row(view.host, 'github-issues-tracker')?.textContent).toContain('2 steps · last run failed')
    expect(row(view.host, 'slack-message-notifier')?.textContent).toContain('0 steps · not in the latest run')
    expect(view.host.textContent).not.toContain('Fetch all issues')
    await act(async () => { (row(view.host, 'github-issues-tracker')?.querySelector('button') as HTMLButtonElement).click() })
    expect(view.host.textContent).toContain('Fetch all issues')
    expect(view.host.textContent).toContain('Write report')
  } finally { await view.cleanup() }
})

it('lists steps from other files under Other steps so none is hidden', async () => {
  const view = await mount([step('1', 'Set up', 'main.py')])
  try {
    expect(view.host.querySelector('[data-testid="workflow-task-other"]')?.textContent).toContain('1 step not in a task')
  } finally { await view.cleanup() }
})

it('asks to delete the chosen task only, and shows no Delete when the page cannot ask', async () => {
  const onDeleteTask = vi.fn()
  const view = await mount([], onDeleteTask)
  try {
    const button = row(view.host, 'slack-message-notifier')?.querySelector('button[title="Delete the task slack-message-notifier"]') as HTMLButtonElement
    await act(async () => { button.click() })
    expect(onDeleteTask).toHaveBeenCalledTimes(1)
    expect(onDeleteTask).toHaveBeenCalledWith('slack-message-notifier')
  } finally { await view.cleanup() }
  const plain = await mount([])
  try { expect(plain.host.querySelector('button[title^="Delete the task"]')).toBeNull() } finally { await plain.cleanup() }
})

it('names the task a run executed: its saved setting first, then the file its steps were written in', () => {
  const steps = (file: string) => ({ a: step('a', 'Some step', file) })
  expect(runTaskName(tasks, { input_preset: 'slack-message-notifier', python_steps: steps('github_issues_tracker.py') })).toBe('slack-message-notifier')
  expect(runTaskName(tasks, { input_preset: null, python_steps: steps('slack_message_notifier.py') })).toBe('slack-message-notifier')
  expect(runTaskName(tasks, { input_preset: 'weekly', python_steps: steps('github_issues_tracker.py') })).toBe('github-issues-tracker')
})

it('says nothing when the run does not show which task it was', () => {
  expect(runTaskName(tasks, { input_preset: 'weekly' })).toBeUndefined()
  expect(runTaskName(tasks, { python_steps: { a: step('a', 'Set up', 'main.py') } })).toBeUndefined()
  expect(runTaskName(tasks, undefined)).toBeUndefined()
  expect(runTaskName([], { input_preset: 'github-issues-tracker' })).toBeUndefined()
})

it('lets every task open, also one with no steps, so the rows line up', async () => {
  const view = await mount([])
  try {
    expect(view.host.querySelectorAll('[data-testid^="workflow-task-"] button[aria-label="Open details"]')).toHaveLength(tasks.length)
    await act(async () => { (row(view.host, 'github-issues-tracker')?.querySelector('button') as HTMLButtonElement).click() })
    expect(view.host.textContent).toContain('No steps yet.')
  } finally { await view.cleanup() }
})

it('labels a task step as scripted when it is opened', async () => {
  const view = await mount([step('1', 'Fetch all issues', 'github_issues_tracker.py')])
  try {
    await act(async () => { (row(view.host, 'github-issues-tracker')?.querySelector('button') as HTMLButtonElement).click() })
    expect(view.host.textContent).toContain('Scripted')
  } finally { await view.cleanup() }
})

it('puts Ask and Run now on each task, and asks or runs only that task', async () => {
  const asked: string[] = []
  const ran: string[] = []
  const view = await mount([], undefined, { onAskTask: task => { asked.push(task) }, onRunTask: async task => { ran.push(task) } })
  try {
    expect(view.host.querySelectorAll('button[title^="Ask about the task"]')).toHaveLength(tasks.length)
    expect(view.host.querySelectorAll('button[title^="Run the task"]')).toHaveLength(tasks.length)
    // the card has no Ask of its own any more
    expect(view.host.querySelector('[data-testid="crew-workflow-tasks"] > div button')?.textContent).not.toBe('Ask')
    await act(async () => { (row(view.host, 'slack-message-notifier')?.querySelector('button[title^="Ask about the task"]') as HTMLButtonElement).click() })
    await act(async () => { (row(view.host, 'slack-message-notifier')?.querySelector('button[title^="Run the task"]') as HTMLButtonElement).click() })
    expect(asked).toEqual(['slack-message-notifier'])
    expect(ran).toEqual(['slack-message-notifier'])
    expect(view.host.textContent).toContain('Started. It shows in Runs.')
  } finally { await view.cleanup() }
})

it('says why a task could not start, and starts one task at a time', async () => {
  const view = await mount([], undefined, { onRunTask: async () => { throw new Error('This workflow is already running.') } })
  try {
    await act(async () => { (row(view.host, 'github-issues-tracker')?.querySelector('button[title^="Run the task"]') as HTMLButtonElement).click() })
    expect(row(view.host, 'github-issues-tracker')?.textContent).toContain('This workflow is already running.')
    expect((row(view.host, 'slack-message-notifier')?.querySelector('button[title^="Run the task"]') as HTMLButtonElement).disabled).toBe(false)
  } finally { await view.cleanup() }
})

it('starts every task, one after the other, from Run all', async () => {
  const ran: string[] = []
  const view = await mount([], undefined, { onRunTask: async task => { ran.push(task) } })
  try {
    const all = view.host.querySelector('button[title="Start every task now"]') as HTMLButtonElement
    await act(async () => { all.click() })
    expect(ran).toEqual(tasks)
    expect(view.host.textContent?.match(/Started\. It shows in Runs\./g)).toHaveLength(tasks.length)
  } finally { await view.cleanup() }
})

it('still starts the other tasks when one cannot start, and shows Run all only for several tasks', async () => {
  const ran: string[] = []
  const view = await mount([], undefined, { onRunTask: async task => { if (task === tasks[0]) throw new Error('Already running.'); ran.push(task) } })
  try {
    await act(async () => { (view.host.querySelector('button[title="Start every task now"]') as HTMLButtonElement).click() })
    expect(ran).toEqual([tasks[1]])
    expect(row(view.host, tasks[0])?.textContent).toContain('Already running.')
  } finally { await view.cleanup() }
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  await act(async () => root.render(<CrewWorkflowTasks tasks={['only']} latestSteps={[]} onRunTask={async () => undefined} />))
  expect(host.querySelector('button[title="Start every task now"]')).toBeNull()
  await act(async () => root.unmount()); host.remove()
})
