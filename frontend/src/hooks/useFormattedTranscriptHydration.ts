import { useEffect } from 'react'
import { hydrateTabEvents } from '../utils/sessionRestore'

// Terminal sends bypass the composer. Refresh the durable transcript whenever
// chat becomes visible, including repeated visits to the same conversation.
export function useFormattedTranscriptHydration(
  sessionId: string | null | undefined,
  formatted: boolean,
  workspacePath: string | undefined,
  enabled: boolean,
) {
  useEffect(() => {
    if (!enabled || !formatted || !sessionId) return
    // Match initial restore's per-tab projection. A full 100-event tool
    // window must not replace the initial compact 40-message history.
    void hydrateTabEvents(sessionId, {
      workspacePath,
      fallbackToChatHistory: true,
      preferChatHistory: true,
      includeUiEvents: true,
    }).catch(error => {
      console.error('[SessionRestore] Formatted transcript hydration failed:', error)
    })
  }, [enabled, formatted, sessionId, workspacePath])
}
