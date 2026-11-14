// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import api from '../../services/api'
import { OpenInEditorAction } from './OpenInEditor'
import { invalidateChatIdentity } from '../../utils/chatIdentity'

vi.mock('../../services/api', () => ({ default: { get: vi.fn(), post: vi.fn() } }))
const state = vi.hoisted(() => ({ actor: 'owner', network: 'local', toast: vi.fn() }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (select: (s: unknown) => unknown) => select({ addToast: state.toast }) }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (s: unknown) => unknown) => select({ user: { id: state.actor } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ getWorkspaceScopedStorageKey: (s: string) => s, useWorkspaceConnectionStore: (select: (s: unknown) => unknown) => select({ activeWorkspaceId: state.network }) }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// PLAT-746-1002: a remembered target never starts an app after caller/network
// changes. The picker resolves via the same server endpoint, not a local path.
it('chooses once through the resolver and discards an old callers pending launch', async () => {
  const native = vi.fn().mockResolvedValue({ ok: true })
  window.electronAPI = { openEditor: native } as never
  const showModal = vi.spyOn(HTMLDialogElement.prototype, 'showModal').mockImplementation(function (this: HTMLDialogElement) { this.open = true })
  const capabilities = { available: true, preferred_editor: '', editors: [{ id: 'vscode', label: 'VS Code' }] }
  vi.mocked(api.get).mockResolvedValue({ data: capabilities } as never)
  vi.mocked(api.post).mockResolvedValue({ data: { launch: { url: 'vscode://file/Users/example/project/a.ts:4' }, request_id: 'test-request' } } as never)
  const host = document.createElement('div'), root = createRoot(host)
  document.body.appendChild(host)
  try {
    await act(async () => root.render(<OpenInEditorAction workspacePath="Crew/example" repo="app" file="a.ts" />))
    await act(async () => (host.querySelector('[aria-label="Open in editor…"]') as HTMLButtonElement).click())
    expect(document.querySelector('dialog[open]')).not.toBeNull()
    await act(async () => (Array.from(document.querySelectorAll('dialog button')).find(b => b.textContent === 'VS Code') as HTMLButtonElement).click())
    expect(api.post).toHaveBeenCalledWith('/api/workspace-editor', expect.objectContaining({ workspace_path: 'Crew/example', repo: 'app', file: 'a.ts', editor: 'vscode', remember: true }), expect.anything())
    expect(native).toHaveBeenCalledWith({ url: 'vscode://file/Users/example/project/a.ts:4', requestId: 'test-request' })
    vi.mocked(api.get).mockResolvedValue({ data: { ...capabilities, preferred_editor: 'vscode' } } as never)
    await act(async () => window.dispatchEvent(new Event('agentworks:editor-preference')))
    let finish!: (v: never) => void
    vi.mocked(api.post).mockImplementation(() => new Promise(resolve => { finish = resolve }) as never)
    await act(async () => (host.querySelector('[aria-label="Open in VS Code"]') as HTMLButtonElement).click())
    invalidateChatIdentity()
    await act(async () => finish({ data: { launch: { url: 'vscode://file/Users/example/private' }, request_id: 'old-request' } } as never))
    expect(native).toHaveBeenCalledTimes(1)
    state.network = 'server'
    await act(async () => root.render(<OpenInEditorAction workspacePath="Crew/example" />))
    expect(host.textContent).toBe('')
  } finally {
    await act(async () => root.unmount())
    host.remove(); showModal.mockRestore(); delete window.electronAPI; state.network = 'local'
  }
})
