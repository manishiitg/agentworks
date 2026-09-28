import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
// Persisted stores need a Storage before they are created at import time.
// Node < 25 has no global localStorage (CI runs Node 20), so provide one.
vi.hoisted(() => {
  const memory = new Map<string, string>()
  const storage = { getItem: (k: string) => memory.get(k) ?? null, setItem: (k: string, v: string) => { memory.set(k, String(v)) }, removeItem: (k: string) => { memory.delete(k) }, clear: () => memory.clear(), key: (i: number) => [...memory.keys()][i] ?? null, get length() { return memory.size } }
  if (typeof globalThis.localStorage === 'undefined') Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true })
  if (typeof globalThis.sessionStorage === 'undefined') Object.defineProperty(globalThis, 'sessionStorage', { value: storage, configurable: true })
})
const activateTab = vi.hoisted(() => vi.fn(() => true))
vi.mock('./activateTab', () => ({ activateTab }))
const openCanonicalActivitySession = vi.hoisted(() => vi.fn(async () => {}))
vi.mock('./workflowSessionRestore', async importOriginal => {
  const actual = await importOriginal<typeof import('./workflowSessionRestore')>()
  return { ...actual, openCanonicalActivitySession }
})
const openWorkAutomationRunChat = vi.hoisted(() => vi.fn(async () => {}))
vi.mock('../products/work/workAutomationRunRestore', () => ({ openWorkAutomationRunChat }))
import type { ActiveSessionInfo } from '../services/api-types'
import { useChatStore, type ChatTab } from '../stores/useChatStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import {
  isWorkProductSession,
  openGlobalActivitySession,
  openGlobalTab,
  workProjectIdForSession,
  workProjectIdForTab,
} from './globalProductNavigation'

const workTab = {
  tabId: 'crew-tab',
  name: 'Crew chat',
  sessionId: 'crew-session',
  metadata: {
    mode: 'multi-agent',
    agentProfileId: 'work',
    agentProfileProjectId: 'project-1',
    agentProfileConversationKey: 'project-1:crew-session',
  },
} as ChatTab

const session = (overrides: Partial<ActiveSessionInfo>): ActiveSessionInfo => ({
  session_id: 'session-1',
  observer_id: '',
  agent_mode: 'multi-agent',
  status: 'running',
  created_at: '',
  last_activity: '',
  ...overrides,
})

beforeEach(() => {
  useProductSurfaceStore.persist.setOptions({
    storage: { getItem: () => null, setItem: () => {}, removeItem: () => {} },
  })
})

afterEach(() => {
  useProductSurfaceStore.setState({
    productSurface: 'agentworks',
    selectedWorkProjectId: null,
    pendingWorkView: null,
  })
  useChatStore.setState({ chatTabs: {}, activeTabId: null, toasts: [] })
  activateTab.mockClear()
  openCanonicalActivitySession.mockClear()
  openWorkAutomationRunChat.mockClear()
})

describe('global AgentWorks and Crew navigation', () => {
  it('recognizes public and physical Crew sessions without admitting other products', () => {
    expect(isWorkProductSession(session({ session_id: 'work:project:project-1' }))).toBe(true)
    expect(isWorkProductSession(session({ workspace_path: '_users/user-1/Chats/Work/projects/demo' }))).toBe(true)
    expect(isWorkProductSession(session({ workspace_path: 'Chats/Video Studio/projects/demo' }))).toBe(false)
  })

  it('resolves stable Crew project identity from tabs and sessions', () => {
    expect(workProjectIdForTab(workTab)).toBe('project-1')
    expect(workProjectIdForSession(session({ session_id: 'work:project:project-2' }))).toBe('project-2')
    expect(workProjectIdForSession(session({ preset_query_id: 'project-3' }))).toBe('project-3')
  })

  it('switches surfaces and selects the Crew project before activating its tab', () => {
    useChatStore.setState({ chatTabs: { [workTab.tabId]: workTab } })

    expect(openGlobalTab(workTab.tabId)).toBe(true)
    expect(useProductSurfaceStore.getState().productSurface).toBe('work')
    expect(useProductSurfaceStore.getState().selectedWorkProjectId).toBe('project-1')
    expect(activateTab).toHaveBeenCalledWith(workTab.tabId)
  })

  it('opens a Crew schedule run in a read-only run tab, not the Work schedules view', async () => {
    const trigger = session({
      session_id: 'schedule-cron--a2e1a916_1789915434926975000',
      agent_mode: 'workflow_phase',
      workspace_path: '_users/default/Chats/Work/projects/news-monitor-a2e1a916',
      triggered_by: 'cron',
    })
    await openGlobalActivitySession(trigger, { source: 'global-activity-monitor' })

    expect(openCanonicalActivitySession).toHaveBeenCalledWith(trigger, { source: 'global-activity-monitor' })
    expect(useProductSurfaceStore.getState().productSurface).toBe('agentworks')
    expect(useProductSurfaceStore.getState().selectedWorkProjectId).toBeNull()
    expect(useProductSurfaceStore.getState().pendingWorkView).toBeNull()
  })

  it('opens a Crew product-schedule run in a Crew run tab, not AgentWorks restore', async () => {
    const trigger = session({
      session_id: 'product-af49f7f6-151d-4f94-9578-acd2c155e1a0',
      agent_mode: 'multi-agent',
      workspace_path: 'Chats/Work/projects/news-monitor',
      triggered_by: 'webhook',
      title: 'News Monitor · news monitor trigger · 2026-09-20 21:52',
    })
    await openGlobalActivitySession(trigger, { title: trigger.title, source: 'global-activity-monitor' })

    expect(openWorkAutomationRunChat).toHaveBeenCalledWith(trigger, { title: trigger.title })
    expect(openCanonicalActivitySession).not.toHaveBeenCalled()
  })

  it('toasts instead of rejecting when the open fails', async () => {
    openCanonicalActivitySession.mockRejectedValueOnce(new Error('boom'))
    await openGlobalActivitySession(session({ session_id: 'plain-chat' }))

    const toasts = useChatStore.getState().toasts
    expect(toasts[toasts.length - 1]).toMatchObject({ type: 'error' })
  })

  it('keeps routing interactive Crew sessions to the Work surface', async () => {
    await openGlobalActivitySession(session({
      session_id: 'work:project:project-4',
    }))

    expect(useProductSurfaceStore.getState().productSurface).toBe('work')
    expect(useProductSurfaceStore.getState().selectedWorkProjectId).toBe('project-4')
    expect(useProductSurfaceStore.getState().pendingWorkView).toBeNull()
    expect(openCanonicalActivitySession).not.toHaveBeenCalled()
  })
})
