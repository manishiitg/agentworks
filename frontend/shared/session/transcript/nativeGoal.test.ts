import { expect, it } from 'vitest'
import type { PollingEvent } from '../types'
import { collapseNativeGoals, nativeGoalUpdate } from './nativeGoal'

it('retains one goal identity across turns, ignores malformed frames and honors native clear (PLAT-746-988)', () => {
  const goal = { objective: 'Finish the task', status: 'active', tokens_used: 42 }
  const frame = (id: string, value: unknown, session = 'chat') => ({ id, session_id: session, type: 'native_goal_updated', data: { data: { update: { provider: 'codex-cli', native_session_id: 'thread', goal: value } } } }) as PollingEvent
  const rows = collapseNativeGoals([frame('first', goal), { id: 'user', type: 'user_message' } as PollingEvent, frame('next', { ...goal, status: 'paused' }), frame('invalid', {}), { ...frame('child-clear', null), parent_execution_id: 'parent-turn', execution_kind: 'background_agent' }, frame('other', goal, 'other-chat')])
  expect(rows.filter(row => row.type === 'native_goal_updated').map(row => row.id)).toEqual(['native-goal:chat', 'native-goal:other-chat'])
  expect(nativeGoalUpdate(rows[0])?.goal?.status).toBe('paused')
  expect(collapseNativeGoals([...rows, frame('clear', null)]).filter(row => row.type === 'native_goal_updated').map(row => row.id)).toEqual(['native-goal:other-chat'])
})

it('reads the restored generic event envelope without changing row identity', () => {
  const event = { id: 'snapshot', session_id: 'chat', type: 'native_goal_updated', data: { data: { data: { update: { provider: 'codex-cli', native_session_id: 'thread', goal: { objective: 'Task', status: 'paused' } } } } } } as PollingEvent
  expect(collapseNativeGoals([event])[0].id).toBe('native-goal:chat')
  expect(nativeGoalUpdate(event)?.goal?.status).toBe('paused')
})
