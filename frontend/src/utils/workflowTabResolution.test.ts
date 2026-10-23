import { describe, expect, it, vi } from 'vitest'
import type { ChatTab } from '../stores/useChatStore'
import { blankWorkflowBuilderTabId, isBlankWorkflowBuilderTab, resolveWorkflowTabForSession, reusableScheduleTabId, scheduleLaneKey } from './workflowTabResolution'

describe('scheduleLaneKey', () => {
  it('is the schedule segment of a scheduler-minted session id', () => {
    expect(scheduleLaneKey('schedule-cron--42eca39a_1788453558390126411')).toBe('42eca39a')
  })

  it('is the same for a cron fire and a "Run now" of the same schedule', () => {
    expect(scheduleLaneKey('schedule-cron--42eca39a_100')).toBe(scheduleLaneKey('schedule-manual--42eca39a_200'))
  })

  it('differs between two schedules of one workflow', () => {
    expect(scheduleLaneKey('schedule-cron--42eca39a_100')).not.toBe(scheduleLaneKey('schedule-cron--86683998_100'))
  })

  it("gives the toolbar's one-off Autopilot its own lane", () => {
    expect(scheduleLaneKey('schedule-manual--manual-p_100')).toBe('manual-p')
  })

  it('keeps an underscore inside the schedule id and still strips the timestamp', () => {
    expect(scheduleLaneKey('schedule-cron--ab_cd_100')).toBe('ab_cd')
  })

  it('is null for anything the scheduler did not mint', () => {
    expect(scheduleLaneKey('fae1da55-e1e0-4b27-81e7-54d3424760da')).toBeNull()
    expect(scheduleLaneKey('bot-whatsapp-1')).toBeNull()
    expect(scheduleLaneKey('')).toBeNull()
    expect(scheduleLaneKey(null)).toBeNull()
  })
})

describe('reusableScheduleTabId', () => {
  const finishedDailyTab = {
    tabId: 'daily-tab',
    sessionId: 'schedule-cron--daily123_1',
    isStreaming: false,
    metadata: { mode: 'workflow' as const, isScheduledRun: true, isViewOnly: true, presetQueryId: 'workflow-upwork' },
  }

  it('reuses the finished tab of the same schedule instead of opening another per run', () => {
    expect(reusableScheduleTabId({ a: finishedDailyTab }, 'workflow-upwork', 'schedule-cron--daily123_2'))
      .toBe('daily-tab')
  })

  it('reuses it across triggers -- a "Run now" continues the cron lane', () => {
    expect(reusableScheduleTabId({ a: finishedDailyTab }, 'workflow-upwork', 'schedule-manual--daily123_2'))
      .toBe('daily-tab')
  })

  it('never lets a different schedule of the same workflow take the tab', () => {
    expect(reusableScheduleTabId({ a: finishedDailyTab }, 'workflow-upwork', 'schedule-cron--weekly45_2'))
      .toBeNull()
  })

  it("never lets a one-off Autopilot take a named schedule's tab (the title-flip bug)", () => {
    expect(reusableScheduleTabId({ a: finishedDailyTab }, 'workflow-upwork', 'schedule-manual--manual-p_2'))
      .toBeNull()
  })

  it('never displaces a live run', () => {
    expect(reusableScheduleTabId(
      { a: { ...finishedDailyTab, isStreaming: true } }, 'workflow-upwork', 'schedule-cron--daily123_2',
    )).toBeNull()
  })

  it('never recycles a tab the user promoted to an interactive chat', () => {
    expect(reusableScheduleTabId(
      { a: { ...finishedDailyTab, metadata: { ...finishedDailyTab.metadata, userInteractiveContinuation: true } } },
      'workflow-upwork', 'schedule-cron--daily123_2',
    )).toBeNull()
  })

  it('never crosses workflows', () => {
    expect(reusableScheduleTabId({ a: finishedDailyTab }, 'workflow-social', 'schedule-cron--daily123_2')).toBeNull()
  })

  it('leaves an ordinary Chat tab alone', () => {
    expect(reusableScheduleTabId(
      { a: { ...finishedDailyTab, metadata: { mode: 'workflow' as const } } },
      'workflow-upwork', 'schedule-cron--daily123_2',
    )).toBeNull()
  })

  it('has nothing to reuse for a session the scheduler did not mint', () => {
    expect(reusableScheduleTabId({ a: finishedDailyTab }, 'workflow-upwork', 'fae1da55')).toBeNull()
  })
})

