import { useEffect, useState } from 'react'
import { ChevronDown, ChevronRight, ExternalLink, MessageSquare } from 'lucide-react'
import api from '../../services/api'
import { GitPullRequestIcon } from '../icons/octicons'
import { WorkspaceViewHeader } from '../workflow/WorkspaceViewHeader'
import DiffRenderer from '../ui/DiffRenderer'
import { reviewEndpoint } from './chatFileReviewData'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { useChatStore } from '../../stores/useChatStore'

export type PullRequestTarget = { repository: string; number: number }

type PRStatus = { state: string; checks?: string; review?: string; failing_checks?: string[]; conflicts?: boolean }
type PRFile = { path: string; previous_path?: string; status: string; additions: number; deletions: number; patch?: string }
type PRComment = { path?: string; line?: number; outdated?: boolean; author: string; body: string; url?: string; at?: string; reply?: boolean; review?: string }
type PRReview = {
  repository: string; number: number; url: string; title: string; body?: string; author?: string; draft?: boolean
  head: string; base: string; additions: number; deletions: number; file_count: number
  status: PRStatus; mergeable: boolean; files: PRFile[]; more_files?: boolean; comments: PRComment[]; can_ask: boolean
}

/** One plain-words line: "Checks passing · 1 approval · ready to merge". */
export function pullRequestStatusLine(pr: Pick<PRReview, 'status' | 'mergeable' | 'draft'>): string {
  const s = pr.status
  if (s.state === 'merged') return 'Merged'
  if (s.state === 'closed') return 'Closed without merging'
  const parts: string[] = []
  if (pr.draft) parts.push('Draft')
  const failing = s.failing_checks?.length ?? 0
  if (s.checks === 'failing') parts.push(failing ? `${failing} ${failing === 1 ? 'check' : 'checks'} failing` : 'Checks failing')
  else if (s.checks === 'pending') parts.push('Checks running')
  else if (s.checks === 'passing') parts.push('Checks passing')
  else if (s.checks === 'unavailable') parts.push('Checks unknown')
  if (s.review === 'approved') parts.push('Approved')
  else if (s.review === 'changes_requested') parts.push('Changes requested')
  else if (s.review === 'commented') parts.push('Review comments')
  else if (s.review === 'none') parts.push('No review yet')
  if (s.conflicts) parts.push('Has conflicts')
  else if (pr.mergeable) parts.push('Ready to merge')
  return parts.join(' · ')
}

const fileStatus: Record<string, string> = { added: 'A', removed: 'D', modified: 'M', renamed: 'R', copied: 'C', changed: 'M' }

/** The fix request the agent receives: every open review comment, with file and line. */
export function fixCommentsMessage(pr: Pick<PRReview, 'repository' | 'number' | 'url' | 'head'>, comments: PRComment[]): string {
  const lines = comments.filter(c => !c.reply && c.review !== 'approved').map(c => {
    const where = c.path ? `${c.path}${c.line ? `:${c.line}` : ''}${c.outdated ? ' (outdated)' : ''}` : 'Review summary'
    return `- ${where} — ${c.author}: ${c.body.trim().replace(/\s+/g, ' ').slice(0, 600)}`
  })
  return `Please address the review comments on pull request #${pr.number} in ${pr.repository} (${pr.url}), branch ${pr.head}:\n\n${lines.join('\n')}\n\nFix what is right, reply where you disagree, then push to the same branch.`
}

export function mergeMessage(pr: Pick<PRReview, 'repository' | 'number' | 'url' | 'base'>): string {
  return `Please merge pull request #${pr.number} in ${pr.repository} (${pr.url}) into ${pr.base}. First ask me which way to merge it (squash, rebase or a merge commit), then merge it with that method and tell me the result.`
}

/** A chat's pull request from GitHub: status, files and review comments.
 *  Merging and fixing are asked of the agent in this chat; the panel itself
 *  never writes to GitHub. */
