// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { TerminalLiveDialog } from './TerminalLiveDialog'
import { teamRequest } from './teamApi'

vi.mock('./teamApi', () => ({ teamRequest: vi.fn(), teamFailure: (error: Error) => ({ message: error.message }) }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// PLAT-746-1030: a current server refusal must remove a previously shown native
// conversation; closing the viewer must stop further reads.
it('drops terminal text when Live access is revoked and stops reading on close', async () => {
  vi.useFakeTimers()
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
  vi.mocked(teamRequest).mockResolvedValueOnce({ messages: [{ Role: 'assistant', Parts: [{ Text: 'Native private sentinel' }] }] }).mockRejectedValue(new Error('This conversation is private.'))
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  let mounted = true
  try {
    await act(async () => root.render(<TerminalLiveDialog target={{ project: 'example' }} agent={{ id: 'terminal', name: 'Helper' }} close={() => {}} />))
    expect(document.body.textContent).toContain('Native private sentinel')
    await act(async () => vi.advanceTimersByTimeAsync(5000))
    expect(document.body.textContent).not.toContain('Native private sentinel')
    expect(document.querySelector('[role="alert"]')?.textContent).toBe('This conversation is private.')
    await act(async () => root.unmount())
    mounted = false
    const calls = vi.mocked(teamRequest).mock.calls.length
    await act(async () => vi.advanceTimersByTimeAsync(10000))
    expect(vi.mocked(teamRequest).mock.calls.length).toBe(calls)
  } finally {
    if (mounted) await act(async () => root.unmount())
    host.remove()
    vi.useRealTimers()
  }
})
