// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import api from '../../services/api'
import { GitChangesList } from './GitPanels'
import { invalidateChatIdentity } from '../../utils/chatIdentity'

const fixture = vi.hoisted(() => ({ actor: 'owner', network: 'local', act: vi.fn().mockResolvedValue({}), refresh: vi.fn().mockResolvedValue(undefined) }))
vi.mock('../../services/api', () => ({ default: { post: vi.fn(), get: vi.fn() } }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (s: unknown) => unknown) => select({ user: { id: fixture.actor } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ getWorkspaceScopedStorageKey: (key: string) => key, useWorkspaceConnectionStore: (select: (s: unknown) => unknown) => select({ activeWorkspaceId: fixture.network }) }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (select: (s: unknown) => unknown) => select({ activeTabId: 'chat', chatTabs: { chat: { sessionId: 'own-chat' } } }) }))
vi.mock('./FileGitContext', () => ({
  useFileGit: () => ({ delivery: true, api: { act: fixture.act, log: async () => [] } }),
  useFileGitStore: (select: (s: unknown) => unknown) => select({ repos: [{ root: 'app', branch: 'feature', ahead: 1, behind: 0, files: [{ path: 'a.ts', status: 'modified', index_status: 'modified' }] }], refresh: fixture.refresh }),
}))
vi.mock('./GitExtras', () => ({ BranchPicker: () => null, ConflictSection: () => null, StashSection: () => null, GitBlamePanel: () => null }))
vi.mock('../chat/PRWatcherControls', () => ({ PRWatcherControls: () => <span>Existing watch controls</span> }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// PLAT-746-1004: exercise the real existing commit box/push buttons and PR
// confirmation, with the server/AI boundary mocked, never a real publication.
it('drafts only editable text; committing, pushing and opening a PR need their own clicks', async () => {
  const host = document.createElement('div'), root = createRoot(host)
  document.body.appendChild(host)
  const show = vi.spyOn(HTMLDialogElement.prototype, 'showModal').mockImplementation(function (this: HTMLDialogElement) { this.open = true })
  vi.mocked(api.post).mockImplementation(async (_path, body) => {
    const request = body as { op: string }
    if (request.op === 'delivery_capabilities') return { data: { available: true, can_push: true, can_draft: true } } as never
    if (request.op === 'draft_commit') return { data: { message: 'Editable draft' } } as never
    if (request.op === 'prepare_pr' || request.op === 'draft_pr') return { data: { title: request.op === 'draft_pr' ? 'Drafted title' : '', body: 'Drafted body', base: 'main', head: 'head-1', branch: 'feature', repository: 'example/project' } } as never
    if (request.op === 'open_pr') return { data: { pr: { number: 42, url: 'https://github.com/example/project/pull/42', base: 'main' }, repository: 'example/project', branch: 'feature' } } as never
    throw Error('unexpected action')
  })
  const click = async (selector: string, source: ParentNode = host) => { await act(async () => (source.querySelector(selector) as HTMLButtonElement).click()) }
  try {
    await act(async () => root.render(<GitChangesList workspacePath="Crew/example" />))
    await click('[aria-label="Draft commit message"]')
    expect((host.querySelector('[aria-label="Commit message"]') as HTMLTextAreaElement).value).toBe('Editable draft')
    expect((host.querySelector('[aria-label="Commit message"]') as HTMLTextAreaElement).disabled).toBe(false)
    expect(fixture.act).not.toHaveBeenCalled()
    const commit = Array.from(host.querySelectorAll('button')).find(button => button.textContent?.includes('Commit 1 staged'))!
    await act(async () => commit.click())
    expect(fixture.act).toHaveBeenCalledWith('Crew/example', 'app', { op: 'commit', message: 'Editable draft', all: false })
    expect(fixture.act).toHaveBeenCalledTimes(1)
    await click('[aria-label="Push"]')
    expect(fixture.act).toHaveBeenLastCalledWith('Crew/example', 'app', { op: 'push' })
    const prepare = Array.from(host.querySelectorAll('button')).find(button => button.textContent?.includes('Open pull request…'))!
    await act(async () => prepare.click())
    expect(vi.mocked(api.post).mock.calls.some(([, body]) => (body as { op: string }).op === 'open_pr')).toBe(false)
    const dialog = document.querySelector('dialog[open]')!
    expect((dialog.querySelector('input') as HTMLInputElement).value).toBe('Drafted title')
    const confirm = Array.from(dialog.querySelectorAll('button')).find(button => button.textContent === 'Open pull request')!
    await act(async () => confirm.click())
    expect(api.post).toHaveBeenCalledWith('/api/workspace-git', expect.objectContaining({ op: 'open_pr', expected_head: 'head-1', title: 'Drafted title', body: 'Drafted body', session_id: 'own-chat' }), expect.anything())
    expect(host.querySelector('a[href="https://github.com/example/project/pull/42"]')).not.toBeNull()
    expect(host.textContent).toContain('Existing watch controls')
    let finish!: (value: never) => void
    vi.mocked(api.post).mockImplementation((_url, body) => (body as { op: string }).op === 'draft_commit' ? new Promise(resolve => { finish = resolve }) as never : Promise.resolve({ data: { available: true, can_push: true, can_draft: true } }) as never)
    await click('[aria-label="Draft commit message"]')
    fixture.actor = 'next-person'; invalidateChatIdentity()
    await act(async () => root.render(<GitChangesList workspacePath="Crew/example" />))
    await act(async () => finish({ data: { message: 'Old private draft' } } as never))
    expect((host.querySelector('[aria-label="Commit message"]') as HTMLTextAreaElement).value).toBe('')
    expect(host.textContent).not.toContain('Old private draft')
  } finally { await act(async () => root.unmount()); host.remove(); show.mockRestore(); fixture.actor = 'owner' }
})
