import { PRWatcherControls } from './PRWatcherControls'
import { useEffect, useState, type ReactNode } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { ArrowDownToLine, GitBranch, Github, MessageSquare, MoreHorizontal, Plus, Undo2, Wrench, Zap } from 'lucide-react'
import api from '../../services/api'
import { useReviewRevision, type ReviewResult } from '../workspace/chatFileReviewData'
import { shownFilePath, type ReplyCardAction, type ReplyFileChangesRef, type ReplyFileGroup, type ReplyPullRequest } from '../../utils/replyFileChanges'
import { useProjectMode } from '../../products/work/codeLocalFiles'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { openChatFiles, openReplyChanges } from '../workspace/chatFileNavigation'
import { useChatStore } from '../../stores/useChatStore'
import { sendWorkspacePaneMessageToChat, openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'
import { CommandIcon } from '../../commands/CommandIcon'
import { assertChatIdentityCurrent, captureChatIdentity } from '../../utils/chatIdentity'
import { openProjectIntegrations } from '../../utils/projectIntegrationNavigation'
import { FileDiffIcon, GitCommitIcon, GitPullRequestIcon } from '../icons/octicons'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '../ui/tooltip'
import { githubConnectionChanged } from '../../utils/githubConnectionEvents'
import { useMobileViewport } from '../../hooks/useMobileViewport'

type PullRequest = ReplyPullRequest
type FileGroup = ReplyFileGroup
// Git status (PR, checks, next step) is only looked up for a checkout on GitHub.
const onGitHub = (group: FileGroup) => group.kind === 'repo' && !!group.github_repo && !!group.branch

const ownName = (path: string, root: string) => root && path.startsWith(`${root}/`) ? path.slice(root.length + 1) : shownFilePath(path)
function prLine(pr: PullRequest) {
  const checks = pr.checks && pr.checks !== 'none' ? ` · checks ${pr.checks}` : ''
  const review = pr.review === 'approved' ? ' · approved' : pr.review === 'changes_requested' ? ' · changes requested' : pr.review === 'commented' ? ' · review comments' : pr.review === 'unavailable' ? ' · review unavailable' : pr.state === 'open' ? ' · no review yet' : ''
  return `PR #${pr.number}${pr.state === 'open' ? '' : ` ${pr.state}`}${checks}${review}`
}

type UndoItem = { path: string; status: string; action: string; reason?: string }
type UndoPlan = { restore: UndoItem[]; skipped: UndoItem[]; note?: string }
const fileList = (items: UndoItem[]) => items.slice(0, 5).map(item => item.path.split('/').pop()).join(', ') + (items.length > 5 ? `, +${items.length - 5} more` : '')
const plural = (n: number) => `${n} file${n === 1 ? '' : 's'}`
const errorText = (cause: unknown, fallback: string) => (cause as { response?: { data?: { error?: { message?: string } } } })?.response?.data?.error?.message || (cause instanceof Error ? cause.message : fallback)
const menuRow = 'flex cursor-pointer items-center gap-2.5 rounded-md px-2.5 py-2 text-sm outline-none data-[highlighted]:bg-muted [&_svg]:h-4 [&_svg]:w-4'
// The card's own icons draw at once; a person's command icon loads by its Lucide name.
const cardIcons: Record<string, ReactNode> = { 'octicon:file-diff': <FileDiffIcon />, 'octicon:git-commit': <GitCommitIcon />, 'octicon:git-pull-request': <GitPullRequestIcon />, 'arrow-down-to-line': <ArrowDownToLine />, wrench: <Wrench />, plus: <Plus /> }
const actionIcon = (icon?: string) => (icon && cardIcons[icon]) || <CommandIcon name={icon} fallback={Zap} />
// The person's own buttons and Add button sit after a divider; phones keep them under More.
const ownButton = (action: ReplyCardAction) => action.kind === 'command' || action.id === 'add_button'
const iconButton = 'inline-flex h-6 w-6 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50 [&_svg]:h-3.5 [&_svg]:w-3.5'
const Divider = () => <span className="mx-1 h-3.5 w-px bg-border" aria-hidden />
// Hover or keyboard focus shows the name and one plain line; touch taps go straight to the button.
function IconButton({ label, name = label, hint, onClick, disabled, strong, children }: { label: string; name?: string; hint: string; onClick: () => void; disabled?: boolean; strong?: boolean; children: ReactNode }) {
  return <Tooltip>
    <TooltipTrigger asChild>
      <button type="button" aria-label={label} disabled={disabled} className={`${iconButton}${strong ? ' text-foreground' : ''}`} onClick={onClick}>{children}</button>
    </TooltipTrigger>
    <TooltipContent className="max-w-xs text-xs"><p className="font-semibold">{name}</p><p className="text-muted-foreground">{hint}</p></TooltipContent>
  </Tooltip>
}
function MoreMenu({ items, onSelect }: { items: ReplyCardAction[]; onSelect: (action: ReplyCardAction) => void }) {
  return <Menu.Root modal={false}>
    <Tooltip>
      <TooltipTrigger asChild>
        <Menu.Trigger type="button" aria-label="More actions" className={`${iconButton} data-[state=open]:bg-muted`}><MoreHorizontal /></Menu.Trigger>
      </TooltipTrigger>
      <TooltipContent className="text-xs">More actions</TooltipContent>
    </Tooltip>
    <Menu.Portal><Menu.Content align="end" sideOffset={6} collisionPadding={12} className="z-[100] max-h-[var(--radix-dropdown-menu-content-available-height)] w-[min(300px,calc(100vw-24px))] overflow-y-auto overscroll-contain rounded-xl border border-border bg-popover p-1.5 text-popover-foreground shadow-lg">
      {items.map(item => <Menu.Item key={item.id} onSelect={() => onSelect(item)} className={menuRow}>{actionIcon(item.icon)}<span className="min-w-0 flex-1 truncate">{item.label}</span></Menu.Item>)}
    </Menu.Content></Menu.Portal>
  </Menu.Root>
}
const endpoint = '/api/workspace-snapshots'
const paramsFor = (reference: ReplyFileChangesRef) => ({ workspace_path: reference.workspacePath, snapshot_id: reference.snapshotId })
// The server decides the card for this viewer: its groups (PR status once looked up) and actions.
type ReplyCard = { groups: FileGroup[]; actions: ReplyCardAction[]; can_edit: boolean }

export function ReplyFileChanges({ reference, panel = false, showFiles = true }: { reference: ReplyFileChangesRef; panel?: boolean; showFiles?: boolean }) {
  const mode = useProjectMode(reference.sessionId)
  const account = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = JSON.stringify([account, server, reference.workspacePath, reference.snapshotId])
  if (mode !== 'dev') return null
  if (reference.unavailable) return <p className="my-2 text-xs text-muted-foreground">{reference.unavailable}</p>
  return <ScopedFileChanges key={`${scope}:${panel}`} reference={reference} panel={panel} showFiles={showFiles} />
}

function ScopedFileChanges({ reference, panel, showFiles }: { reference: ReplyFileChangesRef; panel: boolean; showFiles: boolean }) {
  const summary = reference.summary
  // Only for a reply saved before summaries were sent with the event, and for the newest reply's "since your last review" line.
  const [result, setResult] = useState<ReviewResult | null>(null)
  const [loadFailed, setLoadFailed] = useState(false)
  const comparison = result?.comparison
  const [revision] = useReviewRevision()
  const phone = useMobileViewport()
  const [reviewError, setReviewError] = useState('')
  const [card, setCard] = useState<ReplyCard | null>(null)
  const [checked, setChecked] = useState<ReplyCard | null>(null)
  const [sharedOpen, setSharedOpen] = useState(false)
  const [githubRevision, setGitHubRevision] = useState(0)
  useEffect(() => {
    const refresh = () => { setChecked(null); setGitHubRevision(value => value + 1) }
    window.addEventListener(githubConnectionChanged, refresh)
    return () => window.removeEventListener(githubConnectionChanged, refresh)
  }, [])
  const [undo, setUndo] = useState<{ plan?: UndoPlan; busy?: boolean; done?: string; error?: string } | null>(null)
  const latest = !reference.latestSnapshotId || reference.latestSnapshotId === reference.snapshotId
  const reviewed = reference.reviewedSnapshotId === reference.snapshotId
  const sinceReview = latest && !!reference.reviewedSnapshotId && !reviewed
  const hasSummary = !!summary
  useEffect(() => {
    const controller = new AbortController()
    // The card itself renders from the reply event; this only fills the "since your last review" line.
    if (!hasSummary || sinceReview) {
      setLoadFailed(false)
      void api.get<ReviewResult>(endpoint, { params: { ...paramsFor(reference), session_id: reference.sessionId, op: 'snapshot_compare' }, signal: controller.signal })
        .then(response => { if (!controller.signal.aborted) setResult(response.data) })
        .catch(() => { if (!controller.signal.aborted) setLoadFailed(true) })
    }
    return () => controller.abort()
  }, [reference.workspacePath, reference.snapshotId, reference.sessionId, hasSummary, sinceReview, revision])
  useEffect(() => {
    if (!hasSummary) return
    const controller = new AbortController()
    // This viewer's actions, without the GitHub lookup.
    void api.get<ReplyCard>(endpoint, { params: { ...paramsFor(reference), session_id: reference.sessionId, op: 'reply_card' }, signal: controller.signal })
      .then(response => { if (!controller.signal.aborted) setCard(response.data) })
      .catch(() => { /* Only the actions every viewer gets. */ })
    return () => controller.abort()
  }, [reference.workspacePath, reference.snapshotId, reference.sessionId, hasSummary])
  const undoParams = (op: string) => ({ ...paramsFor(reference), session_id: reference.sessionId, op })
  const undoCall = (method: 'get' | 'post', op: string) => method === 'get' ? api.get<{ undo: UndoPlan }>(endpoint, { params: undoParams(op) }) : api.post<{ undo: UndoPlan }>(endpoint, {}, { params: undoParams(op) })
  const startUndo = async () => {
    setUndo({ busy: true })
    try { setUndo({ plan: (await undoCall('get', 'snapshot_undo_preview')).data.undo }) } catch (cause) { setUndo({ error: errorText(cause, 'Could not check what to undo. Try again.') }) }
  }
  const confirmUndo = async () => {
    setUndo({ busy: true })
    try {
      const plan = (await undoCall('post', 'snapshot_undo')).data.undo
      setUndo({ done: `Restored ${plural(plan.restore.length)}${plan.skipped.length ? `, left ${plural(plan.skipped.length)} alone` : ''}.` })
    } catch (cause) { setUndo({ error: errorText(cause, 'Could not undo this reply. Try again.') }) }
  }
  const ask = async (message: string) => {
    setReviewError('')
    try {
      const identity = captureChatIdentity()
      const tabId = Object.values(useChatStore.getState().chatTabs).find(tab => tab.sessionId === reference.sessionId)?.tabId
      if (!tabId) throw new Error('Open the original chat to ask for this.')
      assertChatIdentityCurrent(identity)
      await sendWorkspacePaneMessageToChat({ tabId, message })
    } catch (cause) { setReviewError(cause instanceof Error ? cause.message : 'Could not send this to the chat. Try again.') }
  }
  // PR shortcuts load independently and never hold the compact toolbar or file list.
  // A reply saved without a summary loads its groups this way at once.
  // PR shortcuts are visible on the compact toolbar; details live in the Files panel.
  const isOpen = panel
  const lookUpPullRequests = !hasSummary || !!summary?.groups.some(onGitHub)
  useEffect(() => {
    if (!lookUpPullRequests) return
    const controller = new AbortController()
    void api.get<ReplyCard>(endpoint, { params: { ...paramsFor(reference), session_id: reference.sessionId, op: 'reply_card', pull_requests: true }, signal: controller.signal })
      .then(response => { if (!controller.signal.aborted) setChecked(response.data) })
      .catch(() => { /* No PR line. */ })
    return () => controller.abort()
  }, [lookUpPullRequests, reference.workspacePath, reference.snapshotId, reference.sessionId, githubRevision])
  // Reviews and checks often finish after the person opens the card. Refresh
  // the latest toolbar and the open panel, using the same server-owned lookup and account.
  useEffect(() => {
    if ((!panel && !latest) || !lookUpPullRequests) return
    const refresh = () => { if (document.visibilityState !== 'hidden') setGitHubRevision(value => value + 1) }
    const timer = window.setInterval(refresh, 60_000)
    window.addEventListener('focus', refresh)
    return () => { window.clearInterval(timer); window.removeEventListener('focus', refresh) }
  }, [panel, latest, lookUpPullRequests])
  // The file list stays as captured; the server's lookup adds only the PR status.
  const groups = checked?.groups ?? summary?.groups ?? []
  const actions = checked?.actions ?? card?.actions ?? summary?.actions ?? []
  const canEdit = (checked ?? card)?.can_edit === true
  const sharedViewer = (checked ?? card)?.can_edit === false
  const sum = (key: 'added' | 'removed') => result?.summary?.[key] ?? (comparison?.changes ?? []).reduce((n, change) => n + (change[key] ?? 0), 0)
  const total = summary ? summary.files : result?.total_changes ?? comparison?.changes?.length ?? 0
  const added = summary ? summary.added : sum('added'), removed = summary ? summary.removed : sum('removed')
  const addButton = (message: string) => { const tabId = Object.values(useChatStore.getState().chatTabs).find(tab => tab.sessionId === reference.sessionId)?.tabId; if (tabId) void openWorkspacePaneChatWithDraft({ tabId, message }).catch(() => setReviewError('Open the original chat to add a button.')) }
  const run = (action: ReplyCardAction) => action.id === 'review' ? (panel ? openChatFiles(reference.sessionId) : sharedViewer ? setSharedOpen(value => !value) : openReplyChanges(reference)) : action.id === 'add_button' ? addButton(action.prompt ?? '') : void ask(action.prompt ?? '')
  // Phones show the card's defaults in the row and the person's own buttons under More.
  const inMore = (action: ReplyCardAction) => action.placement === 'more' || (phone && action.placement === 'row' && ownButton(action))
  const row = actions.filter(action => action.placement === 'row' && !inMore(action))
  const more = actions.filter(inMore)
  const rowButton = (action: ReplyCardAction) => <IconButton key={action.id} strong={action.id === 'review'} label={action.aria_label || action.label} name={action.label} hint={action.description ?? ''} onClick={() => run(action)}>{actionIcon(action.icon)}</IconButton>
  const repoGroups = groups.filter(group => group.kind === 'repo')
  const checkoutSummary = (responsive = false) => <button type="button" aria-label={`${repoGroups.length} worktrees`} title={repoGroups.map(group => `${group.name} · ${group.branch || ''}`).join('\n')}
    className={`${responsive ? 'reply-toolbar-checkout-summary ' : ''}inline-flex min-w-0 items-center gap-1 rounded px-1 text-muted-foreground hover:bg-muted hover:text-foreground`}
    onClick={() => sharedViewer ? setSharedOpen(value => !value) : openReplyChanges(reference)}><GitBranch className="h-3.5 w-3.5 shrink-0" /><span className="truncate">{repoGroups.length} worktree{repoGroups.length === 1 ? '' : 's'}</span></button>

  // No card until at least one changed file is known (also while loading); a reply that changed nothing shows nothing.
  if (total === 0) return loadFailed ? <section className="my-2 rounded-md border border-border px-2.5 py-2 text-xs text-muted-foreground" aria-label="File changes unavailable">
    Saved file changes for this reply could not be loaded. <button type="button" className="underline hover:text-foreground" onClick={() => openChatFiles(reference.sessionId)}>Review chat changes</button>
  </section> : null
  if (!panel) return <TooltipProvider delayDuration={150}><section className="reply-file-toolbar my-2 rounded-md border border-border px-2.5 py-1 text-xs" aria-label="Reply changes">
    <div className="flex min-w-0 items-center gap-2">
      <button type="button" aria-label={`Open ${plural(total)} changed in this reply`} className="flex shrink-0 items-center gap-2 whitespace-nowrap text-left font-medium text-muted-foreground hover:text-foreground [&_svg]:h-3.5 [&_svg]:w-3.5" aria-expanded={sharedViewer ? sharedOpen : undefined} onClick={() => sharedViewer ? setSharedOpen(value => !value) : openReplyChanges(reference)}>
        <FileDiffIcon /><span>{plural(total)}</span>
        <span className="reply-toolbar-line-counts font-normal"><span className="text-diff-add">+{added}</span> <span className="text-diff-del">−{removed}</span></span>
      </button>
      <div className="flex min-w-0 flex-1 items-center gap-2">
      {repoGroups.length > 1 ? checkoutSummary() : <>
      <div className="reply-toolbar-checkout-inline flex min-w-0 flex-1 items-center gap-2">
      {repoGroups.map(group => <div key={group.root} className="flex min-w-0 flex-1 items-center gap-1">
        <span className="inline-flex min-w-0 items-center gap-1 text-muted-foreground" title={`${group.root} · ${group.branch || ''}`}><GitBranch className="h-3 w-3 shrink-0" /><span className="truncate">{group.name}{group.branch ? ` · ${group.branch}` : ''}</span></span>
        {group.pr?.state === 'connect_github' && canEdit && <IconButton label="Connect GitHub" hint="Use your saved account to see PR status." onClick={() => openProjectIntegrations('github', reference.sessionId)}><Github /></IconButton>}
        {group.pr?.state === 'unavailable' && <IconButton label="Retry PR status" hint="GitHub status could not be checked." onClick={() => setGitHubRevision(value => value + 1)}><Github /></IconButton>}
        {!!group.pr?.number && group.pr.url && <>
          <a className={iconButton} aria-label={prLine(group.pr)} title={prLine(group.pr)} href={group.pr.url} target="_blank" rel="noreferrer"><GitPullRequestIcon /></a>
          <a className={iconButton} aria-label={`Review comments on PR #${group.pr.number}`} title="Review comments" href={`${group.pr.url}#discussion_bucket`} target="_blank" rel="noreferrer"><MessageSquare /></a>
          {canEdit && <PRWatcherControls compact group={group} sessionId={reference.sessionId} />}
        </>}
      </div>)}
      </div>
      {repoGroups.length > 0 && checkoutSummary(true)}
      </>}
      </div>
      <div className="flex shrink-0 items-center gap-0.5">
        {row.filter(action => !ownButton(action)).map(rowButton)}
        {row.some(ownButton) && <Divider />}
        {row.filter(ownButton).map(rowButton)}
        {more.length > 0 && <MoreMenu items={more} onSelect={run} />}
      </div>
    </div>
    {reviewError && <p role="alert" className="mt-1 text-xs">{reviewError}</p>}
    {sharedViewer && sharedOpen && <div className="mt-2 rounded border border-border p-2" aria-label="Reply file names">
      {groups.map(group => <div key={`${group.kind}:${group.root}`} className="space-y-1">
        <p className="text-muted-foreground">{group.name}</p>
        {!!group.pr?.number && group.pr.url && <a className="underline" href={group.pr.url} target="_blank" rel="noreferrer">{prLine(group.pr)}</a>}
        <ul>{group.files.map(file => <li key={file.path} className="break-all font-mono">{shownFilePath(file.path)}</li>)}</ul>
        {group.more_count > 0 && <p className="text-muted-foreground">+ {group.more_count} more</p>}
      </div>)}
    </div>}
  </section></TooltipProvider>
  return (
    <section className="p-3 text-xs" aria-label="Files changed during this reply">
      <div className="flex items-center gap-2">
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-2 font-medium text-muted-foreground">
          {plural(total)}
          <span className="font-normal"><span className="text-diff-add">+{added}</span> <span className="text-diff-del">−{removed}</span></span>
          {latest && reviewed && <span className="text-xs font-normal text-muted-foreground">Reviewed up to here</span>}
        </div>
        <TooltipProvider delayDuration={150}><div className="flex shrink-0 items-center gap-0.5">
          {row.filter(action => !ownButton(action)).map(rowButton)}
          {row.some(ownButton) && <Divider />}
          {row.filter(ownButton).map(rowButton)}
          {more.length > 0 && <MoreMenu items={more} onSelect={run} />}
        </div></TooltipProvider>
      </div>
      {latest && result?.since_review && <p className="mt-1 text-xs">Since your last review: {result.since_review.files} files <span className="text-diff-add">+{result.since_review.added}</span> <span className="text-diff-del">−{result.since_review.removed}</span></p>}
      {reviewError && <p role="alert" className="mt-1 text-xs">{reviewError}</p>}
      {isOpen && <div>
        {[...groups].sort((x, y) => Number(x.kind === 'crew_files') - Number(y.kind === 'crew_files')).map(group => (
          <div key={`${group.kind}:${group.root}`} className="mt-3 border-t border-border pt-2 first:mt-2 first:border-0 first:pt-0">
            <div className="flex flex-wrap items-center gap-2">
              {group.kind === 'crew_files' ? <span className="text-xs text-muted-foreground">Crew files · not in Git</span> : <b>{group.name}</b>}
              {group.new_checkout && <span className="rounded-full border border-border px-2 text-xs text-muted-foreground">new copy · {group.total_files} files</span>}
              {group.kind === 'repo' && group.branch && <span className="rounded-full border border-border px-2 text-xs text-muted-foreground">branch {group.branch}</span>}
            </div>
            {showFiles && <ul className="mt-1 space-y-1">
              {group.files.map(change => (
                <li key={change.path} className="flex flex-wrap items-center gap-2">
                  <button type="button" className="min-w-0 break-all text-left font-mono text-xs" onClick={() => openChatFiles(reference.sessionId, { reference, change })}>{ownName(change.path, group.root)}</button>
                  <span className="text-xs text-muted-foreground">{change.status === 'A' ? 'Added' : change.status === 'D' ? 'Deleted' : ''}</span>
                  {!change.binary && typeof change.added === 'number' && typeof change.removed === 'number'
                    && <span className="text-xs" aria-label={`${change.added} lines added, ${change.removed} lines removed`}><span className="text-diff-add">+{change.added}</span> <span className="text-diff-del">−{change.removed}</span></span>}
                </li>
              ))}
              {group.more_count > 0 && <li className="text-xs text-muted-foreground">+ {group.more_count} more</li>}
            </ul>}
            {group.kind === 'repo' && group.pr?.state === 'unavailable' && <p className="mt-1 text-xs text-muted-foreground">Could not check PR status for {group.github_repo || group.name}. {canEdit ? <><button type="button" className="underline hover:text-foreground" onClick={() => openProjectIntegrations('github', reference.sessionId)}>Change GitHub account</button><span aria-hidden> · </span><button type="button" className="underline hover:text-foreground" onClick={() => setGitHubRevision(value => value + 1)}>Try again</button></> : <>Ask the owner to check their GitHub connection.</>}</p>}
            {group.kind === 'repo' && group.pr?.state === 'connect_github' && canEdit && <button type="button" className="mt-1 text-xs underline" onClick={() => openProjectIntegrations('github', reference.sessionId)}>Connect GitHub to see PR status</button>}
            {group.kind === 'repo' && group.pr && group.pr.number && group.pr.url && <div className="mt-1 flex flex-wrap items-center gap-x-3 text-xs">
              <a className="underline" href={group.pr.url} target="_blank" rel="noreferrer">{prLine(group.pr)}</a>
              {canEdit && <PRWatcherControls group={group} sessionId={reference.sessionId} />}
              {actions.filter(action => action.placement === 'pr_line' && action.group === group.root).map(action => <button key={action.id} type="button" className="text-muted-foreground underline hover:text-foreground" onClick={() => run(action)}>{action.label}</button>)}
            </div>}
          </div>
        ))}
        {canEdit && undo === null && <button type="button" className="mt-3 inline-flex items-center gap-1.5 rounded-md border border-border px-2 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground [&_svg]:h-3.5 [&_svg]:w-3.5" onClick={() => void startUndo()}><Undo2 />Undo this reply</button>}
        {canEdit && undo && <div className="mt-2 rounded border border-border p-2 text-xs" role="group" aria-label="Undo this reply">
          {undo.busy && <span>Working…</span>}
          {undo.error && <p role="alert">{undo.error}</p>}
          {undo.done && <p>{undo.done}</p>}
          {undo.plan && <>
            <p>{undo.plan.restore.length ? `This will put back ${plural(undo.plan.restore.length)} (${fileList(undo.plan.restore)})` : 'There is nothing to put back'}{undo.plan.skipped.length ? ` and leave ${plural(undo.plan.skipped.length)} alone because a later reply changed them (${fileList(undo.plan.skipped)}).` : '.'}</p>
            {undo.plan.note && <p className="mt-1">{undo.plan.note}</p>}
            <div className="mt-1 flex gap-2">
              {undo.plan.restore.length > 0 && <button type="button" className="rounded border border-primary px-2 py-1" onClick={() => void confirmUndo()}>Undo {plural(undo.plan.restore.length)}</button>}
              <button type="button" className="rounded border px-2 py-1" onClick={() => setUndo(null)}>Cancel</button>
            </div>
          </>}
          {(undo.done || undo.error) && <button type="button" className="mt-1 underline" onClick={() => setUndo(null)}>Close</button>}
        </div>}
      </div>}

    </section>
  )
}
