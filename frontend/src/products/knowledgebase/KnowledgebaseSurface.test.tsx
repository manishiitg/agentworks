// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({
  chatProps: {} as Record<string, any>,
  state: { chatTabs: {} as Record<string, any>, tabEvents: {}, createChatTab: vi.fn(), setTabConfig: vi.fn(), setTabMetadata: vi.fn() },
  resolve: vi.fn(), startNew: vi.fn(), activate: vi.fn(), hydrate: vi.fn(),
}))
vi.mock('../../services/api', () => ({ agentApi: { resolveAgentProfileConversation: mocks.resolve, startNewAgentProfileConversation: mocks.startNew } }))
vi.mock('../../services/knowledgebaseApi', () => ({ knowledgebaseApi: { bootstrap: async () => ({ chat_workspace: 'Chats/Knowledgebase' }), proposals: async () => ({ proposals: [] }) }, knowledgebaseError: (error: Error) => error.message }))
vi.mock('../../components/ChatArea', () => ({ default: React.forwardRef((_props: any, _ref) => { mocks.chatProps = _props; return <div data-testid="shared-chat">{_props.landingContent}</div> }) }))
vi.mock('../../components/ModePresetBar', () => ({ ModePresetBar: () => null }))
vi.mock('../../components/topbar/LlmModalHost', () => ({ default: () => null }))
vi.mock('../../components/chat/AgentWorksChatTabItem', () => ({ AgentWorksChatTabItem: ({ tab }: any) => <button>{tab.name}</button> }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: Object.assign((select: any) => select(mocks.state), { getState: () => mocks.state }), waitForChatStoreHydration: async () => {} }))
vi.mock('../../stores/useModeStore', () => ({ useModeStore: { getState: () => ({ setModeCategory: vi.fn() }) } }))
vi.mock('../../stores/useAppStore', () => ({ useAppStore: Object.assign((select: any) => select({}), { getState: () => ({ setAgentMode: vi.fn() }) }) }))
vi.mock('../../stores/useLLMStore', () => ({ useLLMStore: Object.assign((select: any) => select({}), { getState: () => ({}) }) }))
vi.mock('../../utils/sessionRestore', () => ({ hydrateTabEvents: mocks.hydrate }))
vi.mock('../../utils/activateTab', () => ({ activateTab: mocks.activate }))
vi.mock('./KnowledgebaseWorkspacePane', () => ({ KnowledgebaseWorkspacePane: ({ view, modelSettings }: any) => <div data-testid="workspace">{view === 'models' ? modelSettings : 'Library'}</div> }))
vi.mock('./KnowledgebaseModelSettings', () => ({ KnowledgebaseModelSettings: () => <span>Shared model settings</span> }))
vi.mock('./KnowledgebaseAccessConfirmation', () => ({ KnowledgebaseAccessConfirmation: () => null }))
import { KnowledgebaseSurface } from './KnowledgebaseSurface'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(cleanup => cleanup()); document.body.innerHTML = ''; vi.clearAllMocks() })
beforeEach(() => {
  mocks.state.chatTabs = {}
  mocks.resolve.mockResolvedValue({ conversation_id: 'old', conversation_key: 'main', session_id: 'old' })
  mocks.startNew.mockResolvedValue({ conversation_id: 'new', conversation_key: 'main', session_id: 'new' })
  mocks.state.createChatTab.mockImplementation(async (name, metadata, sessionId) => { const tabId = `tab-${sessionId}`; mocks.state.chatTabs[tabId] = { tabId, name, metadata }; return tabId })
})
async function mount() {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); cleanups.push(() => act(() => root.unmount()))
  await act(async () => { root.render(<KnowledgebaseSurface />) })
  return host
}
describe('Knowledge Base shared platform chat', () => {
  it('uses Vault’s standard ChatArea configuration and places Models in the workspace', async () => {
    const host = await mount()
    expect(mocks.chatProps.compact).toBe(true)
    expect(mocks.chatProps.showProductSteerAction).toBe(true)
    expect(mocks.chatProps.inputVariant).toBeUndefined()
    expect(mocks.chatProps.fullTurnStreaming).toBeUndefined()
    expect(mocks.chatProps.hideRuntimeStatus).toBeUndefined()
    expect(mocks.chatProps.showProductTerminalControl).toBeUndefined()
    expect(host.querySelector('details')).toBeNull()
    expect(host.textContent).not.toContain('Access assistant models')
    expect(host.textContent).toContain('Manage Knowledge Base access')
    await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Models"]')!.click() })
    expect(host.querySelector('[data-testid="workspace"]')?.textContent).toBe('Shared model settings')
    expect(host.querySelector('[data-testid="shared-chat"]')?.textContent).not.toContain('Shared model settings')
  })
  it('rotates profile conversations through the shared API and preserves the old chat as view-only', async () => {
    await mount()
    await act(async () => { mocks.chatProps.onNewChat() })
    expect(mocks.startNew).toHaveBeenCalledWith('knowledgebase', { conversation_key: 'main' })
    expect(mocks.state.setTabMetadata).toHaveBeenCalledWith('tab-old', { isViewOnly: true })
    expect(mocks.chatProps.tabId).toBe('tab-new')
    expect(mocks.activate).toHaveBeenLastCalledWith('tab-new')
  })
})
