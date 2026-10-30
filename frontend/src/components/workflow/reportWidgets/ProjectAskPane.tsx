import { lazy, Suspense, useEffect, useState } from 'react'
import { useAuthStore } from '../../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../../stores/useWorkspaceConnectionStore'
import { openProjectAskConversation } from '../../../utils/projectAskConversation'
import { WorkingTipsContext } from '../../chat/workingTipsContext'

const ChatArea = lazy(() => import('../../ChatArea'))

/** A shared project without a dashboard still uses its caller's normal Ask chat. */
export function ProjectAskPane({ workspacePath }: { workspacePath: string }) {
  const viewer = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = JSON.stringify([server, viewer, workspacePath])
  const [bound, setBound] = useState<{ scope: string; tabId: string } | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [retry, setRetry] = useState(0)
  useEffect(() => {
    let cancelled = false
    setBound(null); setError(null)
    if (viewer) void openProjectAskConversation({ workspacePath }, { activate: false, userView: true }).then(tabId => {
      if (!cancelled) setBound({ scope, tabId })
    }).catch(reason => {
      if (!cancelled) setError(reason instanceof Error ? reason.message : 'Could not open chat.')
    })
    return () => { cancelled = true }
  }, [scope, viewer, workspacePath, retry])
  const tabId = bound?.scope === scope ? bound.tabId : null
  return <section aria-label="Project chat" className="flex min-h-0 flex-1 flex-col">
    {tabId ? <WorkingTipsContext.Provider value={false}><Suspense fallback={<p role="status">Opening chat…</p>}><ChatArea tabId={tabId} compact hideHeader previousChatsWorkspacePath={workspacePath} landingContent={<div />} composerPlaceholder="Ask a question…" /></Suspense></WorkingTipsContext.Provider>
      : <div className="p-4 text-sm text-muted-foreground"><p role="status">{error || (viewer ? 'Opening chat…' : 'Sign in to open chat.')}</p>{error && <button type="button" onClick={() => setRetry(value => value + 1)} className="mt-2 underline">Try opening chat again</button>}</div>}
  </section>
}
