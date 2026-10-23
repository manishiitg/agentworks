import type { WorkflowOwnerTab, WorkflowBuilderSessionResponse } from '../services/api-types'
import type { ChatTab } from '../stores/useChatStore'
import { agentApi } from '../services/api'
import { useChatStore } from '../stores/useChatStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { currentWorkflowBuilderBinding, publishWorkflowBuilderBinding } from '../hooks/useWorkflowBuilderBindings'
import { captureChatIdentity, isChatIdentityCurrent } from './chatIdentity'
import { ensureWorkflowComposerTab } from './workflowSessionRestore'
import { resolveWorkflowTabForSession } from './workflowTabResolution'

export function workflowOwnerTabMetadata(presetId: string, tab: WorkflowOwnerTab): NonNullable<ChatTab['metadata']> {
  return { mode: 'workflow', phaseId: 'workflow-builder', phaseName: 'Builder', presetQueryId: presetId,
    isWorkflowDraft: false, workflowOwnerTabID: tab.id, workflowOwnerTabIsMain: tab.is_main,
    agentProfileTabOwnerID: tab.owner_id, agentProfileTabOwnerName: tab.owner_name, agentProfileTabCanWrite: tab.can_write }
}

const hydrating = new Map<string, Promise<string>>()
export async function hydrateWorkflowOwnerTabs(binding: WorkflowBuilderSessionResponse): Promise<void> {
  const presetId = binding.preset_query_id
  if (!presetId || !binding.tabs) return
  const identity = captureChatIdentity()
  for (const tab of binding.tabs) {
    const key = `${identity}:${presetId}:${tab.id}`
    let pending = hydrating.get(key)
    if (!pending) {
      pending = (async () => {
        const store = useChatStore.getState()
        const found = Object.values(store.chatTabs).find(item => item.metadata?.mode === 'workflow' && item.sessionId === tab.session_id)
        const metadata = workflowOwnerTabMetadata(presetId, tab)
        if (found) {
          useChatStore.setState(state => ({ chatTabs: { ...state.chatTabs, [found.tabId]: { ...state.chatTabs[found.tabId], name: tab.name, metadata: { ...state.chatTabs[found.tabId].metadata, ...metadata } } } }))
          return found.tabId
        }
        return store.createChatTab(tab.name, metadata, tab.session_id, { activate: false })
      })().finally(() => hydrating.delete(key))
      hydrating.set(key, pending)
    }
    await pending
    if (!isChatIdentityCurrent(identity)) return
  }
  // A server-closed tab disappears from the shared strip, without stopping a
  // different owner's work or changing the selection during navigation.
  const ids = new Set(binding.tabs.map(tab => tab.id))
  useChatStore.setState(state => {
    const chatTabs = { ...state.chatTabs }
    for (const tab of Object.values(chatTabs)) {
      if (tab.metadata?.presetQueryId === presetId && tab.metadata.workflowOwnerTabID && !ids.has(tab.metadata.workflowOwnerTabID)) delete chatTabs[tab.tabId]
    }
    return { chatTabs }
  })
}

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
  await hydrateWorkflowOwnerTabs(binding)
  if (!binding.session_id) return ensureWorkflowComposerTab(presetId)
  return (await resolveWorkflowTabForSession({
    getTabs: () => useChatStore.getState().chatTabs,
    presetQueryId: presetId, sessionId: binding.session_id,
    name: binding.tabs?.find(tab => tab.session_id === binding.session_id)?.name || 'Builder',
    metadata: binding.tabs?.find(tab => tab.session_id === binding.session_id) ? workflowOwnerTabMetadata(presetId, binding.tabs.find(tab => tab.session_id === binding.session_id)!) : { mode: 'workflow', phaseId: 'workflow-builder', presetQueryId: presetId },
    createChatTab: (name, metadata, sessionId) => store.createChatTab(name, metadata, sessionId, { activate: false }),
    updateTabSessionId: store.updateTabSessionId,
  })).tabId
}
