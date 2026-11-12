// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { QueuedWaitingAge, QueuedInboxStatus, QueuedMessageNotice } from './QueuedMessageControls'

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

// Native uncertainty is a durable server verdict, not a browser retry decision.
it('shows an unconfirmed incoming steer without queue controls or a spinner', async () => {
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    const metadata = { source: 'agent_message', delivery_status: 'unconfirmed' }
    await act(async () => root.render(<><QueuedInboxStatus metadata={metadata} /><QueuedMessageNotice metadata={metadata} /></>))
    expect(host.querySelector('[aria-label="Delivery unconfirmed"]')).not.toBeNull()
    expect(host.textContent).toContain('will not be sent again automatically')
    expect(host.querySelector('button')).toBeNull()
  } finally { await act(async () => root.unmount()) }
})
