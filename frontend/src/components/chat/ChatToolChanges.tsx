import { useCallback, useEffect, useState } from 'react'
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
  const refresh = useCallback(() => {
    if (!sessionId || document.visibilityState === 'hidden') return
    agentApi.getToolChanges(sessionId).then(setChanges).catch(() => setChanges(null))
  }, [sessionId])
  useEffect(() => {
    setChanges(null)
    refresh()
    const timer = window.setInterval(refresh, POLL_MS)
    window.addEventListener('focus', refresh)
    return () => { window.clearInterval(timer); window.removeEventListener('focus', refresh) }
  }, [refresh])
  // A finished reply may have rebuilt the chat with the new apps.
  useEffect(() => { if (!streaming) refresh() }, [streaming, refresh])
  if (!sessionId || !changes || changes.added.length + changes.removed.length === 0) return null
  const reload = () => {
    setBusy(true)
    agentApi.reloadTools(sessionId).then(setChanges).catch(refresh).finally(() => setBusy(false))
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
