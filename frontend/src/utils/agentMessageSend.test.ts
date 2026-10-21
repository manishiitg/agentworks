import { describe, expect, it } from 'vitest'
import { isAgentMessageSendToolCall, parseAgentMessageSend } from './agentMessageSend'

describe('agent message send', () => {
  it('reads who a message went to and whether the server took it', () => {
    const args = JSON.stringify({ target: 'agent:claude-reviewer', message: 'Build is ready.', submission_id: 's1' })
    const ok = parseAgentMessageSend('mcp__agentworks__send_message', args, JSON.stringify({ accepted: true, status: 'accepted', message_id: 'm1' }), 'ok')
    expect(ok).toMatchObject({ target: 'claude-reviewer', message: 'Build is ready.', accepted: true, failed: false })
    expect(parseAgentMessageSend('send_message', args, undefined, 'running')).toMatchObject({ accepted: false, failed: false })
  })

  it('shows a refused send as failed with the reason, and ignores other tools', () => {
    const failed = parseAgentMessageSend('send_message', JSON.stringify({ target: 'Crew/ci-cd', message: 'Hi' }), 'choose another agent or chat', 'error')
    expect(failed).toMatchObject({ target: 'Crew/ci-cd', failed: true })
    expect(isAgentMessageSendToolCall('read_agent_messages')).toBe(false)
    expect(parseAgentMessageSend('send_message', '{}', undefined)).toBeNull()
  })
})
