import { afterEach, expect, it, vi } from 'vitest'
import { openProjectAskConversation } from './projectAskConversation'

const mocks = vi.hoisted(() => ({ resolve: vi.fn(), create: vi.fn(), hydrate: vi.fn(), workflow: vi.fn(), mode: vi.fn(), app: vi.fn() }))
vi.mock('../services/api', () => ({ agentApi: { resolveAgentProfileConversation: mocks.resolve } }))
vi.mock('../stores/useChatStore', () => ({ waitForChatStoreHydration: async () => {}, useChatStore: { getState: () => ({ createChatTab: mocks.create, tabEvents: { 'caller-session': ['history'] } }) } }))
vi.mock('../stores/useModeStore', () => ({ useModeStore: { getState: () => ({ setModeCategory: mocks.mode }) } }))
vi.mock('../stores/useAppStore', () => ({ useAppStore: { getState: () => ({ setAgentMode: mocks.app }) } }))
vi.mock('./sessionRestore', () => ({ hydrateTabEvents: mocks.hydrate }))
vi.mock('./workspacePaneChat', () => ({ resolveWorkspacePaneChatTab: mocks.workflow }))
vi.mock('./chatIdentity', () => ({ captureChatIdentity: () => 1, assertChatIdentityCurrent: vi.fn() }))
afterEach(() => vi.resetAllMocks())

it.each([['crew', 'work', 'Crew/team'], ['code', 'code', '_users/owner/Chats/Code/projects/team']])('binds a %s-origin Crew to its ordinary caller conversation and reuses its saved session', async (product, profile, workspacePath) => {
  mocks.resolve.mockResolvedValue({ session_id: 'caller-session', conversation_key: 'crew-1', conversation_id: 'caller-conversation' })
  mocks.create.mockResolvedValue('existing-tab')
  expect(await openProjectAskConversation({ product, projectId: 'crew-1', workspacePath })).toBe('existing-tab')
  expect(mocks.resolve).toHaveBeenCalledWith(profile, { conversation_key: 'crew-1' }, null)
  expect(mocks.create).toHaveBeenCalledWith('Chat', expect.objectContaining({ agentProfileId: profile, agentProfileBuilder: false, agentProfileWorkspace: workspacePath, agentProfileProjectId: 'crew-1', agentProfileConversationKey: 'crew-1', agentProfileConversationId: 'caller-conversation', agentProfileChatContract: 'profile-v1' }), 'caller-session', { activate: true })
  expect(mocks.hydrate).not.toHaveBeenCalled()
})

it('uses the normal workflow chat resolver for workflows and Relays, rejecting a different project', async () => {
  mocks.workflow.mockResolvedValue({ tabId: 'normal-tab', targetTab: { metadata: { presetQueryId: 'workflow-1' } } })
  for (const product of ['workflow', 'relay']) expect(await openProjectAskConversation({ product, projectId: 'workflow-1', workspacePath: 'Workflow/demo' })).toBe('normal-tab')
  expect(mocks.workflow).toHaveBeenCalledWith({ workspacePath: 'Workflow/demo', message: '' }, undefined)
  await expect(openProjectAskConversation({ product: 'workflow', projectId: 'different', workspacePath: 'Workflow/demo' })).rejects.toThrow('conversation changed')
  expect(mocks.resolve).not.toHaveBeenCalled()
})

it('prepares a dashboard chat without activating it or changing the current mode', async () => {
 mocks.resolve.mockResolvedValue({ session_id: 'caller-session', conversation_key: 'crew-1' })
 mocks.create.mockResolvedValue('widget-tab')
 expect(await openProjectAskConversation({ product: 'code', projectId: 'crew-1', workspacePath: 'Crew/team' }, { activate: false })).toBe('widget-tab')
 expect(mocks.create).toHaveBeenCalledWith('Chat', expect.any(Object), 'caller-session', { activate: false })
 expect(mocks.mode).not.toHaveBeenCalled()
 expect(mocks.app).not.toHaveBeenCalled()
})