describe('resolveWorkflowTabForSession', () => {
  const tab = (overrides: Partial<ChatTab>): ChatTab => ({
    tabId: 'tab', name: 'Daily', sessionId: 'schedule-cron--daily123_1',
    isStreaming: false, isCompleted: false, hasRunningBgAgents: false, isSyntheticTurn: false,
    canSteer: false, hideToolCalls: true, viewMode: 'terminal', config: {} as ChatTab['config'],
    createdAt: 1, lastAccessedAt: 1, lastViewedEventCount: 0, lastViewedEventCounts: { micro: 0 },
    metadata: { mode: 'workflow', presetQueryId: 'workflow-upwork', isScheduledRun: true, isViewOnly: true },
    ...overrides,
  })
  const scheduled = { mode: 'workflow' as const, presetQueryId: 'workflow-upwork', isScheduledRun: true, isViewOnly: true }
  const builder = { mode: 'workflow' as const, presetQueryId: 'workflow-upwork', phaseId: 'workflow-builder' }

  const run = (tabs: ChatTab[], sessionId: string, metadata: NonNullable<ChatTab['metadata']>) => {
    const createChatTab = vi.fn(async () => 'new-tab')
    const updateTabSessionId = vi.fn()
    const byId = Object.fromEntries(tabs.map(t => [t.tabId, t]))
    const result = resolveWorkflowTabForSession({
      getTabs: () => byId, presetQueryId: 'workflow-upwork', sessionId, name: 'n', metadata,
      createChatTab, updateTabSessionId,
    })
    return { result, createChatTab, updateTabSessionId }
  }

  it('returns the tab already bound to the session and opens nothing', async () => {
    const { result, createChatTab, updateTabSessionId } = run([tab({})], 'schedule-cron--daily123_1', scheduled)
    expect(await result).toEqual({ tabId: 'tab', via: 'existing' })
    expect(createChatTab).not.toHaveBeenCalled()
    expect(updateTabSessionId).not.toHaveBeenCalled()
  })

  it("rebinds the same schedule's finished tab to the new run instead of opening a tab", async () => {
    const { result, createChatTab, updateTabSessionId } = run([tab({})], 'schedule-cron--daily123_2', scheduled)
    expect(await result).toEqual({ tabId: 'tab', via: 'lane' })
    expect(updateTabSessionId).toHaveBeenCalledWith('tab', 'schedule-cron--daily123_2')
    expect(createChatTab).not.toHaveBeenCalled()
  })

  it("opens a new tab for an Autopilot run rather than taking a named schedule's", async () => {
    const { result, createChatTab } = run([tab({})], 'schedule-manual--manual-p_2', scheduled)
    expect(await result).toEqual({ tabId: 'new-tab', via: 'created' })
    expect(createChatTab).toHaveBeenCalledTimes(1)
  })

  it("a Builder chat never takes a schedule's tab", async () => {
    const { result, updateTabSessionId } = run([tab({})], 'fae1da55', builder)
    expect(await result).toEqual({ tabId: 'new-tab', via: 'created' })
    expect(updateTabSessionId).not.toHaveBeenCalled()
  })

  it('never rebinds an interactive Autopilot conversation as the persistent Builder', async () => {
    const pulse = tab({ tabId: 'pulse', name: 'Autopilot', sessionId: 'schedule-goallead--goal-g1', metadata: { ...builder, isViewOnly: false, isScheduledRun: false, isExecutionRun: true } })
    const { result, updateTabSessionId } = run([pulse], 'builder-session', builder)
    expect(await result).toEqual({ tabId: 'new-tab', via: 'created' })
    expect(updateTabSessionId).not.toHaveBeenCalled()
  })

  it('picks the schedule tab, not the Builder chat, when both share a session id', async () => {
    const chat = tab({ tabId: 'chat', metadata: { ...builder } })
    const lane = tab({ tabId: 'lane' })
    const { result } = run([chat, lane], 'schedule-cron--daily123_1', scheduled)
    expect((await result).tabId).toBe('lane')
    const forChat = run([chat, lane], 'schedule-cron--daily123_1', builder)
    expect((await forChat.result).tabId).toBe('chat')
  })

  it('reads tabs live, so a tab created by an earlier await is seen', async () => {
    const byId: Record<string, ChatTab> = {}
    const createChatTab = vi.fn(async () => 'new-tab')
    const first = await resolveWorkflowTabForSession({
      getTabs: () => byId, presetQueryId: 'workflow-upwork', sessionId: 'schedule-cron--daily123_1',
      name: 'n', metadata: scheduled, createChatTab, updateTabSessionId: vi.fn(),
    })
    byId['new-tab'] = tab({ tabId: 'new-tab' })
    const second = await resolveWorkflowTabForSession({
      getTabs: () => byId, presetQueryId: 'workflow-upwork', sessionId: 'schedule-cron--daily123_1',
      name: 'n', metadata: scheduled, createChatTab, updateTabSessionId: vi.fn(),
    })
    expect(first.via).toBe('created')
    expect(second).toEqual({ tabId: 'new-tab', via: 'existing' })
    expect(createChatTab).toHaveBeenCalledTimes(1)
  })
})

