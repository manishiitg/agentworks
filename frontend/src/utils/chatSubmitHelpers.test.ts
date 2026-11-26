import { expect, it } from 'vitest'
import { buildAgentProfileChatRequest } from './chatSubmitHelpers'
import type { AgentQueryRequest } from '../services/api-types'

// PLAT-746-938: the profile adapter dropped pending identities before the HTTP request.
it('keeps a caption-less pending upload in the normal Crew request', () => {
  const attachment = { hash: 'a'.repeat(64), name: 'screenshot.png' }
  const request = buildAgentProfileChatRequest({ query: '', pending_attachments: [attachment] } as AgentQueryRequest, 'example:chat:side')
  const wire = JSON.parse(JSON.stringify(request))
  expect(wire.message).toBe('')
  expect(wire.pending_attachments).toEqual([attachment])
  expect(wire.conversation_key).toBe('example:chat:side')
  expect(buildAgentProfileChatRequest({ query: 'hello' } as AgentQueryRequest)).not.toHaveProperty('pending_attachments')
})

// PLAT-746-1008: the ordinary Crew adapter must not drop explicit selection.
it('preserves only the requested skill identity in the Crew request', () => {
 const wire = buildAgentProfileChatRequest({ query: '', requested_skill_id: 'opaque-current-skill' } as AgentQueryRequest, 'example:chat:side')
 expect(JSON.parse(JSON.stringify(wire))).toMatchObject({message:'',requested_skill_id:'opaque-current-skill',conversation_key:'example:chat:side'})
})
