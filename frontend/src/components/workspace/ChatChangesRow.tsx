import { useEffect, useState } from 'react'
import { AlertTriangle, FileDiff } from 'lucide-react'
import api from '../../services/api'
import type { ReplyFileChangesRef } from '../../utils/replyFileChanges'
import { useChatStore } from '../../stores/useChatStore'
import { WorkspaceContextRow } from './WorkspaceContextCard'
import { reviewEndpoint, useReviewRevision, type ReviewHistory, type ProjectCheckout } from './chatFileReviewData'

/** Each real checkout has shared history across chats; counts describe its latest saved change. */
export function ChatChangesRow({ workspacePath, sessionId, onOpen }: { workspacePath: string; sessionId: string; onOpen: (reply?: ReplyFileChangesRef) => void }) {
  const [checkouts, setCheckouts] = useState<ProjectCheckout[] | null>(null)
  const [error, setError] = useState('')
  const [retry, setRetry] = useState(0)
  const [revision] = useReviewRevision()
  const savedEvents = useChatStore(state => Object.values(state.tabEvents).flat().filter(event => event.type === 'reply_file_changes' || event.type === 'file_reviewed').map(event => event.id).join(','))
  useEffect(() => { setCheckouts(null); setError('') }, [workspacePath])
  useEffect(() => {
    const controller = new AbortController()
    const load = () => {
      void api.get<ReviewHistory>(reviewEndpoint, { params: { workspace_path: workspacePath, history_scope: 'project', op: 'snapshots' }, signal: controller.signal })
        .then(({ data }) => { if (!controller.signal.aborted) { setCheckouts(data.checkouts ?? []); setError('') } })
        .catch(() => { if (!controller.signal.aborted) setError('Could not load changes.') })
    }
    load()
    // Other chats and the paired execution computer need no current-chat event.
    const timer = window.setInterval(() => { if (document.visibilityState !== 'hidden') load() }, 60_000)
    window.addEventListener('focus', load)
    return () => { controller.abort(); window.clearInterval(timer); window.removeEventListener('focus', load) }
  }, [workspacePath, revision, savedEvents, retry])
  // One copy: a single "Changes" row. Several: one heading, then each Git copy by its full name: several copies of one repository (cd-web1854-main,
  // cd-web1854-always-on) looked like duplicates when every row said "Changes" and cut the name short.
  const shown = checkouts?.filter(checkout => checkout.captures[0]) ?? []
  return <div className="space-y-0.5">
    {shown.length > 1 && <p className="flex items-center gap-2 px-2.5 pt-1 text-xs font-medium text-muted-foreground"><FileDiff aria-hidden="true" className="h-3.5 w-3.5" />Changes</p>}
    {shown.length ? shown.map(checkout => {
      const capture = checkout.captures[0]
      if (!capture) return null
      const warning = capture.note
      return <WorkspaceContextRow key={checkout.id} icon={FileDiff} hint={`${checkout.root || checkout.name}${capture.branch ? ` · ${capture.branch}` : ''}. ${warning || 'Latest saved project changes; other chats or editors may contribute.'}${checkout.working_tree === 'clean' ? ' Working tree clean.' : checkout.working_tree === 'dirty' ? ' Uncommitted changes present.' : ''}`} onClick={() => onOpen({ workspacePath, sessionId, snapshotId: capture.snapshot_id, historyScope: 'project', checkoutId: checkout.id })} detail={<span className="inline-flex items-center gap-1.5 whitespace-nowrap tabular-nums">
        <span>{capture.files} file{capture.files === 1 ? '' : 's'}</span><span className="text-diff-add">+{capture.added}</span><span className="text-diff-del">−{capture.removed}</span>
        {warning && <span role="img" aria-label={warning} title={warning} className="inline-flex shrink-0 text-amber-500"><AlertTriangle aria-hidden="true" className="h-3 w-3" /></span>}
      </span>}><span title={checkout.root || checkout.name} className="block min-w-0">
        {shown.length === 1
          ? <span className="flex min-w-0 items-center gap-1.5"><span>Changes</span><span className="truncate text-xs text-muted-foreground">{checkout.name}</span></span>
          : <span className="block break-all">{checkout.name}</span>}
        {(checkout.worktree_of || capture.branch) && <span className="block truncate text-xs text-muted-foreground">{[checkout.worktree_of && `worktree of ${checkout.worktree_of}`, capture.branch && `branch ${capture.branch}`].filter(Boolean).join(' · ')}</span>}
      </span></WorkspaceContextRow>
    }) : <WorkspaceContextRow icon={FileDiff} hint="Saved project changes, separated by worktree." onClick={() => onOpen()} detail={error ? 'Unavailable' : checkouts === null ? 'Loading…' : '0 files'}>Changes</WorkspaceContextRow>}
    {error && <p role="alert" className="px-1 text-xs text-muted-foreground">{error} <button type="button" className="underline hover:text-foreground" onClick={() => setRetry(value => value + 1)}>Retry</button></p>}
  </div>
}
