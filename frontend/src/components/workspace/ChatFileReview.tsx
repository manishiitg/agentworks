import { useEffect, useState } from 'react'
import api from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { captureChatIdentity, assertChatIdentityCurrent } from '../../utils/chatIdentity'
import DiffRenderer, { type DiffLineAnchor } from '../ui/DiffRenderer'
import { SavedFile } from '../chat/ReplyFileChanges'
import { markFilesReviewed, reviewEndpoint as endpoint, useReviewRevision, type ReviewResult as Result, type FileChange as Change } from './chatFileReview'

/** Mounted with an account/server/chat key by its owner. All requests and sends
 * stay with that chat; the server resolves and authorizes its saved boundaries. */
export function ChatFileReview({ workspacePath, sessionId, tabId, onBrowse }: {
  workspacePath: string; sessionId: string; tabId: string; onBrowse: () => void
}) {
  const [reviewId, setReviewId] = useState('')
  const [result, setResult] = useState<Result | null>(null)
  const [file, setFile] = useState('')
  const [diff, setDiff] = useState<Result | null>(null)
  const [revision, refresh] = useReviewRevision()
  const [error, setError] = useState('')
  const [anchor, setAnchor] = useState<DiffLineAnchor | null>(null)
  const [comment, setComment] = useState('')
  const [sending, setSending] = useState(false)
  const [sent, setSent] = useState(false)
  const [preview, setPreview] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const snapshotId = ''
  const baseline = result?.history.baseline_snapshot_id
  const params = { workspace_path: workspacePath, session_id: sessionId, review_id: reviewId, baseline_snapshot_id: baseline }
  useEffect(() => {
    const controller = new AbortController()
    setResult(null); setDiff(null); setFile(''); setAnchor(null); setError(''); setPreview(false)
    void api.get<Result>(endpoint, { params: { workspace_path: workspacePath, session_id: sessionId, review_id: reviewId, op: 'snapshot_compare' }, signal: controller.signal })
      .then(({ data }) => { if (!controller.signal.aborted) setResult(data) })
      .catch(() => { if (!controller.signal.aborted) setError('Could not load this saved comparison. Refresh and try again.') })
    return () => controller.abort()
  }, [workspacePath, sessionId, reviewId, revision])
  const through = result?.history.last_snapshot_id
  useEffect(() => {
    const controller = new AbortController()
    setDiff(null); setAnchor(null); setPreview(false); setSent(false)
    if (!file || !through) return () => controller.abort()
    void api.get<Result>(endpoint, { params: { workspace_path: workspacePath, session_id: sessionId, baseline_snapshot_id: baseline, through_snapshot_id: through, op: 'snapshot_compare', file }, signal: controller.signal })
      .then(({ data }) => { if (!controller.signal.aborted) setDiff(data) })
      .catch(() => { if (!controller.signal.aborted) setError('This saved file is unavailable. Refresh the comparison.') })
    return () => controller.abort()
  }, [file, baseline, through, workspacePath, sessionId, revision])
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
  const selected = changes.find(change => change.path === file)
  const send = async (looksGood = false) => {
    const identity = captureChatIdentity()
    setSending(true); setError(''); setSent(false)
    try {
      let message = 'Looks good.'
      if (!looksGood) {
        if (!anchor || !comment.trim() || !through) return
        const { data } = await api.get<{ context: string }>(endpoint, { params: {
          ...params, through_snapshot_id: through, op: 'snapshot_compare', file, side: anchor.side, line: anchor.line,
        } })
        if (!data.context) throw new Error('This saved line is no longer available. Refresh the comparison.')
        message = `${data.context}\n\n${comment.trim()}`
      }
      assertChatIdentityCurrent(identity)
      if (useChatStore.getState().chatTabs[tabId]?.sessionId !== sessionId) throw new Error('This chat changed. Reopen Review changes before sending.')
      await sendWorkspacePaneMessageToChat({ tabId, message })
      setComment(''); setAnchor(null); setSent(true)
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Could not send to this chat.') }
    finally { setSending(false) }
  }
  return <section className="flex h-full min-h-0 flex-col" aria-label="Changes in this chat">
    <header className="flex flex-wrap items-center gap-2 border-b border-border p-3">
      <strong className="mr-auto">{reviewId ? 'Reviewed changes' : 'Changes since your last review'}</strong>
      <button type="button" className="text-xs underline" onClick={refresh}>Refresh</button>
      <button type="button" className="text-xs underline" onClick={onBrowse}>Browse files</button>
      {!!result?.history.reviews?.length && <details className="w-full text-xs"><summary className="cursor-pointer">Review history</summary><div className="mt-2 flex flex-wrap gap-3"><button type="button" className="underline" onClick={() => setReviewId('')}>Changes since your last review</button>{[...result.history.reviews].reverse().map(point => <button type="button" className="underline" key={point.snapshot_id} onClick={() => setReviewId(point.snapshot_id)}>Reviewed {new Date(point.reviewed_at).toLocaleString()}</button>)}</div></details>}
    </header>
    {error && <p role="alert" className="p-3 text-sm text-destructive">{error}</p>}
    <div className="min-h-0 flex-1 overflow-auto">
      {!result && !error && <p className="p-3 text-sm">Loading saved comparison…</p>}
      {result && !result.comparison && <p className="p-3 text-sm">{result.history.note}</p>}
      {(result?.comparison?.uncaptured_changes ?? 0) > 0 && <p role="status" className="p-3 text-xs text-muted-foreground">{result?.comparison?.uncaptured_changes} {result?.comparison?.uncaptured_changes === 1 ? 'file may' : 'files may'} have changed but could not be compared.</p>}
      <div className="grid gap-1 border-b border-border p-2">
        {result?.comparison && <p className="p-2 text-xs text-muted-foreground">{result.total_changes ?? changes.length} changed files</p>}
        {changes.map(change => <button type="button" key={change.path} aria-pressed={file === change.path} className={`flex gap-2 rounded px-2 py-1 text-left text-xs ${file === change.path ? 'bg-muted' : 'hover:bg-muted/50'}`} onClick={() => { setFile(change.path); setError('') }}>
          <span className="min-w-0 flex-1 break-all">{change.path}</span><span>{change.status === 'A' ? 'Added' : change.status === 'D' ? 'Deleted' : ''}</span><span className="text-green-600">{!change.binary && `+${change.added ?? 0}`}</span><span className="text-red-500">{change.binary ? 'Binary' : `−${change.removed ?? 0}`}</span>
        </button>)}
        {!!result?.next_offset && <button type="button" disabled={loadingMore} className="p-2 text-xs underline" onClick={() => void loadMore()}>{loadingMore ? 'Loading…' : 'Show more files'}</button>}
        {result?.comparison && changes.length === 0 && <p className="p-2 text-sm">No changes since this review point.</p>}
      </div>
      {selected && <div className="p-2">
        <h3 className="mb-2 break-all text-sm font-medium">{file}</h3>
        {!diff ? <p className="text-sm">Loading file…</p>
          : !diff.comparison ? <p className="text-sm">{diff.history.note}</p>
          : selected.binary ? <button type="button" className="rounded border px-2 py-1 text-sm" onClick={() => setPreview(true)}>Open saved version</button>
          : <><p className="mb-2 text-xs text-muted-foreground">Select a line number to ask for a change.</p><DiffRenderer content={diff.comparison.diff || ''} onSelectLine={setAnchor} maxHeightClassName="max-h-none" />{diff.comparison.truncated && <p className="text-xs">Only the start of this large comparison is shown.</p>}</>}
        {preview && through && <SavedFile reference={{ workspacePath, sessionId, snapshotId }} query={{ session_id: sessionId, through_snapshot_id: through, baseline_snapshot_id: baseline || 'start' }} change={selected} view="preview" close={() => setPreview(false)} />}
      </div>}
      {anchor && <div className="space-y-2 border-t p-3"><p className="text-xs">{file} · {anchor.side} line {anchor.line}</p><textarea aria-label="Request a change" className="min-h-20 w-full rounded border bg-background p-2 text-sm" value={comment} onChange={event => setComment(event.target.value)} placeholder="What should change here?" /><button type="button" disabled={sending || !comment.trim()} className="rounded border px-3 py-1 text-sm disabled:opacity-50" onClick={() => void send()}>Send to this chat</button></div>}
      <p className="p-3 text-xs text-muted-foreground">{result?.comparison?.note}</p>
    </div>
    {!reviewId && result?.history.can_review && through && <div className="border-t px-3 py-2 text-xs">{result.history.baseline_snapshot_id === through ? 'Reviewed up to here' : <button type="button" disabled={sending} className="rounded border px-3 py-1" onClick={() => { setSending(true); void markFilesReviewed(workspacePath,sessionId,through).catch(() => setError('Could not save the review point. Refresh and try again.')).finally(() => setSending(false)) }}>Mark as reviewed</button>}</div>}
    <footer className="flex items-center gap-2 border-t p-3 text-xs"><span className="mr-auto">{sent ? 'Sent to this chat.' : 'Posts a message in this chat.'}</span><button type="button" disabled={sending || !changes.length} className="rounded border px-3 py-1 disabled:opacity-50" onClick={() => void send(true)}>Looks good</button></footer>
  </section>
}
