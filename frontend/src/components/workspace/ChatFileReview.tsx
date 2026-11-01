import { ProjectChangesPanel } from './ProjectChangesPanel'
import { ReplyFileChanges } from '../chat/ReplyFileChanges'
import { openChatFiles } from './chatFileNavigation'
import { PRWatcherControls } from '../chat/PRWatcherControls'
import type { ReplyFileChangesRef, ReplyFileGroup } from '../../utils/replyFileChanges'
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { AlertTriangle, FolderOpen, Info, RefreshCw } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '../ui/tooltip'
import { workspaceGitApi, type GitRepo } from '../../services/workspaceGit'
import { GitReview } from './GitReview'
import { WorkspaceViewHeader } from '../workflow/WorkspaceViewHeader'
import { shownFilePath } from '../../utils/replyFileChanges'
import api from '../../services/api'
import { INLINE_REVIEW_FILE_LIMIT, ReplyFileViewer } from './ReplyFileViewer'
import { reviewEndpoint as endpoint, useReviewRevision, type ReviewResult as Result, type ReviewHistory, type FileChange as Change } from './chatFileReviewData'

// Older replies' saved copies are cleared to save space; name the message the comparison starts from.
const startedFrom = (message?: { text: string; at?: string }) => {
  if (!message?.text) return ' since the oldest reply still saved'
  const at = message.at ? new Date(message.at).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : ''
  return ` since your message “${message.text}”${at ? ` (${at})` : ''}. Earlier changes are no longer saved.`
}

const iconButton = 'rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'

type ReviewProps = { projectHistory?: boolean; initialCheckoutId?: string; toolbar?: ReactNode; reply?: ReplyFileChangesRef; allChat?: boolean; initialScope?: 'chat' | 'uncommitted'; outsideGit?: boolean; workspacePath: string; sessionId: string; tabId: string; onBrowse: () => void }

/** Mounted with an account/server/chat key by its owner. A folder holding Git
 * repositories is reviewed with Git (Uncommitted or Branch / PR); any other
 * folder keeps "Changes in this chat" from the chat's saved copies. Git is
 * never set up here. */
export function ChatFileReview(props: ReviewProps) {
  if (props.projectHistory) return <ProjectChangesPanel {...props} />
  return <CheckoutFileReview {...props} />
}

function CheckoutFileReview(props: ReviewProps) {
  const [repos, setRepos] = useState<GitRepo[] | null>(null)
  const [recentRepos, setRecentRepos] = useState<string[]>([])
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    let live = true
    if (props.outsideGit || props.reply) { setRepos([]); setRecentRepos([]); return }
    void Promise.all([
      workspaceGitApi.status(props.workspacePath),
      api.get<ReviewHistory>(endpoint, { params: { workspace_path: props.workspacePath, session_id: props.sessionId, op: 'snapshots' } })
        .then(({ data }) => data.recent_repos ?? []).catch(() => [] as string[]),
    ]).then(([found, recent]) => { if (live) { setRecentRepos(recent); setRepos(found) } })
      .catch(() => { if (live) setRepos(current => current ?? []) })
    return () => { live = false }
  }, [props.workspacePath, props.sessionId, revision, props.outsideGit, props.reply?.snapshotId])
  if (props.reply) return <SnapshotReview {...props} />
  if (!repos) return <section className="flex h-full min-h-0 flex-col" aria-label="Review changes">
    <WorkspaceViewHeader title="Files" subtitle="Review changes" />
    <p className="p-3 text-sm">Loading…</p>
  </section>
  // Reply cards default to saved chat changes; sidebar links can open current Git changes.
  return repos.length && !props.outsideGit
    ? <GitReview initialScope={props.initialScope} workspacePath={props.workspacePath} sessionId={props.sessionId} tabId={props.tabId} repos={repos} recentRepos={recentRepos} onRefresh={() => setRevision(value => value + 1)} onBrowse={props.onBrowse}
      renderChat={bar => <SnapshotReview {...props} bar={bar} />} />
    : <SnapshotReview {...props} />
}

/** All requests and sends stay with that chat; the server resolves and
 * authorizes its saved boundaries. */
