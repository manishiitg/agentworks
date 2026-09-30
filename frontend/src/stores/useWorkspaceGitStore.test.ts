import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../services/workspaceGit', () => ({ workspaceGitApi: { status: vi.fn() } }))
import { workspaceGitApi } from '../services/workspaceGit'
import { gitFullPath, repoForPath, useWorkspaceGitStore } from './useWorkspaceGitStore'

const repo = (root: string, files: Array<{ path: string; status: 'modified' | 'untracked' }>) => ({
  root, branch: 'main', ahead: 0, behind: 0, files,
})

beforeEach(() => useWorkspaceGitStore.getState().clear())

describe('workspace git store', () => {
  it('joins the workspace, repo and file into the path the tree uses', () => {
    expect(gitFullPath('/Chats/Code/projects/p1/', 'app', 'src/a.ts')).toBe('Chats/Code/projects/p1/app/src/a.ts')
    expect(gitFullPath('Chats/Code/projects/p1', '', 'a.ts')).toBe('Chats/Code/projects/p1/a.ts')
  })

  it('marks changed files and every folder above them', async () => {
    vi.mocked(workspaceGitApi.status).mockResolvedValue([repo('app', [{ path: 'src/a.ts', status: 'modified' }, { path: 'new.txt', status: 'untracked' }])])
    await useWorkspaceGitStore.getState().refresh('Chats/Code/projects/p1')
    const state = useWorkspaceGitStore.getState()
    expect(state.fileStatus.get('Chats/Code/projects/p1/app/src/a.ts')?.status).toBe('modified')
    expect(state.fileStatus.get('Chats/Code/projects/p1/app/new.txt')?.status).toBe('untracked')
    for (const dir of ['Chats', 'Chats/Code/projects/p1', 'Chats/Code/projects/p1/app']) {
      expect(state.changedDirs.get(dir)).toBe('modified') // amber beats the green untracked file
    }
    expect(state.changedDirs.get('Chats/Code/projects/p1/app/src')).toBe('modified')
    expect(state.changedDirs.has('Chats/Code/projects/p1/app/src/a.ts')).toBe(false)
  })

  it('finds the repo a path belongs to, preferring the deepest one', () => {
    const repos = [repo('', []), repo('app', [])]
    expect(repoForPath('Chats/Code/projects/p1', repos, 'Chats/Code/projects/p1/app/src/a.ts')).toMatchObject({ file: 'src/a.ts', repo: { root: 'app' } })
    expect(repoForPath('Chats/Code/projects/p1', repos, 'Chats/Code/projects/p1/readme.md')).toMatchObject({ file: 'readme.md', repo: { root: '' } })
    expect(repoForPath('Chats/Code/projects/p1', [repo('app', [])], 'Chats/Code/projects/p1/other/a.ts')).toBeNull()
  })

  it('keeps the newest status when refreshes overlap', async () => {
    let resolveFirst: (value: ReturnType<typeof repo>[]) => void = () => undefined
    vi.mocked(workspaceGitApi.status)
      .mockImplementationOnce(() => new Promise(resolve => { resolveFirst = resolve }))
      .mockResolvedValueOnce([repo('app', [{ path: 'b.ts', status: 'modified' }])])
    const first = useWorkspaceGitStore.getState().refresh('w')
    await useWorkspaceGitStore.getState().refresh('w')
    resolveFirst([repo('app', [{ path: 'old.ts', status: 'modified' }])])
    await first
    const paths = [...useWorkspaceGitStore.getState().fileStatus.keys()]
    expect(paths).toEqual(['w/app/b.ts'])
  })
})
