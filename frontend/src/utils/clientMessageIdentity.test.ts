import { expect, it } from 'vitest'
import type { PollingEvent } from '../services/api-types'
import { keepUnechoedProvisionals, pendingQueuedProvisionals, reconcileDurableUserEchoes } from './clientMessageIdentity'

it('reconciles all provisional source messages with one server combined echo live and on restore', () => {
  const pending = ['one', 'two', 'three'].map(id => ({ client_message_id: id, content: id, can_manage: true }))
  const original = pendingQueuedProvisionals(pending, [], 'chat')
  const echo = { id: 'user:one', type: 'user_message', sequence: 10, data: { type: 'user_message', data: {
    content: 'combined', metadata: { source: 'queued_batch', client_message_id: 'one', combined_client_message_ids: ['one', 'two', 'three'] },
  } } } as PollingEvent
  const live = reconcileDurableUserEchoes(original, [echo])
  expect(live.current).toEqual([])
  expect(live.replacedIds).toHaveLength(3)
  expect(live.incoming).toHaveLength(1)
  expect(keepUnechoedProvisionals([echo], original)).toEqual([echo])
  expect(pendingQueuedProvisionals(pending, [echo], 'chat')).toEqual([])
})

it('does not float confirmed or older-page-echoed provisional bubbles below later replies (#4300)', () => {
  const confirmedProvisional = {
    id: 'user:sub-1',
    type: 'user_message',
    timestamp: '2026-11-26T16:22:37.548Z',
    data: {
      type: 'user_message',
      data: {
        content: 'mockup is good',
        metadata: { client_message_id: 'sub-1', provisional: true, confirmation: 'confirmed' },
      },
    },
  } as PollingEvent
  const stillQueuedProvisional = {
    id: 'user:sub-2',
    type: 'user_message',
    timestamp: '2026-11-26T16:25:00.000Z',
    data: {
      type: 'user_message',
      data: {
        content: 'still queued',
        metadata: { client_message_id: 'sub-2', provisional: true, delivery_status: 'queued_for_turn' },
      },
    },
  } as PollingEvent
  const echoedInOlder = {
    id: 'user:sub-old',
    type: 'user_message',
    timestamp: '2026-11-26T16:20:00.000Z',
    data: {
      type: 'user_message',
      data: {
        content: 'older',
        metadata: { client_message_id: 'sub-old', provisional: true },
      },
    },
  } as PollingEvent
  const olderDurable = {
    id: 'user:sub-old',
    type: 'user_message',
    sequence: 5,
    timestamp: '2026-11-26T16:20:01.000Z',
    data: { type: 'user_message', data: { content: 'older', metadata: { client_message_id: 'sub-old' } } },
  } as PollingEvent
  const laterReply = {
    id: 'turn-complete',
    type: 'unified_completion',
    sequence: 20,
    timestamp: '2026-11-26T16:23:30.000Z',
    data: { type: 'unified_completion', data: { final_result: 'done' } },
  } as PollingEvent

  expect(keepUnechoedProvisionals([laterReply], [confirmedProvisional, echoedInOlder, stillQueuedProvisional], [olderDurable]))
    .toEqual([laterReply, stillQueuedProvisional])
})
