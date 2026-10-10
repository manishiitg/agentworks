import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PollingEvent } from '../services/api-types'
import {
  clientMessageEventId,
  keepUnechoedProvisionals,
  readClientMessageId,
  reconcileDurableUserEchoes,
} from './clientMessageIdentity'
import { applyLiveInputConfirmation, readLiveInputConfirmation } from './liveInputReceipt'

const SESSION = 'client-id-session'

const createMemoryStorage = (): Storage => {
  const values = new Map<string, string>()
  return {
    get length() { return values.size },
    clear: () => values.clear(),
    getItem: key => values.get(key) ?? null,
    key: index => Array.from(values.keys())[index] ?? null,
    removeItem: key => { values.delete(key) },
    setItem: (key, value) => { values.set(key, value) },
  }
}

function userRow(id: string, content: string, metadata: Record<string, unknown> = {}): PollingEvent {
  return {
    id, type: 'user_message', timestamp: '2026-09-23T05:25:08Z', session_id: SESSION,
    data: { data: { content, metadata } },
  } as unknown as PollingEvent
}

const provisional = (clientId: string, content: string, extra: Record<string, unknown> = {}) =>
  userRow(clientMessageEventId(clientId), content, { client_message_id: clientId, provisional: true, ...extra })

const durable = (clientId: string, content: string, extra: Record<string, unknown> = {}) =>
  userRow(clientMessageEventId(clientId), content, { client_message_id: clientId, ...extra })

const answer = (id: string, content: string): PollingEvent => ({
  id, type: 'unified_completion', timestamp: '2026-09-23T05:25:12Z', session_id: SESSION,
  data: { data: { content } },
} as unknown as PollingEvent)

function metadataOf(event: PollingEvent | undefined): Record<string, unknown> {
  return ((event?.data as unknown as { data?: { metadata?: Record<string, unknown> } })?.data?.metadata) ?? {}
}

describe('reconcileDurableUserEchoes', () => {
  it('replaces the provisional bubble and carries its receipt', () => {
    const result = reconcileDurableUserEchoes(
      [answer('a', 'earlier'), provisional('b', 'title?', { queue_position: 1 })],
      [durable('b', 'title?', { message_id: 'steer-b' })],
    )
    expect(result.current.map(event => event.id)).toEqual(['a'])
    expect(result.replacedIds).toEqual(['user:b'])
    expect(metadataOf(result.incoming[0])).toMatchObject({ message_id: 'steer-b', queue_position: 1 })
  })

  it('leaves rows without a client id (bots, auto-notifications, legacy) alone', () => {
    const bot = userRow('turn-bot', 'from slack')
    const result = reconcileDurableUserEchoes([provisional('x', 'hi')], [bot])
    expect(result.current.map(event => event.id)).toEqual(['user:x'])
    expect(result.incoming).toEqual([bot])
    expect(readClientMessageId(bot)).toBe('')
  })
})

describe('keepUnechoedProvisionals', () => {
  it('keeps a queued bubble until its durable row is on the page', () => {
    const page = [durable('a', 'first')]
    const kept = keepUnechoedProvisionals(page, [provisional('a', 'first'), provisional('b', 'second')])
    expect(kept.map(event => event.id)).toEqual(['user:a', 'user:b'])
    expect(keepUnechoedProvisionals([...page, durable('b', 'second')], [provisional('b', 'second')])
      .map(event => event.id)).toEqual(['user:a', 'user:b'])
  })
})

describe('receipts', () => {
  it('match a row by client id', () => {
    const confirmation = readLiveInputConfirmation({
      id: 'steer-b:confirmed', type: 'live_input_confirmed', timestamp: '', session_id: SESSION,
      data: { data: { message_id: 'steer-b', outcome: 'confirmed', metadata: { client_message_id: 'b' } } },
    } as unknown as PollingEvent)
    expect(confirmation?.clientMessageId).toBe('b')
    const upgraded = applyLiveInputConfirmation([provisional('b', 'title?')], confirmation!)
    expect(metadataOf(upgraded[0]).confirmation).toBe('confirmed')
  })
})

describe('store ordering (project-a A/B incident)', () => {
  beforeEach(() => {
    vi.resetModules()
    vi.stubGlobal('localStorage', createMemoryStorage())
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('moves a message sent mid-reply below the reply it waited behind', async () => {
    const { useChatStore } = await import('../stores/useChatStore')
    const store = useChatStore.getState()
    store.setTabEvents(SESSION, [durable('a', 'this ticket <url>')])
    store._addTabEventsImmediate(SESSION, [provisional('b', 'whats the title of this ticket?')])
    store._addTabEventsImmediate(SESSION, [answer('answer-a', 'This is WEB-1681 ...')])
    store._addTabEventsImmediate(SESSION, [durable('b', 'whats the title of this ticket?')])
    store._addTabEventsImmediate(SESSION, [answer('answer-b', 'Centralize shared database migrations in lib-core')])

    const events = useChatStore.getState().getTabEvents(SESSION)
    expect(events.map(event => event.id)).toEqual(['user:a', 'answer-a', 'user:b', 'answer-b'])
    expect(metadataOf(events[2]).provisional).toBeUndefined()
  }, 60000)

  it('keeps two rows for identical text sent twice, and one row for a retried send', async () => {
    const { useChatStore } = await import('../stores/useChatStore')
    const store = useChatStore.getState()
    store.setTabEvents(SESSION, [])
    store._addTabEventsImmediate(SESSION, [provisional('one', 'ok'), provisional('two', 'ok')])
    store._addTabEventsImmediate(SESSION, [durable('one', 'ok')])
    store._addTabEventsImmediate(SESSION, [durable('two', 'ok')])
    store._addTabEventsImmediate(SESSION, [durable('two', 'ok')])
    expect(useChatStore.getState().getTabEvents(SESSION).map(event => event.id)).toEqual(['user:one', 'user:two'])
  }, 60000)
})

describe('pendingQueuedProvisionals', () => {
  it('shows queued messages without a durable row as queued provisional bubbles', async () => {
    const { pendingQueuedProvisionals } = await import('./clientMessageIdentity')
    const present = [{ id: 'user:sub-1', type: 'user_message' }] as never
    const rows = pendingQueuedProvisionals([
      { client_message_id: 'sub-1', content: 'already echoed' },
      { client_message_id: 'sub-2', content: 'still queued', queue_position: 1, queued_at: '2026-09-23T05:00:00Z' },
    ], present, 'chat-1')
    expect(rows).toHaveLength(1)
    const row = rows[0] as unknown as { id: string; session_id: string; data: { data: { content: string; metadata: Record<string, unknown> } } }
    expect(row.id).toBe('user:sub-2')
    expect(row.session_id).toBe('chat-1')
    expect(row.data.data.content).toBe('still queued')
    expect(row.data.data.metadata).toMatchObject({ client_message_id: 'sub-2', provisional: true, delivery_status: 'queued_for_turn', queue_position: 1 })
  })
})
