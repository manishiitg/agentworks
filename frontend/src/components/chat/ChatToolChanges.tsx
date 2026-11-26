import { useCallback, useEffect, useRef, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { agentApi, type ToolChanges } from '../../services/api'

// A running coding app keeps the tools it started with (#3528). The server says which Vault apps changed since;
// Reload tools makes the next message start the app fresh with them. The browser only shows and asks.
const POLL_MS = 60_000

function changeText(changes: ToolChanges): string {
  const parts: string[] = []
  if (changes.added.length) parts.push(`${changes.added.length === 1 ? 'New app ready' : 'New apps ready'}: ${changes.added.join(', ')}`)
  if (changes.removed.length) parts.push(`${changes.removed.length === 1 ? 'App removed' : 'Apps removed'}: ${changes.removed.join(', ')}`)
  return parts.join('. ')
}

export function ChatToolChanges({ sessionId, streaming }: { sessionId?: string; streaming: boolean }) {
  const [changes, setChanges] = useState<ToolChanges | null>(null)
  const [busy, setBusy] = useState(false)
  const requestSeqRef = useRef(0)
  const activeSessionRef = useRef(sessionId)
  activeSessionRef.current = sessionId
  const prevStreamingRef = useRef(streaming)
  const refresh = useCallback(() => {
    if (!sessionId || document.visibilityState === 'hidden') return
    const seq = ++requestSeqRef.current
    const targetSession = sessionId
    agentApi.getToolChanges(targetSession)
      .then(result => {
        if (requestSeqRef.current === seq && activeSessionRef.current === targetSession) setChanges(result)
      })
      .catch(() => {
        if (requestSeqRef.current === seq && activeSessionRef.current === targetSession) setChanges(null)
      })
  }, [sessionId])
  useEffect(() => {
    requestSeqRef.current += 1
    setChanges(null)
    setBusy(false)
    refresh()
    const timer = window.setInterval(refresh, POLL_MS)
    window.addEventListener('focus', refresh)
    return () => {
      requestSeqRef.current += 1
      window.clearInterval(timer)
      window.removeEventListener('focus', refresh)
    }
  }, [refresh])
  // A finished reply may have rebuilt the chat with the new apps.
  useEffect(() => {
    const wasStreaming = prevStreamingRef.current
    prevStreamingRef.current = streaming
    if (wasStreaming && !streaming) refresh()
  }, [streaming, refresh])
  if (!sessionId || !changes || changes.added.length + changes.removed.length === 0) return null
  const reload = () => {
    const seq = ++requestSeqRef.current
    const targetSession = sessionId
    setBusy(true)
    agentApi.reloadTools(targetSession)
      .then(result => {
        if (requestSeqRef.current === seq && activeSessionRef.current === targetSession) setChanges(result)
      })
      .catch(() => {
        if (activeSessionRef.current === targetSession) refresh()
      })
      .finally(() => {
        if (activeSessionRef.current === targetSession) setBusy(false)
      })
  }
  return (
    <div role="status" aria-live="polite" className="mx-4 mt-1 flex shrink-0 items-center gap-2 rounded-md border border-border bg-muted/40 px-3 py-1.5 text-xs text-muted-foreground">
      <span className="min-w-0 flex-1 truncate" title={changeText(changes)}>
        {changeText(changes)}.{' '}
        {changes.reload_requested ? 'Your next message reloads the tools.' : 'Reload tools to use this in this chat; the next reply is a little slower.'}
      </span>
      {!changes.reload_requested && (
        <button type="button" disabled={busy} onClick={reload} className="inline-flex shrink-0 items-center gap-1 rounded border border-border px-2 py-0.5 text-foreground hover:bg-muted disabled:opacity-50">
          <RefreshCw className={`h-3 w-3 ${busy ? 'animate-spin' : ''}`} />Reload tools
        </button>
      )}
    </div>
  )
}
