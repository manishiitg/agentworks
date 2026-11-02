// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import type { ConditionTrigger } from '../../api/conditionTriggers'
import { PRWatcherControls } from './PRWatcherControls'

const fixture = vi.hoisted(() => ({ watchers: [] as ConditionTrigger[], tabs: {} as Record<string, unknown> }))
const send = vi.hoisted(() => vi.fn(async (_request: { tabId: string; message: string }) => ({})))
const manage = vi.hoisted(() => vi.fn(async () => { fixture.watchers = fixture.watchers.map(item => ({ ...item, enabled: false })) }))
vi.mock('axios', () => ({ default: { isAxiosError: () => false } }))
vi.mock('../../utils/workspacePaneChat', () => ({ sendWorkspacePaneMessageToChat: send }))
vi.mock('../../api/conditionTriggers', () => ({ conditionTriggersApi: { list: async () => ({ triggers: fixture.watchers }), manage } }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: Object.assign((select: (state: unknown) => unknown) => select({ chatTabs: fixture.tabs }), { getState: () => ({ chatTabs: fixture.tabs }) }) }))

it('sends PR requirements to the original chat and retains existing watch status and Stop', async () => {
  // PLAT-871-29: Watch PR should start a requirements conversation, not create a preset watch directly.
  fixture.tabs = { original: { tabId: 'original', sessionId: 'original-chat', metadata: { agentProfileProjectId: 'project', agentProfileId: 'work' } }, active: { tabId: 'active', sessionId: 'other-chat', metadata: { agentProfileProjectId: 'project', agentProfileId: 'work' } } }
  const watch: ConditionTrigger = { id: 'watch', name: 'CI and review follow-up', enabled: true, revision: 'revision', cron_expression: '* * * * *', timezone: '', script: '', sha256: '', target: { kind: 'agent' }, github_pr: { repository: 'example/project', number: 7, mode: 'autofix', worktree: 'worktree-a', session_id: 'original-chat', login: 'chosen-account' } }
  fixture.watchers = [watch, { ...watch, id: 'other-chat', github_pr: { ...watch.github_pr!, session_id: 'other-chat' } }, { ...watch, id: 'other-tree', github_pr: { ...watch.github_pr!, worktree: 'worktree-b' } }]
  const element = document.createElement('div'); document.body.append(element)
  const root = createRoot(element)
  const button = (label: string) => element.querySelector<HTMLButtonElement>(`[aria-label="${label}"]`)!
  try {
    await act(async () => root.render(<PRWatcherControls compact sessionId="original-chat" group={{ root: 'worktree-a', github_repo: 'example/project', pr: { state: 'open', number: 7 } }} />))
    expect(element.querySelector('select')).toBeNull()
    await act(async () => button('Watch PR').click())
    expect(send).toHaveBeenLastCalledWith({ tabId: 'original', message: expect.stringContaining('https://github.com/example/project/pull/7') })
    expect(send.mock.calls[0][0].message).toContain('worktree-a')
    expect(manage).not.toHaveBeenCalled()
    expect(element.textContent).toContain('Sent to chat')
    await act(async () => button('PR watch status').click())
    expect(element.querySelectorAll('button')).toHaveLength(3) // request, status, and only this PR/chat/worktree's Stop
    expect(element.textContent).toContain('CI and review follow-up')
    await act(async () => [...element.querySelectorAll('button')].find(item => item.textContent === 'Stop')!.click())
    expect(manage).toHaveBeenCalledWith('project', 'update', 'watch', { enabled: false }, 'revision', 'work')
    expect(element.textContent).toContain('Paused')
    delete fixture.tabs.original
    send.mockClear()
    await act(async () => button('Watch PR').click())
    expect(send).not.toHaveBeenCalled()
    expect(element.querySelector('[aria-label="Watch PR"]')).toBeNull()
  } finally { await act(async () => root.unmount()); element.remove() }
})