describe('isBlankWorkflowBuilderTab', () => {
  const chat = (overrides: Partial<ChatTab>): ChatTab => ({
    tabId: 'chat', name: 'Automation Builder', sessionId: 's1',
    isStreaming: false, isCompleted: false, hasRunningBgAgents: false, isSyntheticTurn: false,
    canSteer: false, hideToolCalls: true, viewMode: 'terminal', config: {} as ChatTab['config'],
    createdAt: 1, lastAccessedAt: 1, lastViewedEventCount: 0, lastViewedEventCounts: { micro: 0 },
    metadata: { mode: 'workflow', presetQueryId: 'workflow-upwork', phaseId: 'workflow-builder' },
    ...overrides,
  })
  const noEvents = {}
  const withChat = { s1: [{ type: 'user_message' } as never] }
  const onlyLifecycle = { s1: [{ type: 'tool_call_start' } as never] }

  it('is blank with no session, or a session that has no real chat in it', () => {
    expect(isBlankWorkflowBuilderTab(chat({ sessionId: null }), 'workflow-upwork', noEvents)).toBe(true)
    expect(isBlankWorkflowBuilderTab(chat({}), 'workflow-upwork', noEvents)).toBe(true)
    expect(isBlankWorkflowBuilderTab(chat({}), 'workflow-upwork', onlyLifecycle)).toBe(true)
  })

  it('is not blank once a message has been sent', () => {
    expect(isBlankWorkflowBuilderTab(chat({}), 'workflow-upwork', withChat)).toBe(false)
  })

  it('never counts a live, restored, view-only, other-workflow or phase tab', () => {
    expect(isBlankWorkflowBuilderTab(chat({ isStreaming: true }), 'workflow-upwork', noEvents)).toBe(false)
    expect(isBlankWorkflowBuilderTab(chat({ config: { restoredConversationPath: 'p' } as ChatTab['config'] }), 'workflow-upwork', noEvents)).toBe(false)
    expect(isBlankWorkflowBuilderTab(chat({ metadata: { mode: 'workflow', presetQueryId: 'workflow-upwork', phaseId: 'workflow-builder', isViewOnly: true } }), 'workflow-upwork', noEvents)).toBe(false)
    expect(isBlankWorkflowBuilderTab(chat({}), 'workflow-social', noEvents)).toBe(false)
    expect(isBlankWorkflowBuilderTab(chat({ metadata: { mode: 'workflow', presetQueryId: 'workflow-upwork', phaseId: 'planning' } }), 'workflow-upwork', noEvents)).toBe(false)
    expect(isBlankWorkflowBuilderTab(chat({ name: 'Run chat', metadata: { mode: 'workflow', presetQueryId: 'workflow-upwork', phaseId: 'workflow-builder', workshopMode: 'run' } }), 'workflow-upwork', noEvents)).toBe(false)
  })

  it('is not the Builder once named from a first message, even before its events arrive', () => {
    expect(isBlankWorkflowBuilderTab(chat({ name: 'fix the login bug' }), 'workflow-upwork', noEvents)).toBe(false)
  })

  it('blankWorkflowBuilderTabId picks the most recently used blank one', () => {
    const older = chat({ tabId: 'older', sessionId: 'a', lastAccessedAt: 10 })
    const newer = chat({ tabId: 'newer', sessionId: 'b', lastAccessedAt: 20 })
    expect(blankWorkflowBuilderTabId({ older, newer }, 'workflow-upwork', {})).toBe('newer')
    expect(blankWorkflowBuilderTabId({}, 'workflow-upwork', {})).toBeNull()
  })
})

