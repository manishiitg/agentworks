import { expect, it } from 'vitest'
import type { PollingEvent } from '../services/api-types'
import { groupTranscriptMessages } from './compactMessageGroups'
const item = (id: string, type: string, metadata: Record<string, unknown>) => ({ kind: 'event' as const, key: id, event: { id, session_id: 'chat', type, data: { data: { metadata } } } as PollingEvent })
it('groups only consecutive server-marked interrupted notices and preserves exact anchored rows (PLAT-746-995)', () => {
  const meta = { retained_turn_outcome: 'interrupted', final_response_missing: true }
  const a = item('a', 'unified_completion', meta), b = item('b', 'unified_completion', meta)
  const ordinary = item('person', 'user_message', { content: 'This reply was interrupted' })
  const grouped = groupTranscriptMessages([a, b, ordinary, a])
  expect(grouped.map(row => row.kind)).toEqual(['message_group', 'event', 'event'])
  expect(grouped[0]).toMatchObject({ events: [a.event, b.event] })
  expect(groupTranscriptMessages([a, b], 'b')).toEqual([a, b])
  expect(groupTranscriptMessages([a, item('stop', 'unified_completion', { ...meta, stopped: true })])).toHaveLength(2)
})
it('groups delivered incoming messages by server sender identity, never labels alone or actionable uncertainty', () => {
  const meta = { source: 'agent_message', sender_agent: 'sender-a', sender_user_id: 'owner', sender_label: 'Review', display_content: 'A message', delivery_status: 'sent_to_cli' }
  const a = item('a', 'user_message', meta), b = item('b', 'user_message', meta)
  const rows = groupTranscriptMessages([a, b, item('other', 'user_message', { ...meta, sender_agent: 'sender-b' }), item('uncertain', 'user_message', { ...meta, delivery_status: 'unconfirmed' })])
  expect(rows.map(row => row.kind)).toEqual(['message_group', 'event', 'event'])
  expect(groupTranscriptMessages([item('cold-a', 'user_message', { ...meta, delivery_status: undefined }), item('cold-b', 'user_message', { ...meta, delivery_status: undefined })])).toHaveLength(1)
  expect(a.event.data).toEqual({ data: { metadata: meta } })
  expect(groupTranscriptMessages([a, item('waiting', 'user_message', { ...meta, delivery_status: 'queued_for_turn' })])).toHaveLength(2)
})
