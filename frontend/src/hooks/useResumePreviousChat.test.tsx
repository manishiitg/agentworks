// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import type { ChatHistorySession } from '../services/api-types'
import { useResumePreviousChat } from './useResumePreviousChat'

const mocks = vi.hoisted(() => ({
  state: {} as Record<string, unknown>,
  bind: vi.fn(),
  hydrate: vi.fn(),
}))
vi.mock('../stores/useChatStore', () => ({ useChatStore: { getState: () => mocks.state } }))
vi.mock('../services/api', () => ({ agentApi: { switchAgentProfileConversation: mocks.bind } }))
vi.mock('../utils/sessionRestore', () => ({ hydrateTabEvents: mocks.hydrate }))
vi.mock('../utils/executionConversationRestore', () => ({ hydrateExecutionConversation: mocks.hydrate }))
vi.mock('../utils/restoredTerminal', () => ({ startRestoredTransportTerminal: vi.fn() }))
vi.mock('../components/PreviousChatHistoryPanel', () => ({
  chatHistoryConversationPath: vi.fn(),
  chatHistoryRuntimeLabel: vi.fn(),
  chatHistorySupportsNativeResume: vi.fn(),
  chatHistoryUsesTerminalRestore: vi.fn(),
  chatHistoryWorkshopModeLabel: vi.fn(),
}))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('returns to an already open saved chat even when all side tabs are occupied', async () => {
  const projectId = 'project-a'
  const metadata = {
    mode: 'multi-agent', agentProfileId: 'code', agentProfileProjectId: projectId,
    agentProfileBuilder: false,
  }
  const tabs = Object.fromEntries(Array.from({ length: 20 }, (_, index) => [
    `side-${index}`, {
      tabId: `side-${index}`, sessionId: `saved-${index}`,
      metadata: { ...metadata, agentProfileConversationKey: `${projectId}:chat:${index}` },
    },
  ]))
  const main = { tabId: 'main', sessionId: 'current-main', metadata: { ...metadata, agentProfileConversationKey: projectId } }
  const switchTab = vi.fn()
  const createChatTab = vi.fn()
  const addToast = vi.fn()
  mocks.state = { activeTabId: 'main', chatTabs: { ...tabs, main }, switchTab, createChatTab, addToast }
  let resume!: ReturnType<typeof useResumePreviousChat>
  function Harness() { resume = useResumePreviousChat(); return null }
  const container = document.createElement('div')
  const root = createRoot(container)
  try {
    await act(async () => { root.render(<Harness />) })
    await act(async () => { await resume({ session_id: 'saved-3', can_resume: true } as ChatHistorySession) })
    expect(switchTab).toHaveBeenCalledExactlyOnceWith('side-3')
    expect(createChatTab).not.toHaveBeenCalled()
    expect(mocks.bind).not.toHaveBeenCalled()
    expect(mocks.hydrate).not.toHaveBeenCalled()
    expect(addToast).not.toHaveBeenCalled()
    expect(Object.keys(mocks.state.chatTabs as object)).toHaveLength(21)
    expect(main.sessionId).toBe('current-main')
  } finally {
    await act(async () => { root.unmount() })
    container.remove()
  }
})
