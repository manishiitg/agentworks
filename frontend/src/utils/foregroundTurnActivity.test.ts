import { describe, expect, it } from 'vitest'
import type { PollingEvent } from '../services/api-types'
import { foregroundTurnCompleted, isForegroundTurnCompletion } from './foregroundTurnActivity'

function event(type: string, fields: Partial<PollingEvent> = {}, data = {}): PollingEvent {
  return { id: type, type, timestamp: '2026-10-01T07:00:00Z', data: { type, data }, ...fields } as PollingEvent
}

describe('latest foreground turn completion', () => {
  it.each(['llm_generation_end', 'unified_completion', 'agent_end', 'conversation_end', 'conversation_error', 'context_cancelled'])('settles on %s despite trailing telemetry', type => {
    expect(foregroundTurnCompleted([event('user_message'), event(type), event('token_usage')])).toBe(true)
  })
  it.each(['user_message', 'conversation_start', 'llm_generation_start', 'streaming_start'])('resets for a new %s', type => {
    expect(foregroundTurnCompleted([event('agent_end'), event(type)])).toBe(false)
  })
  it.each([
    { component: 'delegation-child' },
    { correlation_id: 'workshop-step' },
    { execution_kind: 'workflow_step', execution_id: 'workflow-step:a' },
    { execution_kind: 'background_agent', execution_id: 'background:a' },
  ])('does not let a child completion settle the main turn', fields => {
    expect(foregroundTurnCompleted([event('user_message'), event('agent_end', fields)])).toBe(false)
    expect(foregroundTurnCompleted([event('agent_end'), event('conversation_start', fields)])).toBe(true)
  })
  it('ignores child identity nested in payload metadata', () => {
    const child = event('llm_generation_end', {}, { metadata: { parent_execution_id: 'workflow-step:a' } })
    expect(isForegroundTurnCompletion(child)).toBe(false)
  })
  it('ignores restored intermediate narration', () => {
    expect(foregroundTurnCompleted([event('user_message'), event('llm_generation_end', {}, { restored_intermediate_update: true })])).toBe(false)
  })
  it('does not infer completion from assistant text or an empty history', () => {
    expect(foregroundTurnCompleted()).toBe(false)
    expect(foregroundTurnCompleted([event('streaming_chunk')])).toBe(false)
  })
})
