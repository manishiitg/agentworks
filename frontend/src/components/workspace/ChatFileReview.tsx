import { openProjectIntegrations } from '../../utils/projectIntegrationNavigation'
import { githubConnectionChanged } from '../../utils/githubConnectionEvents'
import { ProjectChangesPanel } from './ProjectChangesPanel'
import { ReplyFileActions, ReplyFileChanges } from '../chat/ReplyFileChanges'
import { pullRequestClick } from './chatFileNavigation'
import { PRWatcherControls } from '../chat/PRWatcherControls'
import type { ReplyCardAction, ReplyFileChangesRef, ReplyFileGroup } from '../../utils/replyFileChanges'
import { useEffect, useState, type ReactNode } from 'react'
import { AlertTriangle, ChevronLeft, ChevronRight, Info, RefreshCw } from 'lucide-react'
import { HideTestsSwitch } from './HideTestsSwitch'
import { chatFileDragProps } from '../../utils/chatFileDrag'
import { isTestPath, useHideTests } from '../../utils/hideTests'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '../ui/tooltip'
import { workspaceGitApi, type GitRepo } from '../../services/workspaceGit'
import { GitReview } from './GitReview'
import { WorkspaceViewHeader } from '../workflow/WorkspaceViewHeader'
import { shownFilePath } from '../../utils/replyFileChanges'
import api from '../../services/api'
import { ReplyFileViewer } from './ReplyFileViewer'
import { reviewEndpoint as endpoint, useReviewRevision, type ReviewResult as Result, type ReviewHistory, type FileChange as Change } from './chatFileReviewData'

// Older replies' saved copies are cleared to save space; name the message the comparison starts from.
const startedFrom = (message?: { text: string; at?: string }) => {
  if (!message?.text) return ' since the oldest reply still saved'
  const at = message.at ? new Date(message.at).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : ''
  return ` since your message “${message.text}”${at ? ` (${at})` : ''}. Earlier changes are no longer saved.`
}

const iconButton = 'rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'

type ReviewProps = { projectHistory?: boolean; initialCheckoutId?: string; initialRepoRoot?: string; toolbar?: ReactNode; reply?: ReplyFileChangesRef; allChat?: boolean; outsideGit?: boolean; workspacePath: string; sessionId: string; tabId: string; onBrowse: () => void; onAsk?: (message: string) => void | Promise<unknown> }

/** Mounted with an account/server/chat key by its owner. A folder holding Git
 * repositories is reviewed with Git (This chat, Not committed or Branch); any other
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
    <WorkspaceViewHeader title="Changes" />
    <p className="p-3 text-sm">Loading…</p>
  </section>
  // The Changes tab always has the same bar (This chat · Not committed · Branch, Commit), owner 20 Nov.
  return repos.length && !props.outsideGit
    ? <GitReview initialRepoRoot={props.initialRepoRoot} workspacePath={props.workspacePath} sessionId={props.sessionId} tabId={props.tabId} repos={repos} recentRepos={recentRepos} onRefresh={() => setRevision(value => value + 1)} onBrowse={props.onBrowse} onAsk={props.onAsk}
      renderChat={bar => <SnapshotReview {...props} allChat bar={bar} />} />
    : <SnapshotReview {...props} />
}

/** All requests and sends stay with that chat; the server resolves and
 * authorizes its saved boundaries. */
