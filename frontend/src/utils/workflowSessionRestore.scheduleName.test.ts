// @vitest-environment happy-dom
import { beforeEach, expect, it, vi } from 'vitest'

vi.hoisted(() => {
  const memory = new Map<string, string>()
  const storage = {
    getItem: (key: string) => memory.get(key) ?? null,
    setItem: (key: string, value: string) => { memory.set(key, value) },
    removeItem: (key: string) => { memory.delete(key) },
  }
  Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true })
})
vi.mock('./executionConversationRestore', () => ({ hydrateExecutionConversation: vi.fn(async () => ({})) }))
vi.mock('./workflowNavigation', () => ({
  activateWorkflowTab: vi.fn(() => true), beginWorkflowNavigation: vi.fn(() => 1),
  isCurrentWorkflowNavigation: vi.fn(() => true), selectWorkflowPreset: vi.fn(),
}))

import type { ActiveSessionInfo } from '../services/api-types'
import { useChatStore, type ChatTab } from '../stores/useChatStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { openCanonicalActivitySession } from './workflowSessionRestore'

const scheduleName = 'Test and Promote (Mon-Fri 8:30 AM IST)'
const run: ActiveSessionInfo = {
  session_id: 'schedule-cron--abc12345_123456', observer_id: '',
  agent_mode: 'workflow', status: 'completed', created_at: '', last_activity: '',
  preset_query_id: 'trading', preset_name: 'trading', triggered_by: 'cron',
  title: scheduleName,
}

beforeEach(() => {
  useGlobalPresetStore.setState(state => ({
    workflowPresets: [{ id: 'trading', label: 'trading' }] as typeof state.workflowPresets,
    activePresetIds: { ...state.activePresetIds, workflow: 'trading' },
  }))
  useChatStore.setState({
    chatTabs: { run: {
      tabId: 'run', name: scheduleName, sessionId: run.session_id,
      metadata: { mode: 'workflow', presetQueryId: 'trading', isScheduledRun: true,
        isViewOnly: true, isExecutionRun: true, scheduledJobName: scheduleName },
    } as ChatTab },
    tabEvents: {}, tabEventIndices: {},
  })
})

it.each(['quick-switcher', 'global-activity-monitor'])('keeps the schedule label when opened from %s with the workflow display title', async source => {
  await openCanonicalActivitySession(run, { source, title: 'trading' })
  const tab = useChatStore.getState().chatTabs.run
  expect(tab.name).toBe(scheduleName)
  expect(tab.metadata?.scheduledJobName).toBe(scheduleName)
  expect(Object.keys(useChatStore.getState().chatTabs)).toEqual(['run'])
})

it('retains the known schedule name when a sparse activity row has no title', async () => {
  await openCanonicalActivitySession({ ...run, title: undefined }, { source: 'quick-switcher', title: 'trading' })
  expect(useChatStore.getState().chatTabs.run.name).toBe(scheduleName)
  expect(useChatStore.getState().chatTabs.run.metadata?.scheduledJobName).toBe(scheduleName)
})

it('repairs a previously overwritten label using the scheduler title', async () => {
  useChatStore.getState().renameTab('run', 'trading')
  useChatStore.getState().setTabMetadata('run', { scheduledJobName: 'trading' })
  await openCanonicalActivitySession(run, { source: 'quick-switcher' })
  expect(useChatStore.getState().chatTabs.run.name).toBe(scheduleName)
  expect(useChatStore.getState().chatTabs.run.metadata?.scheduledJobName).toBe(scheduleName)
})
