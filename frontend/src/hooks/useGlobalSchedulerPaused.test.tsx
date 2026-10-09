// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { useGlobalSchedulerPaused } from './useGlobalSchedulerPaused'

vi.mock('../api/scheduler', () => ({ schedulerApi: { getConfig: vi.fn() } }))
import { schedulerApi } from '../api/scheduler'

const getConfig = schedulerApi.getConfig as ReturnType<typeof vi.fn>

const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(cleanup => cleanup()); vi.unstubAllGlobals(); vi.useRealTimers() })

function mountPaused(pollMs = 30_000, product?: string) {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  function Probe() {
    const paused = useGlobalSchedulerPaused(product, pollMs)
    return <span data-testid="paused">{paused === null ? 'unknown' : paused ? 'paused' : 'running'}</span>
  }
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  act(() => root.render(<Probe />))
  const text = () => host.querySelector('[data-testid="paused"]')!.textContent
  let unmounted = false
  const unmount = () => { if (!unmounted) { act(() => root.unmount()); host.remove(); unmounted = true } }
  cleanups.push(unmount)
  return { text, unmount }
}

it('reflects the global pause and re-polls on the interval', async () => {
  vi.useFakeTimers()
  getConfig.mockResolvedValue({ globally_paused: false })
  const { text } = mountPaused(1_000)
  expect(text()).toBe('unknown')
  await act(async () => { await Promise.resolve() })
  expect(text()).toBe('running')
  getConfig.mockResolvedValue({ globally_paused: true })
  await act(async () => { vi.advanceTimersByTime(1_000); await Promise.resolve() })
  expect(text()).toBe('paused')
  expect(getConfig).toHaveBeenCalledTimes(2)
})

it('keeps the last known value when a poll fails', async () => {
  vi.useFakeTimers()
  getConfig.mockResolvedValue({ globally_paused: true })
  const { text } = mountPaused(1_000)
  await act(async () => { await Promise.resolve() })
  expect(text()).toBe('paused')
  getConfig.mockRejectedValueOnce(new Error('offline'))
  await act(async () => { vi.advanceTimersByTime(1_000); await Promise.resolve() })
  expect(text()).toBe('paused')
})

// PLAT-782: a product's own pause shows on that product only; the pause of everything shows everywhere.
it('follows the pause of its own product, not the others', async () => {
  getConfig.mockResolvedValue({ globally_paused: false, paused_products: ['work'] })
  const crew = mountPaused(30_000, 'work')
  await act(async () => { await Promise.resolve() })
  expect(crew.text()).toBe('paused')
  const goals = mountPaused(30_000, 'agentworks')
  await act(async () => { await Promise.resolve() })
  expect(goals.text()).toBe('running')
})
