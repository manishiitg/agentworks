import { useEffect, useState } from 'react'
import api from '../../services/api'
import { markFilesReviewed, useReviewRevision, type ReviewResult } from '../workspace/chatFileReview'
import type { ReplyFileChangesRef } from '../../utils/replyFileChanges'
import { useProjectMode } from '../../products/work/codeLocalFiles'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { openChatFiles } from '../workspace/chatFileNavigation'

const endpoint = '/api/workspace-snapshots'
const paramsFor = (reference: ReplyFileChangesRef) => ({ workspace_path: reference.workspacePath, snapshot_id: reference.snapshotId })

export function ReplyFileChanges({ reference }: { reference: ReplyFileChangesRef }) {
  const mode = useProjectMode(reference.sessionId)
  const account = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = JSON.stringify([account, server, reference.workspacePath, reference.snapshotId])
  if (mode !== 'dev') return null
  return <ScopedFileChanges key={scope} reference={reference} />
}

function ScopedFileChanges({ reference }: { reference: ReplyFileChangesRef }) {
  const [result, setResult] = useState<ReviewResult | null>(null)
  const comparison = result?.comparison
  const [revision] = useReviewRevision()
  const [reviewing, setReviewing] = useState(false)
  const [reviewError, setReviewError] = useState('')
  const latest = result?.history.latest_snapshot_id === reference.snapshotId && (!reference.latestSnapshotId || reference.latestSnapshotId === reference.snapshotId)
  const reviewed = result?.history.baseline_snapshot_id === reference.snapshotId
  useEffect(() => {
    const controller = new AbortController()
    void api.get<ReviewResult>(endpoint, { params: { ...paramsFor(reference), session_id: reference.sessionId, op: 'snapshot_compare' }, signal: controller.signal })
      .then(response => { if (!controller.signal.aborted) setResult(response.data) })
      .catch(() => { /* Expired/unavailable history has no change card. */ })
    return () => controller.abort()
  }, [reference.workspacePath, reference.snapshotId, reference.sessionId, reference.latestSnapshotId, revision])
  if (!comparison?.changes?.length && !(latest && result?.history.can_review)) return null
  if (!comparison) return null
  return (
    <section className="my-3 rounded-lg border border-border px-3 py-2 text-sm" aria-label="Files changed during this reply">
      <details>
        <summary className="cursor-pointer font-medium">Files changed during this reply · {result?.total_changes ?? comparison.changes.length}</summary>
        <ul className="mt-2 space-y-1">
          {comparison.changes.map(change => (
            <li key={change.path} className="flex flex-wrap items-center gap-2">
              <button type="button" className="min-w-0 break-all text-left underline underline-offset-2" onClick={() => openChatFiles(reference.sessionId, { reference, change })}>{change.path}</button>
              <span className="text-xs text-muted-foreground">{change.status === 'A' ? 'Added' : change.status === 'D' ? 'Deleted' : 'Changed'}</span>
              {!change.binary && typeof change.added === 'number' && typeof change.removed === 'number'
                && <span className="text-xs" aria-label={`${change.added} lines added, ${change.removed} lines removed`}><span className="text-green-600">+{change.added}</span> <span className="text-red-500">−{change.removed}</span></span>}
              {!change.binary && <button type="button" className="ml-auto text-xs underline" onClick={() => openChatFiles(reference.sessionId, { reference, change })}>Compare</button>}
            </li>
          ))}
        </ul>
        {!!result?.next_offset && <button type="button" className="mt-2 text-xs underline" onClick={() => {
          const shown = result
          void api.get<ReviewResult>(endpoint, { params: { ...paramsFor(reference), session_id: reference.sessionId, op: 'snapshot_compare', offset: shown.next_offset } })
            .then(({ data }) => setResult(current => current === shown && data.comparison ? { ...data, comparison: { ...data.comparison, changes: [...shown.comparison!.changes, ...data.comparison.changes] } } : current))
            .catch(() => setReviewError('Could not load more files. Try again.'))
        }}>Show more files</button>}
      <button type="button" className="mt-2 text-xs underline" onClick={() => openChatFiles(reference.sessionId)}>Review changes</button>
      {latest && <div className="mt-2 flex flex-wrap items-center gap-2 border-t pt-2 text-xs">
        {result?.since_review && <span>Since your last review: {result.since_review.files} files <span className="text-green-600">+{result.since_review.added}</span> <span className="text-red-500">−{result.since_review.removed}</span></span>}
        {reviewed ? <span>Reviewed up to here</span> : result?.history.can_review && <button type="button" disabled={reviewing} className="rounded border px-2 py-1" onClick={() => { setReviewing(true); setReviewError(''); void markFilesReviewed(reference.workspacePath,reference.sessionId,reference.snapshotId).catch(() => setReviewError('Could not save the review point. Try again.')).finally(() => setReviewing(false)) }}>Mark as reviewed</button>}
        {reviewError && <p role="alert">{reviewError}</p>}
      </div>}
      </details>

    </section>
  )
}
