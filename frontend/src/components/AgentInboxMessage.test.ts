import { expect, it } from 'vitest'
import { agentInboxMessage, agentInboxPreview } from './AgentInboxMessage'

it('PLAT-746 presents admitted Mac messages without requiring network-only fields', () => {
  expect(agentInboxMessage({ source: 'agent_message', sender_label: 'Reviewer', display_content: 'Actual reply', shared_message_id: 'reply' })?.body).toBe('Actual reply')
  expect(agentInboxMessage({ source: 'owner_app', sender_label: 'Reviewer', display_content: '[Agent message from Reviewer]' })).toBeNull()
  expect(agentInboxMessage({ source: 'agent_message', sender_label: 'Reviewer' })).toBeNull()
})

it('PLAT-746 shows an agent message as one plain preview line until opened', () => {
  expect(agentInboxPreview('\n## Done: PR merged\n\nMore detail')).toBe('Done: PR merged')
  expect(agentInboxPreview('x'.repeat(200))).toHaveLength(161)
})
