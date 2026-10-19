// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({
  backupConfigured: false, isAdmin: true, canWrite: true, submit: vi.fn(),
  chatProps: {} as Record<string, any>,
  state: { chatTabs: {} as Record<string, any>, tabEvents: {}, createChatTab: vi.fn(), setTabConfig: vi.fn(), setTabMetadata: vi.fn() },
  resolve: vi.fn(), startNew: vi.fn(), activate: vi.fn(), hydrate: vi.fn(),
}))
vi.mock('../../utils/workspacePaneChat', () => ({ sendWorkspacePaneMessageToChat: vi.fn() }))
vi.mock('../../hooks/useProductUrlView', () => ({ useProductUrlView: vi.fn() }))
vi.mock('../../services/api', () => ({ agentApi: { resolveAgentProfileConversation: mocks.resolve, startNewAgentProfileConversation: mocks.startNew } }))
vi.mock('../../services/knowledgebaseApi', () => ({ knowledgebaseApi: { bootstrap: async () => ({ chat_workspace: 'Chats/Knowledgebase', backup_configured: mocks.backupConfigured, is_admin: mocks.isAdmin, can_write: mocks.canWrite }), proposals: async () => ({ proposals: [] }) }, knowledgebaseError: (error: Error) => error.message }))
vi.mock('../../components/ChatArea', () => ({ default: React.forwardRef((_props: any, ref) => { React.useImperativeHandle(ref, () => ({ submitQuery: mocks.submit })); mocks.chatProps = _props; return <div data-testid="shared-chat">{_props.landingContent}<textarea aria-label="Chat draft" /></div> }) }))
vi.mock('../../components/ModePresetBar', () => ({ ModePresetBar: () => null }))
vi.mock('../../components/topbar/LlmModalHost', () => ({ default: () => null }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: Object.assign((select: any) => select(mocks.state), { getState: () => mocks.state }), waitForChatStoreHydration: async () => {} }))
vi.mock('../../stores/useModeStore', () => ({ useModeStore: { getState: () => ({ setModeCategory: vi.fn() }) } }))
vi.mock('../../stores/useAppStore', () => ({ useAppStore: Object.assign((select: any) => select({}), { getState: () => ({ setAgentMode: vi.fn() }) }) }))
vi.mock('../../stores/useLLMStore', () => ({ useLLMStore: Object.assign((select: any) => select({}), { getState: () => ({}) }) }))
vi.mock('../../utils/sessionRestore', () => ({ hydrateTabEvents: mocks.hydrate }))
vi.mock('../../utils/activateTab', () => ({ activateTab: mocks.activate }))
vi.mock('./KnowledgebaseWorkspacePane', () => ({ KnowledgebaseWorkspacePane: ({ view, modelSettings, onAsk }: any) => <div data-testid="workspace">{view === 'models' ? modelSettings : view}{onAsk && <button onClick={() => void onAsk()}>Inspect selected folder</button>}</div> }))
vi.mock('./KnowledgebaseModelSettings', () => ({ KnowledgebaseModelSettings: () => <span>Shared model settings</span> }))
vi.mock('./KnowledgebaseAccessConfirmation', () => ({ KnowledgebaseAccessConfirmation: () => <div data-testid="existing-approval-flow" /> }))
import { requestServiceChat, useServiceChatNavigation } from '../../utils/serviceChatNavigation'
import { KnowledgebaseSurface } from './KnowledgebaseSurface'
import { usePanelSwitcherStore } from '../../stores/usePanelSwitcherStore'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach(cleanup => cleanup()); document.body.innerHTML = ''; window.localStorage?.clear(); vi.clearAllMocks() })
beforeEach(() => {
  useServiceChatNavigation.setState({ request: null })
  mocks.backupConfigured = false; mocks.isAdmin = true; mocks.canWrite = true; mocks.submit.mockResolvedValue(undefined)
  mocks.chatProps = {}; mocks.state.chatTabs = {}
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
async function click(host: HTMLElement, label: string) {
  await act(async () => {
    const button = [...host.querySelectorAll<HTMLButtonElement>('button')].find(button => (button.getAttribute('aria-label') ?? button.textContent?.trim())?.startsWith(label) && !button.closest('[hidden]'))
    expect(button, label).toBeDefined(); button!.click()
  })
}
describe('Brain library with a secondary access chat', () => {
  it('opens the library and shared overview without creating or restoring a chat', async () => {
    const host = await mount()
    expect(host.querySelector('h1')?.textContent).toBe('Library')
    expect(host.querySelector('[data-testid="workspace"]')?.textContent).toContain('library')
    expect(host.querySelector('[aria-label="Workspace context"]')).not.toBeNull()
    expect(host.querySelector('[aria-label="Access management chat"]')).toBeNull()
    expect(host.querySelector('[data-testid="shared-chat"]')).toBeNull()
    expect(mocks.resolve).not.toHaveBeenCalled(); expect(mocks.state.createChatTab).not.toHaveBeenCalled()
    expect(mocks.submit).not.toHaveBeenCalled()
    expect(host.querySelector('[data-testid="existing-approval-flow"]')).not.toBeNull()
    for (const label of ['Folder access', 'Access chat', 'Connect AI tools', 'Schedules', 'Secrets', 'Models', 'Backup']) expect(host.textContent).toContain(label)
  })
  it('opens the existing chat explicitly without sending and preserves it and its draft when closed', async () => {
    const host = await mount()
    await click(host, 'Access chat')
    expect(mocks.resolve).toHaveBeenCalledWith('knowledgebase', { conversation_key: 'main' })
    expect(mocks.submit).not.toHaveBeenCalled()
    expect(mocks.chatProps.compact).toBe(true); expect(mocks.chatProps.showProductSteerAction).toBe(true)
    expect(mocks.chatProps.showProductTerminalControl).toBeUndefined()
    expect(host.querySelector('[aria-label="Access management chat"]')).not.toBeNull()
    const chat = host.querySelector('[data-testid="shared-chat"]')!
    const draft = host.querySelector<HTMLTextAreaElement>('[aria-label="Chat draft"]')!
    draft.value = 'Unsent draft'
    await click(host, 'Back to Brain overview')
    expect(host.querySelector('[aria-label="Brain overview"]')).not.toBeNull()
    expect(host.querySelector('[data-testid="shared-chat"]')).toBe(chat)
    await click(host, 'Access chat')
    expect(host.querySelector('[aria-label="Chat draft"]')).toBe(draft)
    expect(draft.value).toBe('Unsent draft')
    expect(mocks.resolve).toHaveBeenCalledTimes(1)
  })
  it('opens folder access directly and keeps model settings in the main workspace with chat closed', async () => {
    const host = await mount()
    await click(host, 'Folder access')
    expect(host.querySelector('[data-testid="workspace"]')?.textContent).toContain('access')
    expect(mocks.resolve).not.toHaveBeenCalled()
    await click(host, 'Models')
    expect(mocks.resolve).toHaveBeenCalledTimes(1)
    expect(host.querySelector('[data-testid="workspace"]')?.textContent).toBe('Shared model settingsInspect selected folder')
    expect(host.querySelector('[aria-label="Access management chat"]')).toBeNull()
    expect(mocks.submit).not.toHaveBeenCalled()
    await click(host, 'Back to library')
    expect(host.querySelector('h1')?.textContent).toBe('Library')
  })
  it('delivers a confirmed folder request after the lazy chat mounts', async () => {
    const host = await mount()
    await click(host, 'Inspect selected folder')
    expect(mocks.submit).toHaveBeenCalledOnce()
    expect(mocks.submit).toHaveBeenCalledWith('Inspect access for folder "organization root".')
    expect(mocks.activate).toHaveBeenLastCalledWith('tab-old')
  })
  it('keeps the existing secure backup action and refreshes status without rotating the chat', async () => {
    const host = await mount()
    const banner = host.querySelector('[data-testid="knowledgebase-backup-banner"]')!
    expect(banner.closest('[aria-label="Brain library and management"]')).not.toBeNull()
    expect(host.querySelector('[aria-label="Brain overview"]')!.textContent).not.toContain('Not set up')
    await click(host, 'Configure backup')
    expect(mocks.submit).not.toHaveBeenCalled()
    expect(mocks.resolve).not.toHaveBeenCalled()
    await new Promise(resolve => setTimeout(resolve, 650))
    await click(host, 'Sure?')
    expect(mocks.submit).toHaveBeenCalledWith(expect.stringContaining('action=configure_backup'))
    mocks.backupConfigured = true
    await click(host, 'Refresh Brain')
    expect(host.querySelector('[data-testid="knowledgebase-backup-banner"]')).toBeNull()
    expect(mocks.resolve).toHaveBeenCalledTimes(1)
  })
  it('keeps non-admin writers in their existing bounds and gives readers only the library', async () => {
    mocks.isAdmin = false
    const host = await mount()
    expect(host.textContent).toContain('Ask an administrator')
    expect(host.textContent).not.toContain('Configure backup')
    mocks.canWrite = false
    await click(host, 'Refresh Brain')
    expect(host.querySelector('[data-testid="knowledgebase-reader"]')).not.toBeNull()
    expect(host.querySelector('[data-testid="existing-approval-flow"]')).toBeNull()
    expect(host.querySelector('[data-testid="shared-chat"]')).toBeNull()
    expect(host.textContent).not.toContain('Access chat')
    expect(host.textContent).not.toContain('Secrets')
    expect(usePanelSwitcherStore.getState().entries.knowledgebase?.panels.map(item => item.id)).toEqual(['library'])
    await act(async () => usePanelSwitcherStore.getState().entries.knowledgebase?.open('models'))
    expect(mocks.resolve).not.toHaveBeenCalled()
    expect(host.querySelector('h1')?.textContent).toBe('Library')
  })
  it('retains lazy chat errors without blocking the library and supports retry and conversation rotation', async () => {
    mocks.resolve.mockRejectedValueOnce(new Error('Assistant unavailable'))
    const host = await mount()
    await click(host, 'Access chat')
    expect(host.textContent).toContain('Assistant unavailable')
    expect(host.querySelector('[data-testid="workspace"]')?.textContent).toContain('library')
    await click(host, 'Retry access chat')
    await act(async () => { mocks.chatProps.onNewChat() })
    expect(mocks.startNew).toHaveBeenCalledWith('knowledgebase', { conversation_key: 'main' })
    expect(mocks.state.setTabMetadata).toHaveBeenCalledWith('tab-old', { isViewOnly: true })
    expect(mocks.chatProps.tabId).toBe('tab-new')
  })
  it('discards a delayed conversation response after write access is revoked', async () => {
    let resolve!: (value: any) => void
    mocks.resolve.mockReturnValueOnce(new Promise(done => { resolve = done }))
    const host = await mount()
    await click(host, 'Access chat')
    mocks.canWrite = false
    await click(host, 'Refresh Brain')
    await act(async () => resolve({ conversation_id: 'old', conversation_key: 'main', session_id: 'old' }))
    expect(mocks.state.createChatTab).not.toHaveBeenCalled()
    expect(host.querySelector('[data-testid="shared-chat"]')).toBeNull()
  })
})


it('opens the selected existing Brain conversation without resolving a different main chat', async () => {
  mocks.state.chatTabs.saved = { tabId: 'saved', sessionId: 'saved-session', metadata: { agentProfileId: 'knowledgebase', isViewOnly: true } }
  requestServiceChat('knowledgebase', 'saved')
  await mount()
  expect(mocks.chatProps.tabId).toBe('saved')
  expect(mocks.state.createChatTab).not.toHaveBeenCalled()
  expect(mocks.resolve).not.toHaveBeenCalled()
  expect(mocks.hydrate).toHaveBeenCalledWith('saved-session', expect.objectContaining({ workspacePath: 'Chats/Knowledgebase' }))
})

it('does not open a requested Brain chat without write permission', async () => {
  mocks.canWrite = false
  requestServiceChat('knowledgebase', 'saved')
  await mount()
  expect(mocks.resolve).not.toHaveBeenCalled()
  expect(mocks.hydrate).not.toHaveBeenCalled()
  expect(mocks.state.createChatTab).not.toHaveBeenCalled()
})
