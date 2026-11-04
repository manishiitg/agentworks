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
    status.mockResolvedValue([{ root:'app', branch:'main', used_in_chat:true, files: [{path:'a.ts'}, {path:'b.ts'}] }])
    await act(async () => root.render(<ChatChangesRow {...props} />))
    expect(host.textContent).toContain('2 files')
    status.mockResolvedValue([{ root:'app', branch:'main', used_in_chat:true, files: [] }])
    await act(async () => window.dispatchEvent(new Event('focus')))
    expect(host.textContent).toContain('0 files')
    expect(host.textContent).toContain('Clean')
    status.mockResolvedValue([])
    await act(async () => window.dispatchEvent(new Event('focus')))
    expect(host.textContent).toBe('')
  } finally { await act(async () => root.unmount()) }
})

it('groups multiple tool-used worktrees and opens the exact checkout, leaving others collapsed', async () => {
  const host = document.createElement('div'); const root = createRoot(host); const onOpen = vi.fn()
  status.mockResolvedValue([
    {root:'api', branch:'fix/api', used_in_chat:true, files:[{path:'a.ts'}]},
    {root:'web', branch:'fix/web', used_in_chat:true, files:[]},
    {root:'unrelated', branch:'main', files:[{path:'other.ts'}]},
  ])
  try {
    await act(async () => root.render(<ChatChangesRow workspacePath="Crew/example" sessionId="chat-a" onOpen={onOpen} />))
    expect(status).toHaveBeenCalledWith('Crew/example', 'chat-a')
    expect(host.textContent).toContain('fix/api')
    expect(host.textContent).toContain('fix/web')
    expect(host.querySelector('details')?.open).toBe(false)
    await act(async () => (Array.from(host.querySelectorAll('button')).find(button => button.textContent?.includes('fix/web')) as HTMLButtonElement).click())
    expect(onOpen).toHaveBeenCalledWith('web')
  } finally { await act(async () => root.unmount()) }
})
