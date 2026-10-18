import type { WorkspaceDestination } from '../services/workspaceState'
import { useChatStore } from '../stores/useChatStore'
import { hydrateExecutionConversation } from './executionConversationRestore'
import { hydrateTabEvents } from './sessionRestore'
import { activateTab } from './activateTab'

// Server checked both the project grant and this person's saved session before
// returning it. Hydration still uses the normal authorized history endpoint.
export async function restoreWorkspaceChat(destination: WorkspaceDestination, signal: AbortSignal) {
  if (!destination.session_id || !destination.id || !destination.workspace_path || signal.aborted) return
  const chat = useChatStore.getState()
  const workflow = destination.surface === 'agentworks' || destination.surface === 'relays'
  const builder = destination.chat?.workshop_mode === 'workshop' && !destination.chat.execution_run
  const common = { isViewOnly: destination.chat?.read_only, isExecutionRun: destination.chat?.execution_run,
    isScheduledRun: destination.chat?.scheduled_run, scheduledJobName: destination.chat?.scheduled_job_name,
    isBotRun: Boolean(destination.chat?.bot_platform), botPlatform: destination.chat?.bot_platform,
    workshopMode: destination.chat?.workshop_mode, readOnlyRestoredAt: Date.now(), workspaceReturnAt: Date.now(),
  }
  const metadata = workflow ? {
    ...common, mode: 'workflow' as const, phaseId: builder ? 'workflow-builder' : undefined, phaseName: builder ? 'Automation Builder' : undefined, presetQueryId: destination.id,

  } : {
    ...common, mode: 'multi-agent' as const, agentProfileId: destination.surface, agentProfileVersion: destination.surface === 'work' ? 3 : 1, agentProfileChatContract: 'profile-v1' as const, agentProfileProjectId: destination.id,
    agentProfileWorkspace: destination.workspace_path, agentProfileBuilder: false,
    agentProfileConversationKey: destination.chat?.conversation_key || `${destination.id}:history:${destination.session_id}`,
    isViewOnly: destination.chat?.read_only,
  }
  const existing = Object.values(chat.chatTabs).find(tab => tab.sessionId === destination.session_id)
  const tabId = existing?.tabId ?? await chat.createChatTab(destination.chat?.name || 'Chat', metadata, destination.session_id)
  if (signal.aborted) return
  chat.setTabMetadata(tabId, metadata)
  if (destination.chat?.view_mode) chat.setTabViewMode(tabId, destination.chat.view_mode)
  const runtime = destination.chat?.execution_run
    ? await hydrateExecutionConversation(destination.session_id, destination.workspace_path)
    : await hydrateTabEvents(destination.session_id, { workspacePath: destination.workspace_path, compact: !destination.chat?.read_only })
  if (signal.aborted) return
  chat.setTabStreaming(tabId, runtime.status === 'running' && (destination.chat?.execution_run === true || (!runtime.isSyntheticTurn && (!runtime.hasRunningBackgroundAgents || runtime.canSteer === true))))
  chat.setTabCompleted(tabId, ['completed', 'stopped', 'error', 'failed'].includes(runtime.status))
  chat.setTabHasRunningBgAgents(tabId, Boolean(runtime.hasRunningBackgroundAgents))
  chat.setTabSyntheticTurn(tabId, Boolean(runtime.isSyntheticTurn))
  chat.setTabCanSteer(tabId, Boolean(runtime.canSteer))
  if (!signal.aborted) activateTab(tabId)
}
