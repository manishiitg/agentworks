import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { findCommand, findCommandAnyMode, getCommands, setProductCommands, setUserCommands } from './registry'
import { toAgentworksCommandDefinitions, type AgentworksProductCommand } from './agentworksProductCommands'
import type { CommandContext, CommandDefinition } from './types'
import { pulseReviewFocuses } from './pulse-review-focus'

const { runPulseMock } = vi.hoisted(() => ({ runPulseMock: vi.fn() }))
vi.mock('../api/scheduler', () => ({ schedulerApi: { runPulse: runPulseMock } }))

// Structural fixtures mirroring agentworksproduct/product.yaml: the adapter
// mechanics (gates, aliases, focus resolution, substitution) are what's under
// test here. The real prompt content is locked by the backend manifest test.
const PULSE_REVIEW_PROMPT = 'Run /run-technical-review. You do it yourself in this turn, and may use your own subagents for parallel reading or analysis. '
  + 'Call get_workflow_command_guidance(kind="engineering-review", focus="{{context}}") and follow the returned instructions verbatim. '
  + 'This is one bounded Review+Fix task: persist the completed technical_review receipt before moving to the fix part. '
  + 'When you are done, present the selected repair objective, changes made, and remaining actionable issues.'

function productCommand(name: string, prompt: string, extra?: Partial<AgentworksProductCommand>): AgentworksProductCommand {
  return { name, description: `${name} description`, icon: 'terminal', aliases: [], menuHidden: false, prompt, ...extra }
}

function agentworksFixture(): AgentworksProductCommand[] {
  return [
    productCommand('design-plan', 'Call get_workflow_command_guidance(kind="design-plan", focus="{{context}}") and follow the returned instructions verbatim.'),
    productCommand('run-plan-drift', 'Run the /run-plan-drift review in this turn. Part 1 may apply bounded safe compatibility and prompt repairs; Part 2 remains read-only. Persist typed review and repair outcomes with their lifecycle outcomes. Focus: {{context}}.', { aliases: ['review-artifact-drift'], menuHidden: true }),
    productCommand('design-dashboard', 'Call get_workflow_command_guidance(kind="design-reporting-ui", focus="{{context}}") and follow the returned instructions verbatim.', { aliases: ['design-reporting-ui'] }),
    productCommand('setup-goals', 'Call get_workflow_command_guidance(kind="setup-goals", focus="{{context}}") and follow the returned instructions verbatim.', { aliases: ['define-success'] }),
    productCommand('run-goal-work', 'Run the /run-goal-work pass in this turn. Call get_workflow_command_guidance(kind="strategy-auditor", focus="{{context}}"). Then present Needs your decision proposals.', { aliases: ['strategy-auditor', 'goal-advisor'] }),
    productCommand('run-technical-review', PULSE_REVIEW_PROMPT, { aliases: ['pulse-review'], menuHidden: true }),
    productCommand('review-code', 'Call get_workflow_command_guidance(kind="design-plan", focus="Architecture focus: inspect saved scripts. Load references/code-authoring.md then references/scripted.md one at a time. Resolve canonical code paths from workflow.json.code_layout_version. do not apply changes in this review. {{context}}").'),
    productCommand('backup', '{{context}} Help me set up or run backup for this workflow.'),
    productCommand('publish', '{{context}} Help me set up or run publish for this workflow.'),
    productCommand('notify', '{{context}} Help me set up or review notifications for this workflow.'),
    ...pulseReviewFocuses.map(focus => productCommand(focus.legacyCommand, PULSE_REVIEW_PROMPT, { menuHidden: true })),
  ]
}

beforeEach(() => {
  setProductCommands(toAgentworksCommandDefinitions(agentworksFixture()))
})

afterEach(() => {
  setProductCommands([])
})

