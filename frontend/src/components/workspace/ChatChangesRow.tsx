import { useEffect, useRef, useState } from 'react'
import { AlertTriangle, FileDiff } from 'lucide-react'
import api from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { WorkspaceContextRow } from './WorkspaceContextCard'
import { reviewEndpoint, useReviewRevision, type ReviewResult } from './chatFileReviewData'

type Props = { workspacePath: string; sessionId: string; onOpen: () => void }

// The compare is slow on the server (seconds on a big chat) and the menu opens on every page change.
// Reuse a recent answer for the same chat and saved replies; a new reply, a review or Retry reloads.
const FRESH_MS = 30_000
const recent = new Map<string, { result: ReviewResult; at: number }>()

/** The menu summarizes this chat; project-wide history stays inside Files. */
export function ChatChangesRow(props: Props) {
  return <ChatChangesSummary key={`${props.workspacePath}:${props.sessionId}`} {...props} />
}

function ChatChangesSummary({ workspacePath, sessionId, onOpen }: Props) {
  const [revision] = useReviewRevision()
  const savedEvents = useChatStore(state => Object.values(state.tabEvents).flat().filter(event => event.session_id === sessionId && (event.type === 'reply_file_changes' || event.type === 'file_reviewed')).map(event => event.id).join(','))
  const cacheKey = `${workspacePath}:${sessionId}:${savedEvents}`
  const [result, setResult] = useState<ReviewResult | null>(() => recent.get(cacheKey)?.result ?? null)
  const [error, setError] = useState('')
  const [retry, setRetry] = useState(0)
  const firstRun = useRef(true)
  useEffect(() => {
    const controller = new AbortController()
    const load = (force: boolean) => {
      const cached = recent.get(cacheKey)
      if (!force && cached && Date.now() - cached.at < FRESH_MS) { setResult(cached.result); return }
      void api.get<ReviewResult>(reviewEndpoint, { params: { workspace_path: workspacePath, session_id: sessionId, baseline_snapshot_id: 'start', op: 'snapshot_compare' }, signal: controller.signal })
        .then(({ data }) => { if (!controller.signal.aborted) {
          if (!data.summary && data.history.replies.length) { setError('Could not load changes.'); return }
          recent.set(cacheKey, { result: data, at: Date.now() })
          setResult(data); setError('')
        } })
        .catch(() => { if (!controller.signal.aborted) setError('Could not load changes.') })
    }
    // Opening the menu reuses a fresh answer; a review, Retry or new saved reply asks again.
    load(!firstRun.current)
    firstRun.current = false
    const onFocus = () => load(false)
    window.addEventListener('focus', onFocus)
    return () => { controller.abort(); window.removeEventListener('focus', onFocus) }
  }, [cacheKey, workspacePath, sessionId, revision, retry])
  const summary = result?.summary
  const comparison = result?.comparison
  const warning = comparison?.note || (comparison?.skipped_count ? 'Some files could not be saved.' : '') || (result?.history.start_message ? 'Earlier changes are no longer saved.' : '')
  return <div>
    <WorkspaceContextRow icon={FileDiff} hint={`Saved changes across this chat’s replies. Other chats or editors using the same files may contribute.${warning ? ` ${warning}` : ''}`} onClick={() => onOpen()} detail={error ? 'Unavailable' : !result ? 'Loading…' : <span className="inline-flex items-center gap-1.5 whitespace-nowrap tabular-nums">
      <span>{summary?.files ?? 0} file{summary?.files === 1 ? '' : 's'}</span><span className="text-diff-add">+{summary?.added ?? 0}</span><span className="text-diff-del">−{summary?.removed ?? 0}</span>
      {warning && <span role="img" aria-label={warning} title={warning} className="inline-flex shrink-0 text-amber-500"><AlertTriangle aria-hidden="true" className="h-3 w-3" /></span>}
    </span>}>Changes</WorkspaceContextRow>
    {error && <p role="alert" className="px-1 text-xs text-muted-foreground">{error} <button type="button" className="underline hover:text-foreground" onClick={() => setRetry(value => value + 1)}>Retry</button></p>}
  </div>
}