function SnapshotReview({ reply, allChat = false, outsideGit = false, workspacePath, sessionId, tabId, onBrowse, bar, toolbar }: ReviewProps & { bar?: ReactNode }) {
  const reviewId = ''
  const [result, setResult] = useState<Result | null>(null)
  const [revision, refresh] = useReviewRevision()
  const [error, setError] = useState('')
  const [selectedKey, setSelectedKey] = useState('')
  const [pullRequests, setPullRequests] = useState<ReplyFileGroup[]>([])
  const [canEdit, setCanEdit] = useState(false)
  const [actions, setActions] = useState<ReplyCardAction[]>([])
  const [loadingMore, setLoadingMore] = useState(false)
  useEffect(() => { window.addEventListener(githubConnectionChanged, refresh); return () => window.removeEventListener(githubConnectionChanged, refresh) }, [refresh])
  const baseline = (reply || allChat) ? 'start' : result?.history.baseline_snapshot_id
  const groupParams: Record<string, string> = reply ? { snapshot_id: reply.snapshotId, ...(reply.historyScope ? { history_scope: reply.historyScope } : {}), ...(reply.checkoutId ? { checkout_id: reply.checkoutId } : {}) } : outsideGit ? { group_kind: 'crew_files' } : {}
  const params = { ...groupParams, workspace_path: workspacePath, session_id: sessionId, review_id: reviewId, baseline_snapshot_id: baseline }
  useEffect(() => {
    const controller = new AbortController()
    setResult(null); setError(''); setPullRequests([]); setCanEdit(false)
    if (!outsideGit && !reply) void api.get<{ groups?: ReplyFileGroup[]; can_edit?: boolean }>(endpoint, { params: { workspace_path: workspacePath, session_id: sessionId, op: 'chat_status' }, signal: controller.signal })
      .then(({ data }) => { if (!controller.signal.aborted) { setPullRequests((data.groups ?? []).filter(group => group.kind === 'repo' && (!!group.pr?.number || group.pr?.state === 'connect_github' || group.pr?.state === 'unavailable'))); setCanEdit(data.can_edit === true) } })
      .catch(() => { /* The saved comparison loads independently of GitHub status. */ })
    void api.get<Result>(endpoint, { params: { ...groupParams, workspace_path: workspacePath, session_id: sessionId, review_id: reviewId, op: 'snapshot_compare', ...((reply || allChat) ? { baseline_snapshot_id: 'start' } : {}) }, signal: controller.signal })
      .then(({ data }) => { if (!controller.signal.aborted) setResult(data) })
      .catch(() => { if (!controller.signal.aborted) setError('Could not load this saved comparison. Refresh and try again.') })
    return () => controller.abort()
  }, [workspacePath, sessionId, reviewId, revision, outsideGit, allChat, reply?.snapshotId, reply?.historyScope, reply?.checkoutId])
  const through = reply?.snapshotId || result?.history.last_snapshot_id
  useEffect(() => {
    setActions([])
    if (reply || !through || !result?.comparison) return
    const controller = new AbortController()
    // Use the displayed saved span, never the latest reply's action context.
    void api.get<{ actions?: ReplyCardAction[] }>(endpoint, { params: { ...params, through_snapshot_id: through, op: 'reply_card', pull_requests: true }, signal: controller.signal })
      .then(({ data }) => { if (!controller.signal.aborted) setActions(data.actions ?? []) })
      .catch(() => { /* Do not invent owner actions when the server cannot return them. */ })
    return () => controller.abort()
  }, [workspacePath, sessionId, reviewId, revision, outsideGit, allChat, reply?.snapshotId, through, baseline, !!result?.comparison])
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
  const [hideTests, setHideTests] = useHideTests()
  const allChanges: Change[] = result?.comparison?.changes ?? []
  const changes = hideTests ? allChanges.filter(change => !isTestPath(change.path)) : allChanges
  const hiddenTests = allChanges.length - changes.length
  const selected = changes.find(change => (change.key || change.path) === selectedKey) ?? changes[0]
  // One layout whatever the count: the list, then the chosen file's changes (owner, 20 Nov).
  const diffs = selected ? (selected.edits?.length ? selected.edits : [selected]) : []
  // A file changed in several turns keeps one saved diff per turn (the server never joins them): show one at a
  // time, the latest first, with arrows to step back (owner, 20 Nov: the stacked copies each said Loading).
  const [editIndex, setEditIndex] = useState<number | null>(null)
  useEffect(() => setEditIndex(null), [selected?.key, selected?.path])
  const shownIndex = Math.min(editIndex ?? diffs.length - 1, diffs.length - 1)
  const shownDiff = diffs[shownIndex]
  const when = (at?: string) => at ? new Date(at).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : ''
  const selectFile = (change: Change) => setSelectedKey(change.key || change.path)
  // Saved-copy caveats in one line; the skipped paths are in its tooltip.
  const skipped = result?.comparison?.skipped_count ?? 0
  const notices = [
    skipped > 0 ? `${skipped} file${skipped === 1 ? '' : 's'} not saved, so their changes and Revert are not available.` : '',
    !skipped && (result?.comparison?.uncaptured_changes ?? 0) > 0 ? `${result?.comparison?.uncaptured_changes} files could not be compared.` : '',
    result?.comparison?.note || '',
    result?.comparison && !reviewId && !result.history.reviews?.length && !result.history.available ? `Saved changes${startedFrom(result.history.start_message)}` : '',
  ].filter(Boolean).join(' ')
  const skippedPaths = (result?.comparison?.snapshot.skipped ?? []).map(shownFilePath).join('\n')
  return <section className="flex h-full min-h-0 flex-col" aria-label={reply ? "Changes in this reply" : "Changes in this chat"}>
    {/* One header: the Files panel's, with this view's name and icon actions (owner, 2026-10-29). */}
    <WorkspaceViewHeader title="Changes" context={<TooltipProvider delayDuration={150}><Tooltip>
      <TooltipTrigger asChild><button type="button" aria-label="About saved changes" className={iconButton}><Info className="h-3.5 w-3.5" aria-hidden="true" /></button></TooltipTrigger>
      <TooltipContent side="bottom" align="start" className="max-w-64 text-xs">Saved project differences. Edits made outside AgentWorks at the same time can also appear.</TooltipContent>
    </Tooltip></TooltipProvider>} subtitle={bar ? undefined : reply ? (reply.historyScope ? 'Project changes' : 'During this turn') : outsideGit ? 'Files outside Git' : 'Changes in this chat'} actions={<>
      {/* With the Changes bar (bar) the switch sits there; a single reply or project view keeps it here. */}
      {!bar && <HideTestsSwitch hide={hideTests} onChange={setHideTests} />}
      <button type="button" aria-label="Refresh changes" title="Refresh" onClick={refresh} className={iconButton}><RefreshCw className="h-3.5 w-3.5" aria-hidden="true" /></button>
    </>} />
    {bar || toolbar}
    {notices && <p role="status" title={skippedPaths || undefined} className="flex shrink-0 items-start gap-1.5 border-b border-border px-3 py-1.5 text-xs text-muted-foreground">{skipped > 0 && <AlertTriangle aria-hidden="true" className="mt-0.5 h-3.5 w-3.5 shrink-0 text-amber-500" />}{notices}</p>}
    {reply && <ReplyFileChanges reference={reply} panel showFiles={false} />}
    {!reply && result?.comparison && <div className="shrink-0 border-b border-border p-3 text-xs">
      <div className="flex items-center gap-2">
        <p className="min-w-0 flex-1 text-muted-foreground">{result.total_changes ?? changes.length} changed files{result.summary && <> <span className="text-diff-add">+{result.summary.added}</span> <span className="text-diff-del">−{result.summary.removed}</span></>}</p>
        <ReplyFileActions key={`${sessionId}:${through}:${baseline}`} actions={actions} sessionId={sessionId} />
      </div>
    </div>}
    {pullRequests.length > 0 && <div className="space-y-2 border-b border-border px-3 py-2 text-xs" aria-label="Pull requests in this chat">
      {pullRequests.map(group => <div key={`${group.root}:${group.branch}`} className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span className="text-muted-foreground">{group.name}{group.branch ? ` · ${group.branch}` : ''}</span>
        {!!group.pr?.number && <a href={group.pr.url} target="_blank" rel="noreferrer" className="underline" onClick={pullRequestClick(sessionId, group.github_repo, group.pr.number)}>PR #{group.pr.number} · {group.pr.state}{group.pr.checks ? ` · checks ${group.pr.checks}` : ''}{group.pr.review ? ` · ${group.pr.review}` : ''}</a>}
        {group.pr?.state === 'connect_github' && (canEdit ? <button type="button" className="underline" onClick={() => openProjectIntegrations('github', sessionId)}>Connect GitHub</button> : <span className="text-muted-foreground">Ask the owner to connect GitHub.</span>)}
        {group.pr?.state === 'unavailable' && <><span className="text-muted-foreground">PR status unavailable.</span>{canEdit && <button type="button" className="underline" onClick={() => openProjectIntegrations('github', sessionId)}>Change GitHub account</button>}<button type="button" className="underline" onClick={refresh}>Try again</button></>}
        {canEdit && !!group.pr?.number && <PRWatcherControls group={group} sessionId={sessionId} />}
      </div>)}
    </div>}
    {error && <p role="alert" className="p-3 text-sm text-destructive">{error}</p>}
    <div className="max-h-[35%] shrink-0 overflow-auto sm:max-h-56">
      {!result && !error && <p className="p-3 text-sm">Loading saved comparison…</p>}
      {result && !result.comparison && <p className="p-3 text-sm">{result.history.note}</p>}
      <div className="grid gap-1 border-b border-border p-2">
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
            <button type="button" {...(change.status !== 'D' ? chatFileDragProps(`${workspacePath}/${change.path}`) : {})} aria-pressed={active} title={`${fullPath}${change.at ? ` · ${new Date(change.at).toLocaleString()}` : ''}`} className={`flex w-full gap-2 rounded px-2 py-1 text-left text-xs hover:bg-muted/50 ${active ? 'bg-muted font-medium' : ''}`} onClick={() => selectFile(change)}>
              <span className="min-w-0 flex-1 break-all">{path}</span><span>{change.status === 'A' ? 'Added' : change.status === 'D' ? 'Deleted' : ''}</span><span className="text-diff-add">{!change.binary && `+${change.added ?? 0}`}</span><span className="text-diff-del">{change.binary ? 'Binary' : `−${change.removed ?? 0}`}</span>
            </button>
          </div>
        })}
        {hiddenTests > 0 && <p className="px-2 pt-1 text-xs text-muted-foreground">{hiddenTests} test file{hiddenTests === 1 ? '' : 's'} hidden.</p>}
        {!!result?.next_offset && <button type="button" disabled={loadingMore} className="p-2 text-xs underline" onClick={() => void loadMore()}>{loadingMore ? 'Loading…' : 'Show more files'}</button>}
        {result?.comparison && changes.length === 0 && <p className="p-2 text-sm">{reply ? 'No files changed in this reply.' : 'No saved changes in this chat.'}</p>}
      </div>
    </div>
    {selected && through && shownDiff && <div className="flex min-h-0 flex-1 flex-col border-t border-border">
      {diffs.length > 1 && <div className="flex shrink-0 items-center gap-2 border-b border-border px-3 py-1 text-xs text-muted-foreground">
        <span className="min-w-0 flex-1 truncate">Changed in {diffs.length} turns · showing {shownIndex + 1} of {diffs.length}{shownDiff.at ? ` (${when(shownDiff.at)})` : ''}</span>
        <button type="button" aria-label="Earlier change" disabled={shownIndex === 0} onClick={() => setEditIndex(shownIndex - 1)} className="rounded p-1 hover:bg-muted disabled:opacity-40"><ChevronLeft className="h-3.5 w-3.5" /></button>
        <button type="button" aria-label="Later change" disabled={shownIndex === diffs.length - 1} onClick={() => setEditIndex(shownIndex + 1)} className="rounded p-1 hover:bg-muted disabled:opacity-40"><ChevronRight className="h-3.5 w-3.5" /></button>
      </div>}
      <div className="min-h-0 flex-1">
        <ReplyFileViewer key={`${workspacePath}:${sessionId}:${baseline}:${through}:${shownDiff.key || shownDiff.path}`} embedded
          selection={{ reference: { workspacePath, sessionId, snapshotId: reply?.snapshotId || '' }, change: shownDiff, query: { ...groupParams, session_id: sessionId, through_snapshot_id: through, baseline_snapshot_id: baseline || 'start', ...(shownDiff.key ? { change_key: shownDiff.key } : {}) } }}
          tabId={tabId} onBack={onBrowse} />
      </div>
    </div>}
  </section>
}
