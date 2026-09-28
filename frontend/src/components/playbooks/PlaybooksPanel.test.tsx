// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import PlaybooksPanel from './PlaybooksPanel'
import { playbooksApi } from '../../api/playbooks'
import { agentApi } from '../../services/api'

vi.mock('../../api/playbooks', () => ({
  playbooksApi: {
    list: vi.fn().mockResolvedValue([{
      id: 'basic-browser-setup', title: 'Basic Browser Setup', description: 'Configure browser access.', version: '0.6.0',
      changelog: [{ version: '0.6.0', summary: 'Adds stable locator and evidence retention guidance.' }],
      category: 'Browser QA', order: 1, inputCount: 2, toolCount: 1,
      setupInputs: [
        { id: 'environment', label: 'Application and environment', required: true },
        { id: 'reporting', label: 'Reporting preference', required: false },
      ],
      requiredCapabilities: ['browser_test_execution', 'workflow_reporting'],
      recommendedTools: [{ id: 'playwright', name: 'Playwright', type: 'cli', purpose: 'Run repeatable browser tests', capability: 'browser_test_execution', optional: true }],
      pulseFocus: [
        { module: 'strategic_review', label: 'Strategic review', focus_areas: ['Coverage of critical journeys'], review_when: ['Business priorities change'] },
      ],
      outputs: ['Reusable browser foundation', 'Live workflow report'],
    }]),
    listInstalled: vi.fn().mockResolvedValue([]),
    install: vi.fn().mockResolvedValue({
      id: 'basic-browser-setup', title: 'Basic Browser Setup', version: '0.6.0',
      category: 'Browser QA', skill_name: 'agentworks-playbook-basic-browser-setup', source_hash: 'sha256:test',
      status: 'draft', installed_at: '2026-09-13T00:00:00Z',
    }),
  },
}))

vi.mock('../../hooks/useCanWriteWorkflow', () => ({
  useCanWriteWorkflow: () => true,
  READ_ONLY_TITLE: 'Read only',
}))

vi.mock('../../services/api', () => ({
  agentApi: { getPlannerFileContent: vi.fn().mockResolvedValue({ data: { content: JSON.stringify({
    schema_version: 1, playbook_id: 'website-growth-loop', playbook_version: '0.2.0',
    checks: [{ id: 'goal_owner' }, { id: 'team_bindings' }, { id: 'test_run' }],
    completed_steps: ['goal_owner'], evidence: { goal_owner: 'Reviewed owner and site in Builder chat' },
  }) } }) },
}))

vi.mock('../workflow/AskAIButton', () => ({
  AskAIButton: ({ label, message }: { label: string; message: string }) => <button type="button" data-message={message}>{label}</button>,
}))

vi.mock('../../stores/useWorkflowManifestStore', () => ({
  useWorkflowManifestStore: { getState: () => ({ refreshWorkflows: vi.fn().mockResolvedValue(undefined) }) },
}))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const click = async (element: Element | null) => {
  if (!(element instanceof HTMLElement)) throw new Error('Expected a clickable element')
  await act(async () => element.click())
}

