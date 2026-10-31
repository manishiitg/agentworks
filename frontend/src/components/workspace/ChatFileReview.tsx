import { ReplyFileChanges } from '../chat/ReplyFileChanges'
import { openChatFiles } from './chatFileNavigation'
import { PRWatcherControls } from '../chat/PRWatcherControls'
import type { ReplyFileChangesRef, ReplyFileGroup } from '../../utils/replyFileChanges'
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { FolderOpen, RefreshCw } from 'lucide-react'
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

type ReviewProps = { reply?: ReplyFileChangesRef; allChat?: boolean; initialScope?: 'chat' | 'uncommitted'; outsideGit?: boolean; workspacePath: string; sessionId: string; tabId: string; onBrowse: () => void }

/** Mounted with an account/server/chat key by its owner. A folder holding Git
 * repositories is reviewed with Git (Uncommitted or Branch / PR); any other
 * folder keeps "Changes in this chat" from the chat's saved copies. Git is
 * never set up here. */
export function ChatFileReview(props: ReviewProps) {
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
function SnapshotReview({ reply, allChat = false, outsideGit = false, workspacePath, sessionId, tabId, onBrowse, bar }: ReviewProps & { bar?: ReactNode }) {
  const [reviewId, setReviewId] = useState('')
  const [result, setResult] = useState<Result | null>(null)
  const [revision, refresh] = useReviewRevision()
  const [error, setError] = useState('')
  const [selectedPath, setSelectedPath] = useState('')
  const [pullRequests, setPullRequests] = useState<ReplyFileGroup[]>([])
  const [canWatch, setCanWatch] = useState(false)
  const viewers = useRef<HTMLDivElement>(null)
  const [loadingMore, setLoadingMore] = useState(false)
  const baseline = (reply || allChat) ? 'start' : result?.history.baseline_snapshot_id
  const groupParams: Record<string, string> = reply ? { snapshot_id: reply.snapshotId } : outsideGit ? { group_kind: 'crew_files' } : {}
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
  }, [workspacePath, sessionId, reviewId, revision, outsideGit, allChat, reply?.snapshotId])
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
  const selected = changes.find(change => change.path === selectedPath) ?? changes[0]
  const inline = changes.length > 0 && (result?.total_changes ?? changes.length) <= INLINE_REVIEW_FILE_LIMIT && !result?.next_offset
  const selectFile = (path: string) => {
    setSelectedPath(path)
    if (inline) Array.from(viewers.current?.querySelectorAll<HTMLElement>('[data-review-file]') ?? [])
      .find(element => element.dataset.reviewFile === path)?.scrollIntoView?.({ block: 'start' })
  }
  return <section className="flex h-full min-h-0 flex-col" aria-label={reply ? "Changes in this reply" : "Changes in this chat"}>
    {/* One header: the Files panel's, with this view's name and icon actions (owner, 2026-10-29). */}
    <WorkspaceViewHeader title="Files" subtitle={reply ? 'This reply' : allChat ? 'All chat changes' : outsideGit ? 'Files outside Git' : reviewId ? 'Reviewed changes' : result?.history.reviews?.length ? 'Changes since your last review' : 'Changes in this chat'} actions={<>
      {reply && <button type="button" className="text-xs underline" onClick={() => openChatFiles(sessionId)}>All chat changes</button>}
      {!reply && !allChat && !!result?.history.reviews?.length && <select aria-label="Review history" title="Review history" value={reviewId} onChange={event => setReviewId(event.target.value)}
        className="max-w-40 rounded border border-border bg-background px-1.5 py-0.5 text-xs text-foreground">
        <option value="">Since your last review</option>
        {[...result.history.reviews].reverse().map(point => <option key={point.snapshot_id} value={point.snapshot_id}>Reviewed {new Date(point.reviewed_at).toLocaleString()}</option>)}
      </select>}
      <button type="button" aria-label="Refresh changes" title="Refresh" onClick={refresh} className={iconButton}><RefreshCw className="h-3.5 w-3.5" aria-hidden="true" /></button>
      <button type="button" aria-label="Browse files" title="Browse files" onClick={onBrowse} className={iconButton}><FolderOpen className="h-3.5 w-3.5" aria-hidden="true" /></button>
    </>} />
    {bar}
    {reply && <ReplyFileChanges reference={reply} panel showFiles={false} />}
    {pullRequests.length > 0 && <div className="space-y-2 border-b border-border px-3 py-2 text-xs" aria-label="Pull requests in this chat">
      {pullRequests.map(group => <div key={group.root} className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span className="text-muted-foreground">{group.name}{group.branch ? ` · ${group.branch}` : ''}</span>
        <a href={group.pr?.url} target="_blank" rel="noreferrer" className="underline">PR #{group.pr?.number} · {group.pr?.state}{group.pr?.checks ? ` · checks ${group.pr.checks}` : ''}{group.pr?.review ? ` · ${group.pr.review}` : ''}</a>
        {canWatch && <PRWatcherControls group={group} sessionId={sessionId} />}
      </div>)}
    </div>}
    {!reply && <p className="border-b border-border px-3 py-1.5 text-xs text-muted-foreground">{outsideGit ? 'Files outside Git changed during this chat, from its saved copies.' : (bar || allChat) ? 'Every file this chat changed, in and outside Git, from its saved copies.' : "This folder isn't a Git project, so changes come from this chat's saved copies."}</p>}
    {error && <p role="alert" className="p-3 text-sm text-destructive">{error}</p>}
    <div className="max-h-[35%] shrink-0 overflow-auto sm:max-h-56">
      {!result && !error && <p className="p-3 text-sm">Loading saved comparison…</p>}
      {result && !result.comparison && <p className="p-3 text-sm">{result.history.note}</p>}
      <div className="grid gap-1 border-b border-border p-2">
        {!reply && result?.comparison && <p className="p-2 text-xs text-muted-foreground">{result.total_changes ?? changes.length} changed files{!reviewId && !result.history.reviews?.length && !result.history.available ? startedFrom(result.history.start_message) : ''}</p>}
        {changes.map(change => <button type="button" key={change.path} aria-pressed={selected?.path === change.path} className={`flex gap-2 rounded px-2 py-1 text-left text-xs hover:bg-muted/50 ${selected?.path === change.path ? 'bg-muted font-medium' : ''}`} onClick={() => selectFile(change.path)}>
          <span className="min-w-0 flex-1 break-all">{shownFilePath(change.path)}</span><span>{change.status === 'A' ? 'Added' : change.status === 'D' ? 'Deleted' : ''}</span><span className="text-diff-add">{!change.binary && `+${change.added ?? 0}`}</span><span className="text-diff-del">{change.binary ? 'Binary' : `−${change.removed ?? 0}`}</span>
        </button>)}
        {!!result?.next_offset && <button type="button" disabled={loadingMore} className="p-2 text-xs underline" onClick={() => void loadMore()}>{loadingMore ? 'Loading…' : 'Show more files'}</button>}
        {result?.comparison && changes.length === 0 && <p className="p-2 text-sm">{reply ? 'No files changed in this reply.' : 'No changes since this review point.'}</p>}
      </div>
      {(result?.comparison?.uncaptured_changes ?? 0) > 0 && <p role="status" className="p-3 text-xs text-muted-foreground">{result?.comparison?.uncaptured_changes} files could not be compared.</p>}
      {result?.comparison?.note && <p className="px-3 py-2 text-xs text-muted-foreground">{result.comparison.note}</p>}
    </div>
    {selected && through && <div ref={viewers} className={`min-h-0 flex-1 border-t border-border ${inline ? 'overflow-y-auto' : ''}`}>
      {(inline ? changes : [selected]).map(change => <ReplyFileViewer key={`${workspacePath}:${sessionId}:${baseline}:${through}:${change.path}`} embedded flow={inline}
        selection={{ reference: { workspacePath, sessionId, snapshotId: reply?.snapshotId || '' }, change, query: { ...groupParams, session_id: sessionId, through_snapshot_id: through, baseline_snapshot_id: baseline || 'start' } }}
        tabId={tabId} onBack={onBrowse} />)}
    </div>}
  </section>
}
