import { useEffect, useState, type ReactNode } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { ArrowDownToLine, MoreHorizontal, Plus, Undo2, Wrench, Zap } from 'lucide-react'
import api from '../../services/api'
import { useReviewRevision, type ReviewResult } from '../workspace/chatFileReview'
import type { ReplyFileChangesRef, ReplyFileGroup, ReplyPullRequest } from '../../utils/replyFileChanges'
import { useProjectMode } from '../../products/work/codeLocalFiles'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { openChatFiles } from '../workspace/chatFileNavigation'
import { useChatStore } from '../../stores/useChatStore'
import { sendWorkspacePaneMessageToChat, openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'
import { commandsApi } from '../../api/commands'
import { renderCommandPrompt } from '../../commands/commandPrompt'
import { CommandIcon } from '../../commands/CommandIcon'
import type { UserCommand } from '../../types/commands'
import { assertChatIdentityCurrent, captureChatIdentity } from '../../utils/chatIdentity'
import { openProjectIntegrations } from '../../utils/projectIntegrationNavigation'
import { FileDiffIcon, GitCommitIcon, GitPullRequestIcon } from '../icons/octicons'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '../ui/tooltip'
import { useMobileViewport } from '../../hooks/useMobileViewport'

type PullRequest = ReplyPullRequest
type FileGroup = ReplyFileGroup
// Git status (PR, checks, next step) is only looked up for a checkout on GitHub.
const onGitHub = (group: FileGroup) => group.kind === 'repo' && !!group.github_repo && !!group.branch

const ownName = (path: string, root: string) => root && path.startsWith(`${root}/`) ? path.slice(root.length + 1) : path
// Plain-language requests for the agent in this same chat. Only this reply's files
// are named, and the agent is told never to push the main branch.
export const commitRequest = (group: FileGroup) => `Commit these files in ${group.name} (${group.root || 'the Crew folder'}) on branch ${group.branch}. Stage only the files listed here, by name, and use a short message that describes the change. Do not push yet.\n${group.files.map(file => `- ${file.path}`).join('\n')}${group.more_count ? `\n(and ${group.more_count} more files from this reply)` : ''}`
export const pushRequest = (group: FileGroup) => `Open a pull request for this reply's work in ${group.name} (${group.root || 'the Crew folder'}). If branch ${group.branch} is the default branch (main or master), first create a new branch from it with a short descriptive name; otherwise use ${group.branch}. Never push the default branch. If these files are not committed yet, commit them, staging only the files listed here, by name:\n${group.files.map(file => `- ${file.path}`).join('\n')}${group.more_count ? `\n(and ${group.more_count} more files from this reply)` : ''}\nThen push the branch to GitHub and open a pull request with a proper description: a short title, a summary of what changed and why based on this chat, the list of changed files, and how it was tested. Reply with the pull request link.`
const fileLines = (group: FileGroup) => `${group.files.map(file => `- ${file.path}`).join('\n')}${group.more_count ? `\n(and ${group.more_count} more files from this reply)` : ''}`
const repoName = (group: FileGroup) => group.github_repo || group.name
const prName = (group: FileGroup) => `PR #${group.pr?.number} in ${repoName(group)}`
export const updatePrRequest = (group: FileGroup) => `Update ${prName(group)} with this reply's work on branch ${group.branch}. If these files are not committed yet, commit them, staging only the files listed here, by name:\n${fileLines(group)}\nThen push branch ${group.branch}, update the pull request description if what it does has changed, and reply with the pull request link.`
export const getLatestRequest = (group: FileGroup) => `Branch ${group.branch} in ${repoName(group)} (${group.root || 'the Crew folder'}) is behind its copy on GitHub. Bring in the latest changes without losing any work here: keep uncommitted changes safe first, resolve any conflicts, then tell me what came in.`
export const fixChecksRequest = (group: FileGroup) => `${prName(group)} has failing checks${group.pr?.failing_checks?.length ? `: ${group.pr.failing_checks.join(', ')}` : ''}. Open the failing runs, fix the cause on branch ${group.branch}, push, and tell me what you changed.`
export const reviewRequest = (group: FileGroup) => `${prName(group)} has review comments asking for changes. Read every review comment, make the requested changes on branch ${group.branch}, push, and tell me what you changed for each comment.`
export const conflictsRequest = (group: FileGroup) => `${prName(group)} cannot merge because of conflicts with its base branch. Bring branch ${group.branch} up to date with the base branch, resolve the conflicts keeping what both sides meant, push, and tell me which files had conflicts and how you resolved them.`
type CardAction = { key: string; label: string; message: string; icon: ReactNode }
const nextStepLabels = { share_pr: 'Share as a pull request', update_pr: 'Update the pull request', get_latest: 'Get the latest changes' } as const
function nextStepAction(group: FileGroup, named: boolean): CardAction | null {
  const step = group.next_step
  if (!step || step === 'none' || !(step in nextStepLabels)) return null
  const message = step === 'share_pr' ? pushRequest(group) : step === 'update_pr' ? updatePrRequest(group) : getLatestRequest(group)
  return { key: `next:${group.root}`, label: `${nextStepLabels[step]}${named ? ` · ${group.name}` : ''}`, message, icon: step === 'get_latest' ? <ArrowDownToLine /> : <GitPullRequestIcon /> }
}
// Only what GitHub reports for an open PR; nothing when the data does not say so.
function fixActions(group: FileGroup, named: boolean): CardAction[] {
  const pr = group.pr
  if (!pr?.number || pr.state !== 'open') return []
  const suffix = named ? ` · ${group.name}` : ''
  const actions: CardAction[] = []
  if (pr.checks === 'failing') actions.push({ key: `fix-checks:${group.root}`, label: `Fix failing checks${suffix}`, message: fixChecksRequest(group), icon: <Wrench /> })
  if (pr.review === 'changes_requested') actions.push({ key: `fix-review:${group.root}`, label: `Address review comments${suffix}`, message: reviewRequest(group), icon: <Wrench /> })
  if (pr.conflicts) actions.push({ key: `fix-conflicts:${group.root}`, label: `Resolve conflicts${suffix}`, message: conflictsRequest(group), icon: <Wrench /> })
  return actions
}
function prLine(pr: PullRequest) {
  const checks = pr.checks && pr.checks !== 'none' ? ` · checks ${pr.checks}` : ''
  const review = pr.review === 'approved' ? ' · approved' : pr.review === 'changes_requested' ? ' · changes requested' : pr.state === 'open' ? ' · no review yet' : ''
  return `PR #${pr.number}${pr.state === 'open' ? '' : ` ${pr.state}`}${checks}${review}`
}

type UndoItem = { path: string; status: string; action: string; reason?: string }
type UndoPlan = { restore: UndoItem[]; skipped: UndoItem[] }
const fileList = (items: UndoItem[]) => items.slice(0, 5).map(item => item.path.split('/').pop()).join(', ') + (items.length > 5 ? `, +${items.length - 5} more` : '')
const plural = (n: number) => `${n} file${n === 1 ? '' : 's'}`
const errorText = (cause: unknown, fallback: string) => (cause as { response?: { data?: { error?: { message?: string } } } })?.response?.data?.error?.message || (cause instanceof Error ? cause.message : fallback)
// This reply's context for a card button: replaces {{context}} in the saved message.
export const cardContext = (groups: FileGroup[]) => {
  const paths = groups.flatMap(group => group.files.map(file => file.path)).slice(0, 20)
  const repo = groups.find(group => group.kind === 'repo' && group.branch)
  const pr = repo?.pr?.url
  return `Context: this reply changed these files: ${paths.join(', ')}${repo ? ` in ${repo.name} on branch ${repo.branch}` : ''}${pr ? `; PR ${pr}` : ''}`
}
const menuRow = 'flex cursor-pointer items-center gap-2.5 rounded-md px-2.5 py-2 text-sm outline-none data-[highlighted]:bg-muted [&_svg]:h-4 [&_svg]:w-4'
const commandIcon = (command: UserCommand) => <CommandIcon name={command.frontmatter.icon} fallback={Zap} />
const iconButton = 'inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50 [&_svg]:h-4 [&_svg]:w-4'
const Divider = () => <span className="mx-1.5 h-4 w-px bg-border" aria-hidden />
// Hover or keyboard focus shows the name and one plain line; touch taps go straight to the button.
function IconButton({ label, name = label, hint, onClick, disabled, strong, children }: { label: string; name?: string; hint: string; onClick: () => void; disabled?: boolean; strong?: boolean; children: ReactNode }) {
  return <Tooltip>
    <TooltipTrigger asChild>
      <button type="button" aria-label={label} disabled={disabled} className={`${iconButton}${strong ? ' text-foreground' : ''}`} onClick={onClick}>{children}</button>
    </TooltipTrigger>
    <TooltipContent className="max-w-xs text-xs"><p className="font-semibold">{name}</p><p className="text-muted-foreground">{hint}</p></TooltipContent>
  </Tooltip>
}
type MenuEntry = { key: string; label: string; icon: ReactNode; onSelect: () => void }
function MoreMenu({ items }: { items: MenuEntry[] }) {
  return <Menu.Root modal={false}>
    <Tooltip>
      <TooltipTrigger asChild>
        <Menu.Trigger type="button" aria-label="More actions" className={`${iconButton} data-[state=open]:bg-muted`}><MoreHorizontal /></Menu.Trigger>
      </TooltipTrigger>
      <TooltipContent className="text-xs">More actions</TooltipContent>
    </Tooltip>
    <Menu.Portal><Menu.Content align="end" sideOffset={6} collisionPadding={12} className="z-[100] max-h-[var(--radix-dropdown-menu-content-available-height)] w-[min(300px,calc(100vw-24px))] overflow-y-auto overscroll-contain rounded-xl border border-border bg-popover p-1.5 text-popover-foreground shadow-lg">
      {items.map(item => <Menu.Item key={item.key} onSelect={item.onSelect} className={menuRow}>{item.icon}<span className="min-w-0 flex-1 truncate">{item.label}</span></Menu.Item>)}
    </Menu.Content></Menu.Portal>
  </Menu.Root>
}
const addButtonStarter = 'Add a button called ___ that sends: ___'

const endpoint = '/api/workspace-snapshots'
const paramsFor = (reference: ReplyFileChangesRef) => ({ workspace_path: reference.workspacePath, snapshot_id: reference.snapshotId })

export function ReplyFileChanges({ reference }: { reference: ReplyFileChangesRef }) {
  const mode = useProjectMode(reference.sessionId)
  const account = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = JSON.stringify([account, server, reference.workspacePath, reference.snapshotId])
  if (mode !== 'dev') return null
  return <ScopedFileChanges key={scope} reference={reference} />
}

function ScopedFileChanges({ reference }: { reference: ReplyFileChangesRef }) {
  const summary = reference.summary
  // Only for a reply saved before summaries were sent with the event, and for the newest reply's "since your last review" line.
  const [result, setResult] = useState<ReviewResult | null>(null)
  const comparison = result?.comparison
  const [revision] = useReviewRevision()
  const phone = useMobileViewport()
  const [reviewError, setReviewError] = useState('')
  const [fetchedGroups, setFetchedGroups] = useState<FileGroup[]>([])
  const [prGroups, setPrGroups] = useState<FileGroup[] | null>(null)
  const [canEdit, setCanEdit] = useState(false)
  const [asked, setAsked] = useState('')
  const [commands, setCommands] = useState<UserCommand[]>([])
  const [open, setOpen] = useState<boolean | null>(null)
  const [undo, setUndo] = useState<{ plan?: UndoPlan; busy?: boolean; done?: string; error?: string } | null>(null)
  const latest = !reference.latestSnapshotId || reference.latestSnapshotId === reference.snapshotId
  const reviewed = reference.reviewedSnapshotId === reference.snapshotId
  const sinceReview = latest && !!reference.reviewedSnapshotId && !reviewed
  const hasSummary = !!summary
  useEffect(() => {
    const controller = new AbortController()
    // The card itself renders from the reply event; this only fills the "since your last review" line.
    if (!hasSummary || sinceReview) {
      void api.get<ReviewResult>(endpoint, { params: { ...paramsFor(reference), session_id: reference.sessionId, op: 'snapshot_compare' }, signal: controller.signal })
        .then(response => { if (!controller.signal.aborted) setResult(response.data) })
        .catch(() => { /* Expired/unavailable history has no change card. */ })
    }
    if (!hasSummary) {
      void api.get<{ groups: FileGroup[] }>(endpoint, { params: { ...paramsFor(reference), session_id: reference.sessionId, op: 'snapshot_groups' }, signal: controller.signal })
        .then(response => { if (!controller.signal.aborted) setFetchedGroups(response.data.groups ?? []) })
        .catch(() => { /* The card still shows the summary line. */ })
    }
    return () => controller.abort()
  }, [reference.workspacePath, reference.snapshotId, reference.sessionId, hasSummary, sinceReview, revision])
  useEffect(() => {
    const controller = new AbortController()
    // Same Builder check as the Crew screens: Run people never see Git buttons.
    void api.get<{ can_build: boolean }>('/api/workspace-access', { params: { workspace: reference.workspacePath }, signal: controller.signal })
      .then(response => { if (!controller.signal.aborted) setCanEdit(response.data.can_build === true) })
      .catch(() => setCanEdit(false))
    return () => controller.abort()
  }, [reference.workspacePath])
  useEffect(() => {
    if (!canEdit) { setCommands([]); return }
    let current = true
    // Buttons are the Crew's commands marked for the card; nothing extra if the list cannot load.
    void commandsApi.listCommands(reference.workspacePath)
      .then(response => { if (current) setCommands((response.commands ?? []).filter(command => command.frontmatter.show_on_card === true)) })
      .catch(() => { if (current) setCommands([]) })
    return () => { current = false }
  }, [canEdit, reference.workspacePath])
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
  const ask = async (key: string, message: string) => {
    setReviewError(''); setAsked('')
    try {
      const identity = captureChatIdentity()
      const tabId = Object.values(useChatStore.getState().chatTabs).find(tab => tab.sessionId === reference.sessionId)?.tabId
      if (!tabId) throw new Error('Open the original chat to ask for this.')
      assertChatIdentityCurrent(identity)
      await sendWorkspacePaneMessageToChat({ tabId, message })
      setAsked(key)
    } catch (cause) { setReviewError(cause instanceof Error ? cause.message : 'Could not send this to the chat. Try again.') }
  }
  const baseGroups = summary?.groups ?? fetchedGroups
  // PR status is a GitHub call: it loads once the card is open and never holds the header or file list.
  const isOpen = open ?? latest
  const lookUpPullRequests = isOpen && hasSummary && baseGroups.some(onGitHub)
  useEffect(() => {
    if (!lookUpPullRequests) return
    const controller = new AbortController()
    void api.get<{ groups: FileGroup[] }>(endpoint, { params: { ...paramsFor(reference), session_id: reference.sessionId, op: 'snapshot_groups' }, signal: controller.signal })
      .then(response => { if (!controller.signal.aborted) setPrGroups(response.data.groups ?? []) })
      .catch(() => { /* No PR line. */ })
    return () => controller.abort()
  }, [lookUpPullRequests, reference.workspacePath, reference.snapshotId, reference.sessionId])
  // The file list stays as captured; the lazy read adds only the PR status and next step.
  const groups = prGroups ? baseGroups.map(group => { const found = prGroups.find(item => item.kind === group.kind && item.root === group.root); return { ...group, pr: found?.pr, next_step: found?.next_step } }) : baseGroups
  const sum = (key: 'added' | 'removed') => result?.summary?.[key] ?? (comparison?.changes ?? []).reduce((n, change) => n + (change[key] ?? 0), 0)
  const total = summary ? summary.files : result?.total_changes ?? comparison?.changes?.length ?? 0
  const added = summary ? summary.added : sum('added'), removed = summary ? summary.removed : sum('removed')
  const repoGroups = groups.filter(group => group.kind === 'repo' && group.branch)
  const named = repoGroups.length > 1
  const sendAction = (action: CardAction): MenuEntry => ({ ...action, onSelect: () => void ask(action.key, action.message) })
  const runCommand = (command: UserCommand) => void ask(`command:${command.frontmatter.name}`, renderCommandPrompt(command.content, cardContext(groups)))
  const addButton = () => { const tabId = Object.values(useChatStore.getState().chatTabs).find(tab => tab.sessionId === reference.sessionId)?.tabId; if (tabId) void openWorkspacePaneChatWithDraft({ tabId, message: addButtonStarter }).catch(() => setReviewError('Open the original chat to add a button.')) }
  // The row keeps the defaults, two of the person's buttons and +; everything else is under More. Phones show the defaults and More only.
  const shownCommands = phone ? [] : commands.slice(0, 2)
  const more: MenuEntry[] = canEdit ? [
    ...repoGroups.flatMap(group => { const next = nextStepAction(group, named); return next ? [sendAction(next)] : [] }),
    ...repoGroups.flatMap(group => fixActions(group, named).map(sendAction)),
    ...commands.slice(shownCommands.length).map(command => ({ key: `command:${command.frontmatter.name}`, label: command.frontmatter.name, icon: commandIcon(command), onSelect: () => runCommand(command) })),
    ...(phone ? [{ key: 'add', label: 'Add button', icon: <Plus />, onSelect: addButton }] : []),
  ] : []
  // No card until at least one changed file is known (also while loading); a reply that changed nothing shows nothing.
  if (total === 0) return null
  return (
    <section className="my-3 rounded-lg border border-border px-3 py-2 text-sm" aria-label="Files changed during this reply">
      <div className="flex items-center gap-2">
        <button type="button" className="flex min-w-0 flex-1 flex-wrap items-center gap-x-2 text-left font-medium" aria-expanded={isOpen} onClick={() => setOpen(!isOpen)}>
          <span aria-hidden>{isOpen ? '▾' : '▸'}</span>Files changed during this reply · {plural(total)}
          <span className="text-xs font-normal"><span className="text-diff-add">+{added}</span> <span className="text-diff-del">−{removed}</span></span>
          {latest && reviewed && <span className="text-xs font-normal text-muted-foreground">Reviewed up to here</span>}
        </button>
        <TooltipProvider delayDuration={150}><div className="flex shrink-0 items-center gap-0.5">
          <IconButton strong label="Review changes" hint="See every file this reply changed, line by line." onClick={() => openChatFiles(reference.sessionId)}><FileDiffIcon /></IconButton>
          {canEdit && repoGroups.length > 0 && <>
            <IconButton label="Commit on the current branch" hint={`Ask the agent to commit these files on ${repoGroups.length === 1 ? repoGroups[0].branch : 'their current branches'}. Nothing is pushed.`} onClick={() => void ask('commit:reply', repoGroups.map(commitRequest).join('\n\n'))}><GitCommitIcon /></IconButton>
            <IconButton label="Push to a branch and open a PR" hint="Ask the agent to push a branch and open a pull request with a full description." onClick={() => void ask('push:reply', repoGroups.map(pushRequest).join('\n\n'))}><GitPullRequestIcon /></IconButton>
          </>}
          {canEdit && !phone && <Divider />}
          {canEdit && shownCommands.map(command => {
            const name = command.frontmatter.name
            return <IconButton key={name} label={command.frontmatter.description || name} name={name} hint={command.frontmatter.description || `Runs your ${name} command in this chat.`} onClick={() => runCommand(command)}>{commandIcon(command)}</IconButton>
          })}
          {canEdit && !phone && <IconButton label="Add button" hint="Add your own button to this card." onClick={addButton}><Plus /></IconButton>}
          {more.length > 0 && <MoreMenu items={more} />}
        </div></TooltipProvider>
      </div>
      {asked && <p className="mt-1 text-xs text-muted-foreground">Asked in this chat.</p>}
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
            <ul className="mt-1 space-y-1">
              {group.files.map(change => (
                <li key={change.path} className="flex flex-wrap items-center gap-2">
                  <button type="button" className="min-w-0 break-all text-left font-mono text-xs" onClick={() => openChatFiles(reference.sessionId, { reference, change })}>{ownName(change.path, group.root)}</button>
                  <span className="text-xs text-muted-foreground">{change.status === 'A' ? 'Added' : change.status === 'D' ? 'Deleted' : ''}</span>
                  {!change.binary && typeof change.added === 'number' && typeof change.removed === 'number'
                    && <span className="text-xs" aria-label={`${change.added} lines added, ${change.removed} lines removed`}><span className="text-diff-add">+{change.added}</span> <span className="text-diff-del">−{change.removed}</span></span>}
                </li>
              ))}
              {group.more_count > 0 && <li className="text-xs text-muted-foreground">+ {group.more_count} more</li>}
            </ul>
            {group.kind === 'repo' && group.pr?.state === 'connect_github' && <button type="button" className="mt-1 text-xs underline" onClick={() => openProjectIntegrations('secrets')}>Connect GitHub to see PR status</button>}
            {group.kind === 'repo' && group.pr && group.pr.number && group.pr.url && <div className="mt-1 flex flex-wrap items-center gap-x-3 text-xs">
              <a className="underline" href={group.pr.url} target="_blank" rel="noreferrer">{prLine(group.pr)}</a>
              {canEdit && fixActions(group, false).map(action => <button key={action.key} type="button" className="text-muted-foreground underline hover:text-foreground" onClick={() => void ask(action.key, action.message)}>{action.label}</button>)}
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
