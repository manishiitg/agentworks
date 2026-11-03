// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { get } }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (select: (state: unknown) => unknown) => select({ tabEvents: {} }) }))
import { ChatChangesRow } from './ChatChangesRow'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const savedChat = (files = 4, added = 50, removed = 13) => ({ data: { history: { replies: [], reviews: [] }, summary: { files, added, removed }, comparison: { note: '', skipped_count: 0 } } })

it('summarizes the current chat and drops previous totals while another chat loads', async () => {
  // PLAT-746-286: project-wide checkout rows appeared in every chat, with a mismatched heading indent.
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); const onOpen = vi.fn()
  const props = { workspacePath: 'Crew/example', sessionId: 'chat-a', onOpen }
  try {
    get.mockRejectedValueOnce(new Error('offline'))
    await act(async () => root.render(<ChatChangesRow {...props} />))
    expect(host.textContent).toContain('Unavailable')
    get.mockResolvedValue(savedChat())
    await act(async () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Retry')!.click())
    expect(get).toHaveBeenLastCalledWith('/api/workspace-snapshots', expect.objectContaining({ params: { workspace_path: 'Crew/example', session_id: 'chat-a', baseline_snapshot_id: 'start', op: 'snapshot_compare' } }))
    expect(host.querySelectorAll('button')).toHaveLength(1)
    expect(host.textContent).toContain('4 files')
    expect(host.textContent).toContain('+50')
    expect(host.textContent).toContain('−13')
    expect(host.querySelector('[role="alert"]')).toBeNull()
    expect(host.querySelector('[class*="amber"]')).toBeNull()
    await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
    expect(onOpen).toHaveBeenCalledWith()
    let resolve!: (value: ReturnType<typeof savedChat>) => void
    get.mockReturnValueOnce(new Promise(done => { resolve = done }))
    await act(async () => root.render(<ChatChangesRow {...props} sessionId="chat-b" />))
    expect(host.textContent).toContain('Loading')
    expect(host.textContent).not.toContain('4 files')
    expect(get).toHaveBeenLastCalledWith('/api/workspace-snapshots', expect.objectContaining({ params: expect.objectContaining({ session_id: 'chat-b' }) }))
    await act(async () => resolve(savedChat(1, 2, 1)))
    expect(host.textContent).toContain('1 file')
    expect(host.textContent).toContain('+2')
  } finally { await act(async () => root.unmount()); host.remove() }
})

it('keeps chat totals visible beside a warning about uncaptured files', async () => {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  const warning = 'Some files could not be captured or compared.'
  const reply = savedChat()
  get.mockResolvedValue({ data: { ...reply.data, comparison: { note: warning, skipped_count: 1 } } })
  try {
    await act(async () => root.render(<ChatChangesRow workspacePath="Crew/example" sessionId="chat-warning" onOpen={() => undefined} />))
    const row = host.querySelector<HTMLButtonElement>('button')!
    expect(row.textContent).toContain('4 files')
    expect(row.textContent).toContain('+50')
    expect(row.textContent).toContain('−13')
    expect(row.textContent).not.toContain('Incomplete')
    expect(row.querySelector('[role="img"]')?.getAttribute('aria-label')).toBe(warning)
    expect(row.querySelector('[role="img"]')?.getAttribute('title')).toBe(warning)
  } finally { await act(async () => root.unmount()); host.remove() }
})
