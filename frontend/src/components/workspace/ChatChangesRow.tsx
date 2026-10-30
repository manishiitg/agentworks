import { useEffect, useState } from 'react'
import { FileDiff } from 'lucide-react'
import api from '../../services/api'
import { workspaceGitApi, type GitRepo } from '../../services/workspaceGit'
import { useChatStore } from '../../stores/useChatStore'
import { WorkspaceContextRow } from './WorkspaceContextCard'
import { reviewEndpoint, useReviewRevision } from './chatFileReviewData'

export type ChangesReviewScope = 'uncommitted' | 'outside-git'

/** Current workspace Git state, shared by every chat using these worktrees. */
export function ChatChangesRow({ workspacePath, sessionId, onOpen }: { workspacePath: string; sessionId: string; onOpen: (scope: ChangesReviewScope) => void }) {
  const [repos, setRepos] = useState<GitRepo[] | null>(null)
  const [otherFiles, setOtherFiles] = useState(0)
  const [error, setError] = useState('')
  const [retry, setRetry] = useState(0)
  const [revision] = useReviewRevision()
  const savedEvents = useChatStore(state => (state.tabEvents[sessionId] ?? []).filter(event => event.type === 'reply_file_changes' || event.type === 'file_reviewed').map(event => event.id).join(','))
  useEffect(() => { setRepos(null); setOtherFiles(0); setError('') }, [workspacePath, sessionId])
  useEffect(() => {
    const controller = new AbortController()
    // The compact count and its destination both use current Git status,
    // never the saved chat comparison or inferred commit authorship.
    void workspaceGitApi.status(workspacePath)
      .then(found => { if (!controller.signal.aborted) { setRepos(found); setError('') } })
      .catch(() => { if (!controller.signal.aborted) setError('Could not load changes.') })
    void api.get<{ groups?: { kind: string; total_files: number }[] }>(reviewEndpoint, { params: { workspace_path: workspacePath, session_id: sessionId, op: 'chat_status' }, signal: controller.signal })
      .then(({ data }) => { if (!controller.signal.aborted) setOtherFiles((data.groups ?? []).filter(group => group.kind === 'crew_files').reduce((sum, group) => sum + group.total_files, 0)) })
      .catch(() => { /* Git remains usable independently of saved comparisons. */ })
    return () => controller.abort()
  }, [workspacePath, sessionId, revision, savedEvents, retry])
  const count = repos?.reduce((sum, repo) => sum + repo.files.length, 0) ?? 0
  const partial = repos?.some(repo => repo.truncated)
  const detail = error ? 'Unavailable' : repos === null ? 'Loading…' : `${partial ? 'At least ' : ''}${count} file${count === 1 ? '' : 's'}`
  return <div className="space-y-0.5">
    {(repos === null || repos.length > 0 || !otherFiles) && <WorkspaceContextRow icon={FileDiff} onClick={() => onOpen('uncommitted')} detail={detail}>Changes</WorkspaceContextRow>}
    {otherFiles > 0 && <WorkspaceContextRow icon={FileDiff} onClick={() => onOpen('outside-git')} detail={`${otherFiles} changed`}>Other files</WorkspaceContextRow>}
    {error && <p role="alert" className="px-1 text-xs text-muted-foreground">{error} <button type="button" className="underline hover:text-foreground" onClick={() => setRetry(value => value + 1)}>Retry</button></p>}
  </div>
}
