import { isOwnRecentAgent } from '../utils/recentAgentVisits'
// @vitest-environment happy-dom
import { expect, it } from 'vitest'
import { useProjectMenuHistoryStore } from './useProjectMenuHistoryStore'

// Recents are explicit personal navigation, not background streaming/completion order.
it('keeps recent agent visits scoped, unique and bounded without moving pinned Crews', () => {
  const store = useProjectMenuHistoryStore.getState()
  const scope = 'recent-agent-test', other = 'other-account'
  store.setPinned(scope, 'crew', true)
  for (let i = 0; i < 12; i++) store.visitAgent(scope, `agent-${i}`)
  store.visitAgent(scope, 'agent-5')
  store.visitAgent(other, 'private-agent')
  expect(useProjectMenuHistoryStore.getState().recentAgents[scope]).toHaveLength(10)
  expect(useProjectMenuHistoryStore.getState().recentAgents[scope][0]).toBe('agent-5')
  expect(useProjectMenuHistoryStore.getState().recentAgents[scope]).not.toContain('private-agent')
  expect(useProjectMenuHistoryStore.getState().pinned[scope]).toEqual(['crew'])
})

// A teammate may be open in the Team header; it never becomes a personal sidebar row.
it('excludes opened teammates and unknown-owner watched chats from personal recents', () => {
  expect(isOwnRecentAgent({ agentProfileId: 'work', agentProfileTabOwnerID: 'other', agentProfileTabCanWrite: false }, 'owner')).toBe(false)
  expect(isOwnRecentAgent({ agentProfileId: 'work', agentProfileTabCanWrite: false }, 'owner')).toBe(false)
  expect(isOwnRecentAgent({ agentProfileId: 'work', agentProfileTabOwnerID: 'owner', agentProfileTabCanWrite: false }, 'owner')).toBe(true)
  expect(isOwnRecentAgent({ agentProfileId: 'work', agentProfileTabCanWrite: true }, 'owner')).toBe(true)
})
