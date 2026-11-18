// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { act, createElement } from 'react'
import { createRoot } from 'react-dom/client'
import { useSubagentChat } from './SubagentChatPanel'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../../services/api', () => ({ agentApi: { getSessionSubagent: get } }))

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
afterEach(() => { vi.useRealTimers(); get.mockReset(); document.body.innerHTML = '' })

const transcript = (status: string) => ({ session_id: 's1', agent_id: 'claude-code:a', name: 'Explore', status, started_at: '2026-11-18T10:00:00Z', events: [] })

it('reads a running subagent again every 2 s and stops once it finished (#3132)', async () => {
  vi.useFakeTimers()
  get
    .mockResolvedValueOnce({ running: true, transcript: transcript('running') })
    .mockResolvedValueOnce({ running: false, transcript: transcript('completed') })
  const seen: Array<boolean | undefined> = []
  const Probe = () => { const { data } = useSubagentChat('s1', 'toolu_1'); seen.push(data?.running); return null }
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => { root.render(createElement(Probe)) })
  expect(get).toHaveBeenCalledTimes(1)
  await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
  expect(get).toHaveBeenCalledTimes(2)
  await act(async () => { await vi.advanceTimersByTimeAsync(10000) })
  expect(get).toHaveBeenCalledTimes(2)
  expect(seen.at(-1)).toBe(false)
  act(() => root.unmount())
})

it('shows the server message when the conversation is gone, without retrying', async () => {
  vi.useFakeTimers()
  get.mockRejectedValue({ response: { status: 404, data: 'Not available any more.' } })
  let error = ''
  const Probe = () => { error = useSubagentChat('s1', 'toolu_1').error; return null }
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => { root.render(createElement(Probe)) })
  await act(async () => { await vi.advanceTimersByTimeAsync(10000) })
  expect(get).toHaveBeenCalledTimes(1)
  expect(error).toBe('Not available any more.')
  act(() => root.unmount())
})
