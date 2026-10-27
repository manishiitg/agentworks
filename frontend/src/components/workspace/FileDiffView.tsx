import { useEffect, useState } from 'react'
import api from '../../services/api'
import type { ReplyFileChangesRef } from '../../utils/replyFileChanges'
import { gitFullPath, repoForPath } from '../../stores/useWorkspaceGitStore'
import { useFileGit } from './FileGitContext'
import DiffRenderer from '../ui/DiffRenderer'

type RepositoryFile = { repo: string; file: string }
type Diff = { diff: string; truncated?: boolean }

/** Snapshot context is supplied only by its reply, never guessed from today's
 * Git status or the most recent chat. The repository reader remains unchanged. */
export function FileDiffView({ workspacePath, file, repositoryFile, reply }: {
  workspacePath: string
  file: string
  repositoryFile?: RepositoryFile
  reply?: ReplyFileChangesRef
}) {
  const git = useFileGit()
  const [scope, setScope] = useState<'reply' | 'uncommitted'>(reply ? 'reply' : 'uncommitted')
  const [discovered, setDiscovered] = useState<RepositoryFile | null>(null)
  const target = repositoryFile ?? discovered
  const selectedRepo = scope === 'uncommitted' ? target : null
  const [result, setResult] = useState<{ value?: Diff; error?: string }>({})

  useEffect(() => {
    if (repositoryFile || !reply) return
    let active = true
    void git.api.status(workspacePath).then(repos => {
      const match = repoForPath(workspacePath, repos, gitFullPath(workspacePath, '', file))
      if (active) setDiscovered(match ? { repo: match.repo.root, file: match.file } : null)
    }).catch(() => { if (active) setDiscovered(null) })
    return () => { active = false }
  }, [git.api, workspacePath, file, repositoryFile?.repo, repositoryFile?.file, reply?.snapshotId])

  useEffect(() => {
    let active = true
    setResult({})
    const load = async (): Promise<Diff> => {
      if (scope === 'reply' && reply) {
        const response = await api.get<Diff>('/api/workspace-snapshots', { params: {
          workspace_path: reply.workspacePath, snapshot_id: reply.snapshotId, op: 'snapshot_compare', file,
        } })
        return response.data
      }
      if (!selectedRepo) throw new Error('No repository for this file.')
      return git.api.diff(workspacePath, selectedRepo.repo, selectedRepo.file)
    }
    void load().then(value => { if (active) setResult({ value }) }).catch(() => {
      if (active) setResult({ error: scope === 'reply'
        ? 'This saved comparison is unavailable or you no longer have access.'
        : 'Could not load the current Git changes. Try opening the file again.' })
    })
    return () => { active = false }
  }, [git.api, workspacePath, file, reply?.workspacePath, reply?.snapshotId, selectedRepo?.repo, selectedRepo?.file, scope])

  return <section aria-label="File comparison">
    <div className="border-b border-border px-3 py-2 text-xs text-muted-foreground">
      {reply && target ? <label className="flex items-center gap-2">Compare
        <select aria-label="Compare files" value={scope} onChange={event => setScope(event.target.value as 'reply' | 'uncommitted')} className="rounded border border-border bg-background px-2 py-1 text-foreground">
          <option value="reply">This reply</option><option value="uncommitted">Uncommitted</option>
        </select>
      </label> : <span>{scope === 'reply' ? 'This reply' : 'Uncommitted'}</span>}
      <p className="mt-1">{scope === 'reply' ? 'Saved before and after this reply.' : 'Current tracked files against HEAD, including staged and unstaged changes.'}</p>
    </div>
    {result.error ? <p role="alert" className="p-4 text-sm text-destructive">{result.error}</p>
      : !result.value ? <p className="p-4 text-sm text-muted-foreground">Loading comparison…</p>
      : !result.value.diff.trim() ? <p className="p-4 text-sm text-muted-foreground">{scope === 'reply' ? 'No changes in this saved comparison.' : 'No changes against the last commit (a new untracked file has no diff yet).'}</p>
      : <div className="p-2"><DiffRenderer content={result.value.diff} maxHeightClassName="max-h-none" />{result.value.truncated && <p className="mt-2 text-xs text-muted-foreground">This comparison is large; only the start is shown.</p>}</div>}
  </section>
}
