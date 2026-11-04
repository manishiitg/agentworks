import { beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  setTabEvents: vi.fn(),
  setTabLastEventIndex: vi.fn(),
  setTabHasMoreOlderEvents: vi.fn(),
  setTabHistoryPagination: vi.fn(),
  getTabEvents: vi.fn(),
  getRecentChatEvents: vi.fn(),
}))

vi.mock('../stores/useChatStore', () => ({
  useChatStore: { getState: () => ({
    setTabEvents: mocks.setTabEvents,
    setTabLastEventIndex: mocks.setTabLastEventIndex,
    setTabHasMoreOlderEvents: mocks.setTabHasMoreOlderEvents,
    setTabHistoryPagination: mocks.setTabHistoryPagination,
    getTabEvents: mocks.getTabEvents,
    markTabHistoryLoaded: () => {},
    chatTabs: {},
  }) },
}))
vi.mock('../stores/useModeStore', () => ({ useModeStore: { getState: () => ({ setModeCategory: vi.fn() }) } }))
vi.mock('../services/api', () => ({ agentApi: { getRecentChatEvents: mocks.getRecentChatEvents } }))

import { conversationToRestoredEvents } from '../../shared/session/restore'
import { hydrateTabEvents } from './sessionRestore'

describe('hydrateTabEvents SQLite source', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    mocks.getTabEvents.mockReturnValue([])
  })

  it('renders the bounded durable page and stores its stable sequence cursor', async () => {
    const events = [
      { id: 'user-1', type: 'user_message', sequence: 41 },
      { id: 'reply-1', type: 'streaming_chunk', sequence: 42 },
    ]
    mocks.getRecentChatEvents.mockResolvedValue({
      events,
      session_status: 'completed',
      oldest_sequence: 41,
      latest_sequence: 42,
      last_processed_index: 42,
      has_more: true,
    })

    const runtime = await hydrateTabEvents('chat-1', { workspacePath: 'Workflow/demo' })

    expect(mocks.getRecentChatEvents).toHaveBeenCalledWith('chat-1', 'Workflow/demo')
    expect(mocks.setTabEvents).toHaveBeenCalledWith('chat-1', events)
    expect(mocks.setTabLastEventIndex).toHaveBeenCalledWith('chat-1', 42)
    expect(mocks.setTabHistoryPagination).toHaveBeenCalledWith('chat-1', { hasMore: true, nextOffset: 41 })
    expect(runtime.restoredEvents).toEqual(events)
  })

  it('keeps a provisional bubble until SQLite has its row, then uses the durable row', async () => {
    const provisional = {
      id: 'user:sub-1', type: 'user_message',
      data: { data: { content: 'hello', metadata: { client_message_id: 'sub-1', provisional: true, message_id: 'm-1' } } },
    }
    mocks.getTabEvents.mockReturnValue([provisional])
    mocks.getRecentChatEvents.mockResolvedValue({ events: [], session_status: 'running', last_processed_index: 0, has_more: false })

    await hydrateTabEvents('chat-1')
    expect(mocks.setTabEvents).toHaveBeenLastCalledWith('chat-1', [provisional])

    const durable = {
      id: 'user:sub-1', type: 'user_message', sequence: 9,
      data: { data: { content: 'hello', metadata: { client_message_id: 'sub-1', message_id: 'm-1' } } },
    }
    mocks.getRecentChatEvents.mockResolvedValue({ events: [durable], session_status: 'running', last_processed_index: 9, has_more: false })

    await hydrateTabEvents('chat-1')
    expect(mocks.setTabEvents).toHaveBeenLastCalledWith('chat-1', [durable])
  })


  it('shows messages still waiting in the server queue after a reload', async () => {
    mocks.getRecentChatEvents.mockResolvedValue({
      events: [{ id: 'user:sub-1', type: 'user_message', sequence: 4 }],
      session_status: 'running', last_processed_index: 4, has_more: false,
      pending_messages: [{ client_message_id: 'sub-2', content: 'next question', queue_position: 1 }],
    })

    await hydrateTabEvents('chat-1')

    const rows = mocks.setTabEvents.mock.lastCall?.[1] as Array<{ id: string }>
    expect(rows.map(row => row.id)).toEqual(['user:sub-1', 'user:sub-2'])
  })

  it('restores a never-used chat (404) as an empty conversation instead of failing', async () => {
    mocks.getRecentChatEvents.mockRejectedValue(
      Object.assign(new Error('Not Found'), { isAxiosError: true, response: { status: 404 } }),
    )

    const runtime = await hydrateTabEvents('fresh-chat')

    expect(runtime.status).toBe('inactive')
    expect(runtime.restoredEvents).toEqual([])
    expect(mocks.setTabHasMoreOlderEvents).toHaveBeenCalledWith('fresh-chat', false)
    expect(mocks.setTabHistoryPagination).toHaveBeenCalledWith('fresh-chat', null)
  })

  it('still surfaces other restore failures', async () => {
    mocks.getRecentChatEvents.mockRejectedValue(
      Object.assign(new Error('Server Error'), { isAxiosError: true, response: { status: 500 } }),
    )

    await expect(hydrateTabEvents('chat-1')).rejects.toThrow('Server Error')
  })
})

describe('legacy JSON diagnostic converter', () => {
  it('keeps meaningful assistant updates but hides persisted tool markers', () => {
    const events = conversationToRestoredEvents({
      session_id: 'legacy',
      conversation_history: [
        { Role: 'human', Parts: [{ Text: 'Inspect it' }] },
        { Role: 'ai', Parts: [{ Text: 'I am checking.' }] },
        { Role: 'ai', Parts: [{ Text: '[Previous tool call: exec({})]' }] },
        { Role: 'ai', Parts: [{ Text: 'It is fixed.' }] },
      ],
    })
    expect(JSON.stringify(events)).toContain('I am checking.')
    expect(JSON.stringify(events)).toContain('It is fixed.')
    expect(JSON.stringify(events)).not.toContain('Previous tool call')
  })
})
