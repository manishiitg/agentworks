import { afterEach, expect, it, vi } from 'vitest'
import { openProjectAskConversation } from './projectAskConversation'

const mocks = vi.hoisted(() => ({ post: vi.fn(), create: vi.fn(), hydrate: vi.fn(), refresh: vi.fn() }))
vi.mock('../services/api', () => ({ default: { post: mocks.post } }))
vi.mock('../stores/useChatStore', () => ({ waitForChatStoreHydration: async () => {}, useChatStore: { getState: () => ({ createChatTab: mocks.create, tabEvents: { 'caller-session': ['history'] } }) } }))
vi.mock('../stores/useGlobalPresetStore', () => ({ useGlobalPresetStore: { getState: () => ({ workflowPresets: [{ id: 'project-1' }], refreshPresets: mocks.refresh }) } }))
vi.mock('./sessionRestore', () => ({ hydrateTabEvents: mocks.hydrate }))
vi.mock('./chatIdentity', () => ({ captureChatIdentity: () => 1, assertChatIdentityCurrent: vi.fn() }))
afterEach(() => vi.resetAllMocks())
const target = { project_id: 'project-1', workspace: 'Workflow/demo', profile_id: '', conversation: { session_id: 'caller-session', conversation_key: 'personal-key', conversation_id: 'conversation' } }

it.each(['workflow', 'relay'])('opens the server-resolved %s recipient chat without selecting or creating a Builder conversation', async product => {
  mocks.post.mockResolvedValue({ data: { ...target, product, mode: 'run' } })
  mocks.create.mockResolvedValue('chat')
  await openProjectAskConversation({ product, projectId: 'project-1', workspacePath: 'Workflow/demo' }, { activate: false })
  expect(mocks.post).toHaveBeenCalledWith('/api/project-ask/conversation', { workspace: 'Workflow/demo' })
  expect(mocks.create).toHaveBeenCalledWith('Chat', expect.objectContaining({ mode: 'workflow', presetQueryId: 'project-1', phaseName: 'Chat', workshopMode: 'run' }), 'caller-session', { activate: false })
  expect(mocks.hydrate).not.toHaveBeenCalled()
})

it('preserves an owner’s normal Builder authority supplied by the server', async () => {
  mocks.post.mockResolvedValue({ data: { ...target, product: 'workflow', mode: 'builder' } })
  await openProjectAskConversation({ workspacePath: 'Workflow/demo' })
  expect(mocks.create).toHaveBeenCalledWith('Chat', expect.objectContaining({ workshopMode: 'workshop' }), 'caller-session', { activate: true })
})

it('uses a Crew’s canonical caller binding and can request the existing owner-as-user view', async () => {
  mocks.post.mockResolvedValue({ data: { ...target, product: 'crew', profile_id: 'work', mode: 'run', workspace: 'Crew/team' } })
  await openProjectAskConversation({ product: 'crew', projectId: 'project-1', workspacePath: 'Crew/team' }, { userView: true, activate: false })
  expect(mocks.post).toHaveBeenCalledWith('/api/project-ask/conversation', { workspace: 'Crew/team', as_user: true })
  expect(mocks.create).toHaveBeenCalledWith('Chat', expect.objectContaining({ agentProfileId: 'work', agentProfileConversationKey: 'personal-key', agentProfileWorkspace: 'Crew/team', agentProfileChatContract: 'profile-v1' }), 'caller-session', { activate: false })
})
