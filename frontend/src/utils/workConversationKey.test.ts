import { describe, expect, it } from 'vitest'
import { workOpeningAgentKey } from './workConversationKey'

// PLAT-746-933: switching from another Crew used its agent slug under the
// destination ID, producing an unavailable-agent 422 even with valid agents.
describe('project opening agent hint stays inside its route identity', () => {
  it('does not carry a previous project or product agent into the destination', () => {
    expect(workOpeningAgentKey('work', 'destination', { surface: 'work', id: 'previous', agent: 'reviewer' })).toBeUndefined()
    expect(workOpeningAgentKey('work', 'destination', { surface: 'agentworks', id: 'destination', agent: 'reviewer' })).toBeUndefined()
  })
  it('preserves explicit same-project agents for the server to validate', () => {
    expect(workOpeningAgentKey('work', 'destination', { surface: 'work', id: 'destination', agent: 'reviewer' })).toBe('destination:chat:reviewer')
    expect(workOpeningAgentKey('work', 'destination', { surface: 'work', id: 'destination', agent: 'removed' })).toBe('destination:chat:removed')
    expect(workOpeningAgentKey('work', 'destination', { surface: 'work', id: 'destination', agent: 'main' })).toBeUndefined()
    expect(workOpeningAgentKey('work', 'destination', null)).toBeUndefined()
  })
})