function SnapshotReview({ reply, allChat = false, outsideGit = false, workspacePath, sessionId, tabId, onBrowse, bar, toolbar }: ReviewProps & { bar?: ReactNode }) {
  const [reviewId, setReviewId] = useState('')
  const [result, setResult] = useState<Result | null>(null)
  const [revision, refresh] = useReviewRevision()
  const [error, setError] = useState('')
  const [selectedKey, setSelectedKey] = useState('')
  const [pullRequests, setPullRequests] = useState<ReplyFileGroup[]>([])
  const [canWatch, setCanWatch] = useState(false)
  const viewers = useRef<HTMLDivElement>(null)
  const [loadingMore, setLoadingMore] = useState(false)
  const baseline = (reply || allChat) ? 'start' : result?.history.baseline_snapshot_id
  const groupParams: Record<string, string> = reply ? { snapshot_id: reply.snapshotId, ...(reply.historyScope ? { history_scope: reply.historyScope } : {}), ...(reply.checkoutId ? { checkout_id: reply.checkoutId } : {}) } : outsideGit ? { group_kind: 'crew_files' } : {}
  const params = { ...groupParams, workspace_path: workspacePath, session_id: sessionId, review_id: reviewId, baseline_snapshot_id: baseline }
  useEffect(() => {
    const controller = new AbortController()
    setResult(null); setError(''); setPullRequests([]); setCanWatch(false)
    if (!outsideGit && !reply) void api.get<{ groups?: ReplyFileGroup[]; history?: { can_review?: boolean } }>(endpoint, { params: { workspace_path: workspacePath, session_id: sessionId, op: 'chat_status' }, signal: controller.signal })
      .then(({ data }) => { if (!controller.signal.aborted) { setPullRequests((data.groups ?? []).filter(group => group.kind === 'repo' && !!group.pr?.number)); setCanWatch(data.history?.can_review === true) } })
      .catch(() => { /* The saved comparison loads independently of GitHub status. */ })
    void api.get<Result>(endpoint, { params: { ...groupParams, workspace_path: workspacePath, session_id: sessionId, review_id: reviewId, op: 'snapshot_compare', ...((reply || allChat) ? { baseline_snapshot_id: 'start' } : {}) }, signal: controller.signal })
      .then(({ data }) => { if (!controller.signal.aborted) setResult(data) })
      .catch(() => { if (!controller.signal.aborted) setError('Could not load this saved comparison. Refresh and try again.') })
    return () => controller.abort()
  }, [workspacePath, sessionId, reviewId, revision, outsideGit, allChat, reply?.snapshotId, reply?.historyScope, reply?.checkoutId])
  const through = reply?.snapshotId || result?.history.last_snapshot_id
  const loadMore = async () => {
    if (!result?.next_offset || !result.comparison) return
    const shown = result
    setLoadingMore(true)
    try {
      const { data } = await api.get<Result>(endpoint, { params: { ...params, op: 'snapshot_compare', through_snapshot_id: reply?.snapshotId || shown.history.last_snapshot_id, offset: shown.next_offset } })
      setResult(current => current === shown && data.comparison ? { ...data, comparison: { ...data.comparison, changes: [...shown.comparison!.changes, ...data.comparison.changes] } } : current)
    } catch { setError('Could not load more files. Refresh and try again.') }
    finally { setLoadingMore(false) }
  }
  const changes: Change[] = result?.comparison?.changes ?? []
  const selected = changes.find(change => (change.key || change.path) === selectedKey) ?? changes[0]
  const inline = changes.length > 0 && changes.reduce((sum, change) => sum + (change.edits?.length || 1), 0) <= INLINE_REVIEW_FILE_LIMIT && !result?.next_offset
  const diffs = (inline ? changes : selected ? [selected] : []).flatMap(change => change.edits?.length ? change.edits : [change])
  const selectFile = (change: Change) => {
    const key = change.key || change.path
    setSelectedKey(key)
    if (inline) Array.from(viewers.current?.querySelectorAll<HTMLElement>('[data-review-file]') ?? [])
      .find(element => element.dataset.reviewFile === key)?.scrollIntoView?.({ block: 'start' })
  }
  return <section className="flex h-full min-h-0 flex-col" aria-label={reply ? "Changes in this reply" : "Changes in this chat"}>
    {/* One header: the Files panel's, with this view's name and icon actions (owner, 2026-10-29). */}
    <WorkspaceViewHeader title="Files" context={<TooltipProvider delayDuration={150}><Tooltip>
      <TooltipTrigger asChild><button type="button" aria-label="About saved changes" className={iconButton}><Info className="h-3.5 w-3.5" aria-hidden="true" /></button></TooltipTrigger>
      <TooltipContent side="bottom" align="start" className="max-w-64 text-xs">Saved project differences. Other chats or editors may contribute.</TooltipContent>
    </Tooltip></TooltipProvider>} subtitle={reply ? (reply.historyScope ? 'Project changes' : 'During this turn') : allChat ? 'All chat changes' : outsideGit ? 'Files outside Git' : reviewId ? 'Reviewed changes' : result?.history.reviews?.length ? 'Changes since your last review' : 'Changes in this chat'} actions={<>
      {reply && <button type="button" className="text-xs underline" onClick={() => reply?.checkoutId ? openChatFiles(sessionId, undefined, reply.checkoutId) : openChatFiles(sessionId)}>Project history</button>}
      {!reply && !allChat && !!result?.history.reviews?.length && <select aria-label="Review history" title="Review history" value={reviewId} onChange={event => setReviewId(event.target.value)}
        className="max-w-40 rounded border border-border bg-background px-1.5 py-0.5 text-xs text-foreground">
        <option value="">Since your last review</option>
        {[...result.history.reviews].reverse().map(point => <option key={point.snapshot_id} value={point.snapshot_id}>Reviewed {new Date(point.reviewed_at).toLocaleString()}</option>)}
      </select>}
      <button type="button" aria-label="Refresh changes" title="Refresh" onClick={refresh} className={iconButton}><RefreshCw className="h-3.5 w-3.5" aria-hidden="true" /></button>
      <button type="button" aria-label="Browse files" title="Browse files" onClick={onBrowse} className={iconButton}><FolderOpen className="h-3.5 w-3.5" aria-hidden="true" /></button>
    </>} />
    {bar || toolbar}
    {(result?.comparison?.skipped_count ?? 0) > 0 && <div role="status" className="shrink-0 border-b border-border px-3 py-2 text-xs">
      <p className="flex items-center gap-1.5"><AlertTriangle aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-amber-500" />{result!.comparison!.skipped_count} file{result!.comparison!.skipped_count === 1 ? '' : 's'} not saved in this capture</p>
      <p className="mt-1 text-muted-foreground">Their diffs and Revert are unavailable. The saved changes below are still available.</p>
      <ul className="mt-1 max-h-24 overflow-y-auto text-muted-foreground">{result?.comparison?.snapshot.skipped?.map((path, index) => <li key={`${index}:${path}`} className="break-all">{shownFilePath(path)}</li>)}</ul>
      {(result?.comparison?.skipped_count ?? 0) > (result?.comparison?.snapshot.skipped?.length ?? 0) && <p className="mt-1 text-muted-foreground">Showing {result?.comparison?.snapshot.skipped?.length ?? 0} of {result?.comparison?.skipped_count} skipped files.</p>}
    </div>}
    {result?.comparison?.note && <p role="status" className="shrink-0 border-b border-border px-3 py-2 text-xs text-muted-foreground">{result.comparison.note}</p>}
    {reply && <ReplyFileChanges reference={reply} panel showFiles={false} />}
    {pullRequests.length > 0 && <div className="space-y-2 border-b border-border px-3 py-2 text-xs" aria-label="Pull requests in this chat">
      {pullRequests.map(group => <div key={group.root} className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span className="text-muted-foreground">{group.name}{group.branch ? ` · ${group.branch}` : ''}</span>
        <a href={group.pr?.url} target="_blank" rel="noreferrer" className="underline">PR #{group.pr?.number} · {group.pr?.state}{group.pr?.checks ? ` · checks ${group.pr.checks}` : ''}{group.pr?.review ? ` · ${group.pr.review}` : ''}</a>
        {canWatch && <PRWatcherControls group={group} sessionId={sessionId} />}
      </div>)}
    </div>}
    {error && <p role="alert" className="p-3 text-sm text-destructive">{error}</p>}
    <div className="max-h-[35%] shrink-0 overflow-auto sm:max-h-56">
      {!result && !error && <p className="p-3 text-sm">Loading saved comparison…</p>}
      {result && !result.comparison && <p className="p-3 text-sm">{result.history.note}</p>}
      <div className="grid gap-1 border-b border-border p-2">
        {!reply && result?.comparison && <p className="p-2 text-xs text-muted-foreground" title="Saved changes observed during this chat’s replies. Other chats using the same files may contribute.">{result.total_changes ?? changes.length} changed files{!reviewId && !result.history.reviews?.length && !result.history.available ? startedFrom(result.history.start_message) : ''}</p>}
        {changes.map((change, index) => {
          const key = change.key || change.path
          const active = (selected?.key || selected?.path) === key
          const group = change.checkout
          const previous = changes[index - 1]?.checkout
          const heading = index === 0 || group?.id !== previous?.id || group?.branch !== previous?.branch
          const fullPath = shownFilePath(change.path)
          const root = group ? shownFilePath(group.root).replace(/\/$/, '') : ''
          const path = root && fullPath.startsWith(`${root}/`) ? fullPath.slice(root.length + 1) : fullPath
          return <div key={key}>
            {heading && (group || changes.some(item => item.checkout)) && <div className="flex min-w-0 items-center gap-1.5 px-2 pb-1 pt-2 text-xs text-muted-foreground" title={group ? `${group.root}${group.branch ? ` · ${group.branch}` : ''}` : 'Files outside Git'}>
              <strong className="truncate font-medium">{group?.name || 'Other files'}</strong>
              {group?.branch && <span className="truncate">· {group.branch}</span>}
            </div>}
            <button type="button" aria-pressed={active} title={`${fullPath}${change.at ? ` · ${new Date(change.at).toLocaleString()}` : ''}`} className={`flex w-full gap-2 rounded px-2 py-1 text-left text-xs hover:bg-muted/50 ${active ? 'bg-muted font-medium' : ''}`} onClick={() => selectFile(change)}>
              <span className="min-w-0 flex-1 break-all">{path}</span><span>{change.status === 'A' ? 'Added' : change.status === 'D' ? 'Deleted' : ''}</span><span className="text-diff-add">{!change.binary && `+${change.added ?? 0}`}</span><span className="text-diff-del">{change.binary ? 'Binary' : `−${change.removed ?? 0}`}</span>
            </button>
          </div>
        })}
        {!!result?.next_offset && <button type="button" disabled={loadingMore} className="p-2 text-xs underline" onClick={() => void loadMore()}>{loadingMore ? 'Loading…' : 'Show more files'}</button>}
        {result?.comparison && changes.length === 0 && <p className="p-2 text-sm">{reply ? 'No files changed in this reply.' : 'No changes since this review point.'}</p>}
      </div>
      {!result?.comparison?.skipped_count && (result?.comparison?.uncaptured_changes ?? 0) > 0 && <p role="status" className="p-3 text-xs text-muted-foreground">{result?.comparison?.uncaptured_changes} files could not be compared.</p>}
    </div>
    {selected && through && <div ref={viewers} className={`min-h-0 flex-1 border-t border-border ${(inline || diffs.length > 1) ? 'overflow-y-auto' : ''}`}>
      {diffs.map(change => <ReplyFileViewer key={`${workspacePath}:${sessionId}:${baseline}:${through}:${change.key || change.path}`} embedded flow={inline || diffs.length > 1}
        selection={{ reference: { workspacePath, sessionId, snapshotId: reply?.snapshotId || '' }, change, query: { ...groupParams, session_id: sessionId, through_snapshot_id: through, baseline_snapshot_id: baseline || 'start', ...(change.key ? { change_key: change.key } : {}) } }}
        tabId={tabId} onBack={onBrowse} />)}
    </div>}
  </section>
}
