import { useEffect, useRef, useState } from 'react'
import { ChevronRight, FileDiff, GitBranch } from 'lucide-react'
import { useChatStore } from '../../stores/useChatStore'
import { workspaceGitApi, type GitRepo } from '../../services/workspaceGit'
import { WorkspaceContextRow } from './WorkspaceContextCard'
import { useReviewRevision } from './chatFileReviewData'

type Props = { workspacePath: string; sessionId: string; onOpen: (repoRoot?: string) => void }

/** Current project Git state, independent of chat history and manual reviews. */
export function ChatChangesRow({ workspacePath, sessionId, onOpen }: Props) {
  const host = useRef<HTMLDivElement>(null)
  const [repos, setRepos] = useState<GitRepo[] | null>(null)
  const [error, setError] = useState('')
  const [revision, refresh] = useReviewRevision()
  const replies = useChatStore(state => Object.values(state.tabEvents).flat().filter(event => event.session_id === sessionId && event.type === 'reply_file_changes').map(event => event.id).join(','))
  useEffect(() => {
    let live = true
    setRepos(null); setError('')
    const load = () => {
      const box = host.current?.getBoundingClientRect()
      if (document.visibilityState === 'hidden' || !host.current?.getClientRects().length || (box && (box.bottom < 0 || box.top > window.innerHeight))) return
      void workspaceGitApi.status(workspacePath, sessionId).then(found => { if (live) { setRepos(found); setError('') } }).catch(() => { if (live) setError('Could not load Git changes.') }) }
    // Initial lookup also runs in test/embedded surfaces without layout.
    void workspaceGitApi.status(workspacePath, sessionId).then(found => { if (live) setRepos(found) }).catch(() => { if (live) setError('Could not load Git changes.') })
    const timer = window.setInterval(load, 15_000)
    window.addEventListener('focus', load)
    document.addEventListener('visibilitychange', load)
    return () => { live = false; window.clearInterval(timer); window.removeEventListener('focus', load); document.removeEventListener('visibilitychange', load) }
  }, [workspacePath, sessionId, replies, revision])
  if (!error && (!repos || repos.length === 0)) return <div ref={host} />
  const used = repos?.filter(repo => repo.used_in_chat) ?? []
  const others = repos?.filter(repo => !repo.used_in_chat) ?? []
  const repoRow = (repo: GitRepo) => <button type="button" key={repo.root} onClick={() => onOpen(repo.root)} title={`${repo.root || workspacePath} · ${repo.branch}`}
    className="flex w-full min-w-0 items-center gap-2 rounded-lg px-2 py-2 text-left hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
    <GitBranch className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
    <span className="min-w-0 flex-1"><span className="block truncate text-sm">{repo.root?.split('/').pop() || workspacePath.split('/').pop() || 'Project'}</span>
      <span className="block truncate text-xs text-muted-foreground">{repo.branch || 'No commits yet'}</span></span>
    <span className="shrink-0 text-xs text-muted-foreground">{repo.files.length ? `${repo.files.length}${repo.truncated ? '+' : ''} changed` : 'Clean'}</span>
  </button>
  const files = repos?.reduce((total, repo) => total + repo.files.length, 0) ?? 0
  return <div ref={host}>
    <WorkspaceContextRow icon={FileDiff} hint="Current staged, unstaged and new files in this project's Git repositories. Committing clears these changes; Branch / PR shows committed work." onClick={() => onOpen()} detail={error ? 'Unavailable' : `${files}${repos?.some(repo => repo.truncated) ? '+' : ''} file${files === 1 ? '' : 's'}`}>Changes</WorkspaceContextRow>
    {used.length > 0 && <div className="mt-1"><p className="px-2 py-1 text-xs text-muted-foreground">Used in this chat</p>{used.map(repoRow)}</div>}
    {others.length > 0 && <details className="mt-1 group/other"><summary className="flex cursor-pointer list-none items-center gap-1 px-2 py-2 text-xs text-muted-foreground"><ChevronRight className="h-3 w-3 group-open/other:rotate-90" aria-hidden="true" />Other worktrees <span className="ml-auto">{others.length}</span></summary>{others.map(repoRow)}</details>}
    {error && <p role="alert" className="px-1 text-xs text-muted-foreground">{error} <button type="button" className="underline" onClick={refresh}>Retry</button></p>}
  </div>
}