describe('Pulse slash commands', () => {
  it('uses one setup flow for new and existing goal setup names', () => {
    const setup = findCommand('setup-goals', 'workflow', 'workshop')
    expect(setup).toBeDefined()
    expect(findCommand('define-success', 'workflow', 'workshop')).toBe(setup)
    expect(findCommand('setup-goals', 'multi-agent')).toBeUndefined()
    const onSubmit = vi.fn()
    setup?.execute({ beforeSlash: 'Keep my existing targets', onSubmit } as unknown as CommandContext)
    expect(onSubmit).toHaveBeenCalledWith(expect.stringContaining('kind="setup-goals"'))
    expect(onSubmit).toHaveBeenCalledWith(expect.stringContaining('Keep my existing targets'))
  })

  it('runs the complete manual Pulse lifecycle through the scheduler backend', async () => {
    runPulseMock.mockResolvedValueOnce({ run_id: 'manual-pulse-1' })
    const addToast = vi.fn()
    const command = findCommand('pulse', 'workflow')

    await command?.execute({
      beforeSlash: '',
      onSubmit: vi.fn(),
      workshopMode: 'workshop',
      workflowWorkspacePath: 'Workflow/social-media',
      getWorkspaceStore: () => ({ activeFolder: null }),
      addToast,
    } as unknown as CommandContext)

    expect(runPulseMock).toHaveBeenCalledWith('Workflow/social-media')
    expect(addToast).toHaveBeenCalledWith('Pulse started', 'success')
  })

  it('uses the chat workflow even when the file browser points elsewhere', async () => {
    runPulseMock.mockClear().mockResolvedValueOnce({ run_id: 'pulse-chat' })
    await findCommand('pulse', 'workflow')?.execute({
      workflowWorkspacePath: 'Workflow/rts-latency',
      getWorkspaceStore: () => ({ activeFolder: 'Workflow/other' }),
      addToast: vi.fn(),
    } as unknown as CommandContext)
    expect(runPulseMock).toHaveBeenCalledExactlyOnceWith('Workflow/rts-latency')
  })

  it('does not run Pulse against a file browser folder when no workflow is open', async () => {
    runPulseMock.mockClear()
    const addToast = vi.fn()
    await findCommand('pulse', 'workflow')?.execute({
      getWorkspaceStore: () => ({ activeFolder: 'Workflow/other' }),
      addToast,
    } as unknown as CommandContext)
    expect(runPulseMock).not.toHaveBeenCalled()
    expect(addToast).toHaveBeenCalledWith('Open a workflow before running Pulse', 'error')
  })

  it('has no slash command for recurring Pulse setup — it is a toolbar/popup toggle', () => {
    const workflowCommand = findCommand('pulse-setup', 'workflow')
    const orgCommand = findCommand('pulse-setup', 'multi-agent')

    expect(workflowCommand).toBeUndefined()
    expect(orgCommand).toBeUndefined()
  })

  it('exposes manual Pulse modules in workflow workshop mode', () => {
    const workflowCommands = getCommands('workflow', 'workshop').map(command => command.command)
    const orgCommands = getCommands('multi-agent').map(command => command.command)

    // Pulse keeps two chat commands; the Pulse tab starts the upkeep reviews.
    for (const command of ['pulse', 'run-goal-work']) {
      expect(workflowCommands).toContain(command)
      expect(orgCommands).not.toContain(command)
    }
    for (const tabOnly of ['run-technical-review', 'run-plan-drift']) {
      expect(workflowCommands).not.toContain(tabOnly)
      expect(findCommand(tabOnly, 'workflow', 'workshop')).toBeDefined()
    }
    for (const retired of ['pulse-fixer', 'merge-pulse-issues', 'pulse-merge']) {
      expect(findCommand(retired, 'workflow', 'workshop')).toBeUndefined()
    }
    // Old names still run as aliases but have no menu rows.
    for (const renamed of ['pulse-review', 'strategy-auditor', 'review-artifact-drift']) {
      expect(workflowCommands).not.toContain(renamed)
      expect(findCommand(renamed, 'workflow', 'workshop')).toBeDefined()
    }
    for (const retiredCommand of ['bug-review', 'review-speed', 'review-cost', 'llm-ops-review', 'ops-review', 'engineering-review', 'specialize-advisors', 'pulse-setup', 'improve-knowledge', 'improve-learnings', 'improve-database', 'improve-report', 'improve-evaluation', 'pulse-review-stores', 'pulse-review-report', 'pulse-review-evaluation', 'pulse-backlog']) {
      expect(workflowCommands).not.toContain(retiredCommand)
    }
  })

  it('consolidates focused menu entries while preserving every shortcut and its access restrictions', () => {
    const menu = getCommands('workflow', 'workshop').map(command => command.command)
    expect(menu).not.toContain('run-technical-review')
    expect(findCommand('run-technical-review', 'workflow', 'workshop')).toBeDefined()
    for (const focus of pulseReviewFocuses) {
      expect(menu).not.toContain(focus.legacyCommand)
      expect(findCommand(focus.legacyCommand, 'workflow', 'workshop')).toBeDefined()
      expect(findCommandAnyMode(focus.legacyCommand)).toBeDefined()
      expect(findCommand(focus.legacyCommand, 'workflow', 'run')).toBeUndefined()
      expect(findCommand(focus.legacyCommand, 'workflow', 'workshop', false)).toBeUndefined()
      expect(findCommand(focus.legacyCommand, 'multi-agent')).toBeUndefined()
    }
  })

  it('uses identical specialist instructions for picker selections, typed focuses, and retained shortcuts', () => {
    const submit = (command: string, context: string, selectedFocus?: string) => {
      let result = ''
      findCommand(command, 'workflow', 'workshop')!.execute({
        beforeSlash: context,
        pulseReviewFocus: selectedFocus,
        onSubmit: (message: string) => { result = message },
        workshopMode: 'workshop',
      } as unknown as CommandContext)
      return result
    }
    for (const focus of pulseReviewFocuses) {
      const context = `${focus.id} investigate the newest regression`
      const picked = submit('pulse-review', context, focus.id)
      expect(picked).toBe(submit('pulse-review', context))
      expect(picked).toBe(submit(focus.legacyCommand, context))
      expect(picked).toContain(focus.instructions)
      expect(picked).toContain(context)
    }
    expect(submit('pulse-review', 'database concerns, but investigate freely', 'auto')).not.toContain('improve-database')
    expect(submit('pulse-review', 'investigate a custom concern')).toContain('investigate a custom concern')
  })

  it('hides Builder reviews from Run and rejects direct command lookup there', () => {
    const runCommands = getCommands('workflow', 'run').map(command => command.command)

    for (const command of ['pulse', 'pulse-review', 'strategy-auditor', 'goal-advisor', 'design-plan', 'review-code', 'review-artifact-drift', 'pulse-review-execution-health', 'pulse-review-validation-contract', 'backup', 'publish', 'notify']) {
      expect(runCommands).not.toContain(command)
      expect(findCommand(command, 'workflow', 'run', true)).toBeUndefined()
      expect(findCommand(command, 'workflow', 'workshop', true)).toBeDefined()
    }
  })

  it('enforces read-only workflow access even when a restored chat says Builder', () => {
    for (const mode of ['run', 'workshop', undefined] as const) {
      const commands = getCommands('workflow', mode, false).map(command => command.command)
      for (const command of getCommands('workflow', 'workshop', true)) {
        expect(commands).not.toContain(command.command)
        expect(findCommand(command.command, 'workflow', mode, false)).toBeUndefined()
      }
    }
  })

  it('keeps commands that explicitly support Run available to readers', () => {
    const runCommand: CommandDefinition = {
      command: 'inspect-output', description: 'Inspect the current output', icon: null,
      modes: ['workflow'], requiredWorkshopMode: 'run', source: 'user', execute: vi.fn(),
    }
    const builderCommand: CommandDefinition = {
      ...runCommand, command: 'edit-output', requiredWorkshopMode: 'workshop',
    }
    setUserCommands([runCommand, builderCommand])
    try {
      expect(getCommands('workflow', 'run', false)).toEqual([runCommand])
      expect(findCommand('inspect-output', 'workflow', 'run', false)).toBe(runCommand)
      expect(findCommand('edit-output', 'workflow', 'run', false)).toBeUndefined()
    } finally {
      setUserCommands([])
    }
  })

  it('reads review-code reference files one at a time', () => {
    let submitted = ''
    findCommand('review-code', 'workflow')?.execute({
      beforeSlash: '',
      onSubmit: (message: string) => { submitted = message },
      workshopMode: 'workshop',
    } as unknown as CommandContext)

    expect(submitted).toContain('references/code-authoring.md')
    expect(submitted).toContain('references/scripted.md')
    expect(submitted).not.toContain('},{')
  })

  it('routes Pulse Review through one retained review and fix task', () => {
    const command = findCommand('pulse-review', 'workflow')
    let submitted = ''

    command?.execute({
      beforeSlash: 'prioritize failed evaluation writes',
      onSubmit: (message: string) => { submitted = message },
      workshopMode: 'workshop',
    } as unknown as CommandContext)

    expect(submitted).toContain('kind="engineering-review"')
    expect(submitted).toContain('Run /run-technical-review. You do it yourself in this turn')
    expect(submitted).not.toContain('BACKGROUND task')
    expect(submitted).not.toContain('run_in_background')
    expect(submitted).not.toContain('required_pulse_review_modules')
    expect(submitted).toContain('present the selected repair objective')
    expect(submitted).toContain('prioritize failed evaluation writes')
  })

  it('routes a manual technical focus through retained Technical Review and Fix', () => {
    const technical = findCommand('pulse-review-execution-health', 'workflow')
    let submitted = ''

    technical?.execute({
      beforeSlash: 'check the newest retry spike',
      onSubmit: (message: string) => { submitted = message },
      workshopMode: 'workshop',
    } as unknown as CommandContext)
    expect(submitted).toContain('kind="engineering-review"')
    expect(submitted).toContain('Manual Pulse review focus: execution_health')
    expect(submitted).not.toContain('required_pulse_review_modules')
    expect(submitted).toContain('bounded Review+Fix')
  })

  it('routes store review aliases through store_integrity review and bounded repair', () => {
    for (const commandName of ['pulse-review-knowledge', 'pulse-review-learnings', 'pulse-review-database']) {
      const command = findCommand(commandName, 'workflow')
      let submitted = ''
      command?.execute({
        beforeSlash: 'repair confirmed ownership drift',
        onSubmit: (message: string) => { submitted = message },
        workshopMode: 'workshop',
      } as unknown as CommandContext)

      expect(submitted).toContain('kind="engineering-review"')
      expect(submitted).toContain('Manual Pulse review focus: store_integrity')
      expect(submitted).toContain('bounded Review+Fix')
    }
  })

  it('runs Strategy Auditor as a background guided review', () => {
    const command = findCommand('strategy-auditor', 'workflow')
    let submitted = ''

    command?.execute({
      beforeSlash: 'focus on repeated targets',
      onSubmit: (message: string) => { submitted = message },
      workshopMode: 'workshop',
    } as unknown as CommandContext)

    expect(submitted).toContain('Run the /run-goal-work pass in this turn')
    expect(submitted).toContain('kind="strategy-auditor"')
    expect(submitted).not.toContain('required_pulse_review_modules')
    expect(submitted).toContain('focus on repeated targets')
    expect(submitted).toContain('Needs your decision proposals')
    expect(submitted).not.toContain('message_sequence=')
    expect(submitted).not.toContain('in severity order')
  })

  it('keeps goal-advisor as a menu-free alias of the same strategic review', () => {
    const command = findCommand('goal-advisor', 'workflow', 'workshop')
    const canonical = findCommand('strategy-auditor', 'workflow', 'workshop')
    expect(command).toBe(canonical)
    expect(findCommandAnyMode('goal-advisor')).toBe(canonical)
    expect(getCommands().map(cmd => cmd.command)).not.toContain('goal-advisor')
    expect(findCommand('goal-advisor', 'multi-agent')).toBeUndefined()
    for (const mode of ['run', 'workshop', undefined] as const) {
      expect(findCommand('goal-advisor', 'workflow', mode, false)).toBeUndefined()
    }
    let submitted = ''

    command?.execute({
      beforeSlash: 'challenge feed concentration',
      onSubmit: (message: string) => { submitted = message },
      workshopMode: 'workshop',
    } as unknown as CommandContext)

    expect(submitted).toContain('get_workflow_command_guidance')
    expect(submitted).toContain('Run the /run-goal-work pass in this turn')
    expect(submitted).not.toContain('goal-advisor')
    expect(submitted).toContain('challenge feed concentration')
    expect(submitted).not.toContain('BACKGROUND task')
    expect(submitted).not.toContain('run_in_background')
    expect(submitted).toContain('Needs your decision proposals')
    expect(submitted).not.toContain('message_sequence=')
  })

  it('uses design-plan as the single comprehensive plan review command', () => {
    const workshopCommands = getCommands('workflow', 'workshop').map(command => command.command)
    const runCommands = getCommands('workflow', 'run').map(command => command.command)

    expect(workshopCommands).toContain('design-plan')
    expect(runCommands).not.toContain('design-plan')
    expect(findCommand('design-plan', 'workflow')?.requiredWorkshopMode).toBe('workshop')
    expect(workshopCommands).not.toContain('review-plan')
    expect(runCommands).not.toContain('review-plan')
  })

  it('keeps design-plan coordination in the main conversation', () => {
    const command = findCommand('design-plan', 'workflow')
    let submitted = ''

    command?.execute({
      beforeSlash: '',
      onSubmit: (message: string) => { submitted = message },
    } as CommandContext)

    expect(submitted).toContain('get_workflow_command_guidance')
    expect(submitted).not.toContain('Run the /design-plan review as a BACKGROUND task')
  })

  it('preserves Plan Drift repair authority in the background wrapper', () => {
    const command = findCommand('review-artifact-drift', 'workflow')
    let submitted = ''

    command?.execute({
      beforeSlash: '',
      onSubmit: (message: string) => { submitted = message },
      workshopMode: 'workshop',
    } as CommandContext)

    expect(submitted).toContain('Part 1 may apply bounded safe compatibility and prompt repairs')
    expect(submitted).toContain('Part 2 remains read-only')
    expect(submitted).not.toContain('do not modify implementation files')
    expect(submitted).toContain('lifecycle outcomes')
  })

  it('routes saved-code review to the read-only architecture flow', () => {
    let submitted = ''
    findCommand('review-code', 'workflow')?.execute({
      beforeSlash: 'check the report exporter',
      onSubmit: (message: string) => { submitted = message },
      workshopMode: 'workshop',
    } as unknown as CommandContext)

    expect(submitted).toContain('kind="design-plan"')
    expect(submitted).not.toContain('kind="review-code"')
    expect(submitted).toContain('workflow.json.code_layout_version')
    expect(submitted).toContain('Architecture focus')
    expect(submitted).toContain('do not apply changes in this review')
    expect(submitted).not.toContain('message_sequence=')
    expect(submitted).toContain('check the report exporter')
  })

  it('keeps workflow configuration actions out of the generic chat slash menu', () => {
    const chatCommands = getCommands('multi-agent').map(command => command.command)

    for (const command of ['build-skill', 'add-skill', 'mcp', 'mcp-add', 'models']) {
      expect(chatCommands).not.toContain(command)
    }
  })
})

