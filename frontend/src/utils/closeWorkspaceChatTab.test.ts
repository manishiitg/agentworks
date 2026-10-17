import { afterEach, expect, it, vi } from 'vitest'
import type { ChatTab } from '../stores/useChatStore'
const mocks = vi.hoisted(() => ({
  state: {} as any,
  current: true,
  stop: vi.fn(async () => {}),
  closeSide: vi.fn(async () => {}),
  activate: vi.fn(),
}))
vi.mock('../services/api', () => ({ agentApi: { stopSession: mocks.stop, closeAgentProfileSideChat: mocks.closeSide } }))
vi.mock('../stores/useChatStore', () => ({ useChatStore: { getState: () => mocks.state } }))
vi.mock('../stores/useProductSurfaceStore', () => ({ useProductSurfaceStore: { getState: () => ({ productSurface: 'code' }) } }))
vi.mock('./activateTab', () => ({ activateTab: mocks.activate }))
vi.mock('./chatIdentity', () => ({ captureChatIdentity: () => 1, isChatIdentityCurrent: () => mocks.current }))
import { canCloseWorkspaceChatTab, closeWorkspaceChatTab } from './closeWorkspaceChatTab'
const tab = (id: string, metadata: ChatTab['metadata'], patch: Partial<ChatTab> = {}) => ({ tabId: id, sessionId: id, createdAt: 1, metadata, ...patch }) as ChatTab
const primary = tab('primary', { agentProfileId: 'code', agentProfileProjectId: 'p', agentProfileConversationKey: 'p' })
const side = tab('side', { agentProfileId: 'code', agentProfileProjectId: 'p', agentProfileConversationKey: 'p:chat:side' })
function setup(target = side) {
  mocks.state = { chatTabs: { primary, [target.tabId]: target }, activeTabId: target.tabId, activeSessionsCache: [], closeTab: vi.fn(async () => {}), addToast: vi.fn() }
}
afterEach(() => { vi.clearAllMocks(); mocks.current = true })
it('protects permanent project and workflow chats', () => {
  expect(canCloseWorkspaceChatTab(primary, { primary, side })).toBe(false)
  const builder = tab('builder', { mode: 'workflow', phaseId: 'workflow-builder', presetQueryId: 'wf' })
  expect(canCloseWorkspaceChatTab(builder, { builder, side })).toBe(false)
})
it('closes a scheduled view without stopping the run and returns to its own builder', async () => {
  const builder = tab('builder', { mode: 'workflow', phaseId: 'workflow-builder', presetQueryId: 'wf' })
  const run = tab('run', { mode: 'workflow', presetQueryId: 'wf', isViewOnly: true, isScheduledRun: true })
  setup(run)
  mocks.state.chatTabs = { builder, run }
  await closeWorkspaceChatTab('run')
  expect(mocks.stop).not.toHaveBeenCalled()
  expect(mocks.state.closeTab).toHaveBeenCalledWith('run', false)
  expect(mocks.activate).toHaveBeenCalledWith('builder')
})
it('stops an idle side chat and closes its server-owned key before removing the tab', async () => {
  setup()
  await closeWorkspaceChatTab('side')
  expect(mocks.stop).toHaveBeenCalledWith('side')
  expect(mocks.closeSide).toHaveBeenCalledWith('code', 'p:chat:side')
  expect(mocks.state.closeTab).toHaveBeenCalledWith('side', false)
  expect(mocks.activate).toHaveBeenCalledWith('primary')
})
it('keeps a working side chat open, including work reported only by the server', async () => {
  setup()
  mocks.state.activeSessionsCache = [{ session_id: 'side', status: 'running' }]
  await closeWorkspaceChatTab('side')
  expect(mocks.state.addToast).toHaveBeenCalled()
  expect(mocks.stop).not.toHaveBeenCalled()
  expect(mocks.state.closeTab).not.toHaveBeenCalled()
})
it('abandons the remaining close operations if the account changes while stopping', async () => {
  setup()
  mocks.stop.mockImplementationOnce(async () => { mocks.current = false })
  await closeWorkspaceChatTab('side')
  expect(mocks.closeSide).not.toHaveBeenCalled()
  expect(mocks.state.closeTab).not.toHaveBeenCalled()
})
