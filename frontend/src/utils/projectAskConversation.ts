import { agentApi } from '../services/api'
import { useChatStore, waitForChatStoreHydration } from '../stores/useChatStore'
import { useModeStore } from '../stores/useModeStore'
import { useAppStore } from '../stores/useAppStore'
import { assertChatIdentityCurrent, captureChatIdentity } from './chatIdentity'
import { hydrateTabEvents } from './sessionRestore'
import { resolveWorkspacePaneChatTab } from './workspacePaneChat'

/** Open the ordinary project conversation, including its existing history.
 * A dashboard is another view of this chat, never a new session or tool mode. */
export async function openProjectAskConversation(project: { product: string; projectId: string; workspacePath: string }): Promise<string> {
  const identity = captureChatIdentity()
  await waitForChatStoreHydration()
  assertChatIdentityCurrent(identity)
  if (project.product === 'crew') {
    // Explicitly omit the unrelated active tab's session. The standard profile
    // endpoint resolves this caller's canonical project conversation.
    const conversation = await agentApi.resolveAgentProfileConversation('work', { conversation_key: project.projectId }, null)
    assertChatIdentityCurrent(identity)
    const store = useChatStore.getState()
    const tabId = await store.createChatTab('Chat', {
      mode: 'multi-agent', agentProfileId: 'work', agentProfileBuilder: false,
      agentProfileWorkspace: project.workspacePath, agentProfileProjectId: project.projectId,
      agentProfileConversationKey: conversation.conversation_key,
      agentProfileConversationId: conversation.conversation_id, agentProfileChatContract: 'profile-v1',
      agentProfileChatProvider: conversation.provider, agentProfileChatConnectionID: conversation.connection_id,
    }, conversation.session_id)
    assertChatIdentityCurrent(identity)
    useModeStore.getState().setModeCategory('multi-agent')
    useAppStore.getState().setAgentMode('multi-agent')
    if ((useChatStore.getState().tabEvents[conversation.session_id]?.length ?? 0) === 0) {
      await hydrateTabEvents(conversation.session_id, { workspacePath: project.workspacePath, fallbackToChatHistory: true, preferChatHistory: true })
      assertChatIdentityCurrent(identity)
    }
    return tabId
  }
  if (project.product !== 'workflow' && project.product !== 'relay') throw new Error('Ask is unavailable for this project.')
  const target = await resolveWorkspacePaneChatTab({ workspacePath: project.workspacePath, message: '' })
  assertChatIdentityCurrent(identity)
  if (target.targetTab.metadata?.presetQueryId !== project.projectId) throw new Error('The project conversation changed. Reopen the dashboard.')
  return target.tabId
}