it('opens a catalog playbook and installs it for Builder setup', async () => {
  const container = document.createElement('div')
  const root = createRoot(container)
  try {
    await act(async () => {
      root.render(<PlaybooksPanel workspacePath="Workflow/demo" />)
      await Promise.resolve()
    })
    expect(container.textContent).toContain('Catalog 1')
    expect(container.textContent).toContain('Basic Browser Setup')
    expect(container.textContent).toContain('Workflow guide')
    expect(container.textContent).toContain('AgentWorks')
    expect(container.textContent).toContain('Agentic Engineering Platform 1 small-team playbook · 0 Crew proposals · 1 Workflow guide')
    expect(container.textContent).toContain('Engineering1')
    expect(container.querySelector('[aria-label="Playbook catalog hierarchy"]')).not.toBeNull()
    expect(container.textContent).toContain('Ask about this playbook')
    expect(playbooksApi.install).not.toHaveBeenCalled()
    const askButton = [...container.querySelectorAll('button')].find(button => button.textContent?.includes('Ask about this playbook'))
    expect(askButton?.getAttribute('data-message')).toContain('Basic Browser Setup')

    await click(container.querySelector('[aria-label="Open Basic Browser Setup details"]'))
    expect(playbooksApi.install).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Setup with Builder')
    expect(container.textContent).toContain('no predefined Crew team or tracked setup checklist')
    expect(container.textContent).toContain('2 inputs guide adaptation')
    expect(container.textContent).toContain('What this playbook covers')
    expect(container.textContent).toContain('Application and environment')
    expect(container.textContent).toContain('Reporting preference(optional)')
    expect(container.textContent).toContain('browser test execution')
    expect(container.textContent).toContain('What you’ll get')
    expect(container.textContent).toContain('Reusable browser foundation')
    expect(container.textContent).toContain('Run repeatable browser tests')
    expect(container.textContent).toContain('Strategic review focus')
    expect(container.textContent).toContain('Coverage of critical journeys')
    expect(container.textContent).toContain('Technical and Architecture Review choose scope from current evidence and structural risk')
    expect(container.textContent).not.toContain('Plan drift review')

    await click([...container.querySelectorAll('button')].find(button => button.textContent?.includes('Use workflow guide')) || null)
    expect(container.textContent).toContain('Workflow guide saved v0.6.0 · draft')
    expect(container.textContent).toContain('Continue setup in Builder')
    expect(container.textContent).toContain('Builder first inspects the current workflow')
    expect(container.textContent).toContain('Installing guidance is not approval')
    const setupButton = [...container.querySelectorAll('button')].find(button => button.textContent?.includes('Continue setup in Builder'))
    expect(setupButton?.getAttribute('data-message')).toContain('summarize what can be reused and what is missing')
    expect(setupButton?.getAttribute('data-message')).toContain('Ask focused questions for unresolved customer choices')
    expect(setupButton?.getAttribute('data-message')).toContain('record the answers as customer direction')

    await click([...container.querySelectorAll('button')].find(button => button.textContent?.includes('Back to catalog')) || null)
    await click([...container.querySelectorAll('[role="tab"]')].find(button => button.textContent?.includes('Installed')) || null)
    expect(container.textContent).toContain('Basic Browser Setup')
    expect(container.textContent).toContain('Engineering / Browser QA · v0.6.0 · draft')
  } finally {
    await act(async () => root.unmount())
  }
})

it('shows an available version and refreshes the installed playbook without claiming workflow changes', async () => {
  vi.mocked(playbooksApi.listInstalled).mockResolvedValueOnce([{
    id: 'basic-browser-setup', title: 'Basic Browser Setup', version: '0.5.0', category: 'Browser QA',
    skill_name: 'agentworks-playbook-basic-browser-setup', source_hash: 'sha256:old', status: 'ready', installed_at: '2026-09-01T00:00:00Z',
  }])
  const container = document.createElement('div')
  const root = createRoot(container)
  try {
    await act(async () => {
      root.render(<PlaybooksPanel workspacePath="Workflow/demo" />)
      await Promise.resolve()
    })
    await click([...container.querySelectorAll('[role="tab"]')].find(button => button.textContent?.includes('Installed')) || null)
    expect(container.textContent).toContain('Update available')
    expect(container.textContent).toContain('latest v0.6.0')
    await click([...container.querySelectorAll('button')].find(button => button.textContent?.includes('Basic Browser Setup')) || null)
    expect(container.textContent).toContain('Workflow guide saved v0.5.0 · ready')
    expect(container.textContent).toContain('Update available · v0.6.0')
    expect(container.textContent).toContain('What changed')
    expect(container.textContent).toContain('v0.6.0: Adds stable locator and evidence retention guidance.')
    expect(container.textContent).toContain('not changed automatically')
    await click([...container.querySelectorAll('button')].find(button => button.textContent?.includes('Update playbook')) || null)
    expect(playbooksApi.install).toHaveBeenCalledWith('Workflow/demo', 'basic-browser-setup')
    expect(container.textContent).toContain('Workflow guide saved v0.6.0 · draft')
  } finally {
    await act(async () => root.unmount())
  }
})

