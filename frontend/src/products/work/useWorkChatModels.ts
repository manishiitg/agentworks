import { useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { agentApi } from '../../services/api'
import type { AgentProfileEffectiveChatModel, PresetLLMConfig } from '../../services/api-types'
import { useChatStore } from '../../stores/useChatStore'
import { belongsToWorkProject } from './workTabs'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'

export const workChatModelsQueryKey = (profileId: string, projectId: string) => ['project-chat-models', profileId, projectId] as const

// Project only server-issued runtime metadata. No inheritance, provider or
// account choice is resolved in the browser.
export function applyWorkChatModels(profileId: string, projectId: string, models: AgentProfileEffectiveChatModel[], canOpenModels = false) {
  const store = useChatStore.getState()
  for (const tab of Object.values(store.chatTabs)) {
    const key = tab.metadata?.agentProfileConversationKey
    if (!belongsToWorkProject(tab, projectId) || tab.metadata?.agentProfileId !== profileId || !key || key.startsWith(`${projectId}:chat:agent-`) || tab.metadata?.isViewOnly || tab.metadata?.agentProfileChatLocation === 'mac') continue
    const model = models.find(item => item.conversation_key === key)
    if (!model) continue
    if (model.error) {
      store.setTabMetadata(tab.tabId, { agentProfileChatModelLabel: model.display_label, agentProfileChatModelOverride: model.own_model, agentProfileCanOpenModels: canOpenModels })
      continue
    }
    const changed = tab.metadata?.agentProfileConnectionID !== model.connection_id || tab.metadata?.agentProfileEngine !== model.engine || tab.metadata?.agentProfileModelID !== model.model_id || tab.metadata?.agentProfileReasoningEffort !== model.reasoning_effort
    if (!changed && tab.metadata?.agentProfileChatModelOverride === model.own_model && tab.metadata?.agentProfileChatModelLabel === model.display_label && tab.metadata?.agentProfileCanOpenModels === canOpenModels) continue
    store.setTabMetadata(tab.tabId, {
      agentProfileChatModelOverride: model.own_model, agentProfileEngine: model.engine, agentProfileConnectionID: model.connection_id,
      agentProfileModelID: model.model_id, agentProfileReasoningEffort: model.reasoning_effort,
      agentProfileChatModelLabel: model.display_label,
      agentProfileCanOpenModels: canOpenModels,
      ...(changed ? { agentProfileRuntimeDirty: Boolean(tab.sessionId) } : {}),
    })
  }
}

export function useWorkChatModels(profileId: string, projectId: string | undefined, config: PresetLLMConfig | undefined, canonicalTabId: string | undefined) {
  const account = useAuthStore(state => state.user?.id)
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const identity = captureChatIdentity()
  const tabKeys = useChatStore(state => Object.values(state.chatTabs).filter(tab => projectId && belongsToWorkProject(tab, projectId)).map(tab => tab.tabId).sort().join('|'))
  const models = useQuery({
    queryKey: [...workChatModelsQueryKey(profileId, projectId || ''), account, workspace, identity, config ?? null, tabKeys],
    queryFn: async () => {
      const data = await agentApi.getAgentProfileChatModelState(profileId, projectId!)
      if (!isChatIdentityCurrent(identity)) throw new Error('Chat account changed.')
      return data
    },
    enabled: Boolean(projectId && canonicalTabId),
    refetchOnWindowFocus: true,
  })
  useEffect(() => {
    if (projectId && models.data && isChatIdentityCurrent(identity)) applyWorkChatModels(profileId, projectId, models.data.effective_chat_models, models.data.can_open_models)
  }, [profileId, projectId, models.data, tabKeys, identity])
}
