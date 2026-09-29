// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('../utils/openWorkspaceFile', () => ({ openWorkspaceFile: vi.fn() }))
vi.mock('../services/api', () => ({ agentApi: { getPlannerFileContent: vi.fn(async () => ({ data: { content: 'line one\nline two' } })) } }))
vi.mock('../stores/useWorkspaceStore', () => {
  const state = { showFileContent: false, files: [], selectedFile: null }
  return { useWorkspaceStore: Object.assign((selector: (s: typeof state) => unknown) => selector(state), { getState: () => state }) }
})
vi.mock('./Workspace', () => ({ default: () => <div>tree</div> }))
vi.mock('./FileContentViewer', () => ({ FileContentViewerBody: () => <div>viewer</div> }))
vi.mock('../services/workspaceGit', () => ({
  workspaceGitApi: {
    status: vi.fn(async () => [{ root: 'app', branch: 'main', ahead: 2, behind: 0, upstream: 'origin/main', files: [
      { path: 'a.ts', status: 'modified', worktree_status: 'modified' },
      { path: 'staged.ts', status: 'added', staged: true, index_status: 'added' },
      { path: 'clash.ts', status: 'conflict', worktree_status: 'conflict' },
    ] }]),
    act: vi.fn(async () => null),
    branches: vi.fn(async () => [
      { name: 'main', current: true },
      { name: 'feature/x' },
      { name: 'origin/dev', remote: true },
    ]),
    stashes: vi.fn(async () => [{ ref: 'stash@{0}', message: 'On main: wip: two', date: '2026-09-29T10:00:00Z' }]),
    blame: vi.fn(async () => ({ lines: [
      { line: 1, hash: 'a'.repeat(40), author: 'Ann', time: 1790000000, summary: 'first commit' },
      { line: 2, hash: 'b'.repeat(40), author: 'Bob', time: 1790000100, summary: 'second commit' },
    ], truncated: false })),
    diff: vi.fn(async () => ({ diff: '', truncated: false })),
    log: vi.fn(async () => [
      { hash: 'aaaaaaa1', author: 'A', date: '2026-09-29T10:00:00Z', subject: 'newest commit', refs: ['HEAD -> main', 'origin/main'] },
      { hash: 'bbbbbbb2', author: 'A', date: '2026-09-28T10:00:00Z', subject: 'older commit', refs: [] },
    ]),
    show: vi.fn(async () => ({ diff: '', truncated: false })),
  },
}))
import { FileWorkspacePane } from './FileWorkspacePane'
import { useWorkspaceGitStore } from '../stores/useWorkspaceGitStore'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

afterEach(() => { useWorkspaceGitStore.getState().clear() })

