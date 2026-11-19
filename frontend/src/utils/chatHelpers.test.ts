import { describe, expect, it } from 'vitest'
import type { PollingEvent } from '../services/api-types'
import { collectChatHelpers, linkedChatHelpers, mergeHelperEvents, orderChatHelpers } from './chatHelpers'

let sequence = 0
const event = (type: string, fields: Record<string, unknown>, timestamp = '2026-11-19T10:00:00Z'): PollingEvent => ({
  id: `e${++sequence}`, type, sequence, session_id: 'chat', timestamp, data: { type, data: fields },
} as PollingEvent)
const helper = (record: Record<string, unknown>) => event('coding_agent_native_activity', { record: { kind: 'helper', provider: 'claude-code', native_session_id: 'n1', ...record } })

describe('collectChatHelpers (#3245)', () => {
  it('lists Claude, Codex and delegated helpers with their state', () => {
    const helpers = collectChatHelpers([
      helper({ task_id: 't1', tool_id: 'toolu_1', origin: 'sdk_helper', state: 'started', name: 'Agent', helper_description: 'Explore checkout code', current_step: 'Reading files', started_at: '2026-11-19T10:00:00Z' }),
      helper({ task_id: 't2', tool_id: 'toolu_2', state: 'completed', helper_description: 'Find coupon code', started_at: '2026-11-19T09:00:00Z', ended_at: '2026-11-19T09:02:00Z' }),
      event('tool_call_start', { tool_call_id: 'call-1', tool_name: 'spawnAgent', tool_params: { arguments: '{"prompt":"Write the new tests"}' } }),
      event('tool_call_start', { tool_call_id: 'd1', tool_name: 'mcp__llm-provider__delegate_coding_agent', tool_params: { arguments: '{"provider":"agy-cli","task":"Review the design","model":"auto"}' } }),
      event('tool_call_end', { tool_call_id: 'd1', tool_name: 'mcp__llm-provider__delegate_coding_agent', result: '{"job_id":"job-7","provider":"agy-cli","status":"queued"}' }),
      event('tool_call_start', { tool_call_id: 'g1', tool_name: 'mcp__llm-provider__get_coding_agent_job', tool_params: { arguments: '{"job_id":"job-7"}' } }),
      event('tool_call_end', { tool_call_id: 'g1', tool_name: 'mcp__llm-provider__get_coding_agent_job', result: '{"job_id":"job-7","status":"completed","result":"Two differences found.","elapsed_seconds":45}' }),
    ])
    expect(helpers.map(item => [item.provider, item.name, item.state])).toEqual([
      ['claude-code', 'Explore checkout code', 'running'],
      ['claude-code', 'Find coupon code', 'done'],
      ['codex-cli', 'Write the new tests', 'unknown'],
      ['agy-cli', 'Review the design', 'done'],
    ])
    expect(helpers[0]).toMatchObject({ activity: 'Reading files', toolCallId: 'toolu_1', stopId: 'claude-task:t1' })
    expect(helpers[1].stopId).toBeUndefined()
    expect(helpers[2]).toMatchObject({ toolCallId: 'call-1', stateFromServer: true })
    expect(helpers[3]).toMatchObject({ delegated: true, jobId: 'job-7', result: 'Two differences found.', durationMs: 45000 })
    expect(helpers[3].toolCallId).toBeUndefined()
    const { running, finished } = orderChatHelpers(helpers)
    expect(running.map(item => item.name)).toEqual(['Explore checkout code'])
    expect(finished).toHaveLength(3)
  })

  it('keeps one row per helper and a late start does not undo its finish', () => {
    const helpers = collectChatHelpers([
      helper({ task_id: 't1', tool_id: 'toolu_1', state: 'started', helper_description: 'Explore' }),
      helper({ task_id: 't1', tool_id: 'toolu_1', state: 'completed', helper_description: 'Explore', duration_ms: 9000 }),
      helper({ task_id: 't1', tool_id: 'toolu_1', state: 'started', helper_description: 'Explore' }),
    ])
    expect(helpers).toHaveLength(1)
    expect(helpers[0]).toMatchObject({ state: 'done', durationMs: 9000 })
  })

  it('skips a quick foreground helper, as the chat does', () => {
    expect(collectChatHelpers([helper({ task_id: 't9', state: 'completed', helper_mode: 'foreground', duration_ms: 1200 })])).toEqual([])
  })

  it('marks a failed delegation as failed with its error', () => {
    const helpers = collectChatHelpers([
      event('tool_call_start', { tool_call_id: 'd2', tool_name: 'mcp__llm-provider__delegate_coding_agent', tool_params: { arguments: '{"provider":"codex-cli","task":"Update the form"}' } }),
      event('tool_call_error', { tool_call_id: 'd2', tool_name: 'mcp__llm-provider__delegate_coding_agent', error: 'provider not installed' }),
    ])
    expect(helpers[0]).toMatchObject({ provider: 'codex-cli', state: 'failed', result: 'provider not installed' })
  })

  it('keeps helpers the live window dropped, each once, from the saved list', () => {
    const early = helper({ task_id: 't1', state: 'completed', helper_description: 'Early helper', duration_ms: 9000 })
    const spawn = event('tool_call_start', { tool_call_id: 'call-9', tool_name: 'spawnAgent', tool_params: { arguments: '{"prompt":"Write the tests"}' } })
    const late = helper({ task_id: 't2', state: 'started', helper_description: 'Late helper' })
    // The live window holds only the newest events; the saved list holds the spawn too.
    const helpers = collectChatHelpers(mergeHelperEvents([early, spawn], [spawn, late]))
    expect(helpers.map(item => item.name)).toEqual(['Early helper', 'Late helper', 'Write the tests'])
    expect(mergeHelperEvents([], [late])).toEqual([late])
  })

  it('lists tasks handed to other tabs as helpers in their app', () => {
    const base = { inbox_id: 'i', task: 'Write the new tests', sent_at: '2026-11-19T10:00:00Z', agent: 'Codex tests', chat_key: 'crew:chat:codex', session_id: 's2', provider: 'codex-cli' }
    const [working, finished, waiting] = linkedChatHelpers([
      { ...base, message_id: 'm1', title: 'Write the new tests', state: 'in_progress', running: true },
      { ...base, message_id: 'm2', title: 'Check the styles', state: 'done', reply: 'Done: no issues.', reply_at: '2026-11-19T10:05:00Z', running: false },
      { ...base, message_id: 'm3', title: 'Pick a colour', state: 'needs_you', reply: 'Which brand colour?', running: false },
    ])
    expect(working).toMatchObject({ key: 'tab:m1', provider: 'codex-cli', state: 'running', activity: 'Working in "Codex tests"', linkedChat: { key: 'crew:chat:codex', sessionId: 's2', running: true } })
    expect(finished).toMatchObject({ state: 'done', result: 'Done: no issues.' })
    expect(finished.endedAt).toBe(Date.parse('2026-11-19T10:05:00Z'))
    expect(waiting).toMatchObject({ state: 'waiting', result: 'Which brand colour?' })
  })
})
