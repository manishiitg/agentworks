import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ChatTab } from '../stores/useChatStore'

type ChatStoreModule = typeof import('../stores/useChatStore')
type GlobalPresetStoreModule = typeof import('../stores/useGlobalPresetStore')
type WorkflowNavigationModule = typeof import('./workflowNavigation')

let useChatStore: ChatStoreModule['useChatStore']
let useGlobalPresetStore: GlobalPresetStoreModule['useGlobalPresetStore']
let navigation: WorkflowNavigationModule

const createMemoryStorage = (): Storage => {
  const values = new Map<string, string>()
  return {
    get length() {
      return values.size
    },
    clear: () => values.clear(),
    getItem: key => values.get(key) ?? null,
    key: index => Array.from(values.keys())[index] ?? null,
    removeItem: key => {
      values.delete(key)
    },
    setItem: (key, value) => {
      values.set(key, value)
    },
  }
}

function workflowTab(workflowId: string, tabId: string, viewMode: ChatTab['viewMode'] = 'terminal'): ChatTab {
  return {
    tabId,
    name: 'Automation Builder',
    sessionId: `session-${workflowId}`,
    isStreaming: false,
    isCompleted: true,
    hasRunningBgAgents: false,
    isSyntheticTurn: false,
    canSteer: false,
    hideToolCalls: false,
    viewMode,
    config: {} as ChatTab['config'],
    createdAt: 1,
    lastViewedEventCount: 0,
    lastViewedEventCounts: { micro: 0 },
    metadata: {
      mode: 'workflow',
      phaseId: 'workflow-builder',
      presetQueryId: workflowId,
    },
  }
}

