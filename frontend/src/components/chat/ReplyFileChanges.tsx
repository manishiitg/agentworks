import { useEffect, useState, type ReactNode } from 'react'
import { Play, Plus, Undo2, Zap } from 'lucide-react'
import api from '../../services/api'
import { useReviewRevision, type FileChange, type ReviewResult } from '../workspace/chatFileReview'
import type { ReplyFileChangesRef } from '../../utils/replyFileChanges'
import { useProjectMode } from '../../products/work/codeLocalFiles'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { openChatFiles } from '../workspace/chatFileNavigation'
import { useChatStore } from '../../stores/useChatStore'
import { sendWorkspacePaneMessageToChat, openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'
import { commandsApi } from '../../api/commands'
import { renderCommandPrompt } from '../../commands/commandPrompt'
import type { UserCommand } from '../../types/commands'
import { assertChatIdentityCurrent, captureChatIdentity } from '../../utils/chatIdentity'
import { openProjectIntegrations } from '../../utils/projectIntegrationNavigation'
import { FileDiffIcon, GitCommitIcon, GitPullRequestIcon } from '../icons/octicons'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '../ui/tooltip'

type PullRequest = { number?: number; url?: string; state: string; checks?: string; review?: string }
type FileGroup = { kind: 'repo' | 'crew_files'; root: string; name: string; branch?: string; new_checkout: boolean; files: FileChange[]; total_files: number; more_count: number; pr?: PullRequest }

const ownName = (path: string, root: string) => root && path.startsWith(`${root}/`) ? path.slice(root.length + 1) : path
// Plain-language requests for the agent in this same chat. Only this reply's files
// are named, and the agent is told never to push the main branch.
export const commitRequest = (group: FileGroup) => `Commit these files in ${group.name} (${group.root || 'the Crew folder'}) on branch ${group.branch}. Stage only the files listed here, by name, and use a short message that describes the change. Do not push yet.\n${group.files.map(file => `- ${file.path}`).join('\n')}${group.more_count ? `\n(and ${group.more_count} more files from this reply)` : ''}`
export const pushRequest = (group: FileGroup) => `Open a pull request for this reply's work in ${group.name} (${group.root || 'the Crew folder'}). If branch ${group.branch} is the default branch (main or master), first create a new branch from it with a short descriptive name; otherwise use ${group.branch}. Never push the default branch. If these files are not committed yet, commit them, staging only the files listed here, by name:\n${group.files.map(file => `- ${file.path}`).join('\n')}${group.more_count ? `\n(and ${group.more_count} more files from this reply)` : ''}\nThen push the branch to GitHub and open a pull request with a proper description: a short title, a summary of what changed and why based on this chat, the list of changed files, and how it was tested. Reply with the pull request link.`
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
  const [result, setResult] = useState<ReviewResult | null>(null)
  const comparison = result?.comparison
  const [revision] = useReviewRevision()
  const [reviewError, setReviewError] = useState('')
  const [groups, setGroups] = useState<FileGroup[]>([])
  const [canEdit, setCanEdit] = useState(false)
  const [asked, setAsked] = useState('')
  const [commands, setCommands] = useState<UserCommand[]>([])
  const [open, setOpen] = useState<boolean | null>(null)
  const [undo, setUndo] = useState<{ plan?: UndoPlan; busy?: boolean; done?: string; error?: string } | null>(null)
  const latest = result?.history.latest_snapshot_id === reference.snapshotId && (!reference.latestSnapshotId || reference.latestSnapshotId === reference.snapshotId)
  const reviewed = result?.history.baseline_snapshot_id === reference.snapshotId
  useEffect(() => {
    const controller = new AbortController()
    void api.get<ReviewResult>(endpoint, { params: { ...paramsFor(reference), session_id: reference.sessionId, op: 'snapshot_compare' }, signal: controller.signal })
      .then(response => { if (!controller.signal.aborted) setResult(response.data) })
      .catch(() => { /* Expired/unavailable history has no change card. */ })
    // Loaded with the card, not on open, so opening or closing it never changes the page height later.
    void api.get<{ groups: FileGroup[] }>(endpoint, { params: { ...paramsFor(reference), session_id: reference.sessionId, op: 'snapshot_groups' }, signal: controller.signal })
      .then(response => { if (!controller.signal.aborted) setGroups(response.data.groups ?? []) })
      .catch(() => { /* The card still shows the summary line. */ })
    // Same Builder check as the Crew screens: Run people never see Git buttons.
    void api.get<{ can_build: boolean }>('/api/workspace-access', { params: { workspace: reference.workspacePath }, signal: controller.signal })
      .then(response => { if (!controller.signal.aborted) setCanEdit(response.data.can_build === true) })
      .catch(() => setCanEdit(false))
    return () => controller.abort()
  }, [reference.workspacePath, reference.snapshotId, reference.sessionId, reference.latestSnapshotId, revision])
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
  const total = result?.total_changes ?? comparison?.changes?.length ?? 0
  const sum = (key: 'added' | 'removed') => result?.summary?.[key] ?? (comparison?.changes ?? []).reduce((n, change) => n + (change[key] ?? 0), 0)
  const added = sum('added'), removed = sum('removed')
  const repoGroups = groups.filter(group => group.kind === 'repo' && group.branch)
  const isOpen = open ?? latest
  // No card until at least one changed file is known (also while loading); a reply that changed nothing shows nothing.
  if (!comparison || (result?.total_changes ?? comparison.changes?.length ?? 0) === 0) return null
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
          {canEdit && <Divider />}
          {canEdit && commands.map(command => {
            const name = command.frontmatter.name
            return <IconButton key={name} label={command.frontmatter.description || name} name={name} hint={command.frontmatter.description || `Runs your ${name} command in this chat.`} onClick={() => void ask(`command:${name}`, renderCommandPrompt(command.content, cardContext(groups)))}>{name === 'check-this-change' ? <Play /> : <Zap />}</IconButton>
          })}
          {canEdit && <IconButton label="Add button" hint="Add your own button to this card." onClick={() => { const tabId = Object.values(useChatStore.getState().chatTabs).find(tab => tab.sessionId === reference.sessionId)?.tabId; if (tabId) void openWorkspacePaneChatWithDraft({ tabId, message: addButtonStarter }).catch(() => setReviewError('Open the original chat to add a button.')) }}><Plus /></IconButton>}
        </div></TooltipProvider>
      </div>
      {(asked === 'commit:reply' || asked === 'push:reply' || commands.some(command => asked === `command:${command.frontmatter.name}`)) && <p className="mt-1 text-xs text-muted-foreground">Asked in this chat.</p>}
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
            {group.kind === 'repo' && group.pr && group.pr.number && group.pr.url && <a className="mt-1 block text-xs underline" href={group.pr.url} target="_blank" rel="noreferrer">{prLine(group.pr)}</a>}
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
