// @vitest-environment happy-dom
import { beforeEach, expect, it, vi } from 'vitest'
import type { PollingEvent } from '../services/api-types'

const api = vi.hoisted(() => ({ recent: vi.fn() }))
vi.mock('../services/api', () => ({ agentApi: { getRecentChatEvents: api.recent } }))

import { useChatStore } from '../stores/useChatStore'
import { hydrateTabEvents } from './sessionRestore'

const session = 'navigation-history'
function row(sequence: number, type = 'user_message', content = `Message ${sequence}`): PollingEvent {
  return {
    id: `journal-${sequence}`, sequence, type, session_id: session,
    timestamp: new Date(Date.UTC(2026, 9, 5, 0, 0, sequence)).toISOString(),
    data: { data: type === 'unified_completion' ? { final_result: content } : { content } },
  } as PollingEvent
}
function page(events: PollingEvent[], hasMore = false) {
  return {
    events, session_status: 'completed', has_more: hasMore,
    oldest_sequence: events[0]?.sequence,
    latest_sequence: events.at(-1)?.sequence,
  }
}
beforeEach(() => {
  api.recent.mockReset()
  useChatStore.setState({ tabEvents: {}, tabHasMoreOlderEvents: {}, tabHistoryPagination: {}, chatTabs: {} })
})

it('keeps page-refresh messages after returning to a tool-heavy chat with a smaller history window', async () => {
  const initial = [row(1), row(2, 'unified_completion'), row(3), row(104, 'unified_completion')]
  api.recent.mockResolvedValueOnce(page(initial))
  await hydrateTabEvents(session)
  expect(useChatStore.getState().tabHasMoreOlderEvents[session]).toBe(false)

  // A full event page may contain only the last turn's 100 tool events.
  const tools = Array.from({ length: 100 }, (_, i) => row(i + 4, 'tool_call_end'))
  api.recent.mockResolvedValue(page([...tools, row(104, 'unified_completion', 'Updated reply')], true))
  for (let visit = 0; visit < 3; visit++) {
    await hydrateTabEvents(session, { compact: false })
    const store = useChatStore.getState()
    expect(store.getTabEvents(session).slice(0, 3).map(event => event.id)).toEqual(['journal-1', 'journal-2', 'journal-3'])
    expect(store.getTabEvents(session).filter(event => event.id === 'journal-104')).toEqual([row(104, 'unified_completion', 'Updated reply')])
    expect(store.tabHasMoreOlderEvents[session]).toBe(false)
    expect(store.tabHistoryPagination[session]).toBeUndefined()
  }
})

it('keeps the original backward cursor with a loaded prefix, including a newer page received during refresh', async () => {
  const store = useChatStore.getState()
  store.setTabEvents(session, [row(50), row(60)])
  store.setTabHasMoreOlderEvents(session, true)
  store.setTabHistoryPagination(session, { hasMore: true, nextOffset: 50, compact: true })
  let resolve!: (value: ReturnType<typeof page>) => void
  api.recent.mockReturnValue(new Promise<ReturnType<typeof page>>(done => { resolve = done }))
  const refresh = hydrateTabEvents(session, { compact: true })
  // Another reader finishes loading history while the request is in flight.
  store.setTabEvents(session, [row(20), row(50), row(60)])
  store.setTabHistoryPagination(session, { hasMore: true, nextOffset: 20, compact: true })
  resolve(page([row(60), row(70)], true))
  await refresh
  expect(store.getTabEvents(session).map(event => event.sequence)).toEqual([20, 50, 60, 70])
  expect(useChatStore.getState().tabHistoryPagination[session]).toEqual({ hasMore: true, nextOffset: 20, compact: true })
})

it('restores a backward cursor if the bounded store trims a previously complete history', async () => {
  const store = useChatStore.getState()
  store.setTabEvents(session, Array.from({ length: 1100 }, (_, i) => row(i + 1)))
  store.setTabHasMoreOlderEvents(session, false)
  store.setTabHistoryPagination(session, null)
  api.recent.mockResolvedValue(page(Array.from({ length: 200 }, (_, i) => row(i + 1101)), true))
  await hydrateTabEvents(session)
  const retained = store.getTabEvents(session)
  expect(retained).toHaveLength(1000)
  expect(retained[0].sequence).toBe(301)
  expect(useChatStore.getState().tabHistoryPagination[session]).toEqual({ hasMore: true, nextOffset: 301 })
})
