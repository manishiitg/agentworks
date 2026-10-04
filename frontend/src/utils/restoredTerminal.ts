import { agentApi } from '../services/api'
import { hydrateTabEvents } from './sessionRestore'

// reconnectWorkflowTabs and handleResumePreviousChat can both fire a restore for
// the same session on page load. Track in-flight restores so the second caller
// piggybacks on the first instead of launching a duplicate tmux reattach.
const restoreInFlight = new Set<string>()

export function startRestoredTransportTerminal(
  sessionId: string | null | undefined,
  restoredConversationPath: string | null | undefined,
  restoredConversationSessionId?: string | null,
  workspacePath?: string | null,
) {
  const targetSessionId = sessionId?.trim()
  const path = restoredConversationPath?.trim()
  const sourceSessionId = restoredConversationSessionId?.trim()
  const workspace = workspacePath?.trim()
  if (!targetSessionId || (!path && !sourceSessionId)) return

  const key = `${targetSessionId}:${path || ''}:${sourceSessionId || ''}:${workspace || ''}`
  if (restoreInFlight.has(key)) return
  restoreInFlight.add(key)

  console.info('[RestoredTerminal] POST /chat-history/restored-terminal', {
    sessionId: targetSessionId,
    path: path || undefined,
    restoredConversationSessionId: sourceSessionId,
    workspacePath: workspace || undefined,
  })
  // A restored tmux snapshot is only the last visible screen and often uses an
  // alternate buffer with zero scrollback. Hydrate the persisted structured
  // conversation before publishing the terminal so the first useful view is
  // the scrollable Formatted transcript instead of an unscrollable Raw pane.
  // This full-history read happens only after the user opens/resumes one chat;
  // the lightweight previous-chat listing remains index-only.
  void hydrateTabEvents(targetSessionId, {
      workspacePath: workspace || undefined,
      fallbackToChatHistory: true,
      preferChatHistory: true,
      compact: false,
    })
    .catch((error) => {
      console.warn('[RestoredTerminal] Failed to hydrate formatted conversation; continuing with terminal restore', {
        sessionId: targetSessionId,
        error,
      })
    })
    .then(() => agentApi.startRestoredTerminal({
      session_id: targetSessionId,
      restored_conversation_path: path || undefined,
      restored_conversation_session_id: sourceSessionId || undefined,
      workspace_path: workspace || undefined,
    }))
    .then((response) => {
      if (response.started) {
        console.info('[RestoredTerminal] terminal restore started', {
          sessionId: targetSessionId,
          hasTerminalSnapshot: Boolean(response.terminal),
        })
      } else {
        console.warn('[RestoredTerminal] Terminal restore did not start', {
          sessionId: targetSessionId,
          reason: response.reason,
          response,
        })
      }
    })
    .catch((error) => {
      console.warn('[RestoredTerminal] Failed to start restored terminal', { sessionId: targetSessionId, error })
      // Restore should not block on a stale or already-closed terminal transport.
      // The next submitted turn will still recreate the provider session when needed.
    })
    .finally(() => {
      restoreInFlight.delete(key)
    })
}
