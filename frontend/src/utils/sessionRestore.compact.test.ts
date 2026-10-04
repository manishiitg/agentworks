import { beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  setTabEvents: vi.fn(),
  setTabLastEventIndex: vi.fn(),
  setTabHasMoreOlderEvents: vi.fn(),
  setTabHistoryPagination: vi.fn(),
  getTabEvents: vi.fn(),
  getRecentChatEvents: vi.fn(),
  chatTabs: {} as Record<string, { tabId: string; sessionId: string; metadata?: Record<string, unknown> }>,
}))

vi.mock('../stores/useChatStore', () => ({
  useChatStore: { getState: () => ({
    chatTabs: mocks.chatTabs,
    setTabEvents: mocks.setTabEvents,
    setTabLastEventIndex: mocks.setTabLastEventIndex,
    setTabHasMoreOlderEvents: mocks.setTabHasMoreOlderEvents,
    setTabHistoryPagination: mocks.setTabHistoryPagination,
    getTabEvents: mocks.getTabEvents,
  }) },
}))
vi.mock('../stores/useModeStore', () => ({ useModeStore: { getState: () => ({ setModeCategory: vi.fn() }) } }))
vi.mock('../services/api', () => ({ agentApi: { getRecentChatEvents: mocks.getRecentChatEvents } }))

import type { PollingEvent } from '../services/api-types'
import { buildTranscriptItems } from './terminalEventTranscript'
import { hydrateTabEvents } from './sessionRestore'

const page = { events: [], session_status: 'completed', oldest_sequence: 41, latest_sequence: 42, last_processed_index: 42, has_more: true }

function mk(id: string, type: string, data: Record<string, unknown>): PollingEvent {
  return {
    id, type, session_id: 's1', execution_kind: 'main_agent', execution_id: 'main:s1',
    timestamp: '2026-09-03T00:00:00Z', data: { data },
  } as unknown as PollingEvent
}
const toolKinds = (events: PollingEvent[]) => buildTranscriptItems(events).filter(item => item.kind === 'tools').length

describe('compact chat restore opt-in', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    for (const key of Object.keys(mocks.chatTabs)) delete mocks.chatTabs[key]
    mocks.getTabEvents.mockReturnValue([])
    mocks.getRecentChatEvents.mockResolvedValue(page)
  })

  it('an interactive chat tab restores compactly and remembers it for paging', async () => {
    mocks.chatTabs.t1 = { tabId: 't1', sessionId: 'chat-1', metadata: {} }
    await hydrateTabEvents('chat-1', { workspacePath: 'w' })
    expect(mocks.getRecentChatEvents).toHaveBeenCalledWith('chat-1', 'w', true)
    expect(mocks.setTabHistoryPagination).toHaveBeenCalledWith('chat-1', { hasMore: true, nextOffset: 41, compact: true })
  })

  it.each(['isViewOnly', 'isExecutionRun', 'isBotRun'])('a %s tab keeps the full durable page', async (flag) => {
    mocks.chatTabs.t1 = { tabId: 't1', sessionId: 'chat-1', metadata: { [flag]: true } }
    await hydrateTabEvents('chat-1', { workspacePath: 'w' })
    expect(mocks.getRecentChatEvents).toHaveBeenCalledWith('chat-1', 'w')
    expect(mocks.setTabHistoryPagination).toHaveBeenCalledWith('chat-1', { hasMore: true, nextOffset: 41 })
  })

  it('an unknown tab and an explicit compact:false keep the full page', async () => {
    await hydrateTabEvents('nobody', {})
    expect(mocks.getRecentChatEvents).toHaveBeenLastCalledWith('nobody', undefined)
    mocks.chatTabs.t1 = { tabId: 't1', sessionId: 'chat-1', metadata: {} }
    await hydrateTabEvents('chat-1', { compact: false })
    expect(mocks.getRecentChatEvents).toHaveBeenLastCalledWith('chat-1', undefined)
  })
})

describe('transcript over a compact restore', () => {
  const older = [
    mk('u1', 'user_message', { content: 'first' }),
    mk('a1', 'unified_completion', { final_result: 'first answer' }),
  ]
  const latest = [
    mk('u2', 'user_message', { content: 'second' }),
    mk('t-s', 'tool_call_start', { tool_call_id: 'c1', tool_name: 'read_file' }),
    mk('t-e', 'tool_call_end', { tool_call_id: 'c1', tool_name: 'read_file' }),
  ]

  it('older turns show no tool batch; the latest turn keeps its tools', () => {
    expect(toolKinds(older)).toBe(0)
    expect(toolKinds([...older, ...latest])).toBe(1)
  })

  it('a turn that finishes and a turn that starts after the restore render without giving older turns tools', () => {
    const finished = [...older, ...latest, mk('a2', 'unified_completion', { final_result: 'second answer' })]
    expect(toolKinds(finished)).toBe(1)
    const next = [
      ...finished,
      mk('u3', 'user_message', { content: 'third' }),
      mk('t3-s', 'tool_call_start', { tool_call_id: 'c3', tool_name: 'grep' }),
      mk('t3-e', 'tool_call_end', { tool_call_id: 'c3', tool_name: 'grep' }),
    ]
    const items = buildTranscriptItems(next)
    expect(items.filter(item => item.kind === 'tools').length).toBe(2)
    const firstTurnItems = items.slice(0, items.findIndex(item => item.kind === 'event' && item.event.id === 'u2'))
    expect(firstTurnItems.some(item => item.kind === 'tools')).toBe(false)
  })
})
