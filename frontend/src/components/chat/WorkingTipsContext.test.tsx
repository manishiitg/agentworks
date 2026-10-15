// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

const state = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { get: state.get } }))
vi.mock('../../stores/useProductSurfaceStore', () => ({ useProductSurfaceStore: (select: (value: { productSurface: string }) => unknown) => select({ productSurface: 'code' }) }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ activeTabId: null, chatTabs: {} }) } }))
import { WorkingStatus } from './WorkingStatus'
import { WorkingTipsContext } from './workingTipsContext'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('shows no tip, and never asks for one, where tips are switched off (dashboards)', async () => {
  vi.useFakeTimers()
  state.get.mockResolvedValue({ data: { tip: { id: 'dash', text: 'Build a dashboard' } } })
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    await act(async () => root.render(<WorkingTipsContext.Provider value={false}><WorkingStatus startedAt={Date.now()} /></WorkingTipsContext.Provider>))
    await act(async () => { await vi.advanceTimersByTimeAsync(20_000) })
    expect(host.textContent).not.toContain('Tip')
    expect(state.get).not.toHaveBeenCalled()
  } finally {
    act(() => root.unmount())
    vi.useRealTimers()
    state.get.mockReset()
  }
})

it('says how long the agent has been quiet once it has produced nothing for a minute and a half', async () => {
  vi.useFakeTimers()
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    const now = Date.now()
    await act(async () => root.render(<WorkingTipsContext.Provider value={false}><WorkingStatus startedAt={now - 300_000} lastOutputAt={now - 60_000} /></WorkingTipsContext.Provider>))
    expect(host.querySelector('[data-testid="working-quiet"]')).toBeNull()
    await act(async () => { await vi.advanceTimersByTimeAsync(40_000) })
    expect(host.querySelector('[data-testid="working-quiet"]')?.textContent).toContain('no new output for 1m 40s')
  } finally {
    act(() => root.unmount())
    vi.useRealTimers()
  }
})
