import { create } from 'zustand'
import { workspaceGitApi, type GitChangedFile, type GitRepo } from '../services/workspaceGit'

/** A file or history view shown in the Files pane's right column. */
export type GitPanel =
  | { kind: 'diff'; repo: string; file: string }
  | { kind: 'history'; repo: string; file: string }

export interface GitDecoration {
  status: GitChangedFile['status']
  staged?: boolean
}

interface WorkspaceGitState {
  workspacePath: string | null
  repos: GitRepo[]
  /** Full workspace path of a changed file -> its status. */
  fileStatus: Map<string, GitDecoration>
  /** Folders (full workspace paths) that contain a changed file, with the most urgent status inside. */
  changedDirs: Map<string, GitDecoration['status']>
  panel: GitPanel | null
  refresh: (workspacePath: string) => Promise<void>
  openPanel: (panel: GitPanel | null) => void
  clear: () => void
}

// Which change a folder shows: red (conflict, deleted) beats amber (modified,
// renamed) beats green (added, untracked), as in VS Code.
const DIR_PRIORITY: Record<GitDecoration['status'], number> = { conflict: 4, deleted: 3, modified: 2, renamed: 2, added: 1, untracked: 1 }

const trim = (value: string) => value.replace(/^\/+|\/+$/g, '')

/** Full workspace path of a repo file, in the form the file tree uses. */
export function gitFullPath(workspacePath: string, repoRoot: string, file: string): string {
  return [trim(workspacePath), trim(repoRoot), file].filter(Boolean).join('/')
}

/** The repo (and file inside it) that a full workspace file path belongs to. */
export function repoForPath(workspacePath: string, repos: GitRepo[], fullPath: string): { repo: GitRepo; file: string } | null {
  const base = trim(workspacePath)
  const path = trim(fullPath)
  let best: { repo: GitRepo; file: string } | null = null
  for (const repo of repos) {
    const prefix = [base, trim(repo.root)].filter(Boolean).join('/')
    if (path.startsWith(`${prefix}/`) && (!best || repo.root.length > best.repo.root.length)) {
      best = { repo, file: path.slice(prefix.length + 1) }
    }
  }
  return best
}

let refreshSequence = 0

export const useWorkspaceGitStore = create<WorkspaceGitState>((set, get) => ({
  workspacePath: null,
  repos: [],
  fileStatus: new Map(),
  changedDirs: new Map(),
  panel: null,
  refresh: async (workspacePath) => {
    const sequence = ++refreshSequence
    try {
      const repos = await workspaceGitApi.status(workspacePath)
      if (sequence !== refreshSequence) return
      const fileStatus = new Map<string, GitDecoration>()
      const changedDirs = new Map<string, GitDecoration['status']>()
      for (const repo of repos) {
        for (const file of repo.files) {
          const full = gitFullPath(workspacePath, repo.root, file.path)
          fileStatus.set(full, { status: file.status, staged: file.staged })
          const parts = full.split('/')
          for (let i = 1; i < parts.length; i++) {
            const dir = parts.slice(0, i).join('/')
            const current = changedDirs.get(dir)
            if (!current || DIR_PRIORITY[file.status] > DIR_PRIORITY[current]) changedDirs.set(dir, file.status)
          }
        }
      }
      set({ workspacePath, repos, fileStatus, changedDirs })
    } catch {
      // Git view is optional: a failed status just shows no decorations.
      if (sequence === refreshSequence && get().workspacePath !== workspacePath) {
        set({ workspacePath, repos: [], fileStatus: new Map(), changedDirs: new Map() })
      }
    }
  },
  openPanel: (panel) => set({ panel }),
  clear: () => {
    refreshSequence++
    set({ workspacePath: null, repos: [], fileStatus: new Map(), changedDirs: new Map(), panel: null })
  },
}))
