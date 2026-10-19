import type { ActiveSessionInfo } from '../../services/api-types'
import type { ChatTab } from '../../stores/useChatStore'
import { hasLiveBackgroundAgents } from '../../utils/activitySessions'
import { sessionRuntimeStatus, runtimeNeedsUserInput } from '../../utils/runtimeActivity'
import { sidebarScope } from '../../utils/sidebarChats'

/** Display only: saved names and server-owned rename behavior stay intact. */
export function workSideChatDisplayName(savedName: string): string {
  const name = savedName.trim()
  return !name || /^Chat [0-9a-f]{8,}$/i.test(name) ? 'Untitled chat' : savedName
}

/** The server decides activity; this projection only removes duplicate tab indicators. */
export function workProjectActivity(sessions: ActiveSessionInfo[], tabs: ChatTab[], projectId: string): 'running' | 'waiting' | null {
  const visible = new Set(tabs.map(tab => tab.sessionId).filter(Boolean))
  let running = false
  for (const session of sessions) {
    const scope = sidebarScope(session.session_id, session.workspace_path)
    if (!scope || !['work', 'code'].includes(scope.surface) || scope.projectId !== projectId) continue
    const hidden = !visible.has(session.session_id)
    if (hidden && runtimeNeedsUserInput(session)) return 'waiting'
    // Foreground activity already has its own tab. Background-only work still needs a signal.
    if ((hidden && sessionRuntimeStatus(session) === 'busy') ||
      (hasLiveBackgroundAgents(session) && session.runtime_state?.foreground_turn.busy !== true)) running = true
  }
  return running ? 'running' : null
}
