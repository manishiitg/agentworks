// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

const state = vi.hoisted(() => {
  const values = new Map<string, string>()
  const storage = { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => { values.set(key, value) }, removeItem: (key: string) => { values.delete(key) }, clear: () => values.clear(), key: () => null, get length() { return values.size } }
  Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true })
  return { userId: 'viewer-a', get: vi.fn() }
})
vi.mock('../../services/api', () => ({ default: { get: state.get } }))
vi.mock('../../stores/useProductSurfaceStore', () => ({ useProductSurfaceStore: (select: (value: { productSurface: string }) => unknown) => select({ productSurface: 'code' }) }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (value: { user: { id: string } }) => unknown) => select({ user: { id: state.userId } }) }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ activeTabId: null, chatTabs: {} }) } }))
import { useRareWorkingTip } from './useRareWorkingTip'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('loads product metadata after working, rotates during long work, and resets for another viewer', async () => {
  vi.useFakeTimers()
  vi.setSystemTime(1_000_000)
  localStorage.clear()
  state.get.mockResolvedValue({ data: { tips: { code: [{ id: 'schedule', text: 'Schedule a report' }, { id: 'dashboard', text: 'Build a dashboard' }] } } })
  function View({ working }: { working: boolean }) { return <div>{useRareWorkingTip(working)}</div> }
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    await act(async () => root.render(<View working />))
    await act(async () => { await vi.advanceTimersByTimeAsync(9999) })
    expect(state.get).not.toHaveBeenCalled()
    await act(async () => { await vi.advanceTimersByTimeAsync(1) })
    expect(host.textContent).toBe('Schedule a report')
    await act(async () => { await vi.advanceTimersByTimeAsync(30 * 60_000) })
    expect(host.textContent).toBe('Build a dashboard')
    await act(async () => root.render(<View working={false} />))
    await act(async () => { await vi.advanceTimersByTimeAsync(60 * 60_000) })
    expect(state.get).toHaveBeenCalledTimes(2)
    expect(host.textContent).toBe('')
    state.userId = 'viewer-b'
    await act(async () => root.render(<View working />))
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(host.textContent).toBe('Schedule a report')
  } finally {
    act(() => root.unmount())
    vi.useRealTimers()
    state.get.mockReset()
  }
})

it('keeps rotating in a second chat after the first chat claims the slot', async () => {
  vi.useFakeTimers()
  vi.setSystemTime(1_000_000)
  state.userId = 'viewer-a'
  localStorage.clear()
  state.get.mockResolvedValue({ data: { tips: { code: [{ id: 'schedule', text: 'Schedule a report' }, { id: 'dashboard', text: 'Build a dashboard' }] } } })
  function View({ working }: { working: boolean }) { return <div>{useRareWorkingTip(working)}</div> }
  const first = document.createElement('div')
  const second = document.createElement('div')
  const rootA = createRoot(first)
  const rootB = createRoot(second)
  try {
    await act(async () => rootA.render(<View working />))
    await act(async () => rootB.render(<View working />))
    // Both timers fire together: the first chat claims the tip while the
    // second finds the slot taken and must retry at the next due time.
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(first.textContent).toBe('Schedule a report')
    expect(second.textContent).toBe('')
    await act(async () => { rootA.unmount() })
    await act(async () => { await vi.advanceTimersByTimeAsync(30 * 60_000) })
    expect(second.textContent).toBe('Build a dashboard')
  } finally {
    act(() => rootB.unmount())
    vi.useRealTimers()
    state.get.mockReset()
  }
})
