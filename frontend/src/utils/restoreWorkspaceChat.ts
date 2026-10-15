import type { WorkspaceDestination } from '../services/workspaceState'
import { useChatStore } from '../stores/useChatStore'
import { hydrateTabEvents } from './sessionRestore'
import { activateTab } from './activateTab'

// Server checked both the project grant and this person's saved session before
// returning it. Hydration still uses the normal authorized history endpoint.
export async function restoreWorkspaceChat(destination: WorkspaceDestination, signal: AbortSignal) {
  if (!destination.session_id || !destination.id || !destination.workspace_path || signal.aborted) return
  const chat = useChatStore.getState()
  const workflow = destination.surface === 'agentworks' || destination.surface === 'relays'
  const metadata = workflow ? {
    mode: 'workflow' as const, phaseId: 'workflow-builder', phaseName: 'Automation Builder', presetQueryId: destination.id,
    isViewOnly: destination.chat?.read_only, readOnlyRestoredAt: Date.now(),
  } : {
    mode: 'multi-agent' as const, agentProfileId: destination.surface, agentProfileProjectId: destination.id,
    agentProfileWorkspace: destination.workspace_path, agentProfileBuilder: false,
    agentProfileConversationKey: destination.chat?.conversation_key || `${destination.id}:history:${destination.session_id}`,
    isViewOnly: destination.chat?.read_only,
  }
  const existing = Object.values(chat.chatTabs).find(tab => tab.sessionId === destination.session_id)
  const tabId = existing?.tabId ?? await chat.createChatTab(destination.chat?.name || 'Chat', metadata, destination.session_id)
  if (signal.aborted) return
  chat.setTabMetadata(tabId, metadata)
  await hydrateTabEvents(destination.session_id, { workspacePath: destination.workspace_path, compact: !destination.chat?.read_only })
  if (!signal.aborted) activateTab(tabId)
}
