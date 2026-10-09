import { describe, expect, it } from 'vitest'
import { shouldHoldSendInBrowser } from './liveInputSubmission'

describe('shouldHoldSendInBrowser', () => {
  it('never holds a send for a chat that has a session, even if the tab believes a turn is streaming', () => {
    // server A SDE crew after a server restart: event stream dropped mid-turn, the
    // provider list 502ed so the tmux route was unknown — sends were stranded.
    expect(shouldHoldSendInBrowser({ isStreaming: true, routeLiveInputToCLI: false, hasSession: true })).toBe(false)
    expect(shouldHoldSendInBrowser({ isStreaming: true, routeLiveInputToCLI: true, hasSession: true })).toBe(false)
  })
  it('holds only for a chat with no session yet while a turn streams', () => {
    expect(shouldHoldSendInBrowser({ isStreaming: true, routeLiveInputToCLI: false, hasSession: false })).toBe(true)
    expect(shouldHoldSendInBrowser({ isStreaming: false, routeLiveInputToCLI: false, hasSession: false })).toBe(false)
  })
})
