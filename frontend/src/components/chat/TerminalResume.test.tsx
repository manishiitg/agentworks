// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { beforeEach, expect, it, vi } from 'vitest'
import { TerminalResumeNotice, continueInTerminal, useTerminalResumeState } from './TerminalResume'

const mock = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), toast: vi.fn(), canWrite: true }))
vi.mock('../../services/api', () => ({ default: { get: mock.get, post: mock.post } }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ chatTabs: { own: { metadata: { agentProfileId: 'work', agentProfileConversationKey: 'project:chat:own', agentProfileTabCanWrite: mock.canWrite } } }, addToast: mock.toast }) } }))
vi.mock('../../utils/chatIdentity', () => ({ captureChatIdentity: () => 'viewer', isChatIdentityCurrent: (identity: string) => identity === 'viewer' }))

beforeEach(() => { mock.get.mockReset(); mock.post.mockReset(); mock.canWrite = true; useTerminalResumeState.setState({ byTab: {}, commandTab: undefined }) })
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('lets the owner recover a terminal-out chat without attach-back', async () => {
  mock.get.mockResolvedValue({ data: { out: true, command: 'agentworks resume opaque-chat', notice: 'Continued in terminal' } })
  mock.post.mockResolvedValue({ data: { out: false } })
  const host = document.createElement('div'), root = createRoot(host)
  document.body.append(host)
  try {
    await act(async () => root.render(<TerminalResumeNotice tabId="own" />))
    const button = [...host.querySelectorAll<HTMLButtonElement>('button')].find(b => b.textContent === 'Continue here instead')
    expect(button).toBeTruthy()
    await act(async () => button!.click())
    expect(mock.post).toHaveBeenCalledWith('/api/external/v1/chats/terminal', null, { params: { conversation_key: 'project:chat:own', action: 'here' } })
    expect(useTerminalResumeState.getState().byTab['viewer:own'].out).toBe(false)
  } finally { await act(async () => root.unmount()); host.remove() }
})

it('only displays a command and never moves a chat while opening the menu', async () => {
  mock.get.mockResolvedValue({ data: { out: false, command: 'agentworks resume opaque-chat', notice: '' } })
  await continueInTerminal('own')
  const host = document.createElement('div'), root = createRoot(host)
  document.body.append(host)
  try {
    await act(async () => root.render(<TerminalResumeNotice tabId="own" />))
    expect(host.textContent).toContain('agentworks resume opaque-chat')
    expect(mock.post).not.toHaveBeenCalled()
    expect(mock.get.mock.calls.every(([, options]) => options.params.action !== 'fetch')).toBe(true)
  } finally { await act(async () => root.unmount()); host.remove() }
})
