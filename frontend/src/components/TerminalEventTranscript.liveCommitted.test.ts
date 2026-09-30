import { describe, expect, it } from 'vitest'
import { liveTextAlreadyCommitted } from './TerminalEventTranscript'

const answer = (content: string) => ({ kind: 'event', key: 'a', event: { type: 'unified_completion', data: { data: { content } } } }) as never
const user = { kind: 'event', key: 'u', event: { type: 'user_message', data: { data: { content: 'q' } } } } as never

describe('liveTextAlreadyCommitted', () => {
  it('drops the live row once the finished reply says the same thing', () => {
    expect(liveTextAlreadyCommitted([user, answer('Hello   world')], 'hello world')).toBe(true)
  })
  it('keeps the live row for a different or missing reply', () => {
    expect(liveTextAlreadyCommitted([user, answer('Other')], 'hello world')).toBe(false)
    expect(liveTextAlreadyCommitted([user], 'hello world')).toBe(false)
  })
  it('never matches a reply from before the latest user message', () => {
    expect(liveTextAlreadyCommitted([answer('hello world'), user], 'hello world')).toBe(false)
  })
})
