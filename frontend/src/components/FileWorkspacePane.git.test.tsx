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
    status: vi.fn(async () => [{ root: 'app', branch: 'main', ahead: 2, behind: 0, upstream: 'origin/main', files: [{ path: 'a.ts', status: 'modified' }] }]),
    diff: vi.fn(async () => ({ diff: '', truncated: false })),
    log: vi.fn(async () => []),
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
      expect(host.textContent).toContain('Changes (1)')
      // The Changes toggle swaps the tree for the changed-file list.
      const button = Array.from(host.querySelectorAll('button')).find(item => item.textContent?.startsWith('Changes'))!
      await act(async () => button.click())
      expect(host.querySelector('[aria-label="Changes"]')?.textContent).toContain('a.ts')
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
