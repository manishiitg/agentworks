import api from '../services/api'
import { useChatStore, waitForChatStoreHydration } from '../stores/useChatStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { assertChatIdentityCurrent, captureChatIdentity } from './chatIdentity'
import { hydrateTabEvents } from './sessionRestore'

type ProjectChat = {
  product: string; project_id: string; workspace: string; mode: 'run' | 'builder'; profile_id: string
  conversation: { session_id: string; conversation_key: string; conversation_id?: string; provider?: string; connection_id?: string }
}

/** The backend resolves project, caller, role and durable conversation. The
 * browser presents that conversation and sends through the normal chat queue. */
export async function openProjectAskConversation(project: { product?: string; projectId?: string; workspacePath: string }, options?: { activate?: boolean; userView?: boolean }): Promise<string> {
  const identity = captureChatIdentity()
  await waitForChatStoreHydration()
  assertChatIdentityCurrent(identity)
  const { data: target } = await api.post<ProjectChat>('/api/project-ask/conversation', { workspace: project.workspacePath, ...(options?.userView ? { as_user: true } : {}) })
  assertChatIdentityCurrent(identity)
  if (project.projectId && target.project_id !== project.projectId) throw new Error('The project conversation changed. Reopen the dashboard.')
  const conversation = target.conversation
  const store = useChatStore.getState()
  if (!target.profile_id && !useGlobalPresetStore.getState().workflowPresets.some(preset => preset.id === target.project_id)) {
    await useGlobalPresetStore.getState().refreshPresets()
    assertChatIdentityCurrent(identity)
  }
  const metadata = target.profile_id ? {
    mode: 'multi-agent' as const, agentProfileId: target.profile_id, agentProfileBuilder: false,
    agentProfileWorkspace: target.workspace, agentProfileProjectId: target.project_id,
    agentProfileConversationKey: conversation.conversation_key, agentProfileConversationId: conversation.conversation_id,
    agentProfileChatContract: 'profile-v1' as const, agentProfileChatProvider: conversation.provider,
    agentProfileChatConnectionID: conversation.connection_id,
  } : {
    mode: 'workflow' as const, presetQueryId: target.project_id, phaseId: 'workflow-builder', phaseName: 'Chat',
    workshopMode: target.mode === 'run' ? 'run' as const : 'workshop' as const,
  }
  const tabId = await store.createChatTab('Chat', metadata, conversation.session_id, { activate: options?.activate !== false })
  assertChatIdentityCurrent(identity)
  if ((useChatStore.getState().tabEvents[conversation.session_id]?.length ?? 0) === 0) {
    await hydrateTabEvents(conversation.session_id, { workspacePath: target.workspace, fallbackToChatHistory: true, preferChatHistory: true })
    assertChatIdentityCurrent(identity)
  }
  return tabId
}
