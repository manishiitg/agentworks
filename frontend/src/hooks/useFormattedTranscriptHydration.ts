import { useCallback, useEffect, useState } from 'react'
import { hydrateTabEvents } from '../utils/sessionRestore'

// Terminal sends bypass the composer. Refresh the durable transcript whenever
// chat becomes visible, including repeated visits to the same conversation.
export function useFormattedTranscriptHydration(
  sessionId: string | null | undefined,
  formatted: boolean,
  workspacePath: string | undefined,
  enabled: boolean,
) {
  const key = JSON.stringify([sessionId, workspacePath])
  const [failure, setFailure] = useState<{ key: string; message: string } | null>(null)
  const [attempt, setAttempt] = useState(0)
  const retry = useCallback(() => { setFailure(null); setAttempt(value => value + 1) }, [])
  useEffect(() => {
    if (!enabled || !formatted || !sessionId) return
    let cancelled = false
    setFailure(null)
    // Match initial restore's per-tab projection. A full 100-event tool
    // window must not replace the initial compact 40-message history.
    void hydrateTabEvents(sessionId, {
      workspacePath,
      fallbackToChatHistory: true,
      preferChatHistory: true,
      includeUiEvents: true,
    }).catch(error => {
      if (!cancelled) setFailure({ key, message: 'Could not load this conversation. Try again.' })
      console.error('[SessionRestore] Formatted transcript hydration failed:', error)
    })
    return () => { cancelled = true }
  }, [enabled, formatted, sessionId, workspacePath, key, attempt])
  return { error: enabled && formatted && failure?.key === key ? failure.message : undefined, retry }
}
