// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { GitChangesList } from './GitPanels'
import { FileGitContext } from './FileGitContext'
import { createWorkspaceGitStore } from '../../stores/useWorkspaceGitStore'
import { workspaceGitApi, type GitRepo } from '../../services/workspaceGit'

const identity = vi.hoisted(() => ({ viewer: 'person-a', server: 'server-a' }))
vi.mock('../../stores/useAuthStore', async importOriginal => {
  const actual = await importOriginal<typeof import('../../stores/useAuthStore')>()
  return { ...actual, useAuthStore: Object.assign((select: (state: unknown) => unknown) => select({ user: { id: identity.viewer } }), actual.useAuthStore) }
})
vi.mock('../../stores/useWorkspaceConnectionStore', async importOriginal => {
  const actual = await importOriginal<typeof import('../../stores/useWorkspaceConnectionStore')>()
  return { ...actual, useWorkspaceConnectionStore: Object.assign((select: (state: unknown) => unknown) => select({ activeWorkspaceId: identity.server }), actual.useWorkspaceConnectionStore) }
})

const cleanups: Array<() => void> = []
afterEach(() => {
  cleanups.splice(0).forEach(cleanup => cleanup())
  vi.restoreAllMocks(); vi.unstubAllGlobals()
  window.localStorage.clear()
  identity.viewer = 'person-a'; identity.server = 'server-a'
})

const clean: GitRepo = { root: 'docs', branch: 'main', ahead: 2, behind: 1, files: [] }
const changed: GitRepo = { root: 'app', branch: 'feature', ahead: 0, behind: 0, files: [{ path: 'report.txt', status: 'modified', worktree_status: 'modified' }] }
async function mount() {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const api = { ...workspaceGitApi, log: vi.fn().mockResolvedValue([]), stashes: vi.fn().mockResolvedValue([]), status: vi.fn().mockResolvedValue([clean, changed]), act: vi.fn() }
  const store = createWorkspaceGitStore(api)
  store.setState({ repos: [clean, changed] })
  const host = document.createElement('div'); document.body.appendChild(host)
  const root = createRoot(host)
  const render = async (path = 'crew-one', key = 'view') => {
    await act(async () => root.render(<FileGitContext.Provider value={{ store, api, openFile: vi.fn(), readFile: vi.fn() }}><GitChangesList key={key} workspacePath={path} /></FileGitContext.Provider>))
  }
  await render()
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  const header = (name: string) => host.querySelector<HTMLButtonElement>(`button[aria-label="Repository ${name}"]`)!
  return { host, render, header, api, store }
}

// PLAT-746-158 B5: large multi-repository views should start compact while a
// viewer's explicit choice survives status refresh, remount and repo order.
it('folds clean repositories, preserves explicit choices, and scopes them to the viewer and workspace', async () => {
  const { host, render, header, api, store } = await mount()
  expect(header('docs').getAttribute('aria-expanded')).toBe('false')
  expect(header('app').getAttribute('aria-expanded')).toBe('true')
  expect(header('docs').closest('section')!.textContent).toContain('clean')
  expect(header('docs').closest('section')!.textContent).toContain('main')
  expect(host.querySelectorAll('textarea')).toHaveLength(1)
  expect(api.log.mock.calls.map(call => call[1])).toEqual(['app'])
  await act(async () => header('docs').click())
  await act(async () => header('app').click())
  await act(async () => store.setState({ repos: [changed, { ...clean, branch: 'release' }] }))
  expect(header('docs').getAttribute('aria-expanded')).toBe('true')
  expect(header('app').getAttribute('aria-expanded')).toBe('false')
  expect(header('docs').closest('section')!.textContent).toContain('release')
  await render('crew-one', 'remount')
  expect(header('docs').getAttribute('aria-expanded')).toBe('true')
  expect(header('app').getAttribute('aria-expanded')).toBe('false')
  await render('crew-two')
  expect(header('docs').getAttribute('aria-expanded')).toBe('false')
  identity.viewer = 'person-b'
  await render()
  expect(header('docs').getAttribute('aria-expanded')).toBe('false')
  identity.viewer = 'person-a'; identity.server = 'server-b'
  await render()
  expect(header('docs').getAttribute('aria-expanded')).toBe('false')
  expect(api.act).not.toHaveBeenCalled()
})

it('keeps folding usable when browser preference storage is unavailable', async () => {
  vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('Storage unavailable') })
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('Storage unavailable') })
  const { header } = await mount()
  expect(header('docs').getAttribute('aria-expanded')).toBe('false')
  await act(async () => header('docs').click())
  expect(header('docs').getAttribute('aria-expanded')).toBe('true')
})