describe('Product commands are scoped to their own surface', () => {
  it('shows a product\'s own commands once registered, and clears them when unregistered', async () => {
    // Product commands live in product.yaml and are delivered by the owning
    // product surface, then cleared on unmount. A profile-less chat does
    // not inherit those commands.
    const { setProductCommands } = await import('./registry')
    setProductCommands([{
      command: 'production', description: 'Start a video production', icon: null,
      modes: ['multi-agent'], source: 'product', execute: () => {},
    } as unknown as import('./types').CommandDefinition])
    try {
      const commands = getCommands('multi-agent').map(command => command.command)
      expect(commands).toContain('production')
    } finally {
      setProductCommands([])
    }
    expect(getCommands('multi-agent').map(command => command.command)).not.toContain('production')
  })

  it('can read the manifest-owned command set without legacy or user commands', async () => {
    const { findProductCommand, getProductCommands, setProductCommands, setUserCommands } = await import('./registry')
    setUserCommands([{
      command: 'personal', description: 'Personal command', icon: null,
      modes: ['multi-agent'], source: 'user', execute: () => {},
    } as unknown as import('./types').CommandDefinition])
    setProductCommands([{
      command: 'production', description: 'Product command', icon: null,
      modes: ['multi-agent'], source: 'product', execute: () => {},
    } as unknown as import('./types').CommandDefinition])
    try {
      expect(getProductCommands('multi-agent').map(command => command.command)).toEqual(['production'])
      expect(findProductCommand('production', 'multi-agent')).toBeDefined()
      expect(findProductCommand('personal', 'multi-agent')).toBeUndefined()
      expect(findProductCommand('pulse-review', 'multi-agent')).toBeUndefined()
    } finally {
      setProductCommands([])
      setUserCommands([])
    }
  })

  it('merges scoped custom commands into a product without adding platform builtins', async () => {
    const { findProductOrUserCommand, getProductAndUserCommands, setProductCommands, setUserCommands } = await import('./registry')
    setUserCommands([{
      command: 'personal', description: 'Project command', icon: null,
      modes: ['multi-agent'], source: 'user', execute: () => {},
    } as unknown as import('./types').CommandDefinition])
    setProductCommands([{
      command: 'production', description: 'Product command', icon: null,
      modes: ['multi-agent'], source: 'product', execute: () => {},
    } as unknown as import('./types').CommandDefinition])
    try {
      expect(getProductAndUserCommands('multi-agent').map(command => command.command)).toEqual(['production', 'personal'])
      expect(findProductOrUserCommand('personal', 'multi-agent')).toBeDefined()
      expect(findProductOrUserCommand('pulse-review', 'multi-agent')).toBeUndefined()
    } finally {
      setProductCommands([])
      setUserCommands([])
    }
  })
})
