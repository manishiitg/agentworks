// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'

const getPlannerFileContent = vi.hoisted(() => vi.fn())
const updatePlannerFile = vi.hoisted(() => vi.fn().mockResolvedValue({}))
vi.mock('../../services/api', () => ({ agentApi: { getPlannerFileContent, updatePlannerFile } }))

import { crewTemplates, parseCrewTemplateSetupState } from './crewTemplates'
import { WorkTemplateSetup } from './WorkTemplateSetup'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

describe('WorkTemplateSetup', () => {
  let container: HTMLDivElement | null = null
  afterEach(() => { container?.remove(); container = null; vi.resetAllMocks() })

  it('shows one pending action, starts chat, and reflects agent-saved completion', async () => {
    const initial = parseCrewTemplateSetupState(crewTemplates[0].files['TEMPLATE_SETUP.json'], crewTemplates[0])!
    let saved = initial
    getPlannerFileContent.mockImplementation(async () => ({ data: { content: JSON.stringify(saved) } }))
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    const onStartSetup = vi.fn().mockResolvedValue(undefined)

    await act(async () => {
      root.render(<WorkTemplateSetup template={crewTemplates[0]} workspacePath="Chats/Work/projects/finance" onStartSetup={onStartSetup} />)
    })
    expect(container.textContent).toContain('Setup pending')
    expect(container.textContent).not.toContain('Mark complete')
    expect(container.querySelectorAll('button')).toHaveLength(2)

    await act(async () => {
      (Array.from(container!.querySelectorAll('button')).find(button => button.textContent === 'Set up in chat') as HTMLButtonElement).click()
    })
    expect(onStartSetup).toHaveBeenCalledOnce()
    expect(updatePlannerFile).not.toHaveBeenCalled()

    saved = { ...initial, completed_steps: initial.checks.map(check => check.id) }
    await act(async () => { (container!.querySelector('[aria-label="Refresh setup status"]') as HTMLButtonElement).click() })
    expect(container.textContent).toContain('Setup complete')
    await act(async () => { root.unmount() })
  })

  it('migrates an earlier Finance Analyst progress file without losing completed checks', async () => {
    getPlannerFileContent.mockResolvedValue({ data: { content: JSON.stringify({ schema_version: 1, template_id: 'finance-analyst', template_version: 1, completed_steps: ['identity', 'skill'] }) } })
    updatePlannerFile.mockResolvedValue({})
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => {
      root.render(<WorkTemplateSetup template={crewTemplates[0]} workspacePath="Chats/Work/projects/finance" onStartSetup={async () => {}} />)
    })
    const saved = JSON.parse(updatePlannerFile.mock.calls[0][1] as string)
    expect(saved.checks).toHaveLength(9)
    expect(saved.completed_steps).toEqual(['identity', 'skill'])
    await act(async () => { root.unmount() })
  })

  it('does not replace a malformed checklist that already has check definitions', async () => {
    getPlannerFileContent.mockResolvedValue({ data: { content: JSON.stringify({ template_id: 'finance-analyst', checks: [{ id: 'custom' }], completed_steps: [] }) } })
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => {
      root.render(<WorkTemplateSetup template={crewTemplates[0]} workspacePath="Chats/Work/projects/finance" onStartSetup={async () => {}} />)
    })
    expect(updatePlannerFile).not.toHaveBeenCalled()
    expect(container.textContent).toContain('saved setup checklist is invalid')
    await act(async () => { root.unmount() })
  })

  it('reads the Tax Export checklist from its own path', async () => {
    const template = crewTemplates.find(item => item.id === 'tax-export')!
    getPlannerFileContent.mockResolvedValue({ data: { content: template.files[template.setupPath] } })
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => { root.render(<WorkTemplateSetup template={template} workspacePath="Chats/Work/projects/finance" onStartSetup={async () => {}} />) })
    expect(getPlannerFileContent).toHaveBeenCalledWith('Chats/Work/projects/finance/templates/tax-export/TEMPLATE_SETUP.json')
    expect(container.textContent).toContain('Tax Export Preparer · Setup pending')
    await act(async () => { root.unmount() })
  })

  it('clears a previous completion when the current checklist cannot be read', async () => {
    const template = crewTemplates[0]
    const initial = parseCrewTemplateSetupState(template.files[template.setupPath], template)!
    getPlannerFileContent.mockResolvedValue({ data: { content: JSON.stringify({ ...initial, completed_steps: initial.checks.map(check => check.id) }) } })
    container = document.createElement('div')
    const root = createRoot(container)
    try {
      await act(async () => { root.render(<WorkTemplateSetup template={template} workspacePath="Chats/Work/projects/finance" onStartSetup={async () => {}} />) })
      expect(container.textContent).toContain('Setup complete')
      getPlannerFileContent.mockRejectedValue(new Error('Access revoked'))
      await act(async () => { (container!.querySelector('[aria-label="Refresh setup status"]') as HTMLButtonElement).click() })
      expect(container.textContent).not.toContain('Setup complete')
      expect(container.textContent).toContain('Access revoked')
    } finally {
      await act(async () => { root.unmount() })
    }
  })

  it('rejects completed progress that removed a required template check', () => {
    const template = crewTemplates[0]
    const saved = JSON.parse(template.files[template.setupPath])
    saved.checks = saved.checks.slice(0, -1)
    saved.completed_steps = saved.checks.map((check: { id: string }) => check.id)
    expect(saved.checks.length).toBeGreaterThanOrEqual(5)
    expect(parseCrewTemplateSetupState(JSON.stringify(saved), template)).toBeNull()
  })
})
