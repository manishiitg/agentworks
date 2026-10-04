// @vitest-environment happy-dom
import { beforeEach, expect, it, vi } from 'vitest'

const fixture = vi.hoisted(() => ({
  tab: { tabId: 'chat', sessionId: 'restored-chat', isStreaming: false, metadata: { mode: 'multi-agent' }, config: { queuedMessages: [] as string[], mcpOAuthNotificationIDs: [] as string[] } },
  getEvents: vi.fn(), setConfig: vi.fn(), addEvents: vi.fn(), activate: vi.fn(),
}))
const getStore = () => ({
  getTab: () => fixture.tab,
  getTabConfig: () => fixture.tab.config,
  setTabConfig: fixture.setConfig,
  setTabViewMode: vi.fn(), setAutoScroll: vi.fn(), getActiveSessions: vi.fn(async () => []),
  addTabEvents: fixture.addEvents,
})
vi.mock('../services/api', () => ({ agentApi: { getSessionEvents: fixture.getEvents } }))
vi.mock('../stores/useChatStore', () => ({ useChatStore: { getState: () => getStore() } }))
vi.mock('../stores/useGlobalPresetStore', () => ({ useGlobalPresetStore: { getState: () => ({}) } }))
vi.mock('../stores/useWorkflowStore', () => ({ useWorkflowStore: { getState: () => ({}) } }))
vi.mock('../utils/activateTab', () => ({ activateTab: fixture.activate }))
vi.mock('../utils/workflowNavigation', () => ({ selectWorkflowPreset: vi.fn() }))
import { deliverMcpOAuthChatNotifications } from './useMcpOAuthChatNotifications'
import { sendWorkspacePaneMessageToChat } from '../utils/workspacePaneChat'

const notice = (id = 'callback-one', status = 'completed') => ({
  id, type: 'synthetic_turn_ready', session_id: 'restored-chat',
  data: { type: 'synthetic_turn_ready', data: { agent_id: `vault-oauth:connection:${id}`, name: 'Notion', status, message: 'internal agent instruction' } },
})
beforeEach(() => {
  vi.clearAllMocks()
  fixture.tab.sessionId = 'restored-chat'
  fixture.tab.isStreaming = false
  fixture.tab.config = { queuedMessages: [], mcpOAuthNotificationIDs: [] }
  fixture.setConfig.mockImplementation((_id, update) => { fixture.tab.config = { ...fixture.tab.config, ...update } })
  fixture.getEvents.mockResolvedValue({ events: [notice()] })
})
it('uses the real global queue for an idle restored chat and persists a receipt against replay', async () => {
  await deliverMcpOAuthChatNotifications('chat', 'restored-chat')
  expect(fixture.tab.config.queuedMessages).toHaveLength(1)
  expect(fixture.tab.config.queuedMessages[0]).toContain('[AUTO-NOTIFICATION]')
  expect(fixture.tab.config.queuedMessages[0]).toContain('Notion is signed in to Vault')
  expect(fixture.activate).toHaveBeenCalledWith('chat')
  expect(fixture.tab.config.mcpOAuthNotificationIDs).toEqual(['callback-one'])
  await deliverMcpOAuthChatNotifications('chat', 'restored-chat')
  expect(fixture.tab.config.queuedMessages).toHaveLength(1)
  // A separate reconnect is a new callback, even though the provider is unchanged.
  fixture.getEvents.mockResolvedValue({ events: [notice(), notice('callback-two', 'failed')] })
  await deliverMcpOAuthChatNotifications('chat', 'restored-chat')
  expect(fixture.tab.config.queuedMessages).toHaveLength(2)
  expect(fixture.tab.config.queuedMessages[1]).toContain('did not finish signing in')
})
it('queues behind a busy turn without stealing its session and deduplicates concurrent deliveries', async () => {
  fixture.tab.isStreaming = true
  await Promise.all([deliverMcpOAuthChatNotifications('chat', 'restored-chat'), deliverMcpOAuthChatNotifications('chat', 'restored-chat')])
  expect(fixture.tab.config.queuedMessages).toHaveLength(1)
  const result = await sendWorkspacePaneMessageToChat({ tabId: 'chat', message: 'repeat', notificationId: 'callback-one' })
  expect(result.queuedBehindRunningTurn).toBe(true)
  expect(fixture.tab.config.queuedMessages).toHaveLength(1)
})
it('does not deliver a late response after the tab changes conversation', async () => {
  fixture.getEvents.mockImplementation(async () => { fixture.tab.sessionId = 'new-chat'; return { events: [notice()] } })
  await deliverMcpOAuthChatNotifications('chat', 'restored-chat')
  expect(fixture.tab.config.queuedMessages).toHaveLength(0)
})
it('does not replay a callback already continued by an older backend', async () => {
  fixture.getEvents.mockResolvedValue({ events: [notice(), { type: 'user_message', data: { data: { content: '[AUTO-NOTIFICATION] internal agent instruction' } } }] })
  await deliverMcpOAuthChatNotifications('chat', 'restored-chat')
  expect(fixture.tab.config.queuedMessages).toHaveLength(0)
  expect(fixture.tab.config.mcpOAuthNotificationIDs).toEqual(['callback-one'])
})
