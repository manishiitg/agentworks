import { describe, expect, it } from 'vitest'
import { normalizeProductChatFailure } from './productChatFailure'

describe('terminal draft refusal', () => {
  it('explains what to do instead of showing the HTTP status', () => {
    const failure = normalizeProductChatFailure('Request failed with status code 423\nfinish or clear the terminal draft before sending another message')
    expect(failure.code).toBe('terminal_draft')
    expect(failure.title).toBe('Finish or clear the terminal input')
    expect(failure.message).toContain('Ctrl+C')
    expect(failure.retryable).toBe(true)
    expect(failure.technicalDetails).toBeUndefined()
  })

  it('also recognises the server error code', () => {
    expect(normalizeProductChatFailure('locked', { code: 'terminal_draft_active' }).code).toBe('terminal_draft')
  })
})
