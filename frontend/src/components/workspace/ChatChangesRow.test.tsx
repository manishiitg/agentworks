// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
const get = vi.hoisted(() => vi.fn())
vi.mock('../../services/api', () => ({ default: { get } }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (select: (state: unknown) => unknown) => select({ tabEvents: {} }) }))
vi.mock('../chat/PRWatcherControls', () => ({ PRWatcherControls: () => <button>Watch PR</button> }))
import { ChatChangesRow } from './ChatChangesRow'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('shows failed status with retry, keeps loaded rows on refresh and scopes the next chat', async () => {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  const group = { kind: 'repo', root: '', name: 'project', total_files: 1, uncommitted: [{ path: 'main.ts', status: 'M' }] }
  const button = (text: string) => [...host.querySelectorAll('button')].find(item => item.textContent?.includes(text))!
  const props = { workspacePath: 'Crew/example', sessionId: 'chat-a', onOpen: vi.fn() }
  try {
    get.mockRejectedValueOnce(new Error('offline'))
    await act(async () => root.render(<ChatChangesRow {...props} />))
    expect(host.textContent).toContain('Unavailable')
    expect(host.textContent).toContain('Could not load changes.')
    get.mockResolvedValueOnce({ data: { groups: [group], history: { can_review: true } } })
    await act(async () => button('Retry').click())
    expect(host.querySelector('[role="alert"]')).toBeNull()
    await act(async () => button('project').click())
    expect(host.textContent).toContain('main.ts')
    await act(async () => button('project').click())
    expect(host.textContent).not.toContain('main.ts')
    get.mockRejectedValueOnce(new Error('offline'))
    await act(async () => window.dispatchEvent(new Event('chat-file-review-changed')))
    expect(host.textContent).toContain('project')
    expect(host.textContent).toContain('Could not load changes.')
    get.mockResolvedValueOnce({ data: { groups: [] } })
    await act(async () => root.render(<ChatChangesRow {...props} sessionId="chat-b" />))
    expect(host.textContent).not.toContain('project')
    expect(host.querySelector('[role="alert"]')).toBeNull()
  } finally { await act(async () => root.unmount()); host.remove() }
})
