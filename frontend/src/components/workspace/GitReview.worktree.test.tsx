// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
vi.mock('./ReplyFileViewer', () => ({ INLINE_REVIEW_FILE_LIMIT: 50, ReplyFileViewer: () => null }))
vi.mock('../workflow/WorkspaceViewHeader', () => ({ WorkspaceViewHeader: () => null }))
const { branchCompare } = vi.hoisted(() => ({ branchCompare: vi.fn(async () => ({ files: [], base: 'main', base_sha: 'old', head_sha: 'new' })) }))
vi.mock('../../services/workspaceGit', () => ({ workspaceGitApi: { branchCompare, branches: vi.fn(async () => [{name:'main'}, {name:'web-base'}]) } }))
import { GitReview } from './GitReview'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
it('opens the clicked worktree rather than the first or every repository', async () => {
  const host = document.createElement('div'); const root = createRoot(host)
  const repos = [
    {root:'api', branch:'fix/api', ahead:0, behind:0, files:[{path:'api.ts', status:'modified' as const}]},
    {root:'web', branch:'fix/web', ahead:0, behind:0, files:[{path:'web.ts', status:'modified' as const}]},
  ]
  try {
    await act(async () => root.render(<GitReview workspacePath="Crew/example" sessionId="chat-a" tabId="tab" repos={repos} recentRepos={[]} initialRepoRoot="web" onRefresh={vi.fn()} onBrowse={vi.fn()} renderChat={() => null} />))
    expect(host.textContent).toContain('web.ts')
    expect(host.textContent).not.toContain('api.ts')
    const worktree = host.querySelector('select')!
    await act(async () => { worktree.value = '*'; worktree.dispatchEvent(new Event('change', {bubbles:true})) })
    expect(host.textContent).toContain('api.ts')
    expect(host.textContent).toContain('web.ts')
    const tab = (label: string) => Array.from(host.querySelectorAll('button')).find(button => button.textContent === label)!
    await act(async () => tab('Branch / PR').click())
    const branchPicker = host.querySelector<HTMLSelectElement>('[aria-label="Worktree"]')!
    await act(async () => { branchPicker.value = 'web'; branchPicker.dispatchEvent(new Event('change', {bubbles:true})) })
    const base = host.querySelector<HTMLSelectElement>('[aria-label="Compare with"]')!
    await act(async () => { base.value = 'web-base'; base.dispatchEvent(new Event('change', {bubbles:true})) })
    await act(async () => tab('Uncommitted').click())
    const picker = host.querySelector<HTMLSelectElement>('[aria-label="Worktree"]')!
    await act(async () => { picker.value = 'repo:api'; picker.dispatchEvent(new Event('change', {bubbles:true})) })
    await act(async () => tab('Branch / PR').click())
    expect(branchCompare).toHaveBeenLastCalledWith('Crew/example', 'api', '')
  } finally { await act(async () => root.unmount()) }
})
