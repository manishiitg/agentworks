// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'

const getGoalSetup = vi.fn()
const dismissGoalSetup = vi.fn()
const sendWorkspacePaneMessageToChat = vi.fn()

vi.mock('../../services/api', () => ({
  workflowManifestApi: {
    getGoalSetup: (...args: unknown[]) => getGoalSetup(...args),
    dismissGoalSetup: (...args: unknown[]) => dismissGoalSetup(...args),
  },
}))
vi.mock('../../utils/workspacePaneChat', () => ({
  sendWorkspacePaneMessageToChat: (...args: unknown[]) => sendWorkspacePaneMessageToChat(...args),
}))
vi.mock('../../utils/goalSetupChat', () => ({
  goalSetupChatMessage: (command: string, playbooks: Array<{ title: string }> = []) => `expanded /${command}${playbooks.length ? ` from ${playbooks[0].title}` : ''}`,
}))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ addToast: vi.fn() }) } }))
const openWorkspaceView = vi.fn()
vi.mock('../../stores/useWorkflowStore', () => ({ useWorkflowStore: { getState: () => ({ openWorkspaceView }) } }))

import { WorkflowGoalSetupBar } from './WorkflowGoalSetupBar'

const pending = {
  show: true, complete: false, dismissed: false, has_runs: false,
  checks: [
    { id: 'playbook', label: 'Playbook', done: false, optional: true },
    { id: 'goal', label: 'Goal', done: true, command: 'setup-goals' },
    { id: 'plan', label: 'Plan', done: false, command: 'design-plan' },
    { id: 'metrics', label: 'Metrics', done: false, command: 'setup-goals' },
  ],
  next: { id: 'plan', label: 'Plan', done: false, command: 'design-plan' },
}

// Nothing set yet: the full card.
const fresh = {
  ...pending,
  checks: pending.checks.map(check => check.id === 'goal' ? { ...check, done: false } : check),
  next: { id: 'goal', label: 'Goal', done: false, command: 'setup-goals' },
}

async function render(props: { workspacePath: string; canEdit: boolean }) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  await act(async () => { root.render(<WorkflowGoalSetupBar {...props} />) })
  return { container, root }
}

afterEach(() => {
  vi.clearAllMocks()
  document.body.innerHTML = ''
})

describe('WorkflowGoalSetupBar', () => {
  it('collapses to one line once the goal is set and starts the next step in chat', async () => {
    getGoalSetup.mockResolvedValue(pending)
    const { container, root } = await render({ workspacePath: 'Workflow/new', canEdit: true })

    expect(container.textContent).toContain('Goal setup · optional')
    expect(container.textContent).toContain('Step 2 of 3')
    expect(container.textContent).not.toContain('Give this automation a goal')
    expect(container.textContent).not.toContain('Start from a playbook')
    const current = container.querySelector('[aria-current="step"]') as HTMLButtonElement
    expect(current?.textContent).toContain('Plan')
    expect(current.disabled).toBe(false)
    const doneStep = Array.from(container.querySelectorAll('ol[aria-label="Setup steps"] button')).find(b => b.textContent?.includes('Goal')) as HTMLButtonElement
    expect(doneStep.disabled).toBe(true)
    const action = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Design the plan in chat')
    expect(action).toBeTruthy()
    await act(async () => { action!.click() })
    expect(sendWorkspacePaneMessageToChat).toHaveBeenCalledWith({ workspacePath: 'Workflow/new', message: 'expanded /design-plan' })
    await act(async () => root.unmount())
  })

  it('can be dismissed, since goals are optional', async () => {
    getGoalSetup.mockResolvedValue(pending)
    dismissGoalSetup.mockResolvedValue({ ...pending, show: false, dismissed: true })
    const { container, root } = await render({ workspacePath: 'Workflow/new', canEdit: true })

    const hide = container.querySelector('[aria-label="Dismiss goal setup"]') as HTMLButtonElement
    expect(hide.textContent).toBe('Hide')
    await act(async () => { hide.click() })
    expect(dismissGoalSetup).toHaveBeenCalledWith('Workflow/new')
    expect(container.textContent).not.toContain('Goal setup')
    await act(async () => root.unmount())
  })

  it('stays hidden for read-only users and once setup is not shown', async () => {
    getGoalSetup.mockResolvedValue(pending)
    const readOnly = await render({ workspacePath: 'Workflow/new', canEdit: false })
    expect(readOnly.container.textContent).toBe('')
    await act(async () => readOnly.root.unmount())

    getGoalSetup.mockResolvedValue({ ...pending, show: false, has_runs: true })
    const ran = await render({ workspacePath: 'Workflow/running', canEdit: true })
    expect(ran.container.textContent).toBe('')
    await act(async () => ran.root.unmount())
  })

  it('before a goal: the full card, with a playbook and no-goal choice beside the main action', async () => {
    getGoalSetup.mockResolvedValue(fresh)
    dismissGoalSetup.mockResolvedValue({ ...fresh, show: false, dismissed: true })
    const first = await render({ workspacePath: 'Workflow/new', canEdit: true })
    expect(first.container.textContent).toContain('Give this automation a goal')
    expect(first.container.textContent).toContain('Not every automation needs one')
    expect(first.container.textContent).toContain('Step 1 of 3')
    const playbookButton = Array.from(first.container.querySelectorAll('button')).find(b => b.textContent === 'Start from a playbook')
    expect(playbookButton).toBeTruthy()
    await act(async () => { playbookButton!.click() })
    expect(openWorkspaceView).toHaveBeenCalledWith('playbooks')
    const noGoal = Array.from(first.container.querySelectorAll('button')).find(b => b.textContent === 'No goal needed')
    expect(noGoal?.getAttribute('aria-label')).toBe('Dismiss goal setup')
    await act(async () => { noGoal!.click() })
    expect(dismissGoalSetup).toHaveBeenCalledWith('Workflow/new')
    await act(async () => first.root.unmount())

    getGoalSetup.mockResolvedValue({
      ...fresh,
      checks: fresh.checks.map(check => check.id === 'playbook' ? { ...check, done: true } : check),
      playbooks: [{ id: 'website-growth-loop', title: 'Website Growth Loop', skill_name: 'agentworks-playbook-website-growth-loop' }],
    })
    const second = await render({ workspacePath: 'Workflow/new', canEdit: true })
    expect(second.container.textContent).toContain('Playbook: Website Growth Loop')
    const action = Array.from(second.container.querySelectorAll('button')).find(b => b.textContent === 'Set the goal in chat')
    await act(async () => { action!.click() })
    expect(sendWorkspacePaneMessageToChat).toHaveBeenCalledWith({ workspacePath: 'Workflow/new', message: 'expanded /setup-goals from Website Growth Loop' })
    await act(async () => second.root.unmount())
  })
})
