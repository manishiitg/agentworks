// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
const state = vi.hoisted(() => ({ user: 'alice', workspace: 'one' }))
vi.mock('../stores/useAuthStore', () => ({ useAuthStore: (select: any) => select({ user: { id: state.user } }) }))
vi.mock('../stores/useWorkspaceConnectionStore', () => ({
  useWorkspaceConnectionStore: (select: any) => select({ activeWorkspaceId: state.workspace }),
  getWorkspaceScopedStorageKey: (key: string) => key,
}))
vi.mock('./useLiveRefetch', () => ({ useLiveRefetch: () => {} }))
vi.mock('../services/api', () => ({ agentApi: { getWorkflowBuilderSessions: vi.fn() } }))
import { useWorkflowBuilderBindings, publishWorkflowBuilderBinding } from './useWorkflowBuilderBindings'
import { invalidateChatIdentity, captureChatIdentity } from '../utils/chatIdentity'
import { agentApi } from '../services/api'
it('coalesces workflow discovery, rejects old-account responses and keeps a server New chat ahead of stale reads', async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  vi.useFakeTimers()
  let resolveOld!: (value: any) => void
  let resolveStale!: (value: any) => void
  vi.mocked(agentApi.getWorkflowBuilderSessions).mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    .mockImplementationOnce(() => new Promise(resolve => { resolveStale = resolve }))
  const container = document.createElement('div')
  const root = createRoot(container)
  const preset = (id: string) => ({ id, selectedFolder: { filepath: `Workflow/${id}` } }) as never
  let requestedSession = 'before'
  function Probe({ id }: { id: string }) {
    const bindings = useWorkflowBuilderBindings([preset(id)], { [id]: [requestedSession] })
    return <span>{bindings[id]?.session_id || 'no-builder'}</span>
  }
  await act(async () => root.render(<><Probe id="one" /><Probe id="two" /></>))
  await act(async () => { await vi.advanceTimersByTimeAsync(15) })
  expect(agentApi.getWorkflowBuilderSessions).toHaveBeenCalledTimes(1)
  expect(vi.mocked(agentApi.getWorkflowBuilderSessions).mock.calls[0][0]).toHaveLength(2)
  state.user = 'bob'; state.workspace = 'other'; invalidateChatIdentity()
  await act(async () => root.render(<><Probe id="one" /><Probe id="two" /></>))
  await act(async () => { await vi.advanceTimersByTimeAsync(15) })
  await act(async () => resolveOld({ success: true, bindings: { one: { session_id: 'alice-private' } } }))
  expect(container.textContent).toBe('no-builderno-builder')
  await act(async () => publishWorkflowBuilderBinding({ success: true, is_builder: true, preset_query_id: 'one', session_id: 'server-new', source: 'none', status: 'completed', events: [] }, captureChatIdentity(), true))
  await act(async () => resolveStale({ success: true, bindings: { one: { session_id: 'old-bob' } } }))
  expect(container.textContent).toBe('server-newno-builder')
  vi.mocked(agentApi.getWorkflowBuilderSessions).mockResolvedValueOnce({ success: true, bindings: {} })
  requestedSession = 'after'
  await act(async () => root.render(<><Probe id="one" /><Probe id="two" /></>))
  await act(async () => { await vi.advanceTimersByTimeAsync(15) })
  expect(container.textContent).toBe('server-newno-builder') // One failed workflow read keeps its last server snapshot.
  await act(async () => root.unmount())
  vi.useRealTimers()
})
