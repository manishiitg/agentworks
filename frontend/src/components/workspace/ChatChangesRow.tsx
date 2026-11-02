import { useEffect, useState } from 'react'
import { AlertTriangle, FileDiff } from 'lucide-react'
import api from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { WorkspaceContextRow } from './WorkspaceContextCard'
import { reviewEndpoint, useReviewRevision, type ReviewResult } from './chatFileReviewData'

type Props = { workspacePath: string; sessionId: string; onOpen: () => void }

/** The menu summarizes this chat; project-wide history stays inside Files. */
export function ChatChangesRow(props: Props) {
  return <ChatChangesSummary key={`${props.workspacePath}:${props.sessionId}`} {...props} />
}

function ChatChangesSummary({ workspacePath, sessionId, onOpen }: Props) {
  const [result, setResult] = useState<ReviewResult | null>(null)
  const [error, setError] = useState('')
  const [retry, setRetry] = useState(0)
  const [revision] = useReviewRevision()
  const savedEvents = useChatStore(state => Object.values(state.tabEvents).flat().filter(event => event.session_id === sessionId && (event.type === 'reply_file_changes' || event.type === 'file_reviewed')).map(event => event.id).join(','))
  useEffect(() => {
    const controller = new AbortController()
    const load = () => {
      void api.get<ReviewResult>(reviewEndpoint, { params: { workspace_path: workspacePath, session_id: sessionId, baseline_snapshot_id: 'start', op: 'snapshot_compare' }, signal: controller.signal })
        .then(({ data }) => { if (!controller.signal.aborted) {
          if (!data.summary && data.history.replies.length) { setError('Could not load changes.'); return }
          setResult(data); setError('')
        } })
        .catch(() => { if (!controller.signal.aborted) setError('Could not load changes.') })
    }
    load()
    window.addEventListener('focus', load)
    return () => { controller.abort(); window.removeEventListener('focus', load) }
  }, [workspacePath, sessionId, revision, savedEvents, retry])
  const summary = result?.summary
  const comparison = result?.comparison
  const warning = comparison?.note || (comparison?.skipped_count ? 'Some files could not be saved.' : '') || (result?.history.start_message ? 'Earlier changes are no longer saved.' : '')
  return <div>
    <WorkspaceContextRow icon={FileDiff} hint={`Saved changes across this chat’s replies. Other chats or editors using the same files may contribute.${warning ? ` ${warning}` : ''}`} onClick={onOpen} detail={error ? 'Unavailable' : !result ? 'Loading…' : <span className="inline-flex items-center gap-1.5 whitespace-nowrap tabular-nums">
      <span>{summary?.files ?? 0} file{summary?.files === 1 ? '' : 's'}</span><span className="text-diff-add">+{summary?.added ?? 0}</span><span className="text-diff-del">−{summary?.removed ?? 0}</span>
      {warning && <span role="img" aria-label={warning} title={warning} className="inline-flex shrink-0 text-amber-500"><AlertTriangle aria-hidden="true" className="h-3 w-3" /></span>}
    </span>}>Changes</WorkspaceContextRow>
    {error && <p role="alert" className="px-1 text-xs text-muted-foreground">{error} <button type="button" className="underline hover:text-foreground" onClick={() => setRetry(value => value + 1)}>Retry</button></p>}
  </div>
}
