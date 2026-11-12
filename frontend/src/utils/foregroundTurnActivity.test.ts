import { expect, it } from 'vitest'
import type { PollingEvent, RuntimeSnapshot } from '../services/api-types'
import { foregroundTurnState } from './foregroundTurnActivity'
import { chatRuntimeActivity } from './chatRuntimeActivity'

// PLAT-746-903: the open page retains a start after the server settles it.
it('settles a retained foreground start only with a server end newer than that start', () => {
  const start: PollingEvent = { id: 'input', type: 'user_message', session_id: 'chat', timestamp: '2026-01-01T10:00:00Z', execution_kind: 'main_agent' }
  const runtime: RuntimeSnapshot = {
    session_id: 'chat', generation: 1, revision: 2, phase: 'completed',
    foreground_turn: { busy: false, has_cancel: false, can_steer: false, synthetic: false },
    background_live: false, terminal_busy: false, waiting_for_user: false,
    started_at: start.timestamp!, last_progress_at: '2026-01-01T10:01:00Z',
    observed_at: '2026-01-01T10:01:00Z', completed_at: '2026-01-01T10:01:00Z',
  }
  expect(foregroundTurnState([start])).toBe('running')
  for (const phase of ['completed', 'failed', 'canceled'] as const) {
    const ended = { ...runtime, phase }
    const foreground = foregroundTurnState([start], ended)
    expect(foreground).toBe('completed')
    expect(chatRuntimeActivity({ isStreaming: true, foregroundTurnCompleted: foreground === 'completed' }).state).toBe('ready')
  }
  // The previous turn's cached terminal snapshot must not clear new input.
  const next = { ...start, id: 'next', timestamp: '2026-01-01T10:02:00Z' }
  expect(foregroundTurnState([start, next], runtime)).toBe('running')
  for (const phase of ['starting', 'running', 'waiting', 'idle'] as const) {
    expect(foregroundTurnState([start], { ...runtime, phase })).toBe('running')
  }
  expect(foregroundTurnState([start], { ...runtime, completed_at: undefined })).toBe('running')
  expect(foregroundTurnState([{ ...start, timestamp: undefined }], runtime)).toBe('running')
  expect(foregroundTurnState([{ ...start, execution_kind: 'background_agent' }], runtime)).toBeUndefined()
})
