import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PollingEvent } from '../services/api-types'

// PLAT-106 P0 acceptance: a Schedule session and an interactive Chat session for
// the SAME workflow are independent conversations. No event produced by one may
// ever become visible under the other — including while both are streaming and
// while older history is still loading.
//
// This asserts exact event/session ownership (acceptance 6), never message text.

const CHAT = 'chat-build-in-public'
const SCHEDULE = 'schedule-cron--51af4f19_1786764627816018000'

const createMemoryStorage = (): Storage => {
  const values = new Map<string, string>()
  return {
    get length() {
      return values.size
    },
    clear: () => values.clear(),
    getItem: (key) => values.get(key) ?? null,
    key: (index) => Array.from(values.keys())[index] ?? null,
    removeItem: (key) => {
      values.delete(key)
    },
    setItem: (key, value) => {
      values.set(key, value)
    },
  }
}

function ev(owner: string, type: string, id: string): PollingEvent {
  return {
    id,
    type,
    timestamp: '2026-08-15T00:00:00Z',
    data: {},
    session_id: owner,
  } as PollingEvent
}

async function loadStore() {
  const mod = await import('./useChatStore')
  return mod.useChatStore
}

describe('concurrent Chat + Schedule session isolation', () => {
  beforeEach(() => {
    vi.resetModules()
    vi.stubGlobal('localStorage', createMemoryStorage())
  })

  afterEach(() => {
    vi.clearAllTimers()
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  // #2826: persisted terminal selection cannot resurrect the removed app UI;
  // normalizing it must preserve the chat's event-backed conversation.
  it('forces saved terminal preferences to chat without discarding conversation events', async () => {
    const useChatStore = await loadStore()
    const { normalizeEventViewMode } = await import('./useChatStore')
    expect(normalizeEventViewMode('terminal')).toBe('formatted')
    useChatStore.setState({chatTabs:{saved:{tabId:'saved',name:'Saved chat',sessionId:CHAT,viewMode:'terminal',metadata:{mode:'multi-agent'}} as any}})
    useChatStore.getState().addTabEvent(CHAT,ev(CHAT,'user_message','kept-native-prompt'))
    useChatStore.getState().setTabViewMode('saved','terminal')
    expect(useChatStore.getState().chatTabs.saved.viewMode).toBe('formatted')
    expect(useChatStore.getState().getTabEvents(CHAT).map(row=>row.id)).toContain('kept-native-prompt')
  })

  it('keeps distinct user, assistant, tool, and completion events in their own session', async () => {
    const useChatStore = await loadStore()
    const store = useChatStore.getState()

    // Acceptance 2: emit distinct events of every kind into both sessions.
    const chatEvents = [
      ev(CHAT, 'user_message', 'chat-user-1'),
      ev(CHAT, 'unified_completion', 'chat-done-1'),
    ]
    const scheduleEvents = [
      ev(SCHEDULE, 'user_message', 'sched-user-1'),
      ev(SCHEDULE, 'background_agent_started', 'sched-tool-1'),
      ev(SCHEDULE, 'unified_completion', 'sched-done-1'),
      ev(SCHEDULE, 'agent_end', 'sched-end-1'),
    ]

    chatEvents.forEach(e => store.addTabEvent(CHAT, e))
    scheduleEvents.forEach(e => store.addTabEvent(SCHEDULE, e))

    const chatIDs = useChatStore.getState().getTabEvents(CHAT).map(e => e.id)
    const scheduleIDs = useChatStore.getState().getTabEvents(SCHEDULE).map(e => e.id)

    // Acceptance 3: neither session contains any of the other's events.
    expect(chatIDs).toEqual(['chat-user-1', 'chat-done-1'])
    expect(scheduleIDs).toEqual(['sched-user-1', 'sched-tool-1', 'sched-done-1', 'sched-end-1'])
    expect(chatIDs.some(id => id.startsWith('sched-'))).toBe(false)
    expect(scheduleIDs.some(id => id.startsWith('chat-'))).toBe(false)
  })

  it('rejects a Schedule event written under the Chat session', async () => {
    const useChatStore = await loadStore()
    const store = useChatStore.getState()

    store.addTabEvent(CHAT, ev(CHAT, 'user_message', 'chat-user-1'))
    // The reported defect: a Schedule-owned event arriving under a response
    // envelope labelled with the Chat session.
    store.addTabEvent(CHAT, ev(SCHEDULE, 'unified_completion', 'sched-leaked-1'))

    const chatIDs = useChatStore.getState().getTabEvents(CHAT).map(e => e.id)
    expect(chatIDs).toEqual(['chat-user-1'])
    expect(chatIDs).not.toContain('sched-leaked-1')
  })

  it('forgets local tabs and observers when the authenticated account changes', async () => {
    const useChatStore = await loadStore()
    const close = vi.fn()
    useChatStore.setState({
      chatTabs: {
        inherited: {
          tabId: 'inherited',
          name: 'Admin chat',
          sessionId: CHAT,
          isStreaming: true,
          isCompleted: false,
          hasRunningBgAgents: false,
          isSyntheticTurn: false,
          canSteer: false,
          hideToolCalls: true,
          viewMode: 'formatted',
          createdAt: Date.now(),
          lastAccessedAt: Date.now(),
          lastViewedEventCount: 0,
          lastViewedEventCounts: { micro: 0 },
          config: {} as never,
          metadata: { mode: 'multi-agent' },
        },
      },
      activeTabId: 'inherited',
      tabEvents: { [CHAT]: [ev(CHAT, 'user_message', 'private-message')] },
      sseConnections: { [CHAT]: { close } } as never,
    })

    useChatStore.getState().discardChatStateForAccountChange()

    expect(close).toHaveBeenCalledOnce()
    expect(useChatStore.getState().chatTabs).toEqual({})
    expect(useChatStore.getState().activeTabId).toBeNull()
    expect(useChatStore.getState().tabEvents).toEqual({})
  })

  it('clears active pollingInterval and discards buffered streaming chunks on account change (#4214)', async () => {
    vi.useFakeTimers()
    const useChatStore = await loadStore()
    const pollTick = vi.fn()
    useChatStore.getState().startPolling(pollTick)
    expect(useChatStore.getState().pollingInterval).not.toBeNull()

    // Buffer a streaming chunk (flushes on a 16ms timer) and switch accounts before it fires.
    useChatStore.getState().appendStreamingChunk(CHAT, 1, 'Account A secret text')
    useChatStore.getState().discardChatStateForAccountChange()

    vi.advanceTimersByTime(2500)
    expect(useChatStore.getState().pollingInterval).toBeNull()
    expect(useChatStore.getState().streamingText[CHAT]).toBeUndefined()
    expect(pollTick).not.toHaveBeenCalled()
  })

  it('holds the invariant while both sessions stream interleaved', async () => {
    const useChatStore = await loadStore()
    const store = useChatStore.getState()

    // Acceptance 4: rapid interleaving, including cross-written events.
    for (let i = 0; i < 25; i++) {
      store.addTabEvent(CHAT, ev(CHAT, 'unified_completion', `chat-${i}`))
      store.addTabEvent(SCHEDULE, ev(SCHEDULE, 'unified_completion', `sched-${i}`))
      // Every other tick, the wrong envelope tries to cross the boundary.
      if (i % 2 === 0) {
        store.addTabEvent(CHAT, ev(SCHEDULE, 'agent_end', `sched-cross-${i}`))
        store.addTabEvent(SCHEDULE, ev(CHAT, 'agent_end', `chat-cross-${i}`))
      }
    }

    const chatIDs = useChatStore.getState().getTabEvents(CHAT).map(e => e.id)
    const scheduleIDs = useChatStore.getState().getTabEvents(SCHEDULE).map(e => e.id)

    expect(chatIDs).toHaveLength(25)
    expect(scheduleIDs).toHaveLength(25)
    expect(chatIDs.every(id => id.startsWith('chat-'))).toBe(true)
    expect(scheduleIDs.every(id => id.startsWith('sched-'))).toBe(true)
    expect(chatIDs.some(id => id.includes('cross'))).toBe(false)
    expect(scheduleIDs.some(id => id.includes('cross'))).toBe(false)
  })

  it('does not migrate events across sessions when older history is loaded late', async () => {
    const useChatStore = await loadStore()
    const store = useChatStore.getState()

    store.addTabEvent(CHAT, ev(CHAT, 'user_message', 'chat-live-1'))

    // Acceptance 5: a late history page for the Schedule session, delivered
    // while Chat is selected, must not land in Chat.
    store.addTabEvents(CHAT, [
      ev(SCHEDULE, 'user_message', 'sched-history-1'),
      ev(SCHEDULE, 'unified_completion', 'sched-history-2'),
    ])
    // Flush the 100ms micro-batch used by addTabEvents.
    await new Promise(resolve => setTimeout(resolve, 150))

    const chatIDs = useChatStore.getState().getTabEvents(CHAT).map(e => e.id)
    expect(chatIDs).toEqual(['chat-live-1'])
  })
})
