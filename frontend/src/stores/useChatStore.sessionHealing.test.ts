import { sessionStreamingState } from '../utils/sessionStreamingState'
import { describe, expect, it } from 'vitest'
import type { ActiveSessionInfo, RuntimeSnapshot } from '../services/api-types'
import { reconcileSessionTabStreamingState, type ChatState, type ChatTab } from './useChatStore'

// A turn a bot channel (WhatsApp, Slack, a scheduled run) starts on a session
// that already has an open UI tab looks, to that tab, identical to no turn at
// all: the tab's own isStreaming/hasRunningBgAgents never flip because the
// UI never submitted anything, and the tab's SSE stream can be silently dead
// (a queued EventSource sits "connecting" forever without ever firing
// onerror — see ChatArea.tsx's foreground-catch-up comment). Caught live: a
// WhatsApp-triggered ExampleApp reply saved to chat history but never
// appeared in the already-open parent tab, even after switching tabs away
// and back.
const baseTab = (overrides: Partial<ChatTab>): ChatTab => ({
  tabId: 'tab-1',
  name: 'Chat',
  sessionId: 'session-1',
  isStreaming: false,
  isCompleted: true,
  hasRunningBgAgents: false,
  isSyntheticTurn: false,
  canSteer: false,
  hideToolCalls: false,
  viewMode: 'formatted',
  config: {},
  createdAt: Date.now(),
  lastViewedEventCount: 0,
  lastViewedEventCounts: {},
  ...overrides,
} as unknown as ChatTab)

const activeSession = (overrides: Partial<ActiveSessionInfo>): ActiveSessionInfo => ({
  session_id: 'session-1',
  observer_id: 'session-1',
  agent_mode: 'multi-agent',
  status: 'running',
  last_activity: new Date().toISOString(),
  created_at: new Date().toISOString(),
  ...overrides,
})

describe('reconcileSessionTabStreamingState', () => {
  it('marks an idle tab streaming when the backend reports its session running', () => {
    const state = {
      chatTabs: { 'tab-1': baseTab({}) },
      tabSessionStatus: {},
    } as unknown as ChatState

    const result = reconcileSessionTabStreamingState(state, [activeSession({ status: 'running' })], Date.now())

    expect(result.chatTabs?.['tab-1'].isStreaming).toBe(true)
    expect(result.chatTabs?.['tab-1'].lastStreamingStartedAt).toBeGreaterThan(0)
  })

  it('recovers background activity without starting foreground streaming', () => {
    const state = {
      chatTabs: { 'tab-1': baseTab({}) },
      tabSessionStatus: {},
    } as unknown as ChatState

    const result = reconcileSessionTabStreamingState(
      state,
      [activeSession({ status: 'completed', has_running_background_agents: true })],
      Date.now(),
    )

    expect(result.chatTabs?.['tab-1'].isStreaming).toBe(false)
    expect(result.chatTabs?.['tab-1'].hasRunningBgAgents).toBe(true)
  })

  it('leaves an already-streaming tab alone (no redundant update)', () => {
    const state = {
      chatTabs: { 'tab-1': baseTab({ isStreaming: true, lastStreamingStartedAt: 123 }) },
      tabSessionStatus: {},
    } as unknown as ChatState

    const result = reconcileSessionTabStreamingState(state, [activeSession({ status: 'running' })], Date.now())

    expect(result.chatTabs).toBeUndefined()
  })

  it('does not heal a tab whose session the backend does not report at all', () => {
    const state = {
      chatTabs: { 'tab-1': baseTab({}) },
      tabSessionStatus: {},
    } as unknown as ChatState

    const result = reconcileSessionTabStreamingState(state, [], Date.now())

    expect(result.chatTabs).toBeUndefined()
  })

  it('still clears stale local streaming state once the backend session is no longer active (existing behavior)', () => {
    const state = {
      chatTabs: { 'tab-1': baseTab({ isStreaming: true, lastStreamingStartedAt: 0 }) },
      tabSessionStatus: { 'tab-1': { status: 'running', agentMode: 'multi-agent', lastActivity: null } },
    } as unknown as ChatState

    const result = reconcileSessionTabStreamingState(state, [], Date.now() + 10 * 60 * 1000)

    expect(result.chatTabs?.['tab-1'].isStreaming).toBe(false)
    expect(result.tabSessionStatus?.['tab-1'].status).toBeNull()
  })
})

