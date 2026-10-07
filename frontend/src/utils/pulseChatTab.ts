import type { ActiveSessionInfo } from '../services/api-types'
import { useChatStore } from '../stores/useChatStore'
import { activateTab } from './activateTab'
import { openGlobalActivitySession } from './globalProductNavigation'

/**
 * Opens or focuses the workflow's "<workflow> Pulse" chat tab: the Pulse
 * conversation's session (PLAT-697). The Pulse tab shows no conversation;
 * after the owner talks to Pulse there, its reply is read in this tab.
 */
export async function openPulseChatTab(workspacePath: string, sessionId: string): Promise<void> {
  const id = sessionId.trim()
  if (!id) return
  const existing = Object.values(useChatStore.getState().chatTabs).find(tab => tab.sessionId === id)
  if (existing) {
    activateTab(existing.tabId)
    return
  }
  const now = new Date().toISOString()
  // A Pulse turn runs as a workflow schedule session (schedule-goallead--…),
  // so it opens through the same path as any scheduled workflow chat.
  const session: ActiveSessionInfo = {
    session_id: id,
    observer_id: '',
    agent_mode: 'workflow',
    status: 'running',
    last_activity: now,
    created_at: now,
    workspace_path: workspacePath,
    triggered_by: 'schedule',
  }
  await openGlobalActivitySession(session)
}
