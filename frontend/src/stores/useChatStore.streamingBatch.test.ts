import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useChatStore } from './useChatStore'

describe('streaming pieces are applied once per frame', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    useChatStore.getState().clearStreamingText('s1')
    useChatStore.setState({ completedStreamingText: {} })
  })
  // Fire a flush a test left queued; dropped with the fake clock it would stay marked as scheduled and starve later tests.
  afterEach(() => { vi.runOnlyPendingTimers(); vi.useRealTimers() })

  it('applies many pieces as one store update, in order', () => {
    let updates = 0
    const unsubscribe = useChatStore.subscribe(() => { updates += 1 })
    for (let index = 2; index < 12; index += 1) {
      useChatStore.getState().appendStreamingChunk('s1', index, `p${index}`, { isDelta: true })
    }
    expect(useChatStore.getState().streamingText.s1).toBeUndefined()
    vi.advanceTimersByTime(16)
    unsubscribe()
    expect(updates).toBe(1)
    expect(useChatStore.getState().streamingText.s1).toBe('p2p3p4p5p6p7p8p9p10p11')
  })

  it('a clear applies queued pieces first, so the completed text is whole', () => {
    useChatStore.getState().appendStreamingChunk('s1', 2, 'hello ', { isDelta: true })
    useChatStore.getState().appendStreamingChunk('s1', 3, 'world', { isDelta: true })
    useChatStore.getState().clearStreamingText('s1')
    vi.advanceTimersByTime(16)
    expect(useChatStore.getState().streamingText.s1).toBeUndefined()
    expect(useChatStore.getState().completedStreamingText.s1).toBe('hello world')
  })

  it('freezing the text streamed so far makes a reply row dated before the message sent mid-answer', () => {
    useChatStore.getState().appendStreamingChunk('s1', 2, 'already streamed', { isDelta: true })
    const row = useChatStore.getState().freezeStreamingTextBefore('s1', '2026-10-28T13:22:40.197Z')
    expect(row?.timestamp).toBe('2026-10-28T13:22:40.196Z')
    expect(row?.type).toBe('llm_generation_end')
    expect(useChatStore.getState().streamingText.s1).toBeUndefined()
    useChatStore.getState().appendStreamingChunk('s1', 3, 'the reply', { isDelta: true })
    vi.advanceTimersByTime(16)
    expect(useChatStore.getState().streamingText.s1).toBe('the reply')
  })

  it('uses only the backend widget stream flag, clears it with the reply and never infers it from deltas', () => {
    useChatStore.getState().appendStreamingChunk('s1', 2, '⏳ value!', { isDelta: true, chatUIStreamMode: 'progressive' })
    useChatStore.getState().flushStreamingChunks('s1')
    useChatStore.getState().appendStreamingChunk('s1', 3, 'Another', { isDelta: true, chatUIStreamMode: 'progressive' })
    useChatStore.getState().flushStreamingChunks('s1')
    expect(useChatStore.getState().streamingText.s1).toBe('⏳ value!Another')
    expect(useChatStore.getState().streamingChatUIMode.s1).toBe('progressive')
    useChatStore.getState().appendStreamingChunk('s1', 4, 'Block', { isDelta: false, chatUIStreamMode: 'complete' })
    useChatStore.getState().flushStreamingChunks('s1')
    expect(useChatStore.getState().streamingChatUIMode.s1).toBe('progressive')
    useChatStore.getState().clearStreamingText('s1')
    expect(useChatStore.getState().streamingChatUIMode.s1).toBeUndefined()
    useChatStore.getState().appendStreamingChunk('s1', 2, 'unclassified', { isDelta: true })
    useChatStore.getState().flushStreamingChunks('s1')
    expect(useChatStore.getState().streamingChatUIMode.s1).toBe('complete')
  })

  it('keeps de-duplicating repeated pieces', () => {
    useChatStore.getState().appendStreamingChunk('s1', 2, 'a', { isDelta: true })
    useChatStore.getState().appendStreamingChunk('s1', 2, 'a', { isDelta: true })
    useChatStore.getState().appendStreamingChunk('s1', 3, 'b', { isDelta: true })
    vi.advanceTimersByTime(16)
    expect(useChatStore.getState().streamingText.s1).toBe('ab')
  })
})