const runtime = (overrides: Partial<RuntimeSnapshot> = {}): RuntimeSnapshot => ({
  session_id: 'session-1', generation: 1, revision: 1, phase: 'idle',
  foreground_turn: { busy: false, has_cancel: false, can_steer: false, synthetic: false },
  background_live: false, terminal_busy: false, waiting_for_user: false,
  started_at: '', last_progress_at: '', observed_at: '', ...overrides,
})

describe('P0 session recovery and event status agreement', () => {
  it.each(['idle', 'completed', 'failed', 'canceled'] as const)(
    'does not resurrect a %s runtime from stale running / background flags', phase => {
      const state = { chatTabs: { 'tab-1': baseTab({}) }, tabSessionStatus: {} } as unknown as ChatState
      const session = activeSession({ status: 'running', has_running_background_agents: true,
        runtime_state: runtime({ phase }) })
      for (let poll = 0; poll < 5; poll++) {
        expect(reconcileSessionTabStreamingState(state, [session], Date.now() + poll * 5000)).toEqual({})
        expect(sessionStreamingState({ ...session, session_status: 'completed' }).isStreaming).toBe(false)
      }
    },
  )

  it.each([
    { name: 'background-only', snapshot: runtime({ phase: 'running', background_live: true }), streaming: false, bg: true },
    { name: 'external foreground', snapshot: runtime({ phase: 'running', foreground_turn: { busy: true, has_cancel: true, can_steer: true, synthetic: false } }), streaming: true, bg: false },
    { name: 'retained busy terminal', snapshot: runtime({ phase: 'running', terminal_busy: true }), streaming: true, bg: false },
    { name: 'synthetic notification', snapshot: runtime({ phase: 'running', foreground_turn: { busy: true, has_cancel: true, can_steer: false, synthetic: true } }), streaming: false, bg: false },
    { name: 'waiting for input', snapshot: runtime({ phase: 'waiting', waiting_for_user: true }), streaming: false, bg: false },
  ])('stays stable across repeated recovery and event ticks: $name', ({ snapshot, streaming, bg }) => {
    let state = { chatTabs: { 'tab-1': baseTab({}) }, tabSessionStatus: {} } as unknown as ChatState
    const session = activeSession({ status: 'running', runtime_state: snapshot })
    // Both endpoints carry the same runtime, even if their legacy statuses disagree.
    const eventActivity = sessionStreamingState({ session_status: 'completed', runtime_state: snapshot })
    expect(eventActivity.isStreaming).toBe(streaming)
    expect(eventActivity.hasRunningBgAgents).toBe(bg)
    state = { ...state, ...reconcileSessionTabStreamingState(state, [session], 10000) }
    expect(state.chatTabs['tab-1'].isStreaming).toBe(streaming)
    expect(state.chatTabs['tab-1'].hasRunningBgAgents).toBe(bg)
    if (streaming || bg) expect(state.chatTabs['tab-1'].isCompleted).toBe(false)
    // Simulate ChatArea applying a status-only SSE tick, then the 5s list refresh.
    state = { ...state, chatTabs: { 'tab-1': { ...state.chatTabs['tab-1'], ...eventActivity } } }
    for (let poll = 1; poll <= 5; poll++) {
      expect(reconcileSessionTabStreamingState(state, [session], 10000 + poll * 5000)).toEqual({})
    }
  })

  it('keeps a just-submitted turn during the startup grace period', () => {
    const state = { chatTabs: { 'tab-1': baseTab({ isStreaming: true, lastStreamingStartedAt: 1000 }) }, tabSessionStatus: {} } as unknown as ChatState
    expect(reconcileSessionTabStreamingState(state, [activeSession({ runtime_state: runtime() })], 1001)).toEqual({})
    expect(reconcileSessionTabStreamingState(state, [activeSession({ runtime_state: runtime() })], 12000).chatTabs?.['tab-1'].isStreaming).toBe(false)
  })
})
