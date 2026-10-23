import type { ActiveSessionInfo } from '../services/api-types'
import { agentApi } from '../services/api'
import { useChatStore } from '../stores/useChatStore'
import { useWorkflowStore } from '../stores/useWorkflowStore'
import { convertObservedWorkflowTabToInteractive } from '../components/workflow/workflowChatTabConversion'
import { activateTab } from './activateTab'
import { openGlobalActivitySession } from './globalProductNavigation'
import { assertChatIdentityCurrent, captureChatIdentity } from './chatIdentity'
import { sendWorkspacePaneMessageToChat } from './workspacePaneChat'

/** Focus the continuing Pulse conversation, never the workflow's Builder. */
export async function openPulseChatTab(workspacePath: string, sessionId: string, busy = false): Promise<string> {
  const identity = captureChatIdentity()
  const id = sessionId.trim()
  if (!id) throw new Error('Autopilot chat is not available. Refresh Autopilot and try again.')
  let tab = Object.values(useChatStore.getState().chatTabs).find(tab => tab.sessionId === id)
  if (!tab) {
    const now = new Date().toISOString()
    const session: ActiveSessionInfo = {
      session_id: id, observer_id: '', agent_mode: 'workflow', status: busy ? 'running' : 'completed',
      last_activity: now, created_at: now, workspace_path: workspacePath, triggered_by: 'schedule',
    }
    await openGlobalActivitySession(session)
    assertChatIdentityCurrent(identity)
    tab = Object.values(useChatStore.getState().chatTabs).find(tab => tab.sessionId === id)
  }
  if (!tab) throw new Error('Could not open Autopilot chat. Refresh Autopilot and try again.')
  // The shared activity opener observes schedule sessions. Talking to Pulse is
  // the existing interactive-continuation action on that exact conversation.
  const tabId = tab.tabId
  useChatStore.setState(state => {
    const current = state.chatTabs[tabId]
    if (!current) return state
    const interactive = convertObservedWorkflowTabToInteractive(current)
    return { chatTabs: { ...state.chatTabs, [tabId]: { ...interactive, name: 'Autopilot', metadata: { ...interactive.metadata, isExecutionRun: true, phaseName: 'Autopilot' } } } }
  })
  if (!activateTab(tabId)) throw new Error('Could not open Autopilot chat. Refresh Autopilot and try again.')
  const workflow = useWorkflowStore.getState()
  workflow.setShowChatArea(true)
  workflow.setFocusedPane('chat')
  return tab.tabId
}

/** The server checks Pulse on/off and goal setup before resolving its chat. */
export async function openWorkflowPulseChatTab(workspacePath: string): Promise<string> {
  const identity = captureChatIdentity()
  const lead = await agentApi.getGoalLead(workspacePath)
  assertChatIdentityCurrent(identity)
  if (!lead.success || lead.error || lead.conversation?.error) {
    throw new Error(lead.error || lead.conversation?.error || 'Could not open Autopilot chat.')
  }
  const sessionId = lead.conversation?.session_id?.trim()
  if (!lead.conversation?.has_goal || !sessionId) {
    throw new Error('Turn Autopilot on and set a goal with Builder before opening its chat.')
  }
  return openPulseChatTab(workspacePath, sessionId, lead.conversation.busy)
}

/** Explicit question actions use Pulse's exact tab and the normal chat delivery path. */
export async function askWorkflowPulse(workspacePath: string, question: string): Promise<void> {
  const identity = captureChatIdentity()
  const tabId = await openWorkflowPulseChatTab(workspacePath)
  assertChatIdentityCurrent(identity)
  await sendWorkspacePaneMessageToChat({ tabId, message: question })
}
