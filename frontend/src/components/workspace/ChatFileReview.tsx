import { useEffect, useState } from 'react'
import api from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { captureChatIdentity, assertChatIdentityCurrent } from '../../utils/chatIdentity'
import { openChatFiles } from './chatFileNavigation'
import { markFilesReviewed, reviewEndpoint as endpoint, useReviewRevision, type ReviewResult as Result, type FileChange as Change } from './chatFileReview'

// Older replies' saved copies are cleared to save space; name the message the comparison starts from.
const startedFrom = (message?: { text: string; at?: string }) => {
  if (!message?.text) return ' since the oldest reply still saved'
  const at = message.at ? new Date(message.at).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : ''
  return ` since your message “${message.text}”${at ? ` (${at})` : ''}. Earlier changes are no longer saved.`
}

/** Mounted with an account/server/chat key by its owner. All requests and sends
 * stay with that chat; the server resolves and authorizes its saved boundaries. */
export function ChatFileReview({ workspacePath, sessionId, tabId, onBrowse }: {
  workspacePath: string; sessionId: string; tabId: string; onBrowse: () => void
}) {
  const [reviewId, setReviewId] = useState('')
  const [result, setResult] = useState<Result | null>(null)
  const [revision, refresh] = useReviewRevision()
  const [error, setError] = useState('')
  const [sending, setSending] = useState(false)
  const [sent, setSent] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const baseline = result?.history.baseline_snapshot_id
  const params = { workspace_path: workspacePath, session_id: sessionId, review_id: reviewId, baseline_snapshot_id: baseline }
  useEffect(() => {
    const controller = new AbortController()
    setResult(null); setError('')
    void api.get<Result>(endpoint, { params: { workspace_path: workspacePath, session_id: sessionId, review_id: reviewId, op: 'snapshot_compare' }, signal: controller.signal })
      .then(({ data }) => { if (!controller.signal.aborted) setResult(data) })
      .catch(() => { if (!controller.signal.aborted) setError('Could not load this saved comparison. Refresh and try again.') })
    return () => controller.abort()
  }, [workspacePath, sessionId, reviewId, revision])
  const through = result?.history.last_snapshot_id
  const loadMore = async () => {
    if (!result?.next_offset || !result.comparison) return
    const shown = result
    setLoadingMore(true)
    try {
      const { data } = await api.get<Result>(endpoint, { params: { ...params, op: 'snapshot_compare', through_snapshot_id: shown.history.last_snapshot_id, offset: shown.next_offset } })
      setResult(current => current === shown && data.comparison ? { ...data, comparison: { ...data.comparison, changes: [...shown.comparison!.changes, ...data.comparison.changes] } } : current)
    } catch { setError('Could not load more files. Refresh and try again.') }
    finally { setLoadingMore(false) }
  }
  const changes: Change[] = result?.comparison?.changes ?? []
  const send = async () => {
    const identity = captureChatIdentity()
    setSending(true); setError(''); setSent(false)
    try {
      assertChatIdentityCurrent(identity)
      if (useChatStore.getState().chatTabs[tabId]?.sessionId !== sessionId) throw new Error('This chat changed. Reopen Review changes before sending.')
      await sendWorkspacePaneMessageToChat({ tabId, message: 'Looks good.' })
      setSent(true)
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Could not send to this chat.') }
    finally { setSending(false) }
  }
  return <section className="flex h-full min-h-0 flex-col" aria-label="Changes in this chat">
    <header className="flex flex-wrap items-center gap-2 border-b border-border p-3">
      <strong className="mr-auto">{reviewId ? 'Reviewed changes' : result?.history.reviews?.length ? 'Changes since your last review' : 'Changes in this chat'}</strong>
      <button type="button" className="text-xs underline" onClick={refresh}>Refresh</button>
      <button type="button" className="text-xs underline" onClick={onBrowse}>Browse files</button>
      {!!result?.history.reviews?.length && <details className="w-full text-xs"><summary className="cursor-pointer">Review history</summary><div className="mt-2 flex flex-wrap gap-3"><button type="button" className="underline" onClick={() => setReviewId('')}>Changes since your last review</button>{[...result.history.reviews].reverse().map(point => <button type="button" className="underline" key={point.snapshot_id} onClick={() => setReviewId(point.snapshot_id)}>Reviewed {new Date(point.reviewed_at).toLocaleString()}</button>)}</div></details>}
    </header>
    {error && <p role="alert" className="p-3 text-sm text-destructive">{error}</p>}
    <div className="min-h-0 flex-1 overflow-auto">
      {!result && !error && <p className="p-3 text-sm">Loading saved comparison…</p>}
      {result && !result.comparison && <p className="p-3 text-sm">{result.history.note}</p>}
      <div className="grid gap-1 border-b border-border p-2">
        {result?.comparison && <p className="p-2 text-xs text-muted-foreground">{result.total_changes ?? changes.length} changed files{!reviewId && !result.history.reviews?.length && !result.history.available ? startedFrom(result.history.start_message) : ''}</p>}
        {changes.map(change => <button type="button" key={change.path} className="flex gap-2 rounded px-2 py-1 text-left text-xs hover:bg-muted/50" onClick={() => { if (through) openChatFiles(sessionId, { reference: { workspacePath, sessionId, snapshotId: '' }, change, query: { session_id: sessionId, through_snapshot_id: through, baseline_snapshot_id: baseline || 'start' } }) }}>
          <span className="min-w-0 flex-1 break-all">{change.path}</span><span>{change.status === 'A' ? 'Added' : change.status === 'D' ? 'Deleted' : ''}</span><span className="text-diff-add">{!change.binary && `+${change.added ?? 0}`}</span><span className="text-diff-del">{change.binary ? 'Binary' : `−${change.removed ?? 0}`}</span>
        </button>)}
        {!!result?.next_offset && <button type="button" disabled={loadingMore} className="p-2 text-xs underline" onClick={() => void loadMore()}>{loadingMore ? 'Loading…' : 'Show more files'}</button>}
        {result?.comparison && changes.length === 0 && <p className="p-2 text-sm">No changes since this review point.</p>}
      </div>
      {(result?.comparison?.uncaptured_changes ?? 0) > 0 && <p role="status" className="p-3 text-xs text-muted-foreground">{result?.comparison?.uncaptured_changes} files could not be compared.</p>}
      <p className="p-3 text-xs text-muted-foreground">{result?.comparison?.note}</p>
    </div>
    {!reviewId && result?.history.can_review && through && <div className="border-t px-3 py-2 text-xs">{result.history.baseline_snapshot_id === through ? 'Reviewed up to here' : <button type="button" disabled={sending} className="rounded border px-3 py-1" onClick={() => { setSending(true); void markFilesReviewed(workspacePath,sessionId,through).catch(() => setError('Could not save the review point. Refresh and try again.')).finally(() => setSending(false)) }}>Mark as reviewed</button>}</div>}
    <footer className="flex items-center gap-2 border-t p-3 text-xs"><span className="mr-auto">{sent ? 'Sent to this chat.' : 'Posts a message in this chat.'}</span><button type="button" disabled={sending || !changes.length} className="rounded border px-3 py-1 disabled:opacity-50" onClick={() => void send()}>Looks good</button></footer>
  </section>
}
