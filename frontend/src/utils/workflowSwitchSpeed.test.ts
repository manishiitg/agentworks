// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// Persisted stores bind their storage when first imported.
vi.hoisted(() => {
  const data = new Map<string, string>()
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => data.get(key) ?? null,
      setItem: (key: string, value: string) => { data.set(key, value) },
      removeItem: (key: string) => { data.delete(key) },
      clear: () => data.clear(),
      key: () => null,
      length: 0,
    },
  })
})
const listRunningWorkflows = vi.hoisted(() => vi.fn())
const getWorkflowBuilderSessions = vi.hoisted(() => vi.fn())
vi.mock('../hooks/useWorkflowBuilderBindings', () => ({ currentWorkflowBuilderBinding: () => undefined, publishWorkflowBuilderBinding: vi.fn(), workflowBuilderMutation: () => 0 }))
vi.mock('../services/api', async importOriginal => {
  const actual = await importOriginal<typeof import('../services/api')>()
  return { ...actual, agentApi: { ...actual.agentApi, listRunningWorkflows, getWorkflowBuilderSessions } }
})

import type { ActiveSessionInfo, PollingEvent, WorkflowBuilderSessionResponse } from '../services/api-types'
import { useChatStore, type ChatTab } from '../stores/useChatStore'
import type { CustomPreset } from '../types/preset'
import { openWorkflowPresetPage } from './workflowSessionRestore'
import { resetWorkflowNavigationForTests } from './workflowNavigation'

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(r => { resolve = r })
  return { promise, resolve }
}

const preset = {
  id: 'workflow-a',
  label: 'Workflow A',
  selectedFolder: { filepath: 'Workflow/a' },
} as unknown as CustomPreset

function builderTab(overrides: Partial<ChatTab> = {}): ChatTab {
  return {
    tabId: 'chat-a',
    name: 'Automation Builder',
    sessionId: 'session-a',
    isStreaming: false,
    isCompleted: true,
    hasRunningBgAgents: false,
    isSyntheticTurn: false,
    canSteer: false,
    hideToolCalls: false,
    viewMode: 'formatted',
    config: {} as ChatTab['config'],
    createdAt: 1,
    lastAccessedAt: 1,
    lastViewedEventCount: 0,
    lastViewedEventCounts: { micro: 0 },
    metadata: { mode: 'workflow', phaseId: 'workflow-builder', presetQueryId: 'workflow-a' },
    ...overrides,
  }
}

const otherTab = builderTab({
  tabId: 'chat-b',
  sessionId: 'session-b',
  metadata: { mode: 'workflow', phaseId: 'workflow-builder', presetQueryId: 'workflow-b' },
})

let bindings: ReturnType<typeof deferred<{success:boolean; bindings:Record<string, WorkflowBuilderSessionResponse>}>>
let activeSessions: ReturnType<typeof deferred<ActiveSessionInfo[]>>
let running: ReturnType<typeof deferred<{ running: [] }>>

beforeEach(() => {
  resetWorkflowNavigationForTests()
  bindings = deferred<{success:boolean; bindings:Record<string, WorkflowBuilderSessionResponse>}>()
  getWorkflowBuilderSessions.mockReturnValue(bindings.promise)
  activeSessions = deferred<ActiveSessionInfo[]>()
  running = deferred<{ running: [] }>()
  listRunningWorkflows.mockReturnValue(running.promise)
  useChatStore.setState({
    getActiveSessions: vi.fn(() => activeSessions.promise),
  })
})

afterEach(() => {
  useChatStore.setState({ chatTabs: {}, activeTabId: null, tabEvents: {} })
  listRunningWorkflows.mockReset()
  getWorkflowBuilderSessions.mockReset()
})

describe('switching to a workflow', () => {
  it('waits for the server binding before choosing a cached workflow chat', async () => {
    const cached = builderTab()
    useChatStore.setState({
      chatTabs: { [cached.tabId]: cached, [otherTab.tabId]: otherTab },
      activeTabId: otherTab.tabId,
      tabEvents: { 'session-a': [{ id: 'e1' } as PollingEvent] },
    })

    const opening = openWorkflowPresetPage(preset)

    // Cached timestamp/tab order is not authority to select a Builder.
    expect(useChatStore.getState().activeTabId).toBe(otherTab.tabId)
    bindings.resolve({success:true, bindings:{'workflow-a':{success:true, is_builder:true, preset_query_id:'workflow-a', session_id:'session-a', source:'live', status:'completed', events:[]}}})
    await opening
    expect(useChatStore.getState().activeTabId).toBe(cached.tabId)
  })

  it('reads the canonical binding without racing legacy running-session lists', async () => {
    const cached = builderTab()
    useChatStore.setState({
      chatTabs: { [cached.tabId]: cached },
      activeTabId: null,
      tabEvents: { 'session-a': [{ id: 'e1' } as PollingEvent] },
    })

    const opening = openWorkflowPresetPage(preset)
    await Promise.resolve()

    expect(getWorkflowBuilderSessions).toHaveBeenCalledExactlyOnceWith([{preset_query_id:'workflow-a', workspace_path:'Workflow/a'}])
    expect(listRunningWorkflows).not.toHaveBeenCalled()
    expect(useChatStore.getState().getActiveSessions).not.toHaveBeenCalled()
    bindings.resolve({success:true, bindings:{}})
    await opening
  })

  it('does not pick an empty remembered tab without a server binding', async () => {
    const empty = builderTab()
    useChatStore.setState({
      chatTabs: { [empty.tabId]: empty, [otherTab.tabId]: otherTab },
      activeTabId: otherTab.tabId,
      tabEvents: {},
    })

    const opening = openWorkflowPresetPage(preset)
    expect(useChatStore.getState().activeTabId).toBe(otherTab.tabId)

    bindings.resolve({success:true, bindings:{}})
    await opening
    expect(useChatStore.getState().activeTabId).toBe(otherTab.tabId)
  })
})
