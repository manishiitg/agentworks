import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
// Persisted stores need a Storage before they are created at import time.
// Node < 25 has no global localStorage (CI runs Node 20), so provide one.
vi.hoisted(() => {
  const memory = new Map<string, string>()
  const storage = { getItem: (k: string) => memory.get(k) ?? null, setItem: (k: string, v: string) => { memory.set(k, String(v)) }, removeItem: (k: string) => { memory.delete(k) }, clear: () => memory.clear(), key: (i: number) => [...memory.keys()][i] ?? null, get length() { return memory.size } }
  if (typeof globalThis.localStorage === 'undefined') Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true })
  if (typeof globalThis.sessionStorage === 'undefined') Object.defineProperty(globalThis, 'sessionStorage', { value: storage, configurable: true })
})

const resolveAgentProfileConversation = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({
  agentApi: { resolveAgentProfileConversation },
  getApiBaseUrl: () => '',
  getAuthToken: () => null,
}))

const loadWorkSessions = vi.hoisted(() => vi.fn())
vi.mock('./workSessions', async importOriginal => {
  const actual = await importOriginal<typeof import('./workSessions')>()
  return { ...actual, loadWorkSessions }
})

const hydrateExecutionConversation = vi.hoisted(() => vi.fn())
vi.mock('../../utils/executionConversationRestore', () => ({ hydrateExecutionConversation }))

import type { ActiveSessionInfo } from '../../services/api-types'
import { useAppStore } from '../../stores/useAppStore'
import { useChatStore } from '../../stores/useChatStore'
import { useModeStore } from '../../stores/useModeStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { openWorkAutomationRunChat } from './workAutomationRunRestore'
import type { WorkSession } from './workSessions'

const project = {
  id: 'news-id',
  title: 'News Monitor',
  workspacePath: '_users/default/Chats/Work/projects/news-monitor',
  identity: { name: 'News', icon: 'N' },
  selectedServers: [],
  selectedSkills: [],
} as unknown as WorkSession

const runSession = (overrides: Partial<ActiveSessionInfo> = {}): ActiveSessionInfo => ({
  session_id: 'product-af49f7f6-151d-4f94-9578-acd2c155e1a0',
  observer_id: '',
  agent_mode: 'multi-agent',
  status: 'running',
  created_at: '',
  last_activity: '',
  title: 'News Monitor · news monitor trigger · 2026-09-20 21:52',
  workspace_path: 'Chats/Work/projects/news-monitor',
  triggered_by: 'webhook',
  ...overrides,
})

beforeEach(() => {
  useProductSurfaceStore.persist.setOptions({
    storage: { getItem: () => null, setItem: () => {}, removeItem: () => {} },
  })
  useAppStore.persist.setOptions({
    storage: { getItem: () => null, setItem: () => {}, removeItem: () => {} },
  })
  useModeStore.persist.setOptions({
    storage: { getItem: () => null, setItem: () => {}, removeItem: () => {} },
  })
  const storageMap = new Map<string, string>()
  const storage = {
    getItem: (key: string) => storageMap.get(key) ?? null,
    setItem: (key: string, value: string) => { storageMap.set(key, value) },
    removeItem: (key: string) => { storageMap.delete(key) },
  }
  vi.stubGlobal('localStorage', storage)
  vi.stubGlobal('window', { dispatchEvent: vi.fn(), localStorage: storage })
  vi.stubGlobal('CustomEvent', function (this: { type: string }, type: string) { this.type = type })
  loadWorkSessions.mockResolvedValue([project])
  resolveAgentProfileConversation.mockResolvedValue({
    session_id: 'crew-session',
    conversation_key: 'news-id',
    conversation_id: 'conv-1',
  })
  hydrateExecutionConversation.mockResolvedValue({ status: 'running' })
})

afterEach(() => {
  useProductSurfaceStore.setState({
    productSurface: 'agentworks',
    selectedWorkProjectId: null,
    pendingWorkView: null,
  })
  useChatStore.setState({ chatTabs: {}, activeTabId: null, toasts: [] })
  vi.unstubAllGlobals()
  vi.clearAllMocks()
})

describe('openWorkAutomationRunChat', () => {
  it('opens the trigger run in its own read-only tab beside the crew chat', async () => {
    await openWorkAutomationRunChat(runSession(), { title: 'News Monitor · news monitor trigger' })

    expect(useProductSurfaceStore.getState().productSurface).toBe('work')
    expect(useProductSurfaceStore.getState().selectedWorkProjectId).toBe('news-id')
    expect(useProductSurfaceStore.getState().pendingWorkView).toBeNull()

    const tabs = Object.values(useChatStore.getState().chatTabs)
    const canonical = tabs.find(tab => tab.metadata?.isViewOnly !== true)
    expect(canonical?.name).toBe('Chat')
    expect(canonical?.sessionId).toBe('crew-session')
    expect(canonical?.metadata?.agentProfileId).toBe('work')
    expect(canonical?.metadata?.agentProfileProjectId).toBe('news-id')

    const runTab = tabs.find(tab => tab.metadata?.isViewOnly === true)
    expect(runTab?.sessionId).toBe('product-af49f7f6-151d-4f94-9578-acd2c155e1a0')
    expect(runTab?.name?.startsWith('News Monit')).toBe(true)
    expect(runTab?.metadata?.agentProfileId).toBe('work')
    expect(runTab?.metadata?.agentProfileProjectId).toBe('news-id')
    expect(runTab?.metadata?.agentProfileConversationKey)
      .toBe('news-id:history:product-af49f7f6-151d-4f94-9578-acd2c155e1a0')
    expect(runTab?.isStreaming).toBe(true)

    expect(hydrateExecutionConversation).toHaveBeenCalledWith(
      'product-af49f7f6-151d-4f94-9578-acd2c155e1a0',
      'Chats/Work/projects/news-monitor',
    )
    expect(useChatStore.getState().activeTabId).toBe(runTab?.tabId)
  })

  it('matches a physical project workspace against a public session workspace', async () => {
    await openWorkAutomationRunChat(runSession({
      workspace_path: '_users/default/Chats/Work/projects/news-monitor/',
    }))

    expect(useProductSurfaceStore.getState().selectedWorkProjectId).toBe('news-id')
  })

  it('reuses the run tab instead of opening a duplicate', async () => {
    await openWorkAutomationRunChat(runSession())
    await openWorkAutomationRunChat(runSession())

    const runTabs = Object.values(useChatStore.getState().chatTabs)
      .filter(tab => tab.sessionId === 'product-af49f7f6-151d-4f94-9578-acd2c155e1a0')
    expect(runTabs).toHaveLength(1)
    expect(runTabs[0]?.metadata?.isViewOnly).toBe(true)
  })

  it('throws when no loaded project owns the run workspace', async () => {
    await expect(openWorkAutomationRunChat(runSession({
      workspace_path: 'Chats/Work/projects/deleted-project',
    }))).rejects.toThrow('no longer available')
    expect(useProductSurfaceStore.getState().productSurface).toBe('agentworks')
  })
})
