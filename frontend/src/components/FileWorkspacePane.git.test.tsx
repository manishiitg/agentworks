// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('../utils/openWorkspaceFile', () => ({ openWorkspaceFile: vi.fn() }))
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
    ] }]),
    act: vi.fn(async () => null),
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
})
