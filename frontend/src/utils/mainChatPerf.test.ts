// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PollingEvent } from '../services/api-types'

function makeEvent(sessionId: string, type: string, id: string): PollingEvent {
  return {
    id,
    type,
    timestamp: '2026-11-26T00:00:00Z',
    data: {},
    session_id: sessionId,
  } as PollingEvent
}

describe('main-workspace render performance fixes (#4224)', () => {
  beforeEach(() => {
    vi.resetModules()
    localStorage.clear()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('updates volatile tabLastViewedCounts without mutating chatTabs reference on event batches or setTabEvents', async () => {
    const { useChatStore } = await import('../stores/useChatStore')
    const { computeNewEventCount } = await import('./chatSubmitHelpers')
    const sid = 'sess-perf-1'
    const tabId = 'tab-perf-1'

    useChatStore.setState({
      activeTabId: tabId,
      chatTabs: {
        [tabId]: {
          tabId,
          name: 'Main',
          sessionId: sid,
          isStreaming: false,
          isCompleted: false,
          hasRunningBgAgents: false,
          isSyntheticTurn: false,
          canSteer: false,
          hideToolCalls: true,
          viewMode: 'formatted',
          createdAt: 1,
          lastAccessedAt: 1,
          lastViewedEventCount: 0,
          lastViewedEventCounts: { micro: 0 },
          config: {} as never,
          metadata: { mode: 'multi-agent' },
        },
      },
    })

    const chatTabsBefore = useChatStore.getState().chatTabs
    useChatStore.getState()._addTabEventsImmediate(sid, [
      makeEvent(sid, 'user_message', 'ev-1'),
      makeEvent(sid, 'unified_completion', 'ev-2'),
    ])
    expect(useChatStore.getState().chatTabs).toBe(chatTabsBefore)
    expect(useChatStore.getState().tabLastViewedCounts[tabId]?.micro).toBeGreaterThan(0)

    const chatTabsBeforeSet = useChatStore.getState().chatTabs
    useChatStore.getState().setTabEvents(sid, [
      makeEvent(sid, 'user_message', 'ev-1'),
      makeEvent(sid, 'unified_completion', 'ev-2'),
      makeEvent(sid, 'user_message', 'ev-3'),
    ])
    expect(useChatStore.getState().chatTabs).toBe(chatTabsBeforeSet)

    const tab = useChatStore.getState().chatTabs[tabId]
    expect(computeNewEventCount(tab, false, useChatStore.getState().tabEvents, () => true)).toBe(0)
  })
})