it('shows Website Growth as a team proposal whose setup continues in Builder chat', async () => {
  vi.mocked(playbooksApi.install).mockResolvedValueOnce({
    id: 'website-growth-loop', title: 'Website Growth Loop', version: '0.2.0',
    category: 'Website Growth', skill_name: 'agentworks-playbook-website-growth-loop',
    source_hash: 'sha256:growth', status: 'draft', installed_at: '2026-09-25T00:00:00Z',
  })
  vi.mocked(playbooksApi.list).mockResolvedValueOnce([{
    id: 'website-growth-loop', title: 'Website Growth Loop', description: 'Grow relevant website traffic.',
    version: '0.2.0', category: 'Website Growth', order: 1, inputCount: 4, toolCount: 2,
    setupPrompt: 'Inspect existing Crews and propose a team before creating agents.',
    agentSlots: [
      { id: 'strategist', agent_playbook_id: 'website-growth-starter', required: true, output: 'growth-priority-brief/v1' },
      { id: 'search', agent_playbook_id: 'search-opportunity-mapper', required: true, output: 'search-opportunity-list/v1' },
    ],
    handoffs: [{ id: 'strategy-to-search', from: 'strategist', to: 'search', artifact_type: 'growth-priority-brief/v1', required: true }],
    setupChecks: ['goal_owner', 'team_bindings', 'test_run'],
  }])
  const container = document.createElement('div')
  const root = createRoot(container)
  try {
    await act(async () => {
      root.render(<PlaybooksPanel workspacePath="Workflow/growth" />)
      await Promise.resolve()
    })
    await click(container.querySelector('[aria-label="Open Website Growth Loop details"]'))
    expect(container.textContent).toContain('Crew automation proposal')
    expect(container.textContent).toContain('Proposed Crew team')
    expect(container.textContent).toContain('Builder must test a blocking validator for each chosen handoff')
    expect(container.textContent).toContain('website growth starter')
    expect(container.textContent).toContain('search opportunity mapper')
    expect(container.textContent).toContain('Choosing this Playbook creates no Crew members')
    expect(container.textContent).toContain('Setup checks')
    expect(container.textContent).toContain('team bindings')
    expect(container.textContent).toContain('Use proposal')
    await click([...container.querySelectorAll('button')].find(button => button.textContent?.includes('Use proposal')) || null)
    expect(container.textContent).toContain('Proposal saved v0.2.0 · draft')
    expect(agentApi.getPlannerFileContent).toHaveBeenCalledWith('Workflow/growth/skills/agentworks-playbook-website-growth-loop/SETUP.json')
    expect(container.textContent).toContain('1 of 3 checks recorded with evidence')
    expect(container.textContent).toContain('Continue setup in Builder')
    const setup = [...container.querySelectorAll('button')].find(button => button.textContent?.includes('Continue setup in Builder'))
    expect(setup?.getAttribute('data-message')).toContain('Inspect existing Crews and propose a team')
    expect(setup?.getAttribute('data-message')).toContain('strategy-to-search: strategist → search, growth-priority-brief/v1, required')
    expect(setup?.getAttribute('data-message')).toContain('blocking validation step before the consumer')
    expect(setup?.getAttribute('data-message')).toContain('one authorized customer-like case manually')
  } finally {
    await act(async () => root.unmount())
  }
})

it('shows Finance proposal progress from its own installed setup record', async () => {
  vi.mocked(playbooksApi.listInstalled).mockResolvedValueOnce([{
    id: 'finance-operations-review', title: 'Finance Operations Review', version: '0.1.0',
    category: 'Finance', skill_name: 'agentworks-playbook-finance-operations-review',
    source_hash: 'sha256:finance', status: 'draft', installed_at: '2026-09-25T00:00:00Z',
  }])
  vi.mocked(playbooksApi.list).mockResolvedValueOnce([{
    id: 'finance-operations-review', title: 'Finance Operations Review', description: 'Review billing exceptions and finance impact.',
    version: '0.1.0', category: 'Finance', order: 1, inputCount: 6, toolCount: 4,
    setupPrompt: 'Inspect current finance Crews and propose a manual first route.',
    agentSlots: [
      { id: 'billing', agent_playbook_id: 'billing-operations-coordinator', required: true, output: 'billing-exception-queue/v1' },
      { id: 'finance', agent_playbook_id: 'finance-analyst', required: true, output: 'finance-impact-readout/v1' },
    ],
    handoffs: [{ id: 'billing-to-finance', from: 'billing', to: 'finance', artifact_type: 'billing-exception-queue/v1', required: true }],
    setupChecks: ['goal_owner', 'team_bindings', 'test_run'],
  }])
  vi.mocked(agentApi.getPlannerFileContent).mockResolvedValueOnce({ data: { content: JSON.stringify({
    schema_version: 1, playbook_id: 'finance-operations-review', playbook_version: '0.1.0',
    checks: [{ id: 'goal_owner' }, { id: 'team_bindings' }, { id: 'test_run' }],
    completed_steps: ['goal_owner'], evidence: { goal_owner: 'Owner and entity confirmed' },
  }) } } as Awaited<ReturnType<typeof agentApi.getPlannerFileContent>>)
  const container = document.createElement('div')
  const root = createRoot(container)
  try {
    await act(async () => {
      root.render(<PlaybooksPanel workspacePath="Workflow/finance" />)
      await Promise.resolve()
    })
    await click(container.querySelector('[aria-label="Open Finance Operations Review details"]'))
    expect(container.textContent).toContain('billing operations coordinator')
    expect(container.textContent).toContain('finance analyst')
    expect(container.textContent).toContain('billing → finance')
    expect(agentApi.getPlannerFileContent).toHaveBeenCalledWith('Workflow/finance/skills/agentworks-playbook-finance-operations-review/SETUP.json')
    expect(container.textContent).toContain('1 of 3 checks recorded with evidence')
    expect(container.textContent).toContain('Proposal saved v0.1.0 · draft')
  } finally {
    await act(async () => root.unmount())
  }
})