describe("resolveWorkflowTabForSession -- one Chat tab per workflow", () => {
  const chat = (overrides: Partial<ChatTab>): ChatTab => ({
    tabId: 'chat', name: 'Automation Builder', sessionId: 'old-session',
    isStreaming: false, isCompleted: false, hasRunningBgAgents: false, isSyntheticTurn: false,
    canSteer: false, hideToolCalls: true, viewMode: 'terminal', config: {} as ChatTab['config'],
    createdAt: 1, lastAccessedAt: 1, lastViewedEventCount: 0, lastViewedEventCounts: { micro: 0 },
    metadata: { mode: 'workflow', presetQueryId: 'workflow-upwork', phaseId: 'workflow-builder' },
    ...overrides,
  })
  const builder = { mode: 'workflow' as const, presetQueryId: 'workflow-upwork', phaseId: 'workflow-builder' }

  const open = (tabs: ChatTab[], tabEvents: Record<string, never[]>) => {
    const createChatTab = vi.fn(async () => 'new-tab')
    const updateTabSessionId = vi.fn()
    const byId = Object.fromEntries(tabs.map(t => [t.tabId, t]))
    const result = resolveWorkflowTabForSession({
      getTabs: () => byId, getTabEvents: () => tabEvents, presetQueryId: 'workflow-upwork',
      sessionId: 'new-session', name: 'Automation Builder', metadata: builder,
      createChatTab, updateTabSessionId,
    })
    return { result, createChatTab, updateTabSessionId }
  }

  it('opens a conversation in the persistent Chat instead of creating a second tab', async () => {
    const { result, createChatTab, updateTabSessionId } = open([chat({})], {})
    expect(await result).toEqual({ tabId: 'chat', via: 'lane' })
    expect(updateTabSessionId).toHaveBeenCalledWith('chat', 'new-session')
    expect(createChatTab).not.toHaveBeenCalled()
  })

  it('rebinds an idle Chat tab that already has a conversation -- a different past chat replaces it', async () => {
    const { result, createChatTab } = open([chat({})], { 'old-session': [{ type: 'user_message' } as never] })
    expect(await result).toEqual({ tabId: 'chat', via: 'lane' })
    expect(createChatTab).not.toHaveBeenCalled()
  })

  it('never takes over a Chat tab that is mid-conversation', async () => {
    const { result, createChatTab } = open([chat({ isStreaming: true })], {})
    expect(await result).toEqual({ tabId: 'new-tab', via: 'created' })
    expect(createChatTab).toHaveBeenCalledTimes(1)
  })

  it('rebinds the idle Chat tab with content and leaves the Builder alone, whichever was used last', async () => {
    const used = chat({ tabId: 'used', sessionId: 'u', lastAccessedAt: 5 })
    const builder = chat({ tabId: 'builder', sessionId: 'b', lastAccessedAt: 50 })
    const { result } = open([used, builder], { u: [{ type: 'user_message' } as never] })
    expect((await result).tabId).toBe('used')
  })

  it('without tab events it cannot judge blankness and opens a new tab (old behaviour)', async () => {
    const createChatTab = vi.fn(async () => 'new-tab')
    const result = await resolveWorkflowTabForSession({
      getTabs: () => ({ chat: chat({}) }), presetQueryId: 'workflow-upwork',
      sessionId: 'new-session', name: 'Automation Builder', metadata: builder,
      createChatTab, updateTabSessionId: vi.fn(),
    })
    expect(result.via).toBe('created')
  })
})

