import { afterEach, expect, it, vi } from 'vitest'
import { SessionEventRecovery } from './sessionEventRecovery'

afterEach(() => vi.restoreAllMocks())

it('leaves healthy streams alone, recovers a silent stream once, and yields when push resumes', async () => {
  let now = 100_000
  vi.spyOn(Date, 'now').mockImplementation(() => now)
  const recovery = new SessionEventRecovery()
  const stream = { lastActivityAt: now, isClosed: false }
  let finish!: (events: string[]) => void
  const read = vi.fn(() => new Promise<string[]>(resolve => { finish = resolve }))

  for (let i = 0; i < 60; i++) {
    now += 1000
    stream.lastActivityAt = now // Normal status ticks, even with no new text.
    await recovery.poll('chat', stream, read)
  }
  expect(read).not.toHaveBeenCalled()

  now += 6000 // Headers alone cannot make a stalled connection healthy forever.
  const pending = recovery.poll('chat', stream, read)
  await recovery.poll('chat', stream, read)
  expect(read).toHaveBeenCalledTimes(1)
  finish(['missed completion'])
  expect(await pending).toEqual(['missed completion'])
  now += 2000
  stream.lastActivityAt = now
  await recovery.poll('chat', stream, read)
  expect(read).toHaveBeenCalledTimes(1)
})

it('backs off failed reads per session without delaying another chat or its next subscription', async () => {
  let now = 100_000
  vi.spyOn(Date, 'now').mockImplementation(() => now)
  const recovery = new SessionEventRecovery()
  const failed = vi.fn(async () => { throw new Error('server unavailable') })
  const other = vi.fn(async () => 'other chat completion')
  for (const delay of [4000, 8000, 16000, 30000, 30000]) {
    await expect(recovery.poll('failed', undefined, failed)).rejects.toThrow('server unavailable')
    now += delay - 1
    await recovery.poll('failed', undefined, failed)
    now++
  }
  expect(failed).toHaveBeenCalledTimes(5)
  expect(await recovery.poll('other', undefined, other)).toBe('other chat completion')
  recovery.retain(new Set(['other']))
  const restored = vi.fn(async () => 'restored chat')
  expect(await recovery.poll('failed', undefined, restored)).toBe('restored chat')
})
