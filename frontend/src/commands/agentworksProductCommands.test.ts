import { describe, expect, it, vi } from 'vitest'
import {
  executeAgentworksProductCommand,
  parseAgentworksProductCommands,
  toAgentworksCommandDefinitions,
  type AgentworksProductCommand,
} from './agentworksProductCommands'
import type { CommandContext } from './types'

function command(overrides: Partial<AgentworksProductCommand> = {}): AgentworksProductCommand {
  return {
    name: 'design-plan',
    description: 'Review the plan',
    icon: 'git-branch',
    aliases: [],
    menuHidden: false,
    prompt: 'Call get_workflow_command_guidance(kind="design-plan", focus="{{context}}").',
    ...overrides,
  }
}

function context(overrides: Partial<CommandContext> = {}): CommandContext {
  return { beforeSlash: '', onSubmit: vi.fn(), ...overrides } as unknown as CommandContext
}

describe('parseAgentworksProductCommands', () => {
  it('reads aliases and menu_hidden from the profile response', () => {
    const parsed = parseAgentworksProductCommands({
      commands: [
        { name: 'setup-goals', description: 'Goals', icon: 'target', aliases: ['define-success'], prompt: 'do {{context}}' },
        { name: 'custom-review', description: 'DB', menu_hidden: true, prompt: 'review {{context}}' },
        { name: 'nameless', description: 'no prompt' },
      ],
    })
    expect(parsed).toHaveLength(2)
    expect(parsed[0]).toMatchObject({ name: 'setup-goals', aliases: ['define-success'], menuHidden: false })
    expect(parsed[1]).toMatchObject({ name: 'custom-review', aliases: [], menuHidden: true })
  })

  it('falls back to the terminal icon for unknown names', () => {
    const [definition] = toAgentworksCommandDefinitions([command({ icon: 'not-a-real-icon' })])
    expect(definition.icon).toBeDefined()
  })
})

describe('toAgentworksCommandDefinitions', () => {
  it('owns the workflow-mode semantics every builder command shares', () => {
    const [definition] = toAgentworksCommandDefinitions([command()])
    expect(definition.modes).toEqual(['workflow'])
    expect(definition.requiredWorkflowMode).toBe('plan')
    expect(definition.requiredWorkshopMode).toBe('workshop')
    expect(definition.source).toBe('product')
    expect(definition.menuHidden).toBeUndefined()
  })

  it('keeps aliases and menu hiding without adding menu entries', () => {
    const [aliased, hidden] = toAgentworksCommandDefinitions([
      command({ name: 'setup-goals', aliases: ['define-success'] }),
      command({ name: 'custom-review', menuHidden: true }),
    ])
    expect(aliased.aliases).toEqual(['define-success'])
    expect(hidden.menuHidden).toBe(true)
  })


})

describe('executeAgentworksProductCommand', () => {
  it('substitutes typed context and trims the submitted prompt', () => {
    const onSubmit = vi.fn()
    executeAgentworksProductCommand(
      command({ prompt: '{{context}}\n\nHelp me with backup.' }),
      context({ beforeSlash: 'nightly runs', onSubmit }),
    )
    expect(onSubmit).toHaveBeenCalledWith('nightly runs\n\nHelp me with backup.')
  })

  it('removes the placeholder and suppresses empty submissions', () => {
    const onSubmit = vi.fn()
    executeAgentworksProductCommand(
      command({ prompt: '  {{context}}  ' }),
      context({ beforeSlash: '', onSubmit }),
    )
    expect(onSubmit).not.toHaveBeenCalled()
  })




})
