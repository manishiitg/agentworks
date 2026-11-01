import { useEffect, useState } from 'react'
import { ArrowLeft } from 'lucide-react'
import api from '../../services/api'
import type { PollingEvent } from '../../services/api-types'
import { TerminalEventTranscript } from '../TerminalEventTranscript'
import { WorkspaceViewHeader } from '../workflow/WorkspaceViewHeader'
import { reviewEndpoint, type ProjectCaptureSource } from './chatFileReviewData'

type TurnPage = { source: ProjectCaptureSource; events: PollingEvent[]; next_sequence: number }

/** Read the exact saved turn without resuming or rebinding any chat. */
export function ProjectTurnPanel({ workspacePath, checkoutId, snapshotId, onBack }: { workspacePath: string; checkoutId: string; snapshotId: string; onBack: () => void }) {
  const [page, setPage] = useState<TurnPage | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [retry, setRetry] = useState(0)
  const [cursor, setCursor] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    setLoading(true); setError('')
    void api.get<TurnPage>(reviewEndpoint, { params: { op: 'snapshot_turn', history_scope: 'project', workspace_path: workspacePath, checkout_id: checkoutId, snapshot_id: snapshotId, ...(cursor ? { after_sequence: cursor } : {}) }, signal: controller.signal })
      .then(({ data }) => { if (!controller.signal.aborted) setPage(previous => cursor && previous ? { ...data, events: [...previous.events, ...data.events] } : data) })
      .catch(() => { if (!controller.signal.aborted) setError('The original turn could not be opened. It may no longer be available to you.') })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [workspacePath, checkoutId, snapshotId, cursor, retry])
  return <section className="flex h-full min-h-0 flex-col" aria-label="Original turn">
    <WorkspaceViewHeader title="Original turn" subtitle={page?.source.agent_name} actions={<button type="button" onClick={onBack} className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"><ArrowLeft className="h-3.5 w-3.5" />Changes</button>} />
    {error && <p role="alert" className="p-3 text-xs">{error} <button type="button" className="underline" onClick={() => setRetry(value => value + 1)}>Retry</button></p>}
    <div className="min-h-0 flex-1"><TerminalEventTranscript terminal={null} events={page?.events} loading={loading && !page} scrollKey={`file-source:${workspacePath}:${snapshotId}`} /></div>
    {!!page?.next_sequence && <button type="button" disabled={loading} className="shrink-0 border-t border-border p-2 text-xs underline" onClick={() => setCursor(page.next_sequence)}>{loading ? 'Loading…' : 'Show more of this turn'}</button>}
  </section>
}
