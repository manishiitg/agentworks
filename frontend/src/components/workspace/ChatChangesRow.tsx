import { useEffect, useRef, useState } from 'react'
import { FileDiff } from 'lucide-react'
import { useChatStore } from '../../stores/useChatStore'
import { workspaceGitApi, type GitRepo } from '../../services/workspaceGit'
import { WorkspaceContextRow } from './WorkspaceContextCard'
import { useReviewRevision } from './chatFileReviewData'

type Props = { workspacePath: string; sessionId: string; onOpen: () => void }

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
      void workspaceGitApi.status(workspacePath).then(found => { if (live) { setRepos(found); setError('') } }).catch(() => { if (live) setError('Could not load Git changes.') }) }
    // Initial lookup also runs in test/embedded surfaces without layout.
    void workspaceGitApi.status(workspacePath).then(found => { if (live) setRepos(found) }).catch(() => { if (live) setError('Could not load Git changes.') })
    const timer = window.setInterval(load, 15_000)
    window.addEventListener('focus', load)
    document.addEventListener('visibilitychange', load)
    return () => { live = false; window.clearInterval(timer); window.removeEventListener('focus', load); document.removeEventListener('visibilitychange', load) }
  }, [workspacePath, sessionId, replies, revision])
  if (!error && (!repos || repos.length === 0)) return <div ref={host} />
  const files = repos?.reduce((total, repo) => total + repo.files.length, 0) ?? 0
  return <div ref={host}>
    <WorkspaceContextRow icon={FileDiff} hint="Current staged, unstaged and new files in this project's Git repositories. Committing clears these changes; Branch / PR shows committed work." onClick={onOpen} detail={error ? 'Unavailable' : `${files}${repos?.some(repo => repo.truncated) ? '+' : ''} file${files === 1 ? '' : 's'}`}>Changes</WorkspaceContextRow>
    {error && <p role="alert" className="px-1 text-xs text-muted-foreground">{error} <button type="button" className="underline" onClick={refresh}>Retry</button></p>}
  </div>
}
