// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { get } }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (select: (state: unknown) => unknown) => select({ tabEvents: {} }) }))
import { ChatChangesRow } from './ChatChangesRow'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const savedReply = async (_path: string, options: { params: Record<string, unknown> }) => ({ data: options.params.op === 'snapshots'
  ? { latest_snapshot_id: `latest-${options.params.session_id}` }
  // Full authorized totals come from the server even when the file list is paged.
  : { summary: { files: 3, added: 48, removed: 12 }, comparison: { changes: [{ path: 'first.ts', added: 1, removed: 0 }] } } })

it('shows failed status with retry and scopes the next chat to its own latest reply', async () => {
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
    expect(host.textContent).toContain('+48')
    expect(host.textContent).toContain('−12')
    expect(host.querySelector('[role="alert"]')).toBeNull()
    expect(host.querySelector('[class*="amber"]')).toBeNull()
    await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
    expect(onOpen).toHaveBeenLastCalledWith(expect.objectContaining({ sessionId: 'chat-a', snapshotId: 'latest-chat-a' }))
    await act(async () => root.render(<ChatChangesRow {...props} sessionId="chat-b" />))
    await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
    expect(onOpen).toHaveBeenLastCalledWith(expect.objectContaining({ sessionId: 'chat-b', snapshotId: 'latest-chat-b' }))
  } finally { await act(async () => root.unmount()); host.remove() }
})
