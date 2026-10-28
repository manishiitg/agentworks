import { useEffect, useState } from 'react'
import api from '../../services/api'
import { markFilesReviewed, useReviewRevision, type FileChange, type ReviewResult } from '../workspace/chatFileReview'
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

type PullRequest = { number?: number; url?: string; state: string; checks?: string; review?: string }
type FileGroup = { kind: 'repo' | 'crew_files'; root: string; name: string; branch?: string; new_checkout: boolean; files: FileChange[]; total_files: number; more_count: number; pr?: PullRequest }

const ownName = (path: string, root: string) => root && path.startsWith(`${root}/`) ? path.slice(root.length + 1) : path
// Plain-language requests for the agent in this same chat. Only this reply's files
// are named, and the agent is told never to push the main branch.
export const commitRequest = (group: FileGroup) => `Commit these files in ${group.name} (${group.root || 'the Crew folder'}) on branch ${group.branch}. Stage only the files listed here, by name, and use a short message that describes the change. Do not push yet.\n${group.files.map(file => `- ${file.path}`).join('\n')}${group.more_count ? `\n(and ${group.more_count} more files from this reply)` : ''}`
export const pushRequest = (group: FileGroup) => `Push branch ${group.branch} of ${group.name} (${group.root || 'the Crew folder'}) to GitHub and open a pull request for it. Never push main or master: if this branch is main or master, make a new branch first. Include only the work from this reply, and send me the pull request link.`
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
  const [reviewing, setReviewing] = useState(false)
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
  const firstChange = comparison?.changes?.[0]
  const isOpen = open ?? latest
  // No card until at least one changed file is known (also while loading); a reply that changed nothing shows nothing.
  if (!comparison || (result?.total_changes ?? comparison.changes?.length ?? 0) === 0) return null
  return (
    <section className="my-3 rounded-lg border border-border px-3 py-2 text-sm" aria-label="Files changed during this reply">
      <button type="button" className="flex w-full flex-wrap items-center gap-x-2 text-left font-medium" aria-expanded={isOpen} onClick={() => setOpen(!isOpen)}>
        <span aria-hidden>{isOpen ? '▾' : '▸'}</span>Files changed during this reply · {plural(total)}
        <span className="text-xs font-normal"><span className="text-green-600">+{added}</span> <span className="text-red-500">−{removed}</span></span>
      </button>
      <div className="mt-1 flex flex-wrap items-center gap-2 text-xs">
        <button type="button" className="rounded border px-2 py-1" onClick={() => openChatFiles(reference.sessionId)}>Review changes</button>
        {firstChange && <button type="button" className="rounded border px-2 py-1" onClick={() => openChatFiles(reference.sessionId, { reference, change: firstChange })}>Compare</button>}
        {canEdit && latest && !reviewed && result?.history.can_review && <button type="button" disabled={reviewing} className="rounded border px-2 py-1" onClick={() => { setReviewing(true); setReviewError(''); void markFilesReviewed(reference.workspacePath,reference.sessionId,reference.snapshotId).catch(() => setReviewError('Could not save the review point. Try again.')).finally(() => setReviewing(false)) }}>Mark as reviewed</button>}
        {latest && reviewed && <span className="text-muted-foreground">Reviewed up to here</span>}
        {canEdit && undo === null && <button type="button" className="rounded border px-2 py-1" onClick={() => void startUndo()}>Undo this reply</button>}
        {canEdit && <span className="text-muted-foreground" aria-hidden>|</span>}
        {canEdit && commands.map(command => (
          <button key={command.frontmatter.name} type="button" className="rounded border px-2 py-1 text-xs" onClick={() => void ask(`command:${command.frontmatter.name}`, renderCommandPrompt(command.content, cardContext(groups)))}>{command.frontmatter.description || command.frontmatter.name}</button>
        ))}
        {canEdit && <button type="button" className="rounded border border-dashed px-2 py-1 text-xs" onClick={() => { const tabId = Object.values(useChatStore.getState().chatTabs).find(tab => tab.sessionId === reference.sessionId)?.tabId; if (tabId) void openWorkspacePaneChatWithDraft({ tabId, message: addButtonStarter }).catch(() => setReviewError('Open the original chat to add a button.')) }}>+ Add button</button>}
        {commands.some(command => asked === `command:${command.frontmatter.name}`) && <span className="text-xs text-muted-foreground">Asked in this chat.</span>}
      </div>
      {latest && result?.since_review && <p className="mt-1 text-xs">Since your last review: {result.since_review.files} files <span className="text-green-600">+{result.since_review.added}</span> <span className="text-red-500">−{result.since_review.removed}</span></p>}
      {reviewError && <p role="alert" className="mt-1 text-xs">{reviewError}</p>}
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
      {isOpen && <div>
        {[...groups].sort((x, y) => Number(x.kind === 'crew_files') - Number(y.kind === 'crew_files')).map(group => (
          <div key={`${group.kind}:${group.root}`} className="mt-3 border-t border-border pt-2 first:mt-2 first:border-0 first:pt-0">
            <div className="flex flex-wrap items-center gap-2">
              <b>{group.kind === 'crew_files' ? 'Crew files' : group.name}</b>
              {group.kind === 'crew_files' && <span className="rounded-full border border-border px-2 text-xs text-muted-foreground">not in Git</span>}
              {group.new_checkout && <span className="rounded-full border border-border px-2 text-xs text-muted-foreground">new copy · {group.total_files} files</span>}
              {group.kind === 'repo' && group.branch && <span className="rounded-full border border-border px-2 text-xs text-muted-foreground">branch {group.branch}</span>}
            </div>
            <ul className="mt-1 space-y-1">
              {group.files.map(change => (
                <li key={change.path} className="flex flex-wrap items-center gap-2">
                  <button type="button" className="min-w-0 break-all text-left underline underline-offset-2" onClick={() => openChatFiles(reference.sessionId, { reference, change })}>{ownName(change.path, group.root)}</button>
                  <span className="text-xs text-muted-foreground">{change.status === 'A' ? 'Added' : change.status === 'D' ? 'Deleted' : 'Changed'}</span>
                  {!change.binary && typeof change.added === 'number' && typeof change.removed === 'number'
                    && <span className="text-xs" aria-label={`${change.added} lines added, ${change.removed} lines removed`}><span className="text-green-600">+{change.added}</span> <span className="text-red-500">−{change.removed}</span></span>}
                </li>
              ))}
              {group.more_count > 0 && <li className="text-xs text-muted-foreground">+ {group.more_count} more</li>}
            </ul>
            {group.kind === 'repo' && group.pr?.state === 'connect_github' && <button type="button" className="mt-1 text-xs underline" onClick={() => openProjectIntegrations('secrets')}>Connect GitHub to see PR status</button>}
            {group.kind === 'repo' && group.pr && group.pr.number && group.pr.url && <a className="mt-1 block text-xs underline" href={group.pr.url} target="_blank" rel="noreferrer">{prLine(group.pr)}</a>}
            {group.kind === 'repo' && canEdit && group.branch && <div className="mt-2 flex flex-wrap items-center gap-2">
              <button type="button" className="rounded border px-2 py-1 text-xs" onClick={() => void ask(`commit:${group.root}`, commitRequest(group))}>Commit</button>
              <button type="button" className="rounded border border-primary px-2 py-1 text-xs" onClick={() => void ask(`push:${group.root}`, pushRequest(group))}>Push &amp; open PR</button>
              {(asked === `commit:${group.root}` || asked === `push:${group.root}`) && <span className="text-xs text-muted-foreground">Asked in this chat.</span>}
            </div>}
          </div>
        ))}
      </div>}

    </section>
  )
}
