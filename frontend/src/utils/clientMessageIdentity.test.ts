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
