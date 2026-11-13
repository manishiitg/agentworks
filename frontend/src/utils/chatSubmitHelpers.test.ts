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
