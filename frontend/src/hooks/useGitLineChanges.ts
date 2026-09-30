import { useEffect, useState } from 'react'
import { workspaceGitApi } from '../services/workspaceGit'
import { repoForPath, useWorkspaceGitStore } from '../stores/useWorkspaceGitStore'
import { allLinesAdded, parseGitDiffLines, type GitLineChanges } from '../utils/gitDiffLines'

/** The changed lines of the open file against HEAD, when it sits in a git repo and has changes. */
export function useGitLineChanges(filePath: string | undefined, content: string): GitLineChanges | undefined {
  const workspacePath = useWorkspaceGitStore(state => state.workspacePath)
  const repos = useWorkspaceGitStore(state => state.repos)
  const [changes, setChanges] = useState<GitLineChanges | undefined>()
  const found = filePath && workspacePath ? repoForPath(workspacePath, repos, filePath) : null
  const entry = found?.repo.files.find(file => file.path === found.file)
  const status = entry?.status
  const repoRoot = found?.repo.root
  const file = found?.file

  useEffect(() => {
    let cancelled = false
    if (!workspacePath || repoRoot === undefined || !file || !status) {
      setChanges(undefined)
      return
    }
    if (status === 'untracked') {
      setChanges(allLinesAdded(content))
      return
    }
    workspaceGitApi.diff(workspacePath, repoRoot, file).then(
      result => { if (!cancelled) setChanges(parseGitDiffLines(result.diff)) },
      () => { if (!cancelled) setChanges(undefined) },
    )
    return () => { cancelled = true }
  }, [workspacePath, repoRoot, file, status, content])

  return changes
}
