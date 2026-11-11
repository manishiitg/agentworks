import { describe, expect, it } from 'vitest'
import { parseAgentMessageSend } from './agentMessageSend'

// PLAT-746: grouped messages calls and MCP envelopes must not disappear into tool counts.
describe('outgoing agent message receipts', () => {
  it('reads a wrapped messages send and only calls accepted delivery sent', () => {
    const args = JSON.stringify({ tool_name: 'messages', arguments: { action: 'send', inbox_id: 'inbox-example', message: 'Please review this.' } })
    const result = JSON.stringify({ content: [{ type: 'text', text: JSON.stringify({ accepted: true, delivery: 'stored', to_label: 'Reviewer' }) }] })
    expect(parseAgentMessageSend('mcp__bridge__call_tool', args, result, 'ok')).toMatchObject({ message: 'Please review this.', targetLabel: 'Reviewer', state: 'Sent' })
    expect(parseAgentMessageSend('mcp__bridge__call_tool', args, '{}', 'ok')?.state).toBe('Delivery unconfirmed')
  })
  it('does not turn mailbox reads or unrelated wrapped tools into sends', () => {
    expect(parseAgentMessageSend('messages', JSON.stringify({ action: 'read', message: 'not a send' }), '{}', 'ok')).toBeNull()
    expect(parseAgentMessageSend('call_tool', JSON.stringify({ tool_name: 'files', arguments: { message: 'not a send' } }), '{}', 'ok')).toBeNull()
  })
  it('keeps refusals distinct from delivery receipts', () => {
    const args = JSON.stringify({ action: 'send', target: 'agent:Reviewer', message: 'Review this.' })
    expect(parseAgentMessageSend('messages', args, '{"accepted":false}', 'ok')?.state).toBe('Couldn’t send')
    expect(parseAgentMessageSend('messages', args, '{"accepted":true,"state":"delivered"}', 'ok')?.state).toBe('Received')
  })
})
