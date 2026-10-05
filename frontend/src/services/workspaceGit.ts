import api from './api'

export type GitFileStatus = 'modified' | 'added' | 'deleted' | 'renamed' | 'untracked' | 'conflict'

export interface GitChangedFile {
  path: string
  /** The change shown in the tree: the unstaged one, else the staged one. */
  status: GitFileStatus
  staged?: boolean
  /** The change staged for the next commit (absent when none). */
  index_status?: GitFileStatus
  /** The change not yet staged, "untracked" for a new file (absent when none). */
  worktree_status?: GitFileStatus
}

export interface GitRepo {
  /** Repo folder relative to the workspace ("" for the workspace itself). */
  root: string
  branch: string
  detached?: boolean
  upstream?: string
  ahead: number
  behind: number
  files: GitChangedFile[]
  truncated?: boolean
}

export interface GitCommit {
  hash: string
  author: string
  date: string
  subject: string
  /** Branches and tags at this commit, e.g. "HEAD -> main", "origin/main". */
  refs?: string[]
}

const ENDPOINT = '/api/workspace-git'

export interface GitBranch {
  name: string
  current?: boolean
  upstream?: string
  /** A remote-tracking branch with no local copy yet. */
  remote?: boolean
}

export interface GitStash {
  ref: string
  message: string
  date: string
}

export interface GitBlameLine {
  line: number
  hash: string
  author: string
  /** Unix seconds. */
  time: number
  summary: string
  uncommitted?: boolean
}

export type GitAction =
  | { op: 'pull' | 'push' }
  | { op: 'stage' | 'unstage' | 'discard'; files: string[] }
  | { op: 'stage' | 'unstage'; all: true }
  | { op: 'commit'; message: string; all?: boolean }
  | { op: 'checkout'; branch: string; remote?: boolean }
  | { op: 'create_branch' | 'delete_branch'; branch: string }
  | { op: 'stash'; message?: string }
  | { op: 'stash_apply' | 'stash_pop' | 'stash_drop'; ref: string }
  | { op: 'resolve'; files: [string]; choice: 'ours' | 'theirs' | 'both' }

/** The server's own sentence for a failed action (permission, nothing staged, filters, lock...). */
export function gitActionError(error: unknown): string {
  const data = (error as { response?: { data?: { error?: string | {message?: string} } } })?.response?.data
  return typeof data?.error === 'string' ? data.error : data?.error?.message || 'Git could not do that.'
}

export const workspaceGitApi = {
  /** Stage, unstage, discard or commit in one repo; resolves with the repo's new state. */
  async act(workspacePath: string, repo: string, action: GitAction): Promise<GitRepo | null> {
    const response = await api.post(ENDPOINT, { workspace_path: workspacePath, repo, ...action })
    return (response.data?.repo as GitRepo | undefined) ?? null
  },
  async status(workspacePath: string): Promise<GitRepo[]> {
    const response = await api.get(ENDPOINT, { params: { workspace_path: workspacePath, op: 'status' } })
    return (response.data?.repos as GitRepo[] | undefined) ?? []
  },
  async diff(workspacePath: string, repo: string, file: string): Promise<{ diff: string; truncated: boolean }> {
    const response = await api.get(ENDPOINT, { params: { workspace_path: workspacePath, op: 'diff', repo, file } })
    return { diff: String(response.data?.diff ?? ''), truncated: !!response.data?.truncated }
  },
  async log(workspacePath: string, repo: string, file?: string): Promise<GitCommit[]> {
    const response = await api.get(ENDPOINT, { params: { workspace_path: workspacePath, op: 'log', repo, file } })
    return (response.data?.commits as GitCommit[] | undefined) ?? []
  },
  async branches(workspacePath: string, repo: string): Promise<GitBranch[]> {
    const response = await api.get(ENDPOINT, { params: { workspace_path: workspacePath, op: 'branches', repo } })
    return (response.data?.branches as GitBranch[] | undefined) ?? []
  },
  async stashes(workspacePath: string, repo: string): Promise<GitStash[]> {
    const response = await api.get(ENDPOINT, { params: { workspace_path: workspacePath, op: 'stashes', repo } })
    return (response.data?.stashes as GitStash[] | undefined) ?? []
  },
  async blame(workspacePath: string, repo: string, file: string): Promise<{ lines: GitBlameLine[]; truncated: boolean }> {
    const response = await api.get(ENDPOINT, { params: { workspace_path: workspacePath, op: 'blame', repo, file } })
    return { lines: (response.data?.lines as GitBlameLine[] | undefined) ?? [], truncated: !!response.data?.truncated }
  },
  async show(workspacePath: string, repo: string, file: string, commit: string): Promise<{ diff: string; truncated: boolean }> {
    const response = await api.get(ENDPOINT, { params: { workspace_path: workspacePath, op: 'show', repo, file, commit } })
    return { diff: String(response.data?.diff ?? ''), truncated: !!response.data?.truncated }
  },
}
