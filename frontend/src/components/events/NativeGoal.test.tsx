import { fireEvent, render, screen } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { NativeGoalRow } from './NativeGoal'
import type { PollingEvent } from '../../services/api-types'

it('makes native continuation pause visible and resumes through the chat command, without inventing counters (PLAT-746-988)', () => {
  const send = vi.fn()
  const event = { id: 'goal', type: 'native_goal_updated', data: { update: { provider: 'codex-cli', native_session_id: 'thread', reason: 'native_continuation_not_started', goal: { objective: '<script>not markup</script>', status: 'paused' } } } } as PollingEvent
  render(<NativeGoalRow event={event} onSendMessage={send} />)
  expect(screen.getByRole('status').textContent).toBe('Paused: Codex did not continue')
  expect(screen.getByText('<script>not markup</script>')).toBeTruthy()
  expect(screen.getByText('Tokens: — · Budget: — · Time: —')).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: 'Resume' }))
  expect(send).toHaveBeenCalledWith('/goal resume')
})
