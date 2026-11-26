// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import api from '../../services/api'
import { useCrewPanel, type CrewPanel } from './useCrewPanel'

const live = vi.hoisted(() => ({ refresh: (() => {}) as () => void }))
vi.mock('../../services/api', () => ({ default: { get: vi.fn() } }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: (refresh: () => void) => { live.refresh = refresh } }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => { vi.useRealTimers(); vi.clearAllMocks() })
const reply = (busy: boolean, extra: Partial<CrewPanel> = {}) => ({ data: { project: 'one', workspace: 'Crew/one', busy, mac_elsewhere: false, sections: [], details: {}, ...extra } })
function Panel({ project = 'one' }: { project?: string }) {
  const state = useCrewPanel('panel-refresh-test', project, `Crew/${project}`)
  return <span>{state.data?.busy ? 'running' : 'finished'}{state.error}<button onClick={state.refresh}>Retry</button></span>
}

// PLAT-746-513: with a connected live feed, a completion refresh inside the
// cache window must get a trailing read; fresh results never self-poll.
it('obeys the server trailing refresh after a cached lifecycle response', async () => {
  vi.useFakeTimers()
  vi.mocked(api.get)
    .mockResolvedValueOnce(reply(true) as never)
    .mockResolvedValueOnce(reply(true, { refresh_after_ms: 1000, refresh_attempt: 1 }) as never)
    .mockResolvedValueOnce(reply(false) as never)
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    await act(async () => root.render(<Panel />))
    expect(host.textContent).toBe('runningRetry')
    await act(async () => { await vi.advanceTimersByTimeAsync(6000) })
    expect(api.get).toHaveBeenCalledTimes(1)
    await act(async () => live.refresh())
    await act(async () => { await vi.advanceTimersByTimeAsync(999) })
    expect(api.get).toHaveBeenCalledTimes(2)
    await act(async () => { await vi.advanceTimersByTimeAsync(1) })
    expect(api.get).toHaveBeenLastCalledWith(expect.any(String), expect.objectContaining({ params: { session_id: undefined, refresh_attempt: 1 } }))
    expect(host.textContent).toBe('finishedRetry')
    await act(async () => { await vi.advanceTimersByTimeAsync(10000) })
    expect(api.get).toHaveBeenCalledTimes(3)
  } finally { await act(async () => root.unmount()) }
})

// A late timer must not read the previous Crew or survive a newer answer.
it('replaces the retry timer and clears it on Crew changes and unmount', async () => {
  vi.useFakeTimers()
  vi.mocked(api.get)
    .mockResolvedValueOnce(reply(true, { refresh_after_ms: 1000, refresh_attempt: 1 }) as never)
    .mockResolvedValueOnce(reply(false) as never)
    .mockResolvedValueOnce(reply(true, { refresh_after_ms: 1000, refresh_attempt: 1 }) as never)
    .mockResolvedValueOnce(reply(true, { refresh_after_ms: 1000, refresh_attempt: 1 }) as never)
  const host = document.createElement('div')
  const root = createRoot(host)
  let unmounted = false
  try {
    await act(async () => root.render(<Panel project="two" />))
    await act(async () => live.refresh())
    await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
    expect(api.get).toHaveBeenCalledTimes(2)
    await act(async () => live.refresh())
    await act(async () => root.render(<Panel project="three" />))
    expect(vi.getTimerCount()).toBe(1)
    await act(async () => root.unmount())
    unmounted = true
    expect(vi.getTimerCount()).toBe(0)
    await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
    expect(api.get).toHaveBeenCalledTimes(4)
  } finally { if (!unmounted) await act(async () => root.unmount()) }
})

// PR1787: independent notifications and manual Retry start a new bounded chain;
// only the server-directed timer carries an earlier attempt forward.
it('restarts the bounded refresh budget for notifications and manual Retry', async () => {
  vi.useFakeTimers()
  vi.mocked(api.get)
    .mockResolvedValueOnce(reply(true, { refresh_after_ms: 100, refresh_attempt: 4 }) as never)
    .mockResolvedValueOnce(reply(true, { refresh_attempt: 4, refresh_note: 'Still updating.' }) as never)
    .mockResolvedValueOnce(reply(true, { refresh_after_ms: 100, refresh_attempt: 1 }) as never)
    .mockResolvedValueOnce(reply(true, { refresh_attempt: 4, refresh_note: 'Still updating.' }) as never)
    .mockResolvedValueOnce(reply(true, { refresh_after_ms: 100, refresh_attempt: 1 }) as never)
    .mockResolvedValueOnce(reply(false) as never)
  const host = document.createElement('div')
  const root = createRoot(host)
  const attempt = () => vi.mocked(api.get).mock.calls.at(-1)?.[1]?.params?.refresh_attempt
  try {
    await act(async () => root.render(<Panel />))
    await act(async () => { await vi.advanceTimersByTimeAsync(100) })
    expect(attempt()).toBe(4)
    expect(vi.getTimerCount()).toBe(0)
    await act(async () => live.refresh())
    expect(attempt()).toBeUndefined()
    await act(async () => { await vi.advanceTimersByTimeAsync(100) })
    expect(attempt()).toBe(1)
    await act(async () => host.querySelector('button')!.click())
    expect(attempt()).toBeUndefined()
    await act(async () => { await vi.advanceTimersByTimeAsync(100) })
    expect(attempt()).toBe(1)
    expect(host.textContent).toBe('finishedRetry')
    await act(async () => { await vi.advanceTimersByTimeAsync(10000) })
    expect(api.get).toHaveBeenCalledTimes(6)
  } finally { await act(async () => root.unmount()) }
})
