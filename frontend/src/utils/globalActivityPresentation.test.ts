import { describe, expect, it } from 'vitest'
import { sessionOriginLabel, titleWithoutOrigin } from './globalActivityPresentation'

describe('sessionOriginLabel', () => {
  it('prefers the server label, then names the trigger kind', () => {
    expect(sessionOriginLabel({ session_id: 's', triggered_by: 'webhook', triggered_by_label: 'Called by RTS Flow Tester' })).toBe('Called by RTS Flow Tester')
    expect(sessionOriginLabel({ session_id: 's', triggered_by: 'webhook' })).toBe('Webhook')
    expect(sessionOriginLabel({ session_id: 's', triggered_by: 'cron' })).toBe('Schedule')
    expect(sessionOriginLabel({ session_id: 's', triggered_by: 'bot:slack', bot_platform: 'slack' })).toBe('Slack message')
    expect(sessionOriginLabel({ session_id: 's', triggered_by: 'external' })).toBe('MCP / API call')
    expect(sessionOriginLabel({ session_id: 's', triggered_by: 'interactive' })).toBe('Chat')
  })
})

describe('titleWithoutOrigin', () => {
  it('drops a trailing origin the row already shows', () => {
    expect(titleWithoutOrigin('SDE · Called by X', 'Called by X')).toBe('SDE')
    expect(titleWithoutOrigin('SDE', 'Called by X')).toBe('SDE')
  })
})