describe('FileWorkspacePane git bar', () => {
  it('shows the branch, ahead count and Changes button once the folder holds a repo', async () => {
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<FileWorkspacePane workspacePath="Chats/Code/projects/p1" />))
      await act(async () => { await Promise.resolve() })
      expect(host.textContent).toContain('main')
      expect(host.querySelector('[aria-label="Source Control"]')).not.toBeNull()
      // The Changes toggle swaps the tree for the changed-file list.
      const button = Array.from(host.querySelectorAll('button')).find(item => item.getAttribute('aria-label') === 'Source Control')!
      await act(async () => button.click())
      const view = host.querySelector('[aria-label="Source control"]')!
      expect(view.textContent).toContain('Staged Changes')
      expect(view.textContent).toContain('staged.ts')
      expect(view.textContent).toContain('a.ts')
      expect(view.textContent).toContain('Commit 1 staged')
      // The Graph lists recent commits with their branch badges.
      expect(view.textContent).toContain('newest commit')
      expect(view.textContent).toContain('origin/main')

      // Commit needs a message; then it sends the commit action and refreshes.
      const { workspaceGitApi } = await import('../services/workspaceGit')
      const commitButton = Array.from(view.querySelectorAll('button')).find(item => item.textContent?.startsWith('Commit 1 staged'))!
      expect(commitButton.hasAttribute('disabled')).toBe(true)
      const box = view.querySelector('textarea')!
      const setValue = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!
      await act(async () => { setValue.call(box, 'add staged'); box.dispatchEvent(new Event('input', { bubbles: true })) })
      expect(commitButton.hasAttribute('disabled')).toBe(false)
      await act(async () => commitButton.click())
      expect(workspaceGitApi.act).toHaveBeenCalledWith('Chats/Code/projects/p1', 'app', { op: 'commit', message: 'add staged', all: false })
      // Stage all sends the stage action for the whole repo.
      await act(async () => (view.querySelector('button[aria-label="Stage all"]') as HTMLButtonElement).click())
      expect(workspaceGitApi.act).toHaveBeenCalledWith('Chats/Code/projects/p1', 'app', { op: 'stage', all: true })
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('shows no git bar when the folder has no repo', async () => {
    const { workspaceGitApi } = await import('../services/workspaceGit')
    vi.mocked(workspaceGitApi.status).mockResolvedValueOnce([])
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<FileWorkspacePane workspacePath="Chats/Code/projects/p2" />))
      await act(async () => { await Promise.resolve() })
      expect(host.textContent).not.toContain('Changes')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('switches branches, resolves conflicts and manages stashes from Source Control', async () => {
    const { workspaceGitApi } = await import('../services/workspaceGit')
    vi.mocked(workspaceGitApi.act).mockClear()
    const asked: string[] = []
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<FileWorkspacePane workspacePath="Chats/Code/projects/p1" onAsk={message => { asked.push(message) }} />))
      await act(async () => { await Promise.resolve() })
      const railButton = host.querySelector('[role="tab"][aria-label="Source Control"]') as HTMLButtonElement
      await act(async () => railButton.click())
      const view = host.querySelector('[aria-label="Source control"]')!

      // Conflicts are their own section, with one-click sides and an agent option.
      expect(view.textContent).toContain('Merge Conflicts')
      const accept = Array.from(view.querySelectorAll('button')).find(item => item.textContent === 'Accept Incoming')!
      await act(async () => accept.click())
      expect(workspaceGitApi.act).toHaveBeenCalledWith('Chats/Code/projects/p1', 'app', { op: 'resolve', files: ['clash.ts'], choice: 'theirs' })
      const agentButton = Array.from(view.querySelectorAll('button')).find(item => item.textContent === 'Agent')!
      await act(async () => agentButton.click())
      expect(asked.at(-1)).toContain('Resolve the merge conflict in `clash.ts`')

      // The branch name opens a switcher; a remote-only branch is tracked on switch.
      const branchButton = Array.from(view.querySelectorAll('button')).find(item => item.getAttribute('title') === 'Switch or create a branch')!
      await act(async () => branchButton.click())
      await act(async () => { await Promise.resolve() })
      const options = Array.from(host.querySelectorAll('[role="option"] button')).map(item => item.textContent)
      expect(options.join('|')).toContain('feature/x')
      const remote = Array.from(host.querySelectorAll('[role="option"] button')).find(item => item.textContent?.includes('origin/dev'))!
      await act(async () => (remote as HTMLButtonElement).click())
      expect(workspaceGitApi.act).toHaveBeenCalledWith('Chats/Code/projects/p1', 'app', { op: 'checkout', branch: 'origin/dev', remote: true })

      // Stashes list with apply/pop.
      const stashToggle = Array.from(view.querySelectorAll('button')).find(item => item.textContent?.startsWith('Stashes'))!
      await act(async () => stashToggle.click())
      await act(async () => { await Promise.resolve() })
      expect(view.textContent).toContain('wip: two')
      const pop = Array.from(view.querySelectorAll('button')).find(item => item.textContent === 'Pop')!
      await act(async () => pop.click())
      expect(workspaceGitApi.act).toHaveBeenCalledWith('Chats/Code/projects/p1', 'app', { op: 'stash_pop', ref: 'stash@{0}' })
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('shows blame by commit and offers the agent for the selected lines', async () => {
    const asked: string[] = []
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<FileWorkspacePane workspacePath="Chats/Code/projects/p1" onAsk={message => { asked.push(message) }} />))
      await act(async () => { await Promise.resolve() })
      useWorkspaceGitStore.getState().openPanel({ kind: 'blame', repo: 'app', file: 'a.ts' })
      await act(async () => { await Promise.resolve(); await Promise.resolve() })
      expect(host.textContent).toContain('Ann')
      expect(host.textContent).toContain('second commit')
      expect(host.textContent).toContain('line two')
      const group = Array.from(host.querySelectorAll('button')).find(item => item.textContent?.includes('Bob'))!
      await act(async () => group.click())
      const explain = Array.from(host.querySelectorAll('button')).find(item => item.textContent?.includes('Explain these lines'))!
      await act(async () => explain.click())
      expect(asked.at(-1)).toContain('lines 2-2')
      expect(asked.at(-1)).toContain('bbbbbbb')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })
})
