// @vitest-environment happy-dom
import { afterAll, afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { ChatTab } from '../stores/useChatStore'
import type { WorkflowBuilderSessionResponse } from '../services/api-types'

vi.hoisted(() => vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => {}, removeItem: () => {} }))
const bindings = vi.hoisted(() => new Map<string, WorkflowBuilderSessionResponse>())
const getWorkflowBuilderSessions = vi.hoisted(() => vi.fn())
vi.mock('../hooks/useWorkflowBuilderBindings', () => ({
  currentWorkflowBuilderBinding: (id: string) => bindings.get(id),
  publishWorkflowBuilderBinding: vi.fn(),
  workflowBuilderMutation: () => 0,
}))
vi.mock('../services/api', async importOriginal => {
  const actual = await importOriginal<typeof import('../services/api')>()
  return { ...actual, agentApi: { ...actual.agentApi, getWorkflowBuilderSessions } }
})
import { useChatStore } from '../stores/useChatStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { ensureWorkflowBuilderTab } from './ensureWorkflowBuilderTab'

function tab(id: string): ChatTab {
  return {
    tabId: `tab-${id}`, name: 'Automation Builder', sessionId: `session-${id}`,
    createdAt: 1, isStreaming: false, config: {},
    metadata: { mode: 'workflow', presetQueryId: id, phaseId: 'workflow-builder' },
  } as ChatTab
}

beforeEach(() => { bindings.clear(); getWorkflowBuilderSessions.mockReset() })
afterAll(() => vi.unstubAllGlobals())
afterEach(() => {
  useChatStore.setState({ chatTabs: {}, activeTabId: null })
  vi.restoreAllMocks()
})

it('does not reactivate the previous chat when both workflows are already cached', async () => {
  const old = tab('old'), selected = tab('selected')
  useChatStore.setState({ chatTabs: { [old.tabId]: old, [selected.tabId]: selected }, activeTabId: old.tabId })
  useGlobalPresetStore.setState(state => ({ activePresetIds: { ...state.activePresetIds, workflow: 'selected' } }))
  bindings.set('selected', { success: true, is_builder: true, preset_query_id: 'selected', session_id: selected.sessionId } as WorkflowBuilderSessionResponse)

  expect(await ensureWorkflowBuilderTab('selected')).toBe(selected.tabId)
  expect(useGlobalPresetStore.getState().activePresetIds.workflow).toBe('selected')
  expect(useChatStore.getState().activeTabId).toBe(old.tabId)
})

it('caches a discovered chat without stealing focus from a newer selection', async () => {
  const newer = tab('newer')
  useChatStore.setState({ chatTabs: { [newer.tabId]: newer }, activeTabId: newer.tabId })
  useGlobalPresetStore.setState(state => ({
    activePresetIds: { ...state.activePresetIds, workflow: 'newer' },
    workflowPresets: [{ id: 'pending', label: 'Pending', selectedFolder: { filepath: 'Workflow/pending' } }] as typeof state.workflowPresets,
  }))
  let finish!: (value: unknown) => void
  getWorkflowBuilderSessions.mockReturnValue(new Promise(resolve => { finish = resolve }))
  const create = vi.spyOn(useChatStore.getState(), 'createChatTab').mockResolvedValue('discovered-tab')

  const pending = ensureWorkflowBuilderTab('pending')
  finish({ bindings: { pending: { session_id: 'discovered-session' } } })
  expect(await pending).toBe('discovered-tab')
  expect(create).toHaveBeenCalledWith('Automation Builder', expect.objectContaining({ presetQueryId: 'pending' }), 'discovered-session', { activate: false })
  expect(useChatStore.getState().activeTabId).toBe(newer.tabId)
  expect(useGlobalPresetStore.getState().activePresetIds.workflow).toBe('newer')
})
