// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { PinnedGoalBar } from './PinnedGoalBar'
import type { PollingEvent } from '../../services/api-types'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const goalEvent = (id: string, goal: Record<string, unknown> | null) => ({ id, type: 'native_goal_updated', data: { update: { provider: 'codex-cli', native_session_id: 'thread', goal } } }) as PollingEvent

// PLAT-746-1036: the goal is pinned above the chat from the newest server goal update, shows its state and reason, and its
// buttons send the same /goal commands a person types; with no controls offered (not the owner) it is only shown.
it('pins the newest goal, shows the blocked reason, sends goal commands and disappears when cleared', async () => {
  const send = vi.fn()
  const host = document.createElement('div')
  const root = createRoot(host)
  const events = [goalEvent('1', { objective: 'old goal', status: 'active' }), goalEvent('2', { objective: 'ship the release', status: 'blocked', last_check: 'Needs a review', tokens_used: 4210, time_used_seconds: 125 })]
  try {
    await act(async () => root.render(<PinnedGoalBar events={events} onCommand={send} />))
    expect(host.querySelector('[role="status"]')?.textContent).toBe('Blocked')
    expect(host.textContent).toContain('ship the release')
    expect(host.textContent).not.toContain('old goal')
    expect(host.textContent).toContain('Needs a review')
    expect(host.textContent).toContain('2m 5s · 4,210 tokens')
    const buttons = [...host.querySelectorAll('button')].map(button => button.textContent)
    expect(buttons).toEqual(['ship the release', 'Resume', 'Clear'])
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Resume')!.click())
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Clear')!.click())
    expect(send.mock.calls).toEqual([['/goal resume'], ['/goal clear']])

    await act(async () => root.render(<PinnedGoalBar events={[goalEvent('3', { objective: 'x', status: 'active' })]} />))
    expect([...host.querySelectorAll('button')].map(button => button.textContent)).toEqual(['x'])
    await act(async () => root.render(<PinnedGoalBar events={[...events, goalEvent('4', null)]} onCommand={send} />))
    expect(host.querySelector('section')).toBeNull()
  } finally { await act(async () => root.unmount()) }
})
