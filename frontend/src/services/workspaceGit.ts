import api from './api'

export type GitFileStatus = 'modified' | 'added' | 'deleted' | 'renamed' | 'untracked' | 'conflict'

export interface GitChangedFile {
  path: string
  status: GitFileStatus
  staged?: boolean
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
}

const ENDPOINT = '/api/workspace-git'

export const workspaceGitApi = {
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
  async show(workspacePath: string, repo: string, file: string, commit: string): Promise<{ diff: string; truncated: boolean }> {
    const response = await api.get(ENDPOINT, { params: { workspace_path: workspacePath, op: 'show', repo, file, commit } })
    return { diff: String(response.data?.diff ?? ''), truncated: !!response.data?.truncated }
  },
}
