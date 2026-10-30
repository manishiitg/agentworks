// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
const { get, status } = vi.hoisted(() => ({ get: vi.fn(), status: vi.fn() }))
vi.mock('../../services/workspaceGit', () => ({ workspaceGitApi: { status } }))
vi.mock('../../services/api', () => ({ default: { get } }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (select: (state: unknown) => unknown) => select({ tabEvents: {} }) }))
import { ChatChangesRow } from './ChatChangesRow'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('shows failed status with retry, keeps loaded rows on refresh and scopes the next chat', async () => {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  const repo = { root: 'project', branch: 'feature', files: [{ path: 'scripts/run-analysis-with-a-long-filename.ts', status: 'untracked' }] }
  const button = (text: string) => [...host.querySelectorAll('button')].find(item => item.textContent?.includes(text))!
  const props = { workspacePath: 'Crew/example', sessionId: 'chat-a', onOpen: vi.fn() }
  try {
    get.mockResolvedValue({ data: { groups: [] } })
    status.mockRejectedValueOnce(new Error('offline'))
    await act(async () => root.render(<ChatChangesRow {...props} />))
    expect(host.textContent).toContain('Unavailable')
    expect(host.textContent).toContain('Could not load changes.')
    status.mockResolvedValueOnce([repo])
    await act(async () => button('Retry').click())
    expect(host.querySelector('[role="alert"]')).toBeNull()
    expect(host.textContent).toContain('1 file')
    expect(host.textContent).not.toContain('Work done in this chat')
    expect(host.querySelector('[class*="amber"]')).toBeNull()
    await act(async () => button('Changes').click())
    expect(props.onOpen).toHaveBeenCalledWith('uncommitted')
    status.mockRejectedValueOnce(new Error('offline'))
    await act(async () => window.dispatchEvent(new Event('chat-file-review-changed')))
    expect(host.textContent).toContain('Unavailable')
    expect(host.textContent).toContain('Could not load changes.')
    status.mockResolvedValueOnce([])
    await act(async () => root.render(<ChatChangesRow {...props} sessionId="chat-b" />))
    expect(host.textContent).not.toContain('project')
    expect(host.querySelector('[role="alert"]')).toBeNull()
  } finally { await act(async () => root.unmount()); host.remove() }
})

it('opens the outside-Git group rather than the whole chat comparison', async () => {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); const onOpen = vi.fn()
  try {
    status.mockResolvedValueOnce([])
    get.mockResolvedValueOnce({ data: { groups: [{ kind: 'crew_files', root: '', name: 'Other files', total_files: 29 }] } })
    await act(async () => root.render(<ChatChangesRow workspacePath="Crew/example" sessionId="chat-a" onOpen={onOpen} />))
    await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
    expect(onOpen).toHaveBeenCalledWith('outside-git')
  } finally { await act(async () => root.unmount()); host.remove() }
})
