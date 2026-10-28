import { useEffect, useState } from 'react'
import api from '../../services/api'
import { markFilesReviewed, useReviewRevision, type FileChange, type ReviewResult } from '../workspace/chatFileReview'
import type { ReplyFileChangesRef } from '../../utils/replyFileChanges'
import { useProjectMode } from '../../products/work/codeLocalFiles'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { openChatFiles } from '../workspace/chatFileNavigation'
import { useChatStore } from '../../stores/useChatStore'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
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
  if (!comparison?.changes?.length && !(latest && result?.history.can_review)) return null
  if (!comparison) return null
  return (
    <section className="my-3 rounded-lg border border-border px-3 py-2 text-sm" aria-label="Files changed during this reply">
      <details>
        <summary className="cursor-pointer font-medium">Files changed during this reply · {result?.total_changes ?? comparison.changes.length}</summary>
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
      {reviewError && <p role="alert" className="mt-2 text-xs">{reviewError}</p>}
      <button type="button" className="mt-2 text-xs underline" onClick={() => openChatFiles(reference.sessionId)}>Review changes</button>
      {latest && <div className="mt-2 flex flex-wrap items-center gap-2 border-t pt-2 text-xs">
        {result?.since_review && <span>Since your last review: {result.since_review.files} files <span className="text-green-600">+{result.since_review.added}</span> <span className="text-red-500">−{result.since_review.removed}</span></span>}
        {reviewed ? <span>Reviewed up to here</span> : result?.history.can_review && <button type="button" disabled={reviewing} className="rounded border px-2 py-1" onClick={() => { setReviewing(true); setReviewError(''); void markFilesReviewed(reference.workspacePath,reference.sessionId,reference.snapshotId).catch(() => setReviewError('Could not save the review point. Try again.')).finally(() => setReviewing(false)) }}>Mark as reviewed</button>}
      </div>}
      </details>

    </section>
  )
}
