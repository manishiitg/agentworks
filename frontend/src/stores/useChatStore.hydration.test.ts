import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

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

describe('useChatStore hydration bootstrap', () => {
  beforeEach(() => {
    vi.resetModules()
    vi.stubGlobal('localStorage', createMemoryStorage())
  })

  afterEach(() => {
    vi.clearAllTimers()
    vi.useRealTimers()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('finishes synchronous storage hydration before callers need the backstop', async () => {
    const chatStore = await import('./useChatStore')

    await chatStore.waitForChatStoreHydration()

    expect(chatStore.getChatStoreHydrationSnapshot()).toEqual({
      status: 'hydrated',
      error: null,
    })
  }, 15_000)

  it('does not persist streaming chunks or an obsolete tree preference', async () => {
    vi.useFakeTimers()
    const storage = createMemoryStorage()
    const setItem = vi.spyOn(storage, 'setItem')
    vi.stubGlobal('localStorage', storage)
    const chatStore = await import('./useChatStore')
    await chatStore.waitForChatStoreHydration()
    expect(chatStore.normalizeEventViewMode('tree')).toBe('formatted')
    chatStore.useChatStore.setState({ eventViewModePreference: 'formatted' })
    vi.advanceTimersByTime(250)
    setItem.mockClear()

    for (let index = 1; index <= 1_000; index += 1) {
      chatStore.useChatStore.getState().appendStreamingChunk('session-1', index, `chunk-${index}`)
    }
    vi.advanceTimersByTime(250)
    expect(setItem).not.toHaveBeenCalled()

    chatStore.useChatStore.setState({ eventViewModePreference: 'tree' })
    chatStore.useChatStore.setState({ eventViewModePreference: 'formatted' })
    chatStore.useChatStore.setState({ eventViewModePreference: 'tree' })
    vi.advanceTimersByTime(250)

    expect(setItem).not.toHaveBeenCalled()
  })

  it('coalesces repeated workflow switches into one durable write', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-07-13T00:00:00Z'))
    const storage = createMemoryStorage()
    const setItem = vi.spyOn(storage, 'setItem')
    vi.stubGlobal('localStorage', storage)
    const chatStore = await import('./useChatStore')
    await chatStore.waitForChatStoreHydration()

    const firstTab = await chatStore.useChatStore.getState().createChatTab('First workflow', {
      mode: 'workflow',
      phaseId: 'workflow-builder',
      presetQueryId: 'workflow-one',
    })
    vi.setSystemTime(new Date('2026-07-13T00:00:00.001Z'))
    const secondTab = await chatStore.useChatStore.getState().createChatTab('Second workflow', {
      mode: 'workflow',
      phaseId: 'workflow-builder',
      presetQueryId: 'workflow-two',
    })
    vi.advanceTimersByTime(250)
    setItem.mockClear()

    for (let index = 0; index < 100; index += 1) {
      chatStore.useChatStore.getState().switchTab(index % 2 === 0 ? firstTab : secondTab)
    }
    vi.advanceTimersByTime(250)

    expect(setItem).toHaveBeenCalledTimes(1)
    expect(chatStore.useChatStore.getState().activeTabId).toBe(secondTab)
  })

  it('marks a background tab completed until the user opens it', async () => {
    const chatStore = await import('./useChatStore')
    await chatStore.waitForChatStoreHydration()

    const firstTab = await chatStore.useChatStore.getState().createChatTab('Current', {
      mode: 'workflow',
      phaseId: 'workflow-builder',
      presetQueryId: 'current-workflow',
    })
    const backgroundTab = await chatStore.useChatStore.getState().createChatTab('Background', {
      mode: 'workflow',
      phaseId: 'workflow-builder',
      presetQueryId: 'background-workflow',
    })
    chatStore.useChatStore.getState().switchTab(firstTab)

    chatStore.useChatStore.getState().setTabStreaming(backgroundTab, true)
    expect(chatStore.useChatStore.getState().getTab(backgroundTab)?.hasUnreadCompletion).toBe(false)

    chatStore.useChatStore.getState().setTabStreaming(backgroundTab, false)
    expect(chatStore.useChatStore.getState().getTab(backgroundTab)?.hasUnreadCompletion).toBe(true)

    chatStore.useChatStore.getState().switchTab(backgroundTab)
    expect(chatStore.useChatStore.getState().getTab(backgroundTab)?.hasUnreadCompletion).toBe(false)
  })

  it('rejects the removed profile-less AgentWorks chat lane', async () => {
    const chatStore = await import('./useChatStore')
    await chatStore.waitForChatStoreHydration()

    await expect(chatStore.useChatStore.getState().createChatTab('Chat', {
      mode: 'multi-agent',
    })).rejects.toThrow('Profile-less AgentWorks Chat has been removed')
    expect(Object.keys(chatStore.useChatStore.getState().chatTabs)).toHaveLength(0)
  })

  it('keeps product-profile project sessions in separate lanes', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-08-07T10:00:00Z'))
    const chatStore = await import('./useChatStore')
    await chatStore.waitForChatStoreHydration()

    const firstProjectTabId = await chatStore.useChatStore.getState().createChatTab('Launch film', {
      mode: 'multi-agent',
      agentProfileId: 'example-product',
      agentProfileVersion: 1,
      agentProfileWorkspace: 'Chats/Example Product/projects/launch-film',
    }, 'example-product:project:launch-film')
    const reusedProjectTabId = await chatStore.useChatStore.getState().createChatTab('Launch film', {
      mode: 'multi-agent',
      agentProfileId: 'example-product',
      agentProfileVersion: 1,
      agentProfileWorkspace: 'Chats/Example Product/projects/launch-film',
    })
    const secondProjectTabId = await chatStore.useChatStore.getState().createChatTab('Customer story', {
      mode: 'multi-agent',
      agentProfileId: 'example-product',
      agentProfileVersion: 1,
      agentProfileWorkspace: 'Chats/Example Product/projects/customer-story',
    }, 'example-product:project:customer-story')

    expect(reusedProjectTabId).toBe(firstProjectTabId)
    expect(secondProjectTabId).not.toBe(firstProjectTabId)
    expect(Object.keys(chatStore.useChatStore.getState().chatTabs)).toHaveLength(2)
  })

  it('prepares and reuses an embedded project chat without replacing a selected side chat', async () => {
    const { useChatStore, waitForChatStoreHydration } = await import('./useChatStore')
    await waitForChatStoreHydration()
    const side = await useChatStore.getState().createChatTab('Chat 2', { mode: 'multi-agent', agentProfileId: 'code', agentProfileProjectId: 'project', agentProfileConversationKey: 'project:chat:side' }, 'side-session')
    const metadata = { mode: 'multi-agent' as const, agentProfileId: 'code', agentProfileProjectId: 'project', agentProfileConversationKey: 'project' }
    const primary = await useChatStore.getState().createChatTab('Chat', metadata, 'primary-session', { activate: false })
    expect(useChatStore.getState().activeTabId).toBe(side)
    expect(await useChatStore.getState().createChatTab('Chat', metadata, 'primary-session', { activate: false })).toBe(primary)
    expect(useChatStore.getState().activeTabId).toBe(side)
    expect(useChatStore.getState().chatTabs[primary].sessionId).toBe('primary-session')
  })

  it('never reuses a product Builder as the project conversation tab', async () => {
    const chatStore = await import('./useChatStore')
    await chatStore.waitForChatStoreHydration()

    const builderTabId = await chatStore.useChatStore.getState().createChatTab('Builder', {
      mode: 'multi-agent',
      agentProfileId: 'work',
      agentProfileVersion: 1,
      agentProfileWorkspace: 'Chats/Work/projects/demo',
      agentProfileProjectId: 'demo',
      agentProfileBuilder: true,
    })
    const chatTabId = await chatStore.useChatStore.getState().createChatTab('Fix the login form', {
      mode: 'multi-agent',
      agentProfileId: 'work',
      agentProfileVersion: 1,
      agentProfileWorkspace: 'Chats/Work/projects/demo',
      agentProfileProjectId: 'demo',
      agentProfileConversationKey: 'demo:chat-1',
      agentProfileBuilder: false,
    })

    expect(chatTabId).not.toBe(builderTabId)
    expect(chatStore.useChatStore.getState().getTab(builderTabId)?.metadata?.agentProfileBuilder).toBe(true)
    expect(chatStore.useChatStore.getState().getTab(chatTabId)?.metadata?.agentProfileBuilder).toBe(false)
    expect(Object.keys(chatStore.useChatStore.getState().chatTabs)).toHaveLength(2)
  })

  it('does not inherit globally connected MCP or skill selections into a product tab', async () => {
    const chatStore = await import('./useChatStore')
    const { useMCPStore } = await import('./useMCPStore')
    const { useAppStore } = await import('./useAppStore')
    await chatStore.waitForChatStoreHydration()
    useMCPStore.setState({ chatSelectedServers: ['google_sheets'] })
    useAppStore.setState({ lastSelectedSkills: ['personal-skill'] })

    const tabId = await chatStore.useChatStore.getState().createChatTab('Builder', {
      mode: 'multi-agent',
      agentProfileId: 'work',
      agentProfileProjectId: 'isolated-project',
      agentProfileBuilder: true,
    })

    expect(chatStore.useChatStore.getState().getTab(tabId)?.config).toMatchObject({
      selectedServers: ['NO_SERVERS'],
      selectedSkills: [],
    })
  })

  it('reuses a keyed product conversation across profile upgrades and workspace moves', async () => {
    const chatStore = await import('./useChatStore')
    await chatStore.waitForChatStoreHydration()

    const originalTabId = await chatStore.useChatStore.getState().createChatTab('Launch film', {
      mode: 'multi-agent',
      agentProfileId: 'example-product',
      agentProfileVersion: 1,
      agentProfileWorkspace: 'Chats/Example Product/projects/old-location',
      agentProfileProjectId: 'launch-2026',
      agentProfileConversationKey: 'launch-2026',
      agentProfileConversationId: 'conversation-launch',
      agentProfileChatContract: 'profile-v1',
    }, 'canonical-session')
    const resumedTabId = await chatStore.useChatStore.getState().createChatTab('Launch film', {
      mode: 'multi-agent',
      agentProfileId: 'example-product',
      agentProfileVersion: 2,
      agentProfileWorkspace: 'Chats/Example Product/projects/new-location',
      agentProfileProjectId: 'launch-2026',
      agentProfileConversationKey: 'launch-2026',
      agentProfileConversationId: 'conversation-launch',
      agentProfileChatContract: 'profile-v1',
    }, 'canonical-session')

    expect(resumedTabId).toBe(originalTabId)
    expect(chatStore.useChatStore.getState().getTab(originalTabId)?.metadata).toMatchObject({
      agentProfileVersion: 2,
      agentProfileWorkspace: 'Chats/Example Product/projects/new-location',
      agentProfileConversationKey: 'launch-2026',
    })
    expect(Object.keys(chatStore.useChatStore.getState().chatTabs)).toHaveLength(1)
  })

  it('restores the existing owner-scoped chat-store envelope', async () => {
    const storage = createMemoryStorage()
    const createdAt = Date.now()
    storage.setItem('chat-store:owner:anonymous', JSON.stringify({
      state: {
        chatTabs: {
          'legacy-tab': {
            tabId: 'legacy-tab',
            name: 'Existing workflow chat',
            sessionId: 'legacy-session',
            isStreaming: false,
            isCompleted: false,
            hasRunningBgAgents: false,
            isSyntheticTurn: false,
            canSteer: false,
            hideToolCalls: true,
            viewMode: 'terminal',
            config: {
              inputText: '',
              useCodeExecutionMode: true,
              selectedServers: [],
              selectedSkills: [],
              selectedSecrets: [],
              llmConfig: { provider: 'codex-cli', model_id: 'gpt-5.6-sol' },
              fileContext: [],
              browserMode: 'none',
              workflowContext: [],
              queuedMessages: [],
            },
            createdAt,
            lastAccessedAt: createdAt,
            lastViewedEventCount: 0,
            lastViewedEventCounts: { micro: 0 },
            metadata: { mode: 'workflow', presetQueryId: 'existing-workflow' },
          },
        },
        activeTabId: 'legacy-tab',
        eventViewModePreference: 'terminal',
      },
      version: 0,
    }))
    vi.stubGlobal('localStorage', storage)

    const chatStore = await import('./useChatStore')
    await chatStore.waitForChatStoreHydration()

    expect(chatStore.useChatStore.getState().activeTabId).toBe('legacy-tab')
    expect(chatStore.useChatStore.getState().chatTabs['legacy-tab']).toMatchObject({
      name: 'Existing workflow chat',
      sessionId: 'legacy-session',
      metadata: { mode: 'workflow', presetQueryId: 'existing-workflow' },
    })
  })
  it('retains separate layouts and drafts for two users with multiple tabs', async () => {
    const { useChatStore, switchChatAccount } = await import('./useChatStore')
    const { captureChatIdentity, isChatIdentityCurrent } = await import('../utils/chatIdentity')
    switchChatAccount('alice')
    const aliceOne = await useChatStore.getState().createChatTab('Alice one', { mode: 'workflow', presetQueryId: 'alice-one' }, 'alice-one')
    const aliceTwo = await useChatStore.getState().createChatTab('Alice two', { mode: 'workflow', presetQueryId: 'alice-two' }, 'alice-two')
    useChatStore.getState().setTabConfig(aliceOne, { inputText: 'Alice private draft', isQueueProcessing: true, queuedSubmission: { id: 'retry-1', messages: ['queued'], sessionId: 'alice-one' } })
    const identity = captureChatIdentity()
    switchChatAccount('bob')
    expect(isChatIdentityCurrent(identity)).toBe(false)
    expect(Object.keys(useChatStore.getState().chatTabs)).toHaveLength(0)
    const bobOne = await useChatStore.getState().createChatTab('Bob one', { mode: 'workflow', presetQueryId: 'bob-one' }, 'bob-one')
    await useChatStore.getState().createChatTab('Bob two', { mode: 'workflow', presetQueryId: 'bob-two' }, 'bob-two')
    useChatStore.getState().setTabConfig(bobOne, { inputText: 'Bob private draft' })
    switchChatAccount('alice')
    expect(Object.keys(useChatStore.getState().chatTabs).sort()).toEqual([aliceOne, aliceTwo].sort())
    expect(useChatStore.getState().getTabConfig(aliceOne)?.inputText).toBe('Alice private draft')
    expect(useChatStore.getState().getTabConfig(aliceOne)?.isQueueProcessing).toBe(false)
    expect(useChatStore.getState().getTabConfig(aliceOne)?.queuedSubmission?.id).toBe('retry-1')
    expect(JSON.stringify(useChatStore.getState().chatTabs)).not.toContain('Bob private draft')
    switchChatAccount('bob')
    expect(Object.keys(useChatStore.getState().chatTabs)).toHaveLength(2)
    expect(useChatStore.getState().getTabConfig(bobOne)?.inputText).toBe('Bob private draft')
  })

  it('ignores the unowned legacy cache and gives drafts a store-owned revision', async () => {
    localStorage.setItem('chat-store', JSON.stringify({ state: { chatTabs: { leaked: { name: 'Legacy private draft' } }, activeTabId: 'leaked' }, version: 0 }))
    const { useChatStore } = await import('./useChatStore')
    expect(useChatStore.getState().chatTabs).toEqual({})
    const tab = await useChatStore.getState().createChatTab('Draft', { mode: 'workflow' }, 'draft')
    useChatStore.getState().setTabConfig(tab, { inputText: 'first' })
    const submittedRevision = useChatStore.getState().getTabConfig(tab)?.composerRevision
    useChatStore.getState().setTabConfig(tab, { inputText: 'new instance edited' })
    expect(useChatStore.getState().getTabConfig(tab)?.composerRevision).toBe((submittedRevision ?? 0) + 1)
    const settledState = useChatStore.getState()
    const subscriber = vi.fn()
    const unsubscribe = useChatStore.subscribe(subscriber)
    useChatStore.getState().setTabConfig(tab, { inputText: 'new instance edited' })
    expect(useChatStore.getState()).toBe(settledState)
    expect(subscriber).not.toHaveBeenCalled()
    unsubscribe()
    expect(useChatStore.getState().getTabConfig(tab)?.composerRevision).toBe((submittedRevision ?? 0) + 1)
  })

  it('migrates deployment-era tabs only after the persisted owner is verified', async () => {
    localStorage.setItem('auth-storage', JSON.stringify({ state: { user: { id: 'alice' }, isAuthenticated: true }, version: 0 }))
    localStorage.setItem('chat-store', JSON.stringify({ state: {
      chatTabs: { retained: { tabId: 'retained', name: 'Retained conversation', sessionId: 'alice-history', config: { inputText: 'unsent draft' } } },
      activeTabId: 'retained',
    }, version: 0 }))
    const { useChatStore, migrateLegacyChatStateForVerifiedAccount } = await import('./useChatStore')
    expect(useChatStore.getState().chatTabs).toEqual({})
    expect(migrateLegacyChatStateForVerifiedAccount('bob')).toBe(false)
    expect(useChatStore.getState().chatTabs).toEqual({})
    expect(migrateLegacyChatStateForVerifiedAccount('alice')).toBe(true)
    expect(useChatStore.getState().activeTabId).toBe('retained')
    expect(useChatStore.getState().chatTabs.retained.config.inputText).toBe('unsent draft')
    expect(localStorage.getItem('chat-store')).toBeNull()
    expect(localStorage.getItem('chat-store:owner:user:alice')).toContain('alice-history')
  })

  it('can retain anonymous legacy layouts after single-user mode is verified', async () => {
    localStorage.setItem('chat-store', JSON.stringify({ state: {
      chatTabs: { local: { tabId: 'local', sessionId: 'local-chat', name: 'Local conversation' } }, activeTabId: 'local',
    }, version: 0 }))
    const { useChatStore, migrateLegacyChatStateForVerifiedAccount } = await import('./useChatStore')
    expect(useChatStore.getState().chatTabs).toEqual({})
    expect(migrateLegacyChatStateForVerifiedAccount(null)).toBe(true)
    expect(useChatStore.getState().activeTabId).toBe('local')
  })

  it('does not undo new tabs or another account while a close awaits session stop', async () => {
    const { useChatStore, switchChatAccount } = await import('./useChatStore')
    const { agentApi } = await import('../services/api')
    let resolveStop!: () => void
    vi.spyOn(agentApi, 'stopSession').mockImplementation(() => new Promise(resolve => { resolveStop = () => resolve({} as never) }))
    switchChatAccount('alice')
    const closing = await useChatStore.getState().createChatTab('Closing', { mode: 'workflow', presetQueryId: 'closing' }, 'closing-session')
    // A background-only/idle foreground chat still needs a terminal stop.
    expect(useChatStore.getState().chatTabs[closing].isStreaming).toBeFalsy()
    const pending = useChatStore.getState().closeTab(closing)
    const newTab = await useChatStore.getState().createChatTab('New', { mode: 'workflow', presetQueryId: 'new' }, 'new-session')
    resolveStop()
    await pending
    expect(useChatStore.getState().chatTabs[newTab]).toBeDefined()
    // Dismissing a read-only view must leave the underlying session running.
    const view = await useChatStore.getState().createChatTab('View', { mode: 'workflow', isViewOnly: true }, 'view-session')
    const callsBeforeViewClose = vi.mocked(agentApi.stopSession).mock.calls.length
    await useChatStore.getState().closeTab(view, false)
    expect(vi.mocked(agentApi.stopSession).mock.calls.length).toBe(callsBeforeViewClose)
    useChatStore.getState().setTabStreaming(newTab, true)
    const oldAccountClose = useChatStore.getState().closeTab(newTab)
    switchChatAccount('bob')
    const bobTab = await useChatStore.getState().createChatTab('Bob', { mode: 'workflow' }, 'bob-session')
    resolveStop()
    await oldAccountClose
    expect(Object.keys(useChatStore.getState().chatTabs)).toEqual([bobTab])
  })

  it('discards old-account monitor responses without clearing the new account request', async () => {
    const { useChatStore, switchChatAccount } = await import('./useChatStore')
    const { agentApi } = await import('../services/api')
    let resolveOld!: () => void
    let resolveNew!: () => void
    const fetch = vi.spyOn(agentApi, 'getHeaderSummary')
      .mockImplementationOnce(() => new Promise(resolve => { resolveOld = () => resolve({ active_sessions: [{ session_id: 'alice-private' }] } as never) }))
      .mockImplementationOnce(() => new Promise(resolve => { resolveNew = () => resolve({ active_sessions: [{ session_id: 'bob-private' }] } as never) }))
    switchChatAccount('alice')
    const old = useChatStore.getState().getActiveSessions(true)
    switchChatAccount('bob')
    const current = useChatStore.getState().getActiveSessions(true)
    resolveOld()
    expect(await old).toEqual([])
    const shared = useChatStore.getState().getActiveSessions(true)
    expect(fetch).toHaveBeenCalledTimes(2)
    resolveNew()
    await Promise.all([current, shared])
    expect(useChatStore.getState().activeSessionsCache.map(session => session.session_id)).toEqual(['bob-private'])
  })

})
