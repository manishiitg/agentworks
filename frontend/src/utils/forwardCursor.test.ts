import { describe, expect, it } from 'vitest'
import { nextForwardCursor, durableStreamStartCursor } from './forwardCursor'

describe('nextForwardCursor', () => {
  it('advances on newer rows', () => {
    expect(nextForwardCursor(5, 9, 4, true)).toBe(9)
  })

  it('never resets a durable cursor to zero on an empty forward page', () => {
    expect(nextForwardCursor(120, 0, 0, true)).toBe(120)
  })

  it('ignores a backward cursor that arrives with rows', () => {
    expect(nextForwardCursor(120, 80, 3, true)).toBe(120)
  })

  it('accepts the server clamping a stale cursor to a non-empty journal tip', () => {
    expect(nextForwardCursor(5000, 140, 0, true)).toBe(140)
  })

  it('lets execution tabs follow in-memory indices that restart after a restart', () => {
    expect(nextForwardCursor(5000, 3, 3, false)).toBe(3)
  })
})

describe('durableStreamStartCursor', () => {
  it('continues mature chats past 9,999 without replaying pruned history', () => {
    expect(durableStreamStartCursor(25_000, [{ sequence: 24_900 }])).toBe(25_000)
    expect(durableStreamStartCursor(0, [{ sequence: 25_000 }])).toBe(25_000)
  })
  it('starts after the newest journal row the tab already holds when the stored cursor was reset', () => {
    expect(durableStreamStartCursor(0, [{ sequence: 3 }, { sequence: 470 }, {}])).toBe(470)
  })

  it('keeps a stored cursor that is already ahead of the held rows', () => {
    expect(durableStreamStartCursor(528, [{ sequence: 470 }])).toBe(528)
  })

  it('starts from zero only for a tab with no journal rows', () => {
    expect(durableStreamStartCursor(-1, [])).toBe(0)
  })
})
