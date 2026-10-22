// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

const state = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { get: state.get } }))
vi.mock('../../stores/useProductSurfaceStore', () => ({ useProductSurfaceStore: (select: (value: { productSurface: string }) => unknown) => select({ productSurface: 'code' }) }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ activeTabId: null, chatTabs: {} }) } }))
import { useRareWorkingTip } from './useRareWorkingTip'
import { PRODUCT_TIPS_PREFERENCE_CHANGED } from '../../utils/productTipsPreference'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('asks the server once, after ten seconds of work, and shows only what the server answers', async () => {
  vi.useFakeTimers()
  state.get.mockResolvedValue({ data: { tip: { id: 'schedule', text: 'Schedule a report' } } })
  function View({ working }: { working: boolean }) { return <div>{useRareWorkingTip(working, 'turn-1')}</div> }
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    await act(async () => root.render(<View working />))
    await act(async () => { await vi.advanceTimersByTimeAsync(9999) })
    expect(state.get).not.toHaveBeenCalled()
    await act(async () => { await vi.advanceTimersByTimeAsync(1) })
    expect(host.textContent).toBe('Schedule a report')
    expect(state.get).toHaveBeenCalledTimes(1)
    expect(state.get.mock.calls[0][1].params).toMatchObject({ surface: 'code', turn: 'turn-1' })
    // No more asking during the same turn, however long it runs.
    await act(async () => { await vi.advanceTimersByTimeAsync(60 * 60_000) })
    expect(state.get).toHaveBeenCalledTimes(1)
    // When the turn ends the tip goes; the server decides whether the next turn gets one.
    await act(async () => root.render(<View working={false} />))
    expect(host.textContent).toBe('')
    state.get.mockResolvedValue({ data: { tip: null } })
    await act(async () => root.render(<View working />))
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(host.textContent).toBe('')
  } finally {
    act(() => root.unmount())
    vi.useRealTimers()
    state.get.mockReset()
  }
})

it('hides a visible tip immediately when disabled and checks the server again when enabled', async () => {
  vi.useFakeTimers()
  state.get.mockResolvedValue({ data: { tip: { id: 'schedule', text: 'Schedule a report' } } })
  function View() { return <div>{useRareWorkingTip(true, 'active-turn')}</div> }
  const host = document.createElement('div'), root = createRoot(host)
  const preference = (enabled: boolean) => act(async () => { window.dispatchEvent(new CustomEvent(PRODUCT_TIPS_PREFERENCE_CHANGED, { detail: { enabled } })) })
  try {
    await act(async () => root.render(<View />))
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(host.textContent).toBe('Schedule a report')
    await preference(false)
    expect(host.textContent).toBe('')
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000) })
    expect(state.get).toHaveBeenCalledTimes(1)
    state.get.mockResolvedValue({ data: { tip: null } })
    await preference(true)
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(state.get).toHaveBeenCalledTimes(2)
    expect(host.textContent).toBe('')
  } finally { act(() => root.unmount()); vi.useRealTimers(); state.get.mockReset() }
})

it('does not restore a late tip response after the preference is disabled', async () => {
  vi.useFakeTimers()
  let resolve!: (value: unknown) => void
  state.get.mockReturnValue(new Promise(done => { resolve = done }))
  function View() { return <div>{useRareWorkingTip(true, 'active-turn')}</div> }
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<View />))
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    const signal = state.get.mock.calls[0][1].signal
    await act(async () => { window.dispatchEvent(new CustomEvent(PRODUCT_TIPS_PREFERENCE_CHANGED, { detail: { enabled: false } })) })
    expect(signal.aborted).toBe(true)
    await act(async () => resolve({ data: { tip: { id: 'late', text: 'Late hint' } } }))
    expect(host.textContent).toBe('')
  } finally { act(() => root.unmount()); vi.useRealTimers(); state.get.mockReset() }
})
