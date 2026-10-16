// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ save: vi.fn(), get: vi.fn(), user: { id: 'alice' }, workspace: 'local' }))
vi.mock('../services/workspaceState', () => ({ workspaceStateApi: { save: mocks.save, get: mocks.get }, saveWorkspaceStateOnExit: vi.fn() }))
vi.mock('../stores/useAuthStore', () => ({ useAuthStore: { getState: () => ({ user: mocks.user }) } }))
vi.mock('../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: { getState: () => ({ activeWorkspaceId: mocks.workspace }) } }))
vi.mock('../stores/useChatStore', () => ({ useChatStore: {} }))
vi.mock('../stores/useProductSurfaceStore', () => ({ useProductSurfaceStore: {} }))
import { queueWorkspaceState, flushWorkspaceState } from './useWorkspaceReturn'
import { useWorkspaceReturnStore } from '../stores/useWorkspaceReturnStore'
const record = { version: 1 as const, revision: 1, last: null, recent: [] }
afterEach(() => { vi.clearAllMocks(); vi.useRealTimers() })
describe('per-user workspace writer', () => {
  it('does not send a pending previous-account workspace as the new person', async () => {
    vi.useFakeTimers()
    useWorkspaceReturnStore.setState({ ready: true, account: 'alice', record, homeOpen: false, restore: null })
    queueWorkspaceState({ surface: 'code', id: 'private-alice' })
    useWorkspaceReturnStore.setState({ account: 'bob', record })
    await flushWorkspaceState()
    expect(mocks.save).not.toHaveBeenCalled()
  })
  it('serializes saves and ignores an old account response', async () => {
    vi.useFakeTimers()
    let complete: (value: unknown) => void = () => {}
    mocks.save.mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    useWorkspaceReturnStore.setState({ ready: true, account: 'alice', record, homeOpen: false, restore: null })
    queueWorkspaceState({ surface: 'code', id: 'first' })
    const first = flushWorkspaceState()
    queueWorkspaceState({ surface: 'code', id: 'second' })
    await flushWorkspaceState()
    expect(mocks.save).toHaveBeenCalledTimes(1)
    mocks.user.id = 'bob'
    useWorkspaceReturnStore.setState({ account: 'bob', record })
    complete({ ...record, revision: 2, last: { surface: 'code', id: 'first' } })
    await first
    expect(useWorkspaceReturnStore.getState().record).toEqual(record)
    expect(mocks.save).toHaveBeenCalledTimes(1)
    mocks.user.id = 'alice'
  })
})

it('drops a pending snapshot when the connected server changes', async () => {
  vi.useFakeTimers()
  const previous = mocks.workspace
  useWorkspaceReturnStore.setState({ ready: true, account: 'alice', workspace: previous, record, homeOpen: false, restore: null })
  queueWorkspaceState({ surface: 'code', id: 'same-id-on-old-server' })
  mocks.workspace = 'different-server'
  await flushWorkspaceState()
  expect(mocks.save).not.toHaveBeenCalled()
  mocks.workspace = previous
  useWorkspaceReturnStore.setState({ workspace: undefined })
})
