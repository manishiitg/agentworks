import { useEffect, useState } from 'react'
import { FileDiff } from 'lucide-react'
import api from '../../services/api'
import type { ReplyFileChangesRef } from '../../utils/replyFileChanges'
import { useChatStore } from '../../stores/useChatStore'
import { WorkspaceContextRow } from './WorkspaceContextCard'
import { reviewEndpoint, useReviewRevision, type ReviewHistory, type ReviewResult, type ReviewSummary } from './chatFileReviewData'

/** The server selects this chat's latest saved reply; workspace Git is a separate review tab. */
export function ChatChangesRow({ workspacePath, sessionId, onOpen }: { workspacePath: string; sessionId: string; onOpen: (reply?: ReplyFileChangesRef) => void }) {
  const [latest, setLatest] = useState<ReplyFileChangesRef | null>(null)
  const [summary, setSummary] = useState<ReviewSummary | null>(null)
  const [note, setNote] = useState('')
  const [error, setError] = useState('')
  const [retry, setRetry] = useState(0)
  const [revision] = useReviewRevision()
  const savedEvents = useChatStore(state => (state.tabEvents[sessionId] ?? []).filter(event => event.type === 'reply_file_changes' || event.type === 'file_reviewed').map(event => event.id).join(','))
  useEffect(() => { setLatest(null); setSummary(null); setError(''); setNote('') }, [workspacePath, sessionId])
  useEffect(() => {
    const controller = new AbortController()
    void (async () => {
      const { data: history } = await api.get<ReviewHistory>(reviewEndpoint, { params: { workspace_path: workspacePath, session_id: sessionId, op: 'snapshots' }, signal: controller.signal })
      const snapshotId = history.latest_snapshot_id || history.last_snapshot_id
      if (!snapshotId) { if (!controller.signal.aborted) { setLatest(null); setSummary({ files: 0, added: 0, removed: 0 }); setError(''); setNote('') }; return }
      const { data } = await api.get<ReviewResult>(reviewEndpoint, { params: { workspace_path: workspacePath, session_id: sessionId, snapshot_id: snapshotId, op: 'snapshot_compare' }, signal: controller.signal })
      if (!data.summary) throw new Error('Saved changes are unavailable.')
      if (!controller.signal.aborted) {
        setLatest({ workspacePath, sessionId, snapshotId, latestSnapshotId: snapshotId })
        setSummary(data.summary); setError(''); setNote(data.comparison?.note || '')
      }
    })().catch(() => { if (!controller.signal.aborted) setError('Could not load changes.') })
    return () => controller.abort()
  }, [workspacePath, sessionId, revision, savedEvents, retry])
  return <div className="space-y-0.5">
    <WorkspaceContextRow icon={FileDiff} hint={note || "Files changed in this chat’s latest saved reply."} onClick={() => onOpen(latest ?? undefined)} detail={error ? 'Unavailable' : summary === null ? 'Loading…' : note ? 'Incomplete' : <span className="inline-flex items-center gap-1.5 whitespace-nowrap tabular-nums">
      <span>{summary.files} file{summary.files === 1 ? '' : 's'}</span>
      <span className="text-diff-add">+{summary.added}</span>
      <span className="text-diff-del">−{summary.removed}</span>
    </span>}>Changes</WorkspaceContextRow>
    {error && <p role="alert" className="px-1 text-xs text-muted-foreground">{error} <button type="button" className="underline hover:text-foreground" onClick={() => setRetry(value => value + 1)}>Retry</button></p>}
  </div>
}
