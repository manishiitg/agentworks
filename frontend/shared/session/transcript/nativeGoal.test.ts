import { expect, it } from 'vitest'
import type { PollingEvent } from '../types'
import { collapseNativeGoals, nativeGoalUpdate } from './nativeGoal'

it('retains one goal identity across turns, ignores malformed frames and honors native clear (PLAT-746-988)', () => {
  const goal = { objective: 'Finish the task', status: 'active', tokens_used: 42 }
  const frame = (id: string, value: unknown, session = 'chat') => ({ id, session_id: session, type: 'native_goal_updated', data: { data: { update: { provider: 'codex-cli', native_session_id: 'thread', goal: value } } } }) as PollingEvent
  const rows = collapseNativeGoals([frame('first', goal), { id: 'user', type: 'user_message' } as PollingEvent, frame('next', { ...goal, status: 'paused' }), frame('invalid', {}), frame('other', goal, 'other-chat')])
  expect(rows.filter(row => row.type === 'native_goal_updated').map(row => row.id)).toEqual(['first', 'other'])
  expect(nativeGoalUpdate(rows[0])?.goal?.status).toBe('paused')
  expect(collapseNativeGoals([...rows, frame('clear', null)]).filter(row => row.type === 'native_goal_updated').map(row => row.id)).toEqual(['other'])
})
