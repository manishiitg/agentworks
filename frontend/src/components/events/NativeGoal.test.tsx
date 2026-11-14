// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { NativeGoalRow } from './NativeGoal'
import type { PollingEvent } from '../../services/api-types'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
it('makes native continuation pause visible and resumes through the chat command, without inventing counters (PLAT-746-988)', async () => {
  const send = vi.fn()
  const event = { id: 'goal', type: 'native_goal_updated', data: { update: { provider: 'codex-cli', native_session_id: 'thread', reason: 'native_continuation_not_started', goal: { objective: '<script>not markup</script>', status: 'paused' } } } } as PollingEvent
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    await act(async () => root.render(<NativeGoalRow event={event} onSendMessage={send} />))
    expect(host.querySelector('[role="status"]')?.textContent).toBe('Paused: Codex did not continue')
    expect(host.textContent).toContain('<script>not markup</script>')
    expect(host.querySelector('script')).toBeNull()
    expect(host.textContent).toContain('Tokens: — · Budget: — · Time: —')
    await act(async () => host.querySelector('button')!.click())
    expect(send).toHaveBeenCalledWith('/goal resume')
  } finally { await act(async () => root.unmount()) }
})
