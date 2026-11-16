import { describe, expect, it } from 'vitest'
import { matchesWorkConversation, workOpeningAgentKey, workOnlineCopyForOpening } from './workConversationKey'

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

// Converted Main and side tabs keep their source identity, never a maker/base runtime substitute.
it('selects only the explicitly admitted online-copy row including Main', () => {
 const side={conversation_key:'crew:chat:kept',conversation_id:'same-id',session_id:'retired',name:'Helper',chat_location:'online_copy' as const,online_copy_address:'agent:owner/source',can_write:false}
 const main={...side,conversation_key:'crew:chat:former-main',is_main:true}
 expect(workOnlineCopyForOpening([side,main])).toBe(main)
 expect(workOnlineCopyForOpening([side,main],side.conversation_key)).toBe(side)
 expect(workOnlineCopyForOpening([side,main],'crew:chat:unrelated')).toBeUndefined()
 expect(workOnlineCopyForOpening([{...side,chat_location:'mac'}],side.conversation_key)).toBeUndefined()
})

// #2785: selecting a Crew replaced its own rows with Team metadata because
// both admitted projections used the same runtime session ID.
it('keeps personal and Team conversation projections separate when their runtime session is shared', () => {
  const ownKey = 'crew:chat:helper'
  const teamKey = 'crew:team:online:chat:helper'
  const own = { sessionId: 'shared-runtime', metadata: { agentProfileId: 'work', agentProfileProjectId: 'crew', agentProfileConversationKey: ownKey, agentProfileTabCanWrite: true } }
  const team = { ...own, metadata: { ...own.metadata, agentProfileConversationKey: teamKey, agentProfileTabCanWrite: false } }
  // Hydration and sidebar navigation must reuse the exact row in either order.
  for (const tabs of [[own, team], [team, own]]) {
    expect(tabs.find(tab => matchesWorkConversation(tab, 'work', 'crew', ownKey))).toBe(own)
    expect(tabs.find(tab => matchesWorkConversation(tab, 'work', 'crew', teamKey))).toBe(team)
  }
  // No runtime fallback may turn a Team projection into a missing own tab.
  expect([team].find(tab => matchesWorkConversation(tab, 'work', 'crew', ownKey))).toBeUndefined()
  expect(matchesWorkConversation(own, 'work', 'other-crew', ownKey)).toBe(false)
  expect(matchesWorkConversation(own, 'other-profile', 'crew', ownKey)).toBe(false)
  expect(matchesWorkConversation({ metadata: { ...own.metadata, isViewOnly: true } }, 'work', 'crew', ownKey)).toBe(false)
})
