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
vi.mock('../stores/useProductSurfaceStore', () => ({ useProductSurfaceStore: { getState: () => ({ productSurface: 'work' }) } }))
vi.mock('./activateTab', () => ({ activateTab: mocks.activate }))
vi.mock('./chatIdentity', () => ({ captureChatIdentity: () => 1, isChatIdentityCurrent: () => mocks.current }))
import { canCloseWorkspaceChatTab, closeWorkspaceChatTab } from './closeWorkspaceChatTab'
const tab = (id: string, metadata: ChatTab['metadata'], patch: Partial<ChatTab> = {}) => ({ tabId: id, sessionId: id, createdAt: 1, metadata, ...patch }) as ChatTab
const primary = tab('primary', { agentProfileId: 'work', agentProfileProjectId: 'p', agentProfileConversationKey: 'p' })
const side = tab('side', { agentProfileId: 'work', agentProfileProjectId: 'p', agentProfileConversationKey: 'p:chat:side' })
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
it('lets the server release an idle chat and close its key before removing the tab', async () => {
  setup()
  await closeWorkspaceChatTab('side')
  expect(mocks.stop).not.toHaveBeenCalled()
  expect(mocks.closeSide).toHaveBeenCalledWith('work', 'p:chat:side')
  expect(mocks.state.closeTab).toHaveBeenCalledWith('side', false)
  expect(mocks.activate).toHaveBeenCalledWith('primary')
})
it('shows the server busy refusal and keeps the chat open without stopping it', async () => {
  setup()
  mocks.state.activeSessionsCache = [{ session_id: 'side', status: 'running' }]
  mocks.closeSide.mockRejectedValueOnce({ response: { status: 409, data: { error: 'This agent is still working. Stop it before closing.' } } })
  await closeWorkspaceChatTab('side')
  expect(mocks.closeSide).toHaveBeenCalledWith('work', 'p:chat:side')
  expect(mocks.state.addToast).toHaveBeenCalledWith('This agent is still working. Stop it before closing.', 'error')
  expect(mocks.stop).not.toHaveBeenCalled()
  expect(mocks.state.closeTab).not.toHaveBeenCalled()
  expect(mocks.activate).not.toHaveBeenCalled()
})
it('does not use stale browser activity to overrule a successful server close', async () => {
  setup({ ...side, isStreaming: true, hasRunningBgAgents: true })
  mocks.state.activeSessionsCache = [{ session_id: 'side', status: 'running' }]
  await closeWorkspaceChatTab('side')
  expect(mocks.closeSide).toHaveBeenCalledWith('work', 'p:chat:side')
  expect(mocks.stop).not.toHaveBeenCalled()
  expect(mocks.state.closeTab).toHaveBeenCalledWith('side', false)
})
it.each([400, 403, 404])('preserves the local tab and shows a server refusal with status %s', async status => {
  setup()
  mocks.closeSide.mockRejectedValueOnce({ response: { status, data: { error: 'This agent cannot be closed.' } } })
  await closeWorkspaceChatTab('side')
  expect(mocks.state.addToast).toHaveBeenCalledWith('This agent cannot be closed.', 'error')
  expect(mocks.state.closeTab).not.toHaveBeenCalled()
})
it('abandons local changes if the account changes while the server closes the chat', async () => {
  setup()
  mocks.closeSide.mockImplementationOnce(async () => { mocks.current = false })
  await closeWorkspaceChatTab('side')
  expect(mocks.closeSide).toHaveBeenCalled()
  expect(mocks.state.closeTab).not.toHaveBeenCalled()
  expect(mocks.activate).not.toHaveBeenCalled()
})
it('does not show a previous account’s refusal after identity changes', async () => {
  setup()
  mocks.closeSide.mockImplementationOnce(async () => {
    mocks.current = false
    throw { response: { status: 403, data: { error: 'Private previous account error' } } }
  })
  await closeWorkspaceChatTab('side')
  expect(mocks.state.addToast).not.toHaveBeenCalled()
  expect(mocks.state.closeTab).not.toHaveBeenCalled()
})

it('keeps another owner’s named tab visible without allowing Close to control it', async () => {
  const observed = tab('observed', { agentProfileId: 'work', agentProfileProjectId: 'p', agentProfileConversationKey: 'p:chat:observed', agentProfileTabCanWrite: false })
  setup(observed)
  expect(canCloseWorkspaceChatTab(observed, mocks.state.chatTabs)).toBe(false)
  await closeWorkspaceChatTab(observed.tabId)
  expect(mocks.closeSide).not.toHaveBeenCalled()
  expect(mocks.state.closeTab).not.toHaveBeenCalled()
})
