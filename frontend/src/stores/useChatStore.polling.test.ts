import { afterEach, expect, it, vi } from 'vitest'
import { useChatStore } from './useChatStore'

afterEach(() => {
  useChatStore.getState().stopPolling()
  vi.useRealTimers()
})

it('does not queue more polls while a slow cycle is outstanding', async () => {
  vi.useFakeTimers()
  let finish!: () => void
  const slow = new Promise<void>(resolve => { finish = resolve })
  const poll = vi.fn(() => slow)
  useChatStore.getState().startPolling(poll)
  await vi.advanceTimersByTimeAsync(10_000)
  expect(poll).toHaveBeenCalledTimes(1)

  // A stop/start while a request is pending must not bypass the guard.
  useChatStore.getState().stopPolling()
  useChatStore.getState().startPolling(poll)
  await vi.advanceTimersByTimeAsync(1000)
  expect(poll).toHaveBeenCalledTimes(1)
  finish()
  await vi.advanceTimersByTimeAsync(1000)
  expect(poll).toHaveBeenCalledTimes(2)
})
