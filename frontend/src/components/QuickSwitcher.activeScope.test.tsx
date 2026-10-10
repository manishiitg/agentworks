// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

// Persisted stores need a Storage before they are created at import time.
vi.hoisted(() => {
  const memory = new Map<string, string>()
  const storage = { getItem: (k: string) => memory.get(k) ?? null, setItem: (k: string, v: string) => { memory.set(k, String(v)) }, removeItem: (k: string) => { memory.delete(k) }, clear: () => memory.clear(), key: (i: number) => [...memory.keys()][i] ?? null, get length() { return memory.size } }
  Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true })
  Object.defineProperty(globalThis, 'sessionStorage', { value: storage, configurable: true })
})

vi.mock('../products/work/workSessions', () => ({
  loadWorkSessionsIncludingShared: vi.fn(async () => [
    { id: 'crew-own', title: 'project-a-flow-tester', identity: { name: 'project-a Flow Tester' } },
    { id: 'crew-shared', title: 'qa', identity: { name: 'QA Bot' }, shared: { ownerId: 'u2', ownerUsername: 'yoav' } },
  ]),
}))

// llm-config-api resolves the API base URL at import time; stub it so this
// component test does not depend on the service modules' init order.
vi.mock('../services/llm-config-api', () => {
  const service = new Proxy({}, { get: () => vi.fn(async () => ({})) })
  return { llmConfigService: service, default: service }
})

import QuickSwitcher from './QuickSwitcher'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { useChatStore } from '../stores/useChatStore'
import type { ActiveSessionInfo } from '../services/api-types'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(fn => fn()) })

const now = new Date().toISOString()
const crewRun: ActiveSessionInfo = {
  session_id: 'work:project:crew-own:trigger:abc', observer_id: '', agent_mode: 'multi-agent', status: 'running',
  last_activity: now, created_at: now,
  title: 'SDE · Called by project-a Flow Tester', triggered_by: 'webhook', triggered_by_label: 'Called by project-a Flow Tester',
  workspace_path: 'Chats/Work/projects/gptlive1-cef0edb2',
}

it('@active lists every running session with what started it, even one with an open tab', async () => {
  useGlobalPresetStore.setState({ workflowPresetsLoaded: true, workflowPresets: [] })
  const tabId = await useChatStore.getState().createChatTab('SDE · Called by project-a Flow Tester', {
    agentProfileId: 'work', agentProfileProjectId: 'crew-own', isViewOnly: true, isScheduledRun: true,
  } as never, crewRun.session_id)
  useChatStore.setState({ activeSessionsCache: [crewRun] } as never)
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanups.push(() => { act(() => root.unmount()); host.remove(); useChatStore.getState().closeTab(tabId) })
  await act(async () => { root.render(<QuickSwitcher isOpen onClose={vi.fn()} initialQuery="@active " />) })
  await act(async () => { await Promise.resolve() })
  expect(host.textContent).toContain('SDE')
  expect(host.textContent).toContain('Called by project-a Flow Tester')
  expect(host.textContent).not.toContain('SDE · Called by project-a Flow Tester')
})
