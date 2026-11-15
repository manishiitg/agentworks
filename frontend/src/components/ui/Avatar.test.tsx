// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { Avatar } from './Avatar'
const state = vi.hoisted(() => ({ get: vi.fn(), fetch: vi.fn(), actor: 'owner', workspace: 'local' }))
vi.mock('../../services/api', () => ({ default: { get: state.get }, getAuthToken: () => 'test-token', getDesktopApiBaseUrl: () => 'https://app.example', getLocalApiBaseUrl: () => 'https://local.example' }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (s: unknown) => unknown) => select({ user: { id: state.actor } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ getRawActiveWorkspaceId: () => state.workspace, useWorkspaceConnectionStore: (select: (s: unknown) => unknown) => select({ activeWorkspaceId: state.workspace }) }))
let root: ReturnType<typeof createRoot>, host: HTMLDivElement
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => { act(() => root?.unmount()); host?.remove(); vi.unstubAllGlobals(); vi.restoreAllMocks(); state.get.mockReset(); state.fetch.mockReset(); state.actor = 'owner'; state.workspace = 'local' })
// PLAT-746-1041: server portraits never send auth to an arbitrary URL and must
// use the source Team connection even when another network is selected.
it('fences photo origins and connection identity and clears a retired account blob', async () => {
  vi.stubGlobal('fetch', state.fetch)
  const create = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:portrait')
  const revoke = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
  await act(async () => root.render(<Avatar name="Example" avatar_url="https://untrusted.example/photo" />))
  expect(state.get).not.toHaveBeenCalled(); expect(state.fetch).not.toHaveBeenCalled(); expect(host.textContent).toBe('E')
  state.fetch.mockResolvedValue({ ok: true, blob: async () => new Blob(['jpeg'], { type: 'image/jpeg' }) })
  const url = '/api/account/avatars/person?revision=one'
  await act(async () => root.render(<Avatar name="Example" avatar_url={url} connection="source" />))
  expect(state.fetch.mock.calls[0][0]).toBe(`https://app.example/api/connections/source${url}`)
  expect(host.querySelector('img')?.getAttribute('src')).toBe('blob:portrait'); expect(create).toHaveBeenCalledOnce()
  state.actor = 'another'; state.fetch.mockRejectedValue(new Error('no access'))
  await act(async () => root.render(<Avatar name="Example" avatar_url={url} connection="source" />))
  expect(revoke).toHaveBeenCalledWith('blob:portrait'); expect(host.querySelector('img')).toBeNull(); expect(host.textContent).toBe('E')
})
