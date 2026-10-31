import { useEffect, useState } from 'react'
import { FileDiff } from 'lucide-react'
import api from '../../services/api'
import type { ReplyFileChangesRef, ReplyFileGroup } from '../../utils/replyFileChanges'
import { useChatStore } from '../../stores/useChatStore'
import { WorkspaceContextRow } from './WorkspaceContextCard'
import { reviewEndpoint, useReviewRevision, type ReviewHistory } from './chatFileReviewData'

/** The server selects this chat's latest saved reply; workspace Git is a separate review tab. */
export function ChatChangesRow({ workspacePath, sessionId, onOpen }: { workspacePath: string; sessionId: string; onOpen: (reply?: ReplyFileChangesRef) => void }) {
  const [latest, setLatest] = useState<ReplyFileChangesRef | null>(null)
  const [count, setCount] = useState<number | null>(null)
  const [note, setNote] = useState('')
  const [error, setError] = useState('')
  const [retry, setRetry] = useState(0)
  const [revision] = useReviewRevision()
  const savedEvents = useChatStore(state => (state.tabEvents[sessionId] ?? []).filter(event => event.type === 'reply_file_changes' || event.type === 'file_reviewed').map(event => event.id).join(','))
  useEffect(() => { setLatest(null); setCount(null); setError(''); setNote('') }, [workspacePath, sessionId])
  useEffect(() => {
    const controller = new AbortController()
    void (async () => {
      const { data: history } = await api.get<ReviewHistory>(reviewEndpoint, { params: { workspace_path: workspacePath, session_id: sessionId, op: 'snapshots' }, signal: controller.signal })
      const snapshotId = history.latest_snapshot_id || history.last_snapshot_id
      if (!snapshotId) { if (!controller.signal.aborted) { setLatest(null); setCount(0); setError(''); setNote('') }; return }
      const { data } = await api.get<{ groups: ReplyFileGroup[]; note?: string }>(reviewEndpoint, { params: { workspace_path: workspacePath, session_id: sessionId, snapshot_id: snapshotId, op: 'reply_card' }, signal: controller.signal })
      if (!controller.signal.aborted) {
        setLatest({ workspacePath, sessionId, snapshotId, latestSnapshotId: snapshotId })
        setCount(data.groups.reduce((sum, group) => sum + group.total_files, 0)); setError(''); setNote(data.note || '')
      }
    })().catch(() => { if (!controller.signal.aborted) setError('Could not load changes.') })
    return () => controller.abort()
  }, [workspacePath, sessionId, revision, savedEvents, retry])
  return <div className="space-y-0.5">
    <WorkspaceContextRow icon={FileDiff} hint={note || "Files changed in this chat’s latest saved reply."} onClick={() => onOpen(latest ?? undefined)} detail={error ? 'Unavailable' : count === null ? 'Loading…' : note ? 'Incomplete' : `${count} file${count === 1 ? '' : 's'}`}>Latest reply</WorkspaceContextRow>
    {error && <p role="alert" className="px-1 text-xs text-muted-foreground">{error} <button type="button" className="underline hover:text-foreground" onClick={() => setRetry(value => value + 1)}>Retry</button></p>}
  </div>
}
