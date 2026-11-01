// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { get } }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (select: (state: unknown) => unknown) => select({ tabEvents: {} }) }))
import { ChatChangesRow } from './ChatChangesRow'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const savedReply = async () => ({ data: { scope: 'project', checkouts: [
  { id: 'worktree-a', root: 'app', name: 'app', captures: [{ snapshot_id: 'saved-a', at: '2026-11-01T00:00:00Z', files: 3, added: 48, removed: 12 }] },
  { id: 'worktree-b', root: 'feature', name: 'feature', captures: [{ snapshot_id: 'saved-b', at: '2026-11-01T00:00:00Z', files: 1, added: 2, removed: 1 }] },
] } })

it('shows failed status with retry and keeps shared checkout history when the active chat changes', async () => {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); const onOpen = vi.fn()
  const props = { workspacePath: 'Crew/example', sessionId: 'chat-a', onOpen }
  try {
    get.mockRejectedValueOnce(new Error('offline'))
    await act(async () => root.render(<ChatChangesRow {...props} />))
    expect(host.textContent).toContain('Unavailable')
    get.mockImplementation(savedReply)
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Retry')!.click())
    expect(host.textContent).toContain('3 files')
    expect(host.textContent).toContain('feature')
    expect(host.textContent).toContain('1 file')
    expect(get).toHaveBeenCalledWith('/api/workspace-snapshots', expect.objectContaining({ params: { workspace_path: 'Crew/example', history_scope: 'project', op: 'snapshots' } }))
    expect(host.textContent).toContain('+48')
    expect(host.textContent).toContain('−12')
    expect(host.querySelector('[role="alert"]')).toBeNull()
    expect(host.querySelector('[class*="amber"]')).toBeNull()
    await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
    expect(onOpen).toHaveBeenLastCalledWith(expect.objectContaining({ sessionId: 'chat-a', snapshotId: 'saved-a', checkoutId: 'worktree-a', historyScope: 'project' }))
    await act(async () => root.render(<ChatChangesRow {...props} sessionId="chat-b" />))
    await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
    expect(onOpen).toHaveBeenLastCalledWith(expect.objectContaining({ sessionId: 'chat-b', snapshotId: 'saved-a', checkoutId: 'worktree-a', historyScope: 'project' }))
  } finally { await act(async () => root.unmount()); host.remove() }
})
