// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { beforeEach, expect, it, vi } from 'vitest'
import { TerminalCommandPopup, continueInTerminal, useTerminalCommand } from './TerminalResume'

const mock = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), toast: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { get: mock.get, post: mock.post } }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ chatTabs: { own: { metadata: { agentProfileId: 'work', agentProfileConversationKey: 'project:chat:own' } } }, addToast: mock.toast }) } }))
vi.mock('../../utils/chatIdentity', () => ({ captureChatIdentity: () => 'viewer', isChatIdentityCurrent: (identity: string) => identity === 'viewer' }))

beforeEach(() => { mock.get.mockReset(); mock.post.mockReset(); useTerminalCommand.setState({ tabId: undefined, command: undefined }) })
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// The terminal gets a copy; asking for the command must never change the chat.
it('only shows the command for a terminal copy and never changes the chat', async () => {
  mock.get.mockResolvedValue({ data: { command: 'agentworks resume opaque-chat' } })
  await continueInTerminal('own')
  const host = document.createElement('div'), root = createRoot(host)
  document.body.append(host)
  try {
    await act(async () => root.render(<TerminalCommandPopup tabId="own" />))
    expect(document.body.textContent).toContain('agentworks resume opaque-chat')
    expect(mock.get).toHaveBeenCalledWith('/api/external/v1/chats/terminal', { params: { conversation_key: 'project:chat:own', action: 'command' } })
    expect(mock.post).not.toHaveBeenCalled()
  } finally { await act(async () => root.unmount()); host.remove() }
})
