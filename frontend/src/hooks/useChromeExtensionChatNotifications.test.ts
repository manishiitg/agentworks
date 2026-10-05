// @vitest-environment happy-dom
import { beforeEach, expect, it, vi } from 'vitest'
const fixture = vi.hoisted(() => ({
  tab: { tabId: 'code-chat', sessionId: 'code-session', isStreaming: true, metadata: { mode: 'multi-agent', agentProfileId: 'code', agentProfileWorkspace: 'Chats/Code/projects/one' }, config: {} as Record<string, any> },
  get: vi.fn(), activate: vi.fn(), refreshSessions: vi.fn(async () => []),
}))
const store = () => ({
  activeTabId: 'code-chat', getTab: () => fixture.tab, getTabConfig: () => fixture.tab.config,
  setTabConfig: (_: string, update: object) => { fixture.tab.config = { ...fixture.tab.config, ...update } },
  setTabViewMode: vi.fn(), setAutoScroll: vi.fn(), getActiveSessions: fixture.refreshSessions,
})
vi.mock('../services/api', () => ({ default: { get: fixture.get } }))
vi.mock('../stores/useChatStore', () => ({ useChatStore: { getState: () => store() } }))
vi.mock('../stores/useGlobalPresetStore', () => ({ useGlobalPresetStore: { getState: () => ({}) } }))
vi.mock('../stores/useWorkflowStore', () => ({ useWorkflowStore: { getState: () => ({}) } }))
vi.mock('../utils/activateTab', () => ({ activateTab: fixture.activate }))
vi.mock('../utils/workflowNavigation', () => ({ selectWorkflowPreset: vi.fn() }))
import { deliverChromeExtensionChatNotifications } from './useChromeExtensionChatNotifications'
const workspace = 'Chats/Code/projects/one'
const observe = (data: object) => fixture.get.mockResolvedValue({ data })
beforeEach(() => {
  vi.clearAllMocks()
  fixture.tab.sessionId = 'code-session'; fixture.tab.metadata.agentProfileId = 'code'
  fixture.tab.config = { queuedMessages: ['Existing request'], mcpOAuthNotificationIDs: [] }
})
it('uses the real global queue for connect, first share, disconnect and reconnect without replay or interrupting a running turn', async () => {
  observe({ selected: true, connected: true, tabs: 0, connection_id: 'connection-one' })
  await Promise.all([deliverChromeExtensionChatNotifications('code-chat', workspace), deliverChromeExtensionChatNotifications('code-chat', workspace)])
  expect(fixture.tab.config.queuedMessages).toHaveLength(2)
  expect(fixture.tab.config.queuedMessages[1]).toContain('without --cdp')
  expect(fixture.tab.config.queuedMessages[1]).toContain('No tabs are shared yet')
  await deliverChromeExtensionChatNotifications('code-chat', workspace)
  expect(fixture.tab.config.queuedMessages).toHaveLength(2)
  observe({ selected: true, connected: true, tabs: 1, connection_id: 'connection-one' })
  await deliverChromeExtensionChatNotifications('code-chat', workspace)
  expect(fixture.tab.config.queuedMessages[2]).toContain('tab is now shared')
  fixture.get.mockRejectedValueOnce(new Error('network down'))
  await expect(deliverChromeExtensionChatNotifications('code-chat', workspace)).rejects.toThrow('network down')
  expect(fixture.tab.config.queuedMessages).toHaveLength(3)
  observe({ selected: true, connected: false, tabs: 0, connection_id: 'connection-one' })
  await deliverChromeExtensionChatNotifications('code-chat', workspace)
  expect(fixture.tab.config.queuedMessages[3]).toContain('Do not fall back')
  await deliverChromeExtensionChatNotifications('code-chat', workspace)
  expect(fixture.tab.config.queuedMessages).toHaveLength(4)
  observe({ selected: true, connected: true, tabs: 1, connection_id: 'connection-two' })
  await deliverChromeExtensionChatNotifications('code-chat', workspace)
  expect(fixture.tab.config.queuedMessages).toHaveLength(5)
  expect(fixture.tab.config.mcpOAuthNotificationIDs).toContain('browser-extension:connection-two:connected')
  expect(fixture.tab.sessionId).toBe('code-session')
  expect(fixture.refreshSessions).toHaveBeenCalledWith(true)
})
it('does not deliver stale responses into another conversation or poll Crew/foreign workspaces', async () => {
  fixture.get.mockImplementationOnce(async () => { fixture.tab.sessionId = 'another-session'; return { data: { selected: true, connected: true, tabs: 1, connection_id: 'connection-one' } } })
  await deliverChromeExtensionChatNotifications('code-chat', workspace)
  expect(fixture.tab.config.queuedMessages).toEqual(['Existing request'])
  fixture.get.mockClear(); fixture.tab.metadata.agentProfileId = 'work'
  await deliverChromeExtensionChatNotifications('code-chat', workspace)
  fixture.tab.metadata.agentProfileId = 'code'
  await deliverChromeExtensionChatNotifications('code-chat', 'Chats/Code/projects/another')
  expect(fixture.get).not.toHaveBeenCalled()
})