beforeEach(async () => {
  vi.resetModules()
  const storage = createMemoryStorage()
  vi.stubGlobal('localStorage', storage)
  vi.stubGlobal('window', {
    localStorage: storage,
    location: {
      hostname: 'localhost',
      origin: 'http://localhost',
    },
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })

  ;({ useChatStore } = await import('../stores/useChatStore'))
  ;({ useGlobalPresetStore } = await import('../stores/useGlobalPresetStore'))
  navigation = await import('./workflowNavigation')

  navigation.resetWorkflowNavigationForTests()
  useGlobalPresetStore.setState(state => ({
    activePresetIds: { ...state.activePresetIds, workflow: null },
  }))
  useChatStore.setState({ chatTabs: {}, activeTabId: null })
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('workflow navigation coordinator', () => {
  it('reveals a selected workflow from Providers without losing the session', async () => {
    const { useLLMStore } = await import('../stores/useLLMStore')
    const { useAppStore } = await import('../stores/useAppStore')
    const tab = workflowTab('workflow-a', 'tab-a')
    useChatStore.setState({ chatTabs: { [tab.tabId]: tab } })
    useAppStore.getState().setShowWorkflowsOverview(true)
    useAppStore.getState().setShowSchedulesOverview(true)
    useLLMStore.getState().setShowLLMModal(true)
    expect(navigation.activateWorkflowTab(tab.tabId)).toBe(true)
    expect(useLLMStore.getState().showLLMModal).toBe(false)
    expect(useAppStore.getState().showWorkflowsOverview).toBe(false)
    expect(useAppStore.getState().showSchedulesOverview).toBe(false)
    expect(useChatStore.getState().activeTabId).toBe(tab.tabId)
    expect(useChatStore.getState().chatTabs[tab.tabId].sessionId).toBe(tab.sessionId)
  })

  it('invalidates an older asynchronous navigation when the user selects another workflow', () => {
    const first = navigation.beginWorkflowNavigation('workflow-a')
    navigation.selectWorkflowPreset('workflow-a')
    const second = navigation.beginWorkflowNavigation('workflow-b')
    navigation.selectWorkflowPreset('workflow-b')

    expect(navigation.isCurrentWorkflowNavigation(first, 'workflow-a')).toBe(false)
    expect(navigation.isCurrentWorkflowNavigation(second, 'workflow-b')).toBe(true)
    expect(navigation.getWorkflowNavigationContext().workflowId).toBe('workflow-b')
  })

  it('rejects a delayed workflow activation after leaving through product navigation', async () => {
    const { openProductWorkspace } = await import('./productWorkspaceNavigation')
    const { useProductSurfaceStore } = await import('../stores/useProductSurfaceStore')
    const tab = workflowTab('workflow-a', 'tab-a')
    useChatStore.setState({ chatTabs: { [tab.tabId]: tab } })
    const pending = navigation.beginWorkflowNavigation('workflow-a')
    navigation.selectWorkflowPreset('workflow-a')
    openProductWorkspace('code')
    // Product switches preserve the workflow for a later return, so checking
    // only the active preset cannot detect that this old lookup lost ownership.
    expect(useGlobalPresetStore.getState().activePresetIds.workflow).toBe('workflow-a')
    expect(navigation.isCurrentWorkflowNavigation(pending, 'workflow-a')).toBe(false)
    expect(navigation.activateWorkflowTab(tab.tabId, { expectedGeneration: pending })).toBe(false)
    expect(useProductSurfaceStore.getState().productSurface).toBe('code')
    expect(useChatStore.getState().activeTabId).toBeNull()
  })

  it('commits workflow, tab, session, and the user-selected terminal view together', () => {
    const tabA = workflowTab('workflow-a', 'tab-a')
    const tabB = workflowTab('workflow-b', 'tab-b')
    useChatStore.setState({
      chatTabs: { [tabA.tabId]: tabA, [tabB.tabId]: tabB },
      eventViewModePreference: 'terminal',
    })

    const first = navigation.beginWorkflowNavigation('workflow-a')
    navigation.selectWorkflowPreset('workflow-a')
    expect(navigation.activateWorkflowTab(tabA.tabId, { expectedGeneration: first })).toBe(true)

    const second = navigation.beginWorkflowNavigation('workflow-b')
    navigation.selectWorkflowPreset('workflow-b')
    expect(navigation.activateWorkflowTab(tabB.tabId, { expectedGeneration: second })).toBe(true)

    const state = useChatStore.getState()
    expect(useGlobalPresetStore.getState().activePresetIds.workflow).toBe('workflow-b')
    expect(state.activeTabId).toBe(tabB.tabId)
    expect(state.chatTabs[tabB.tabId].viewMode).toBe('terminal')
    expect(navigation.getWorkflowNavigationContext()).toMatchObject({
      workflowId: 'workflow-b',
      tabId: 'tab-b',
      sessionId: 'session-workflow-b',
      viewMode: 'terminal',
    })
  })

  it('rejects a late tab commit from a stale generation', () => {
    const tabA = workflowTab('workflow-a', 'tab-a')
    const tabB = workflowTab('workflow-b', 'tab-b')
    useChatStore.setState({ chatTabs: { [tabA.tabId]: tabA, [tabB.tabId]: tabB } })

    const stale = navigation.beginWorkflowNavigation('workflow-a')
    navigation.selectWorkflowPreset('workflow-a')
    const current = navigation.beginWorkflowNavigation('workflow-b')
    navigation.selectWorkflowPreset('workflow-b')

    expect(navigation.activateWorkflowTab(tabA.tabId, { expectedGeneration: stale })).toBe(false)
    expect(navigation.activateWorkflowTab(tabB.tabId, { expectedGeneration: current })).toBe(true)
    expect(useChatStore.getState().activeTabId).toBe(tabB.tabId)
  })

  it('a direct schedule/main switch invalidates a pending open within the same workflow', () => {
    const main = workflowTab('workflow-a', 'main')
    const run = { ...workflowTab('workflow-a', 'run'), sessionId: 'schedule-run' }
    useChatStore.setState({ chatTabs: { main, run } })
    const pending = navigation.beginWorkflowNavigation('workflow-a')
    navigation.selectWorkflowPreset('workflow-a')
    expect(navigation.activateWorkflowTab('run')).toBe(true)
    expect(navigation.activateWorkflowTab('main', { expectedGeneration: pending })).toBe(false)
    expect(useChatStore.getState().activeTabId).toBe('run')
    expect(navigation.getWorkflowNavigationContext().sessionId).toBe('schedule-run')
  })
})
