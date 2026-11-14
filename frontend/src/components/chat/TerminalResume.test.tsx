// @vitest-environment happy-dom
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { TerminalResumeNotice, continueInTerminal, useTerminalResumeState } from './TerminalResume'

const mock = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), toast: vi.fn(), canWrite: true }))
vi.mock('../../services/api', () => ({ default: { get: mock.get, post: mock.post } }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ chatTabs: { own: { metadata: { agentProfileId: 'work', agentProfileConversationKey: 'project:chat:own', agentProfileTabCanWrite: mock.canWrite } } }, addToast: mock.toast }) } }))
vi.mock('../../utils/chatIdentity', () => ({ captureChatIdentity: () => 'viewer', isChatIdentityCurrent: (identity: string) => identity === 'viewer' }))

beforeEach(() => { mock.get.mockReset(); mock.post.mockReset(); mock.canWrite = true; useTerminalResumeState.setState({ byTab: {}, commandTab: undefined }) })
afterEach(cleanup)

it('lets the owner recover a terminal-out chat without attach-back', async () => {
  mock.get.mockResolvedValue({ data: { out: true, command: 'agentworks resume opaque-chat', notice: 'Continued in terminal' } })
  mock.post.mockResolvedValue({ data: { out: false } })
  render(<TerminalResumeNotice tabId="own" />)
  fireEvent.click(await screen.findByRole('button', { name: 'Continue here instead' }))
  await waitFor(() => expect(mock.post).toHaveBeenCalledWith('/api/external/v1/chats/terminal', null, { params: { conversation_key: 'project:chat:own', action: 'here' } }))
  await waitFor(() => expect(useTerminalResumeState.getState().byTab['viewer:own'].out).toBe(false))
})

it('only displays a command and never moves a chat while opening the menu', async () => {
  mock.get.mockResolvedValue({ data: { out: false, command: 'agentworks resume opaque-chat', notice: '' } })
  await continueInTerminal('own')
  render(<TerminalResumeNotice tabId="own" />)
  expect(await screen.findByText('agentworks resume opaque-chat')).toBeTruthy()
  expect(mock.post).not.toHaveBeenCalled()
  expect(mock.get.mock.calls.every(([, options]) => options.params.action !== 'fetch')).toBe(true)
})