export function PullRequestPanel({ target, workspacePath, sessionId, tabId, onBack }: { target: PullRequestTarget; workspacePath: string; sessionId: string; tabId: string; onBack: () => void }) {
  const [pr, setPr] = useState<PRReview | null>(null)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const [open, setOpen] = useState<Record<string, boolean>>({})
  const [asked, setAsked] = useState('')
  useEffect(() => {
    const controller = new AbortController()
    setError('')
    void api.get<PRReview>(reviewEndpoint, { params: { workspace_path: workspacePath, session_id: sessionId, op: 'pull_request', repository: target.repository, number: target.number }, signal: controller.signal })
      .then(({ data }) => { if (!controller.signal.aborted) setPr(data) })
      .catch((err: { response?: { data?: { message?: string } } }) => {
        if (!controller.signal.aborted) setError(err?.response?.data?.message || 'Could not load this pull request. Try again.')
      })
    return () => controller.abort()
  }, [workspacePath, sessionId, target.repository, target.number, revision])

  const ask = async (kind: string, message: string) => {
    try {
      await sendWorkspacePaneMessageToChat({ tabId, message })
      setAsked(kind)
    } catch (err) {
      useChatStore.getState().addToast(err instanceof Error ? err.message : 'Could not send to the chat.', 'error')
    }
  }

  const title = `PR #${target.number}`
  if (error) return <section className="flex h-full flex-col">
    <WorkspaceViewHeader icon={<GitPullRequestIcon className="h-4 w-4" />} title={title} subtitle={target.repository} actions={<button type="button" className="text-xs underline" onClick={onBack}>Back</button>} />
    <div className="p-3 text-sm"><p>{error}</p><button type="button" className="mt-2 text-xs underline" onClick={() => setRevision(v => v + 1)}>Try again</button></div>
  </section>
  if (!pr) return <section className="flex h-full flex-col">
    <WorkspaceViewHeader icon={<GitPullRequestIcon className="h-4 w-4" />} title={title} subtitle={target.repository} />
    <p className="p-3 text-sm text-muted-foreground">Loading…</p>
  </section>

  const openComments = pr.comments.filter(c => !c.reply && c.review !== 'approved')
  const byFile = new Map<string, PRComment[]>()
  for (const c of pr.comments) if (c.path) byFile.set(c.path, [...(byFile.get(c.path) ?? []), c])
  const summaries = pr.comments.filter(c => !c.path)
  const isOpen = pr.status.state === 'open'
  const button = 'rounded border px-2 py-1 text-xs hover:bg-muted disabled:opacity-50'

  return <section className="flex h-full min-h-0 flex-col">
    <WorkspaceViewHeader icon={<GitPullRequestIcon className="h-4 w-4" />} title={`${title} · ${pr.title}`} subtitle={`${pr.repository} · ${pr.head} → ${pr.base}`}
      actions={<div className="flex items-center gap-2">
        <a className="inline-flex items-center gap-1 text-xs underline" href={pr.url} target="_blank" rel="noreferrer">GitHub<ExternalLink className="h-3 w-3" /></a>
        <button type="button" className="text-xs underline" onClick={onBack}>Back</button>
      </div>} />
    <div className="min-h-0 flex-1 space-y-3 overflow-auto p-3 text-sm">
      <div>
        <p className="font-medium">{pullRequestStatusLine(pr)}</p>
        {!!pr.status.failing_checks?.length && <p className="mt-1 text-xs text-muted-foreground">Failing: {pr.status.failing_checks.join(', ')}</p>}
        <p className="mt-1 text-xs text-muted-foreground">{pr.file_count} {pr.file_count === 1 ? 'file' : 'files'} · +{pr.additions} −{pr.deletions}{pr.author ? ` · opened by ${pr.author}` : ''}</p>
        {isOpen && pr.can_ask && <div className="mt-2 flex flex-wrap gap-2">
          {openComments.length > 0 && <button type="button" className={button} disabled={asked === 'fix'} onClick={() => void ask('fix', fixCommentsMessage(pr, openComments))}>
            <MessageSquare className="mr-1 inline h-3 w-3" />{asked === 'fix' ? 'Sent to the agent' : 'Ask the agent to fix these'}
          </button>}
          {pr.mergeable && <button type="button" className={button} disabled={asked === 'merge'} onClick={() => void ask('merge', mergeMessage(pr))}>
            {asked === 'merge' ? 'Sent to the agent' : 'Ask the agent to merge'}
          </button>}
        </div>}
      </div>
      {pr.body?.trim() && <p className="whitespace-pre-wrap break-words text-xs text-muted-foreground">{pr.body.trim().slice(0, 4000)}</p>}
      {summaries.length > 0 && <div className="space-y-2">
        <h3 className="text-xs font-semibold">Reviews</h3>
        {summaries.map((c, i) => <Comment key={`s${i}`} comment={c} />)}
      </div>}
      <div>
        <h3 className="mb-1 text-xs font-semibold">Files changed</h3>
        <ul className="divide-y rounded border">
          {pr.files.map(file => {
            const comments = byFile.get(file.path) ?? []
            const expanded = open[file.path] ?? false
            return <li key={file.path}>
              <button type="button" className="flex w-full items-center gap-2 px-2 py-1 text-left text-xs hover:bg-muted" aria-expanded={expanded} onClick={() => setOpen(v => ({ ...v, [file.path]: !expanded }))}>
                {expanded ? <ChevronDown className="h-3 w-3 shrink-0" /> : <ChevronRight className="h-3 w-3 shrink-0" />}
                <span className="w-3 shrink-0 font-mono text-muted-foreground">{fileStatus[file.status] ?? 'M'}</span>
                <span className="min-w-0 flex-1 truncate font-mono" title={file.previous_path ? `${file.previous_path} → ${file.path}` : file.path}>{file.path}</span>
                {comments.length > 0 && <span className="inline-flex shrink-0 items-center gap-0.5 text-muted-foreground"><MessageSquare className="h-3 w-3" />{comments.length}</span>}
                <span className="shrink-0 text-muted-foreground">+{file.additions} −{file.deletions}</span>
              </button>
              {expanded && <div className="border-t">
                {file.patch ? <DiffRenderer content={`--- a/${file.previous_path || file.path}\n+++ b/${file.path}\n${file.patch}`} /> : <p className="p-2 text-xs text-muted-foreground">No text diff to show here{file.status === 'removed' ? '' : '; open it on GitHub'}.</p>}
                {comments.length > 0 && <div className="space-y-2 border-t p-2">{comments.map((c, i) => <Comment key={i} comment={c} />)}</div>}
              </div>}
            </li>
          })}
        </ul>
        {pr.more_files && <p className="mt-1 text-xs text-muted-foreground">Showing the first {pr.files.length} files. <a className="underline" href={`${pr.url}/files`} target="_blank" rel="noreferrer">See all on GitHub</a></p>}
      </div>
    </div>
  </section>
}

function Comment({ comment }: { comment: PRComment }) {
  const label = comment.review === 'changes_requested' ? ' requested changes' : comment.review === 'approved' ? ' approved' : ''
  return <div className={`rounded border p-2 text-xs ${comment.reply ? 'ml-4' : ''}`}>
    <p className="text-muted-foreground">
      <span className="font-medium text-foreground">{comment.author}</span>{label}
      {comment.line ? ` · line ${comment.line}` : ''}{comment.outdated && comment.path ? ' · outdated' : ''}
      {comment.url && <> · <a className="underline" href={comment.url} target="_blank" rel="noreferrer">open</a></>}
    </p>
    <p className="mt-1 whitespace-pre-wrap break-words">{comment.body.trim().slice(0, 4000)}</p>
  </div>
}