describe('resolveWorkflowTabForSession -- no sessionId (ensure the persistent Chat)', () => {
  const chat = (overrides: Partial<ChatTab>): ChatTab => ({
    tabId: 'chat', name: 'Automation Builder', sessionId: 'existing-session',
    isStreaming: false, isCompleted: false, hasRunningBgAgents: false, isSyntheticTurn: false,
    canSteer: false, hideToolCalls: true, viewMode: 'terminal', config: {} as ChatTab['config'],
    createdAt: 1, lastAccessedAt: 1, lastViewedEventCount: 0, lastViewedEventCounts: { micro: 0 },
    metadata: { mode: 'workflow', presetQueryId: 'workflow-upwork', phaseId: 'workflow-builder' },
    ...overrides,
  })
  const builder = { mode: 'workflow' as const, presetQueryId: 'workflow-upwork', phaseId: 'workflow-builder', phaseName: 'Automation Builder' }

  const ensure = (tabs: ChatTab[], tabEvents: Record<string, never[]> = {}, presetQueryId = 'workflow-upwork') => {
    const createChatTab = vi.fn(async () => 'new-tab')
    const byId = Object.fromEntries(tabs.map(t => [t.tabId, t]))
    const result = resolveWorkflowTabForSession({
      getTabs: () => byId, getTabEvents: () => tabEvents, presetQueryId,
      name: 'Automation Builder', metadata: builder,
      createChatTab, updateTabSessionId: vi.fn(),
    })
    return { result, createChatTab }
  }

  it('reuses the workflow\'s blank builder tab instead of creating a new one', async () => {
    const { result, createChatTab } = ensure([chat({})])
    expect(await result).toEqual({ tabId: 'chat', via: 'existing' })
    expect(createChatTab).not.toHaveBeenCalled()
  })

  it('reuses a Chat that already has content instead of creating another one', async () => {
    const { result, createChatTab } = ensure([chat({ metadata: { ...builder } })], { 'existing-session': [{ type: 'user_message' } as never] })
    expect(await result).toEqual({ tabId: 'chat', via: 'existing' })
    expect(createChatTab).not.toHaveBeenCalled()
  })

  it('never mistakes a different workflow\'s blank tab for this one\'s', async () => {
    const { result, createChatTab } = ensure([chat({ metadata: { ...builder, presetQueryId: 'workflow-social' } })])
    expect((await result).via).toBe('created')
    expect(createChatTab).toHaveBeenCalledTimes(1)
  })

  it('concurrent callers for the same workflow share one creation instead of each creating a tab -- the two "Chat" tabs, 4 seconds apart, bug', async () => {
    let resolveCreate!: (id: string) => void
    const createChatTab = vi.fn(() => new Promise<string>(resolve => { resolveCreate = resolve }))
    const byId: Record<string, ChatTab> = {}
    const args = {
      getTabs: () => byId, getTabEvents: () => ({}), presetQueryId: 'workflow-upwork',
      name: 'Automation Builder', metadata: builder, createChatTab, updateTabSessionId: vi.fn(),
    }

    // Two independent discoverers (the boot reconnect and the preset-restore
    // retry) both find no blank tab and both start resolving before either
    // has created one -- exactly the race caught live via console.trace.
    const first = resolveWorkflowTabForSession(args)
    const second = resolveWorkflowTabForSession(args)

    resolveCreate('new-tab')
    const [firstResult, secondResult] = await Promise.all([first, second])

    expect(createChatTab).toHaveBeenCalledTimes(1)
    expect(firstResult.tabId).toBe('new-tab')
    expect(secondResult.tabId).toBe('new-tab')
  })

  it('a later caller after the race has settled creates its own tab, not a stale shared one', async () => {
    const byId: Record<string, ChatTab> = {}
    const createChatTab = vi.fn(async () => 'new-tab')
    const args = {
      getTabs: () => byId, getTabEvents: () => ({}), presetQueryId: 'workflow-upwork',
      name: 'Automation Builder', metadata: builder, createChatTab, updateTabSessionId: vi.fn(),
    }
    await resolveWorkflowTabForSession(args)
    byId['new-tab'] = chat({ tabId: 'new-tab', sessionId: 'new-tab-session' })

    const second = await resolveWorkflowTabForSession(args)
    expect(second).toEqual({ tabId: 'new-tab', via: 'existing' })
    expect(createChatTab).toHaveBeenCalledTimes(1)
  })
})
