import { describe, expect, it } from 'vitest'
import { currentTurnActivity, currentTurnPhase, currentTurnStartedAt, lastAgentOutputAt } from './turnStart'

const at = (iso: string) => Date.parse(iso)
const ev = (type: string, timestamp: string) => ({ type, timestamp })

describe('lastAgentOutputAt', () => {
  const base = [ev('unified_completion', '2026-10-15T10:00:00Z'), ev('user_message', '2026-10-15T10:01:00Z')]

  it('is the turn start while the agent has produced nothing', () => {
    expect(lastAgentOutputAt(base)).toBe(at('2026-10-15T10:01:00Z'))
  })

  it('is the newest output event of the turn, ignoring receipts and the person\'s own messages', () => {
    const events = [...base, ev('streaming_chunk', '2026-10-15T10:01:30Z'), ev('tool_call_start', '2026-10-15T10:02:10Z'), ev('live_input_confirmed', '2026-10-15T10:09:00Z'), ev('user_message', '2026-10-15T10:01:40Z')]
    expect(lastAgentOutputAt(events)).toBe(at('2026-10-15T10:02:10Z'))
  })

  it('does not count output from the previous turn', () => {
    const events = [ev('streaming_chunk', '2026-10-15T09:59:00Z'), ...base]
    expect(lastAgentOutputAt(events)).toBe(at('2026-10-15T10:01:00Z'))
  })

  it('says nothing when the turn start is not proven by the loaded events', () => {
    const noEnd = [ev('user_message', '2026-10-15T10:01:00Z'), ev('streaming_chunk', '2026-10-15T10:01:30Z')]
    expect(currentTurnStartedAt(noEnd)).toBeUndefined()
    expect(lastAgentOutputAt(noEnd)).toBeUndefined()
    expect(lastAgentOutputAt(undefined)).toBeUndefined()
  })
})

describe('currentTurnPhase', () => {
  const phaseEvent = (phase: string, text: string, since: string) => ({ type: 'turn_phase', data: { data: { data: { phase, text, since } } } })
  const user = { type: 'user_message', data: undefined }

  it('is the newest phase the server announced for the turn now running', () => {
    const events = [user, phaseEvent('starting', 'Starting Muse', '2026-10-15T10:01:00Z'), phaseEvent('retrying', 'Retrying: no response in time (attempt 1 of 5, next in 10s)', '2026-10-15T10:02:30Z')]
    expect(currentTurnPhase(events as never)).toEqual({ phase: 'retrying', text: 'Retrying: no response in time (attempt 1 of 5, next in 10s)', since: Date.parse('2026-10-15T10:02:30Z') })
  })

  it('is undefined once the turn has ended, and when the server never said', () => {
    expect(currentTurnPhase([phaseEvent('running', '', '2026-10-15T10:01:00Z'), { type: 'unified_completion', data: undefined }] as never)).toBeUndefined()
    expect(currentTurnPhase([user] as never)).toBeUndefined()
    expect(currentTurnPhase(undefined)).toBeUndefined()
  })
})


describe('currentTurnActivity', () => {
  const activity = { type: 'turn_phase', data: { data: { data: { phase: 'running', since: '2026-10-15T10:02:00Z', turn_started_at: '2026-10-15T10:01:00Z', last_activity_at: '2026-10-15T10:02:30Z' } } } }

  it('restores the server clock even in a truncated event window', () => {
    expect(currentTurnActivity([activity] as never)).toEqual({ turnStartedAt: at('2026-10-15T10:01:00Z'), lastActivityAt: at('2026-10-15T10:02:30Z') })
  })

  it('does not reset for user input or phase-only announcements', () => {
    expect(currentTurnActivity([activity, { type: 'user_message' }, { type: 'turn_phase', data: { data: { phase: 'retrying', since: '2026-10-15T10:04:00Z' } } }] as never)).toEqual(currentTurnActivity([activity] as never))
  })

  it('does not infer a clock from history or keep a completed turn clock', () => {
    expect(currentTurnActivity([ev('unified_completion', '2026-10-15T10:00:00Z'), ev('user_message', '2026-10-15T10:01:00Z')] as never)).toBeUndefined()
    expect(currentTurnActivity([activity, { type: 'unified_completion' }] as never)).toBeUndefined()
    expect(currentTurnActivity(undefined)).toBeUndefined()
  })
})
