import { useEffect, useState } from 'react'
import { FileDiff, GitBranch } from 'lucide-react'
import axios from 'axios'
import { PRWatcherControls } from '../chat/PRWatcherControls'
import type { ReplyPullRequest } from '../../utils/replyFileChanges'
import api from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { WorkspaceContextRow } from './WorkspaceContextCard'
import { reviewEndpoint, useReviewRevision } from './chatFileReviewData'

type ChatFile = { path: string; status: string; added?: number; removed?: number }
type ChatGroup = {
  kind: 'repo' | 'crew_files'; root: string; name: string; branch?: string; total_files: number
  github_repo?: string; pr?: ReplyPullRequest
  uncommitted?: ChatFile[]; commits?: { sha: string; subject: string; files: number }[]; commits_known?: boolean
}

// What the server says about one checkout, in plain words: its pull request,
// the commits made in this chat and what is not committed yet.
function repoSummary(group: ChatGroup) {
  const parts: string[] = []
  if (group.pr?.number) parts.push(`PR #${group.pr.number} ${group.pr.state}`)
  if (group.commits?.length) parts.push(`${group.commits.length} commit${group.commits.length === 1 ? '' : 's'}`)
  const open = group.uncommitted?.length ?? 0
  parts.push(open ? `${open} not committed` : 'all committed')
  return parts.join(' · ')
}

/** Where this chat's work stands (PLAT-746): per repository from Git, other files as saved. The server decides. */
export function ChatChangesRow({ workspacePath, sessionId, onOpen }: { workspacePath: string; sessionId: string; onOpen: (outsideGit?: boolean) => void }) {
  const [groups, setGroups] = useState<ChatGroup[] | null>(null)
  const [open, setOpen] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [retry, setRetry] = useState(0)
  const [canReview, setCanReview] = useState(false)
  const [revision] = useReviewRevision()
  const savedEvents = useChatStore(state => (state.tabEvents[sessionId] ?? []).filter(event => event.type === 'reply_file_changes' || event.type === 'file_reviewed').map(event => event.id).join(','))
  // A new chat starts empty; a refresh of the same chat keeps the rows on screen until the answer arrives.
  useEffect(() => { setGroups(null); setError(''); setCanReview(false); setOpen(null) }, [workspacePath, sessionId])
  useEffect(() => {
    const controller = new AbortController()
    void api.get<{ groups?: ChatGroup[]; history?: { can_review?: boolean } }>(reviewEndpoint, { params: { workspace_path: workspacePath, session_id: sessionId, op: 'chat_status' }, signal: controller.signal })
      .then(({ data }) => { if (!controller.signal.aborted) { setGroups(data.groups ?? []); setCanReview(data.history?.can_review === true); setError('') } })
      .catch(cause => { if (!controller.signal.aborted) setError(axios.isAxiosError(cause) && cause.response?.data?.error?.code === 'invalid_action' ? 'This changes view needs the updated AgentWorks server.' : 'Could not load changes.') })
    return () => controller.abort()
  }, [workspacePath, sessionId, revision, savedEvents, retry])
  const notice = error && <p role="alert" className="px-1 text-xs text-muted-foreground">{error} <button type="button" className="underline hover:text-foreground" onClick={() => setRetry(value => value + 1)}>Retry</button></p>
  if (!groups?.length) return <div><WorkspaceContextRow icon={FileDiff} onClick={() => onOpen()} detail={error ? 'Unavailable' : groups === null ? 'Loading…' : undefined}>Changes in this chat</WorkspaceContextRow>{notice}</div>
  return <div className="space-y-0.5">
    {notice}
    {groups.map(group => {
      if (group.kind !== 'repo') {
        return <WorkspaceContextRow key="other" icon={FileDiff} onClick={() => onOpen(true)} detail={`${group.total_files} changed`}>Other files</WorkspaceContextRow>
      }
      const expanded = open === group.root
      const uncommitted = group.uncommitted ?? []
      return <div key={group.root}>
        <WorkspaceContextRow icon={GitBranch} active={expanded} attention={uncommitted.length > 0} hint={group.branch} onClick={() => setOpen(expanded ? null : group.root)} detail={repoSummary(group)}>{group.name}</WorkspaceContextRow>
        {expanded && <div className="ml-2.5 space-y-3 border-l border-border py-2 pl-3 text-xs">
          {group.branch && <p className="text-muted-foreground">Branch {group.branch}</p>}
          <section aria-label="Not committed" className="space-y-1">
            <div className="flex items-center gap-2"><span className="font-medium">Not committed</span><span className="flex-1" />
              {uncommitted.length > 0 && <button type="button" onClick={() => onOpen()} className="rounded-md border border-border px-2 py-0.5 hover:bg-muted">Review</button>}</div>
            {uncommitted.length ? <ul className="space-y-0.5 font-mono">{uncommitted.map(file => <li key={file.path} className="flex gap-2">
              <span className={file.status === 'A' ? 'text-diff-add' : file.status === 'D' ? 'text-diff-del' : 'text-amber-500'}>{file.status}</span>
              <span className="min-w-0 flex-1 truncate" title={file.path}>{file.path.split('/').slice(-2).join('/')}</span>
              {file.added !== undefined && <span className="text-diff-add">+{file.added}</span>}{file.removed !== undefined && <span className="text-diff-del">−{file.removed}</span>}
            </li>)}</ul> : <p className="text-muted-foreground">Nothing. Everything is committed.</p>}
          </section>
          <section aria-label="Work done in this chat" className="space-y-1">
            <span className="font-medium">Work done in this chat</span>
            {group.pr?.number && <p><a href={group.pr.url} target="_blank" rel="noopener noreferrer" className="underline">PR #{group.pr.number}</a> {group.pr.state}{group.pr.checks ? ` · checks ${group.pr.checks}` : ''}{group.pr.review ? ` · ${group.pr.review}` : ''}</p>}
            {canReview && <PRWatcherControls group={group} sessionId={sessionId} />}
            {group.commits?.length ? <ul className="space-y-0.5">{group.commits.map(commit => <li key={commit.sha} className="flex gap-2"><span className="font-mono text-muted-foreground">{commit.sha.slice(0, 7)}</span><span className="min-w-0 flex-1 truncate" title={commit.subject}>{commit.subject}</span><span className="text-muted-foreground">{commit.files} file{commit.files === 1 ? '' : 's'}</span></li>)}</ul>
              : <p className="text-muted-foreground">{group.commits_known ? 'No commits in this chat yet.' : 'Commits show from the next reply on.'}</p>}
          </section>
        </div>}
      </div>
    })}
  </div>
}
