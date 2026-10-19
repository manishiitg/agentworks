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

it('announces server state changes without announcing playful words or elapsed ticks', async () => {
  vi.useFakeTimers()
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    const now = Date.now()
    const render = async (phase?: { phase: string; text: string; since: number }) => act(async () => root.render(<WorkingTipsContext.Provider value={false}><WorkingStatus startedAt={now} lastOutputAt={now} phase={phase} /></WorkingTipsContext.Provider>))
    await render()
    const status = host.querySelector('[role="status"]')!
    expect(status.textContent).toBe('Working…')
    await act(async () => { vi.advanceTimersByTime(15_000) })
    expect(status.textContent).toBe('Working…')
    await render({ phase: 'starting', text: 'Starting Cursor', since: now })
    expect(status.textContent).toBe('Starting Cursor')
    await render({ phase: 'waiting_for_you', text: 'Waiting for your answer', since: now })
    expect(status.textContent).toBe('Waiting for your answer')
    await render({ phase: 'quiet', text: 'Still working · no update for 2 min', since: now })
    expect(status.textContent).toBe('Still working · no update for 2 min')
    await render({ phase: 'running', text: '', since: now })
    expect(status.textContent).toBe('Working…')
    expect(status.getAttribute('aria-atomic')).toBe('true')
  } finally {
    act(() => root.unmount())
    vi.useRealTimers()
  }
})

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

it('renders elapsed server activity time without deciding quiet in the browser', async () => {
  vi.useFakeTimers()
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    const now = Date.now()
    await act(async () => root.render(<WorkingTipsContext.Provider value={false}><WorkingStatus startedAt={now - 300_000} lastOutputAt={now - 60_000} /></WorkingTipsContext.Provider>))
    expect(host.querySelector('[data-testid="working-quiet"]')).toBeNull()
    await act(async () => { await vi.advanceTimersByTimeAsync(40_000) })
    expect(host.textContent).toContain('1m 40s since update')
    expect(host.textContent).not.toContain('no new output')
  } finally {
    act(() => root.unmount())
    vi.useRealTimers()
  }
})

it('uses agent activity rather than phase start, with whole turn duration in the tooltip', async () => {
  vi.useFakeTimers()
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    const now = Date.now()
    await act(async () => root.render(<WorkingTipsContext.Provider value={false}><WorkingStatus startedAt={now - 300_000} lastOutputAt={now - 300_000} phase={{ phase: 'starting', text: 'Starting Muse', since: now - 12_000 }} /></WorkingTipsContext.Provider>))
    expect(host.querySelector('[data-testid="working-word"]')?.textContent).toBe('Starting Muse')
    expect(host.textContent).toContain('5m 00s since update')
    expect(host.querySelector('[title]')?.getAttribute('title')).toBe('Turn duration: 5m 00s')
    expect(host.querySelector('[data-testid="working-quiet"]')).toBeNull()
    await act(async () => root.render(<WorkingTipsContext.Provider value={false}><WorkingStatus startedAt={now - 300_000} lastOutputAt={now} phase={{ phase: 'running', text: '', since: now }} /></WorkingTipsContext.Provider>))
    expect(host.querySelector('[data-testid="working-word"]')?.textContent).not.toContain('Starting')
    expect(host.textContent).toContain('0s since update')
    await act(async () => root.render(<WorkingTipsContext.Provider value={false}><WorkingStatus startedAt={now - 300_000} lastOutputAt={now} phase={{ phase: 'retrying', text: 'Retrying', since: now - 1_000 }} /></WorkingTipsContext.Provider>))
    expect(host.textContent).toContain('0s since update')
    await act(async () => root.render(<WorkingTipsContext.Provider value={false}><WorkingStatus startedAt={now - 300_000} lastOutputAt={now - 120_000} phase={{ phase: 'quiet', text: 'Still working · no update for 2 min', since: now }} /></WorkingTipsContext.Provider>))
    expect(host.textContent).toContain('Still working · no update for 2 min')
    expect(host.textContent).not.toContain('since update')
  } finally {
    act(() => root.unmount())
    vi.useRealTimers()
  }
})
