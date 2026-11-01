import { useEffect, useState } from 'react'
import api from '../../services/api'
import { ChatFileReview } from './ChatFileReview'
import { WorkspaceViewHeader } from '../workflow/WorkspaceViewHeader'
import { reviewEndpoint, useReviewRevision, type ReviewHistory } from './chatFileReviewData'

// History selects an exact saved capture within a real checkout. It never
// combines overlapping turns into an invented net diff or attributes authorship.
export function ProjectChangesPanel({ workspacePath, sessionId, tabId, onBrowse }: { workspacePath: string; sessionId: string; tabId: string; onBrowse: () => void }) {
  const [history, setHistory] = useState<ReviewHistory | null>(null)
  const [checkoutId, setCheckoutId] = useState('')
  const [snapshotId, setSnapshotId] = useState('')
  const [error, setError] = useState('')
  const [revision, refresh] = useReviewRevision()
  useEffect(() => {
    const controller = new AbortController()
    setError('')
    void api.get<ReviewHistory>(reviewEndpoint, { params: { workspace_path: workspacePath, history_scope: 'project', op: 'snapshots' }, signal: controller.signal })
      .then(({ data }) => { if (!controller.signal.aborted) setHistory(data) })
      .catch(() => { if (!controller.signal.aborted) setError('Could not load project history.') })
    return () => controller.abort()
  }, [workspacePath, revision])
  const checkout = history?.checkouts?.find(item => item.id === checkoutId) ?? history?.checkouts?.[0]
  const capture = checkout?.captures.find(item => item.snapshot_id === snapshotId) ?? checkout?.captures[0]
  const select = 'min-w-0 rounded border border-border bg-background px-1.5 py-0.5 text-xs text-foreground'
  if (!checkout || !capture) return <section className="flex h-full min-h-0 flex-col">
    <WorkspaceViewHeader title="Files" subtitle="Project changes" />
    <p className="p-3 text-sm">{error || history?.note || (history ? 'No saved project changes yet.' : 'Loading…')}</p>
    {error && <button type="button" className="p-3 text-xs underline" onClick={refresh}>Retry</button>}
  </section>
  const toolbar = <div className="flex min-w-0 gap-2 border-b border-border px-3 py-2">
    <select aria-label="Worktree" className={`${select} flex-1`} value={checkout.id} onChange={event => { setCheckoutId(event.target.value); setSnapshotId('') }}>
      {history!.checkouts!.map(item => <option key={item.id} value={item.id}>{item.name}{item.captures[0]?.branch ? ` · ${item.captures[0].branch}` : ''}</option>)}
    </select>
    <select aria-label="Saved project history" className={`${select} max-w-[55%]`} value={capture.snapshot_id} onChange={event => setSnapshotId(event.target.value)}>
      {checkout.captures.map((item, index) => <option key={item.snapshot_id} value={item.snapshot_id}>{index === 0 ? 'Latest · ' : ''}{new Date(item.at).toLocaleString()}{item.branch ? ` · ${item.branch}` : ''} · {item.files} files{item.note ? ' · Incomplete' : ''}</option>)}
    </select>
  </div>
  return <ChatFileReview key={`${checkout.id}:${capture.snapshot_id}`} toolbar={toolbar} reply={{ workspacePath, sessionId, snapshotId: capture.snapshot_id, historyScope: 'project', checkoutId: checkout.id }} workspacePath={workspacePath} sessionId={sessionId} tabId={tabId} onBrowse={onBrowse} />
}
