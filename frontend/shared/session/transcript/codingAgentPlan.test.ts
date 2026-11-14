import { describe, expect, it } from 'vitest'
import { buildTranscriptItems } from './terminalEventTranscript'
import { codingAgentPlan } from './codingAgentPlan'
import type { PollingEvent } from '../types'

function plan(id: string, turn: string, status: string, text = 'Inspect code'): PollingEvent {
  return { id, type: 'coding_agent_plan', session_id: 'chat', timestamp: '2026-11-14T00:00:00Z', data: {
    turn_id: turn, data: { provider: 'codex-cli', plan_id: `native-${id}`, items: [{ id: 'step-1', text, status }] },
  } } as PollingEvent
}

describe('native CLI plan projection (PLAT-746)', () => {
  it('replaces in place across tool calls, preserves done state after the reply, and separates turns', () => {
    const reply = { id: 'reply', type: 'conversation_response', session_id: 'chat', data: { content: 'Done' } } as PollingEvent
    const rows = buildTranscriptItems([plan('first', 'turn1', 'running'), reply, plan('last', 'turn1', 'completed'), plan('next', 'turn2', 'pending')])
      .filter(row => row.kind === 'event' && row.event.type === 'coding_agent_plan')
    expect(rows).toHaveLength(2)
    expect(rows[0].key).toBe('first')
    if (rows[0].kind === 'event') expect(codingAgentPlan(rows[0].event)?.items[0].status).toBe('completed')
    expect(rows[1].key).toBe('next')
  })

  it('ignores partial/malformed snapshots, clears only on an explicit empty list, and never reads prose', () => {
    const good = plan('first', 'turn1', 'completed')
    const bad = plan('bad', 'turn1', 'invented')
    const rows = buildTranscriptItems([good, bad])
    const saved = rows.find(row => row.kind === 'event' && row.event.type === 'coding_agent_plan')
    expect(saved?.key).toBe('first')
    const empty = { ...good, id: 'clear', data: { turn_id: 'turn1', plan_id: 'native', items: [] } } as PollingEvent
    expect(buildTranscriptItems([good, empty]).filter(row => row.kind === 'event' && row.event.type === 'coding_agent_plan')).toHaveLength(0)
    expect(codingAgentPlan({ ...good, type: 'conversation_response', data: { content: '- [x] Invented status' } } as PollingEvent)).toBeUndefined()
  })
})
