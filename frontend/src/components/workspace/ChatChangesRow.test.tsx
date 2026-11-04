// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
const { status } = vi.hoisted(() => ({ status: vi.fn() }))
vi.mock('../../services/workspaceGit', () => ({ workspaceGitApi: { status } }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (select: (state: unknown) => unknown) => select({ tabEvents: {} }) }))
vi.mock('./chatFileReviewData', () => ({ useReviewRevision: () => [0, vi.fn()] }))
import { ChatChangesRow } from './ChatChangesRow'
vi.spyOn(HTMLElement.prototype, 'getClientRects').mockReturnValue([{}] as unknown as DOMRectList)
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
it('uses project Git counts, clears after commit, and hides for folders without Git', async () => {
  // PLAT-746-319: cumulative saved reply counts grew even after committing.
  const host = document.createElement('div'); const root = createRoot(host)
  const props = { workspacePath: 'Crew/example', sessionId: 'chat-a', onOpen: vi.fn() }
  try {
    status.mockResolvedValue([{ files: [{path:'a.ts'}, {path:'b.ts'}] }])
    await act(async () => root.render(<ChatChangesRow {...props} />))
    expect(host.textContent).toContain('2 files')
    status.mockResolvedValue([{ files: [] }])
    await act(async () => window.dispatchEvent(new Event('focus')))
    expect(host.textContent).toContain('0 files')
    status.mockResolvedValue([])
    await act(async () => window.dispatchEvent(new Event('focus')))
    expect(host.textContent).toBe('')
  } finally { await act(async () => root.unmount()) }
})
