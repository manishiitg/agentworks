import { useEffect } from 'react'
import { loadAgentProfileProviderOptions } from '../../utils/agentProfileCapabilities'
import { agentApi } from '../../services/api'
import type { AgentProfileChatModel, PresetLLMConfig } from '../../services/api-types'
import { useChatStore } from '../../stores/useChatStore'
import { belongsToWorkProject } from './workTabs'
import type { WorkLLMSelection } from './workSessions'
import { workLLMSelectionFromConfig } from './workSessions'

export const CHAT_MODELS_CHANGED = 'agentworks:chat-models-changed'
export const CHAT_MODELS_REFRESH = 'agentworks:chat-models-refresh'

// Server registry, not local storage, owns each person's per-chat choice.
export function applyWorkChatModels(profileId: string, projectId: string, models: AgentProfileChatModel[], fallback?: WorkLLMSelection & { engine?: string }, defaultLabel?: string) {
  const store = useChatStore.getState()
  for (const tab of Object.values(store.chatTabs)) {
    const key = tab.metadata?.agentProfileConversationKey
    if (!belongsToWorkProject(tab, projectId) || tab.metadata?.agentProfileId !== profileId || !key || key.startsWith(`${projectId}:chat:agent-`) || tab.metadata?.isViewOnly) continue
    const own = models.find(model => model.conversation_key === key)
    if (!own && !fallback) continue
    const engine = own?.engine || fallback!.engine || fallback!.provider
    const modelId = own?.model_id || (own ? '' : fallback!.modelId)
    const effort = own?.reasoning_effort || (own ? undefined : fallback?.reasoningEffort)
    const connection = !own || own.engine === (fallback?.engine || fallback?.provider) ? fallback?.connectionId : undefined
    const changed = tab.metadata?.agentProfileEngine !== engine || tab.metadata?.agentProfileModelID !== modelId || tab.metadata?.agentProfileReasoningEffort !== effort || tab.metadata?.agentProfileConnectionID !== connection
    const label = own ? own.display_label : defaultLabel
    if (!changed && tab.metadata?.agentProfileChatModelOverride === Boolean(own) && tab.metadata?.agentProfileChatModelLabel === label) continue
    store.setTabMetadata(tab.tabId, {
      agentProfileChatModelOverride: Boolean(own), agentProfileEngine: engine,
      agentProfileModelID: modelId, agentProfileReasoningEffort: effort,
      agentProfileConnectionID: connection,
      agentProfileChatModelLabel: label,
      ...(changed ? { agentProfileRuntimeDirty: Boolean(tab.sessionId) } : {}),
    })
  }
}

export function useWorkChatModels(profileId: string, projectId: string | undefined, config: PresetLLMConfig | undefined, canonicalTabId: string | undefined) {
  const tabKeys = useChatStore(state => Object.values(state.chatTabs).filter(tab => projectId && belongsToWorkProject(tab, projectId)).map(tab => tab.tabId).sort().join('|'))
  useEffect(() => {
    if (!projectId || !canonicalTabId) return
    let sequence = 0
    let cancelled = false
    let fallback: (WorkLLMSelection & { engine?: string }) | undefined = workLLMSelectionFromConfig(config) ?? undefined
    let defaultLabel: string | undefined
    const apply = (models: AgentProfileChatModel[]) => {
      if (!cancelled) applyWorkChatModels(profileId, projectId, models, fallback, defaultLabel)
    }
    const load = () => {
      const current = ++sequence
      void Promise.resolve().then(async () => {
        const [models, options] = await Promise.all([
          agentApi.getAgentProfileChatModelState(profileId, projectId),
          loadAgentProfileProviderOptions(profileId),
        ])
        const option = options.find(item => item.provider === fallback?.provider) || options.find(item => item.default) || options[0]
        if (fallback && option?.provider === fallback.provider) fallback = { ...fallback, engine: option.id }
        if (!fallback && option) fallback = { engine: option.id, provider: option.provider || option.id, modelId: option.model_id || '', reasoningEffort: typeof option.options?.reasoning_effort === 'string' ? option.options.reasoning_effort : undefined }
        if (current === sequence) { defaultLabel = models.default_model_label; apply(models.chat_models) }
      }).catch(() => undefined) // Shared/read-only projects never gain an owner's registry access.
    }
    const changed = (event: Event) => {
      const detail = (event as CustomEvent<{ profileId: string; projectId: string; models: AgentProfileChatModel[] }>).detail
      if (detail?.profileId === profileId && detail.projectId === projectId) { ++sequence; apply(detail.models) }
    }
    load()
    window.addEventListener('focus', load)
    window.addEventListener(CHAT_MODELS_REFRESH, load)
    window.addEventListener(CHAT_MODELS_CHANGED, changed)
    return () => { cancelled = true; window.removeEventListener('focus', load); window.removeEventListener(CHAT_MODELS_REFRESH, load); window.removeEventListener(CHAT_MODELS_CHANGED, changed) }
  }, [profileId, projectId, config, canonicalTabId, tabKeys])
}
