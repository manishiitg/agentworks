import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useChatStore } from './useChatStore'

describe('streaming pieces are applied once per frame', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    useChatStore.getState().clearStreamingText('s1')
    useChatStore.setState({ completedStreamingText: {} })
  })
  afterEach(() => vi.useRealTimers())

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

  it('keeps de-duplicating repeated pieces', () => {
    useChatStore.getState().appendStreamingChunk('s1', 2, 'a', { isDelta: true })
    useChatStore.getState().appendStreamingChunk('s1', 2, 'a', { isDelta: true })
    useChatStore.getState().appendStreamingChunk('s1', 3, 'b', { isDelta: true })
    vi.advanceTimersByTime(16)
    expect(useChatStore.getState().streamingText.s1).toBe('ab')
  })
})
