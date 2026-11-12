// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { QueuedWaitingAge } from './QueuedMessageControls'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
it('formats only server-provided waiting age and updates as time passes', async () => {
  vi.useFakeTimers()
  const host = document.createElement('div')
  const root = createRoot(host)
  vi.setSystemTime(new Date('2026-01-01T12:40:00Z'))
  try {
    await act(async () => root.render(<QueuedWaitingAge />))
    expect(host.textContent).toBe('')
    await act(async () => root.render(<QueuedWaitingAge since="2026-01-01T12:00:00Z" />))
    expect(host.textContent).toBe('Waiting 40 min')
    await act(async () => vi.advanceTimersByTime(60_000))
    expect(host.textContent).toBe('Waiting 41 min')
  } finally { await act(async () => root.unmount()); vi.useRealTimers() }
})
