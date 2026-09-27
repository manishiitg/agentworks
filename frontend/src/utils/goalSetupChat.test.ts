import { afterEach, describe, expect, it } from 'vitest'
import { toAgentworksCommandDefinitions } from '../commands/agentworksProductCommands'
import { setProductCommands } from '../commands/registry'
import { goalSetupChatMessage } from './goalSetupChat'

afterEach(() => setProductCommands([]))

// The bar's buttons send the same message the command would if typed, and a
// working one before the product commands load.
describe('goalSetupChatMessage', () => {
  it('falls back to the command guidance before commands load', () => {
    for (const command of ['setup-goals', 'design-plan', 'design-dashboard']) {
      expect(goalSetupChatMessage(command)).toContain(`get_workflow_command_guidance(kind="${command}"`)
    }
  })

  it('uses the loaded product command', () => {
    setProductCommands(toAgentworksCommandDefinitions([{
      name: 'setup-goals', description: 'Goals', icon: 'target', aliases: [], menuHidden: false,
      prompt: 'Call get_workflow_command_guidance(kind="setup-goals", focus="{{context}}") and follow it.',
    }]))
    const message = goalSetupChatMessage('setup-goals')
    expect(message).toContain('kind="setup-goals"')
    expect(message).toContain('Requested from goal setup.')
  })

  it('starts from an installed playbook', () => {
    const message = goalSetupChatMessage('design-plan', [{ title: 'Website Growth Loop', skill_name: 'agentworks-playbook-website-growth-loop' }])
    expect(message).toContain('Website Growth Loop')
    expect(message).toContain('agentworks-playbook-website-growth-loop')
    expect(message).toContain('follow its setup checks')
  })
})
