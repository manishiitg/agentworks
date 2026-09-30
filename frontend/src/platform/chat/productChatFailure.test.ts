import { describe, expect, it } from 'vitest'
import { normalizeProductChatFailure } from './productChatFailure'

describe('account change refusal', () => {
  it('tells the person to start a new chat, from the code or from the old server text', () => {
    for (const failure of [
      normalizeProductChatFailure('This chat started on a different account than the one this project now uses.', { code: 'account_change_requires_new_conversation' }),
      normalizeProductChatFailure('account change requires a new conversation'),
    ]) {
      expect(failure.code).toBe('account_change_requires_new_conversation')
      expect(failure.title).toBe('This chat cannot switch accounts')
      expect(failure.message).toContain('Switch the project back')
      expect(failure.retryable).toBe(false)
    }
  })
})
