// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({
  backupConfigured: false, isAdmin: true, canWrite: true, submit: vi.fn(),
  chatProps: {} as Record<string, any>,
  state: { chatTabs: {} as Record<string, any>, tabEvents: {}, createChatTab: vi.fn(), setTabConfig: vi.fn(), setTabMetadata: vi.fn() },
  history: vi.fn(), resolve: vi.fn(), startNew: vi.fn(), activate: vi.fn(), hydrate: vi.fn(), prepare: vi.fn(),
}))
vi.mock('../../utils/workspacePaneChat', () => ({ sendWorkspacePaneMessageToChat: vi.fn() }))
vi.mock('../../hooks/useProductUrlView', () => ({ useProductUrlView: vi.fn() }))
vi.mock('../../services/api', () => ({ agentApi: { listChatHistorySessions: mocks.history, resolveAgentProfileConversation: mocks.resolve, startNewAgentProfileConversation: mocks.startNew } }))
vi.mock('../../services/knowledgebaseApi', () => ({ knowledgebaseApi: { bootstrap: async () => ({ chat_workspace: 'Chats/Knowledgebase', backup_configured: mocks.backupConfigured, is_admin: mocks.isAdmin, can_write: mocks.canWrite }), proposals: async () => ({ proposals: [] }), prepareAccess: mocks.prepare }, knowledgebaseError: (error: Error) => error.message }))
vi.mock('../../components/ChatArea', () => ({ default: React.forwardRef((_props: any, ref) => { React.useImperativeHandle(ref, () => ({ submitQuery: mocks.submit })); mocks.chatProps = _props; return <div data-testid="shared-chat">{_props.landingContent}<textarea aria-label="Chat draft" /></div> }) }))
vi.mock('../../components/ModePresetBar', () => ({ ModePresetBar: () => null }))
vi.mock('../../components/topbar/LlmModalHost', () => ({ default: () => null }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: Object.assign((select: any) => select(mocks.state), { getState: () => mocks.state }), waitForChatStoreHydration: async () => {} }))
vi.mock('../../stores/useModeStore', () => ({ useModeStore: { getState: () => ({ setModeCategory: vi.fn() }) } }))
vi.mock('../../stores/useAppStore', () => ({ useAppStore: Object.assign((select: any) => select({}), { getState: () => ({ setAgentMode: vi.fn() }) }) }))
vi.mock('../../stores/useLLMStore', () => ({ useLLMStore: Object.assign((select: any) => select({}), { getState: () => ({}) }) }))
vi.mock('../../utils/sessionRestore', () => ({ hydrateTabEvents: mocks.hydrate }))
vi.mock('../../utils/activateTab', () => ({ activateTab: mocks.activate }))
vi.mock('./KnowledgebaseWorkspacePane', () => ({ KnowledgebaseWorkspacePane: ({ view, modelSettings, onAsk, onRefresh, onOpenAccess, backup, crew }: any) => <div data-testid="workspace">{view === 'models' ? modelSettings : view === 'backup' ? backup : view === 'crew' ? crew : view}{view === 'library' && <><button aria-label="Refresh files" onClick={onRefresh} /><button onClick={onOpenAccess}>Folder access</button></>}{onAsk && <button onClick={() => void onAsk()}>Inspect selected folder</button>}</div> }))
vi.mock('./KnowledgebaseUseCrew', () => ({ KnowledgebaseUseCrew: ({ onPreviousChat }: any) => <button onClick={onPreviousChat}>Open previous Brain conversation</button> }))
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
  mocks.prepare.mockResolvedValue({ proposal_id: 'proposal' }); mocks.chatProps = {}; mocks.state.chatTabs = {}
  mocks.history.mockResolvedValue({ sessions: [{ session_id: 'old', title: 'Previous Brain conversation' }] })
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
  if (label === 'Open previous Brain conversation' && !host.textContent?.includes(label)) await click(host, 'Use in a Crew')
  await act(async () => {
    const button = [...host.querySelectorAll<HTMLButtonElement>('button')].find(button => (button.getAttribute('aria-label') ?? button.textContent?.trim())?.startsWith(label) && !button.closest('[hidden]'))
    expect(button, label).toBeDefined(); button!.click()
  })
}
describe('Brain library with a secondary access chat', () => {
  it('opens the library and shared overview without creating or restoring a chat', async () => {
    const host = await mount()
    expect(host.querySelector('h1')?.textContent).toBe('Brain')
    expect(host.querySelector('[data-testid="workspace"]')?.textContent).toContain('library')
    expect(host.querySelector('[aria-label="Workspace context"]')).not.toBeNull()
    expect(host.querySelector('[aria-label="Previous Brain conversation"]')).toBeNull()
    expect(host.querySelector('[data-testid="shared-chat"]')).toBeNull()
    expect(host.querySelector('[aria-label="More workspace tools"]')).toBeNull()
    expect(host.querySelector('[aria-label="Refresh Brain"]')).toBeNull()
    expect(host.querySelector('[aria-label="Back to library"]')).toBeNull()
    expect(host.querySelectorAll('[aria-label="Refresh files"]')).toHaveLength(1)
    expect(mocks.resolve).not.toHaveBeenCalled(); expect(mocks.state.createChatTab).not.toHaveBeenCalled()
    expect(mocks.submit).not.toHaveBeenCalled()
    expect(host.querySelector('[data-testid="existing-approval-flow"]')).not.toBeNull()
    for (const label of ['Folder access', 'Use in a Crew', 'Connect AI tools', 'Backup']) expect(host.textContent).toContain(label)
  })
  it('opens the existing chat explicitly without sending and preserves it and its draft when closed', async () => {
    const host = await mount()
    await click(host, 'Open previous Brain conversation')
    expect(mocks.history).toHaveBeenCalledWith(1, 0, 'Chats/Knowledgebase')
    expect(mocks.resolve).not.toHaveBeenCalled(); expect(mocks.chatProps.hideInput).toBe(true); expect(mocks.chatProps.forcePreviousChats).not.toBe(true)
    expect(mocks.submit).not.toHaveBeenCalled()
    expect(mocks.chatProps.compact).toBe(true); expect(mocks.chatProps.showProductSteerAction).toBeUndefined()
    expect(mocks.chatProps.showProductTerminalControl).toBeUndefined()
    expect(host.querySelector('[aria-label="Previous Brain conversation"]')).not.toBeNull()
    const chat = host.querySelector('[data-testid="shared-chat"]')!
    const draft = host.querySelector<HTMLTextAreaElement>('[aria-label="Chat draft"]')!
    draft.value = 'Unsent draft'
    await click(host, 'Back to Brain overview')
    expect(host.querySelector('[aria-label="Brain overview"]')).not.toBeNull()
    expect(host.querySelector('[data-testid="shared-chat"]')).toBe(chat)
    await click(host, 'Open previous Brain conversation')
    expect(host.querySelector('[aria-label="Chat draft"]')).toBe(draft)
    expect(draft.value).toBe('Unsent draft')
    expect(mocks.history).toHaveBeenCalledTimes(1)
  })
  it('keeps access management out of chat and exposes only the simple primary panels', async () => {
    const host = await mount()
    await click(host, 'Folder access')
    expect(host.querySelector('[data-testid="workspace"]')?.textContent).toContain('access')
    expect(mocks.resolve).not.toHaveBeenCalled()
    expect(host.textContent).not.toContain('Assistant model')
    expect(host.textContent).not.toContain('Access chat')
    expect(usePanelSwitcherStore.getState().entries.knowledgebase?.panels.map(item => item.id)).toEqual(['library', 'access', 'backup', 'crew'])
  })
  it('prepares backup through the form without sending anything to an assistant', async () => {
    const host = await mount()
    await click(host, 'Backup'); await click(host, 'Set up backup')
    const form = host.querySelector('form')!
    const inputs = form.querySelectorAll('input')
    await act(async () => {
      for (const [input,value] of [[inputs[0],'https://github.com/org/brain.git'],[inputs[1],'git']] as const) {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input,value)
        input.dispatchEvent(new Event('input', { bubbles: true }))
      }
    })
    await act(async () => form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
    expect(mocks.prepare).toHaveBeenCalledWith(expect.objectContaining({ action: 'configure_backup', remote_url: 'https://github.com/org/brain.git', username: 'git' }))
    expect(host.textContent).toContain('Nothing has been applied yet')
    expect(mocks.resolve).not.toHaveBeenCalled(); expect(mocks.submit).not.toHaveBeenCalled()
  })
  it('keeps non-admin writers in their existing bounds and gives readers only the library', async () => {
    mocks.isAdmin = false
    const host = await mount()
    await click(host, 'Backup')
    expect(host.textContent).toContain('Ask an administrator')
    expect(host.textContent).not.toContain('Set up backup')
    await click(host, 'Library')
    expect(host.textContent).not.toContain('Configure backup')
    await click(host, 'Library')
    mocks.canWrite = false
    await click(host, 'Refresh files')
    expect(host.querySelector('[data-testid="knowledgebase-reader"]')).not.toBeNull()
    expect(host.querySelector('[data-testid="existing-approval-flow"]')).toBeNull()
    expect(host.querySelector('[data-testid="shared-chat"]')).toBeNull()
    expect(host.textContent).not.toContain('Access chat')
    expect(host.textContent).not.toContain('Secrets')
    expect(usePanelSwitcherStore.getState().entries.knowledgebase?.panels.map(item => item.id)).toEqual(['library'])
    await act(async () => usePanelSwitcherStore.getState().entries.knowledgebase?.open('models'))
    expect(mocks.resolve).not.toHaveBeenCalled()
    expect(host.querySelector('h1')?.textContent).toBe('Brain')
  })
  it('shows configured backup settings on demand without preparing the assistant', async () => {
    mocks.backupConfigured = true
    const host = await mount()
    await click(host, 'Backup')
    expect(host.querySelector('[aria-label="Brain backup"]')?.textContent).toContain('Backup configured')
    expect(host.querySelector('[aria-label="Brain backup"]')?.textContent).toContain('Change backup settings')
    expect(host.querySelector('[data-testid="knowledgebase-backup-banner"]')).toBeNull()
    expect(mocks.resolve).not.toHaveBeenCalled()
    expect(mocks.submit).not.toHaveBeenCalled()
  })
  it('returns to the library from the product title and shared page home event', async () => {
    const host = await mount()
    await click(host, 'Backup')
    await act(async () => host.querySelector<HTMLButtonElement>('h1 button')!.click())
    expect(host.querySelector('[data-testid="workspace"]')?.textContent).toContain('library')
    await click(host, 'Backup')
    await act(async () => { window.dispatchEvent(new CustomEvent('open-product-home', { detail: 'knowledgebase' })) })
    expect(host.querySelector('[data-testid="workspace"]')?.textContent).toContain('library')
    expect(mocks.resolve).not.toHaveBeenCalled()
    expect(mocks.submit).not.toHaveBeenCalled()
  })
  it('retains lazy chat errors without blocking the library and supports retry and conversation rotation', async () => {
    mocks.history.mockRejectedValueOnce(new Error('History unavailable'))
    const host = await mount()
    await click(host, 'Open previous Brain conversation')
    expect(host.textContent).toContain('History unavailable')
    expect(host.querySelector('[data-testid="workspace"]')).not.toBeNull()
    await click(host, 'Retry history')
    expect(mocks.chatProps.hideInput).toBe(true); expect(mocks.chatProps.forcePreviousChats).not.toBe(true)
    expect(mocks.chatProps.onNewChat).toBeUndefined()
    expect(mocks.startNew).not.toHaveBeenCalled()
  })
  it('does not create a replacement when no old conversation exists', async () => {
    mocks.history.mockResolvedValue({ sessions: [] })
    const host = await mount(); await click(host, 'Open previous Brain conversation')
    expect(host.textContent).toContain('No previous Brain conversations')
    expect(mocks.resolve).not.toHaveBeenCalled(); expect(mocks.startNew).not.toHaveBeenCalled(); expect(mocks.state.createChatTab).not.toHaveBeenCalled()
  })
  it('discards a delayed conversation response after write access is revoked', async () => {
    let resolve!: (value: any) => void
    mocks.history.mockReturnValueOnce(new Promise(done => { resolve = done }))
    const host = await mount()
    await click(host, 'Open previous Brain conversation')
    await click(host, 'Back to Brain overview')
    await click(host, 'Library')
    mocks.canWrite = false
    await click(host, 'Refresh files')
    await act(async () => resolve({ sessions: [{ session_id: 'old' }] }))
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
