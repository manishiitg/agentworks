import { agentApi } from '../services/api'
import { useChatStore } from '../stores/useChatStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { currentWorkflowBuilderBinding, publishWorkflowBuilderBinding } from '../hooks/useWorkflowBuilderBindings'
import { captureChatIdentity, isChatIdentityCurrent } from './chatIdentity'
import { ensureWorkflowComposerTab } from './workflowSessionRestore'
import { resolveWorkflowTabForSession } from './workflowTabResolution'

/** Discover/cache the server-selected Builder without changing navigation.
 * Callers that intend to open it must activate it after checking their intent
 * is still current. Restoring a previously active tab here can undo a workflow
 * switch, including when both conversations were already cached.
 */
export async function ensureWorkflowBuilderTab(presetId: string): Promise<string> {
  const store = useChatStore.getState()
  const identity = captureChatIdentity()
  const preset = useGlobalPresetStore.getState().workflowPresets.find(item => item.id === presetId)
  let binding = currentWorkflowBuilderBinding(presetId)
  if (!binding && preset?.selectedFolder?.filepath) {
    const response = await agentApi.getWorkflowBuilderSessions([{
      preset_query_id: presetId, workspace_path: preset.selectedFolder.filepath,
    }])
    if (!isChatIdentityCurrent(identity)) throw new Error('Chat identity changed')
    binding = response.bindings[presetId]
    if (binding) publishWorkflowBuilderBinding(binding, identity)
  }
  if (!binding) throw new Error('Current Builder unavailable')
  if (!binding.session_id) return ensureWorkflowComposerTab(presetId)
  return (await resolveWorkflowTabForSession({
    getTabs: () => useChatStore.getState().chatTabs,
    presetQueryId: presetId, sessionId: binding.session_id,
    name: 'Automation Builder',
    metadata: { mode: 'workflow', phaseId: 'workflow-builder', phaseName: 'Automation Builder', presetQueryId: presetId, isWorkflowDraft: false },
    createChatTab: (name, metadata, sessionId) => store.createChatTab(name, metadata, sessionId, { activate: false }),
    updateTabSessionId: store.updateTabSessionId,
  })).tabId
}
