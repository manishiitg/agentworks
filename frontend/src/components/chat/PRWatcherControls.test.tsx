// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import type { ConditionTrigger } from '../../api/conditionTriggers'
import { PRWatcherControls } from './PRWatcherControls'

const fixture = vi.hoisted(() => ({ watchers: [] as ConditionTrigger[] }))
const post = vi.hoisted(() => vi.fn(async () => {
  fixture.watchers = [{ id: 'watch', name: 'Watch PR', enabled: true, revision: 'revision', cron_expression: '* * * * *', timezone: '', script: '', sha256: '', target: { kind: 'agent' }, github_pr: { repository: 'example/project', number: 7, mode: 'autofix', worktree: '', session_id: 'original-chat', login: 'chosen-account' } }]
}))
const manage = vi.hoisted(() => vi.fn(async () => { fixture.watchers = [] }))
vi.mock('axios', () => ({ default: { post, isAxiosError: () => false } }))
vi.mock('../../services/api', () => ({ getApiBaseUrl: () => 'https://example.com', getAuthToken: () => 'test-auth' }))
vi.mock('../../api/conditionTriggers', () => ({ conditionTriggersApi: { list: async () => ({ triggers: fixture.watchers }), manage } }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: (select: (state: unknown) => unknown) => select({ chatTabs: { tab: { sessionId: 'original-chat', metadata: { agentProfileProjectId: 'project', agentProfileId: 'work' } } } }) }))

it('uses the normal Watcher endpoint with the original chat and allows stopping its watch', async () => {
  const element = document.createElement('div'); document.body.append(element)
  const root = createRoot(element)
  const button = (text: string) => [...element.querySelectorAll('button')].find(item => item.textContent === text)!
  try {
    await act(async () => root.render(<PRWatcherControls sessionId="original-chat" group={{ root: '', github_repo: 'example/project', pr: { state: 'open', number: 7 } }} />))
    await act(async () => button('Watch PR').click())
    const select = element.querySelector('select')!
    await act(async () => { select.value = 'autofix'; select.dispatchEvent(new Event('change', { bubbles: true })) })
    await act(async () => button('Start auto-fix').click())
    expect(post).toHaveBeenCalledWith('/api/condition-triggers', expect.objectContaining({ action: 'create', project_id: 'project', trigger: expect.objectContaining({ github_pr: expect.objectContaining({ repository: 'example/project', number: 7, mode: 'autofix', worktree: '', review_bots: [] }), target: { kind: 'agent', message: 'Report the PR result in this chat.' } }) }), expect.objectContaining({ headers: { Authorization: 'Bearer test-auth', 'X-Session-ID': 'original-chat' } }))
    expect(element.textContent).toContain('Watching · chosen-account')
    await act(async () => button('Stop').click())
    expect(manage).toHaveBeenCalledWith('project', 'update', 'watch', { enabled: false }, 'revision', 'work')
  } finally { await act(async () => root.unmount()); element.remove() }
})
