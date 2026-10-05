import { useEffect, useState } from 'react'
import { useFileGit, useFileGitStore } from '../components/workspace/FileGitContext'
import { repoForPath } from '../stores/useWorkspaceGitStore'
import { allLinesAdded, parseGitDiffLines, type GitLineChanges } from '../utils/gitDiffLines'

/** The changed lines of the open file against HEAD, when it sits in a git repo and has changes. */
export function useGitLineChanges(filePath: string | undefined, content: string): GitLineChanges | undefined {
  const git = useFileGit()
  const workspacePath = useFileGitStore(state => state.workspacePath)
  const repos = useFileGitStore(state => state.repos)
  const [changes, setChanges] = useState<GitLineChanges | undefined>()
  const found = filePath && workspacePath !== null ? repoForPath(workspacePath, repos, filePath) : null
  const entry = found?.repo.files.find(file => file.path === found.file)
  const status = entry?.status
  const repoRoot = found?.repo.root
  const file = found?.file

  useEffect(() => {
    let cancelled = false
    if (workspacePath === null || repoRoot === undefined || !file || !status) {
      setChanges(undefined)
      return
    }
    if (status === 'untracked') {
      setChanges(allLinesAdded(content))
      return
    }
    git.api.diff(workspacePath, repoRoot, file).then(
      result => { if (!cancelled) setChanges(parseGitDiffLines(result.diff)) },
      () => { if (!cancelled) setChanges(undefined) },
    )
    return () => { cancelled = true }
  }, [workspacePath, repoRoot, file, status, content, git.api])

  return changes
}
