// @vitest-environment happy-dom
import { requestServiceChat, useServiceChatNavigation } from '../../utils/serviceChatNavigation'
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { GatewaySurface } from './GatewaySurface'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'
import { usePanelSwitcherStore } from '../../stores/usePanelSwitcherStore'
const auth = vi.hoisted(() => ({ user: { id: 'admin', is_admin: true } as { id?: string; is_admin: boolean; is_vault_reader?: boolean; can_create?: boolean } | null, checkAuth: vi.fn() }))
const globalPage = vi.hoisted(() => ({ adminPage: null as string | null, showLLMModal: false }))
const chatRuntime = vi.hoisted(() => {
  const state = { chatTabs: {} as Record<string, any>, tabEvents: {}, activeSessionsCache: [] as any[], createChatTab: vi.fn(), setTabConfig: vi.fn(), setTabMetadata: vi.fn() }
  return { state, resolve: vi.fn(), startNew: vi.fn(), hydrate: vi.fn(), activate: vi.fn(), draft: vi.fn() }
})
vi.mock('../../services/api', () => ({ getApiBaseUrl: () => 'http://127.0.0.1:18161', getAuthToken: () => 'product-jwt', authApi: { listAdminUsers: async () => ({ users: [], products: [] }) }, agentApi: { getToolDetail: vi.fn(), resolveAgentProfileConversation: chatRuntime.resolve, startNewAgentProfileConversation: chatRuntime.startNew } }))
vi.mock('../../components/ModePresetBar', () => ({ ModePresetBar: ({ productActions }: any) => <header><button aria-label="Switch product">Vault</button>{productActions}</header> }))
vi.mock('../../components/ChatArea', () => ({ default: ({ tabId, composerPlaceholder, onNewChat }: any) => <div data-testid="shared-chat-area" data-tab-id={tabId}><textarea placeholder={composerPlaceholder} /><button onClick={onNewChat}>New assistant conversation</button></div> }))
vi.mock('../../components/GlobalHumanFeedbackPrompt', () => ({ GlobalHumanFeedbackPrompt: () => null }))
vi.mock('../../components/UpdateProgressToast', () => ({ UpdateProgressToast: () => null }))
vi.mock('../../components/topbar/LlmModalHost', () => ({ default: () => null }))
vi.mock('../../components/SchedulesPage', () => ({ default: () => null }))
vi.mock('../../components/AdminPages', () => ({ default: () => null }))
vi.mock('../../components/MyVaultsPage', () => ({ default: ({ embedded }: { embedded?: boolean }) => <div data-testid="my-vaults" data-embedded={String(embedded)}>Personal accounts and shared vaults</div> }))
vi.mock('./GatewayAuditPanel', () => ({ GatewayAuditPanel: ({ tab }: any) => <div data-testid="audit-content">{tab}</div> }))
vi.mock('./GatewayConnectPanel', () => ({ GatewayConnectPanel: () => <div data-testid="endpoint-content">Endpoint connection</div> }))
vi.mock('./GatewayModelSettings', () => ({ GatewayModelSettings: () => <span>Shared provider model settings</span> }))
vi.mock('../../components/secrets/SecretSelectionSection', () => ({ SecretSelectionSection: () => <div data-testid="secret-management" /> }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (selector: (state: any) => unknown) => selector(auth) }))
vi.mock('../../stores/useAppStore', () => ({ useAppStore: Object.assign((selector: (state: any) => unknown) => selector(globalPage), { getState: () => ({ ...globalPage, setAdminPage: vi.fn(), setShowSchedulesOverview: vi.fn(), setShowWorkflowsOverview: vi.fn() }) }) }))
vi.mock('../../stores/useLLMStore', () => ({ useLLMStore: Object.assign((selector: (state: any) => unknown) => selector(globalPage), { getState: () => ({ ...globalPage, setShowLLMModal: vi.fn() }) }) }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: Object.assign((selector: (state: any) => unknown) => selector(chatRuntime.state), { getState: () => chatRuntime.state }), waitForChatStoreHydration: () => Promise.resolve() }))
vi.mock('../../utils/sessionRestore', () => ({ hydrateTabEvents: chatRuntime.hydrate }))
vi.mock('../../utils/activateTab', () => ({ activateTab: chatRuntime.activate }))
vi.mock('../../utils/workspacePaneChat', () => ({ openWorkspacePaneChatWithDraft: chatRuntime.draft }))
vi.mock('../../stores/useProductSurfaceStore', () => ({ useProductSurfaceStore: (selector: (state: any) => unknown) => selector({ productSurface: 'mcp-gateway' }) }))
vi.mock('../../stores/useMCPStore', () => ({ useMCPStore: (selector: (state: any) => unknown) => selector({ toolList: [], refreshTools: async () => {}, isLoadingTools: false, toolsError: null }) }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const BASE = 'http://127.0.0.1:18161'
function stubGatewayUrl(url: string | null) { if (url === null) delete window.__APP_RUNTIME_CONFIG__; else window.__APP_RUNTIME_CONFIG__ = { gatewayUrl: url } }
function jsonResponse(status: number, body: unknown): Response { return { ok: status >= 200 && status < 300, status, json: () => Promise.resolve(body) } as Response }
function healthyFetch() { return (url: string) => {
  const responses: Record<string, unknown> = { '/api/admin/setup/status': { configured: false }, '/api/admin/access/packages': { packages: [] }, '/api/admin/access/history': { events: [] }, '/api/admin/users': { users: [] }, '/api/admin/connectors': { connectors: [] }, '/api/admin/catalog': { providers: [] }, '/api/admin/tools': { tools: [] } }
  const path = Object.keys(responses).find(path => url.endsWith(path))
  if (path) return Promise.resolve(jsonResponse(200, responses[path]))
  if (url.includes('/api/admin/groups')) return Promise.resolve(jsonResponse(200, { groups: [] }))
  return Promise.resolve(jsonResponse(404, { error: 'nope' }))
} }

describe('GatewaySurface', () => {
  let container: HTMLDivElement | null = null, surfaceRoot: Root | null = null
  const main = () => container!.querySelector<HTMLElement>('main[aria-label="Vault management"]')!
  const headerMenu = async () => {
    if (!container!.querySelector('[data-product-picker-menu]')) await act(async () => container!.querySelector<HTMLButtonElement>('[aria-label="Switch product: Vault"]')!.click())
    return container!.querySelector<HTMLElement>('[aria-label="Product pages"]')!
  }
  const button = (root: Element, label: string) => [...root.querySelectorAll<HTMLButtonElement>('button')].find(b => b.getAttribute('aria-label') === label || b.textContent?.trim() === label)!
  const shortcut = async (label: string) => { const menu = await headerMenu(); await act(async () => button(menu, label).click()) }
  const openAssistant = () => shortcut('Vault assistant')
  const closeAssistant = async () => { await act(async () => button(container!, 'Close Vault assistant').click()) }
  beforeEach(() => {
    useServiceChatNavigation.setState({ request: null })
    const values = new Map<string, string>()
    vi.stubGlobal('localStorage', { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => values.set(key, value), removeItem: (key: string) => values.delete(key), clear: () => values.clear() })
  })
  afterEach(() => {
    act(() => surfaceRoot?.unmount()); surfaceRoot = null; container?.remove(); container = null
    stubGatewayUrl(null); vi.unstubAllGlobals(); vi.restoreAllMocks()
    auth.user = { id: 'admin', is_admin: true }; auth.checkAuth.mockClear()
    globalPage.adminPage = null; globalPage.showLLMModal = false; sessionStorage.clear(); window.localStorage?.clear()
  })
  async function renderSurface(standalone = false) {
    container = document.createElement('div'); document.body.appendChild(container); chatRuntime.state.chatTabs = {}
    for (const mock of [chatRuntime.resolve, chatRuntime.hydrate, chatRuntime.activate, chatRuntime.draft, chatRuntime.state.createChatTab, chatRuntime.state.setTabMetadata]) mock.mockReset()
    chatRuntime.resolve.mockResolvedValue({ conversation_id: 'conversation', conversation_key: 'main', session_id: 'session' })
    chatRuntime.startNew.mockReset().mockResolvedValue({ conversation_id: 'new', conversation_key: 'main', session_id: 'new' })
    chatRuntime.state.createChatTab.mockImplementation(async (name: string, metadata: any, sessionId: string) => { const tabId = `tab-${sessionId}`; chatRuntime.state.chatTabs[tabId] = { name, metadata, sessionId, tabId, isStreaming: false }; return tabId })
    surfaceRoot = createRoot(container); await act(async () => surfaceRoot!.render(<GatewaySurface standalone={standalone} />))
  }
  function healthy() { stubGatewayUrl(BASE); const mock = vi.fn((url: string, _init?: RequestInit) => healthyFetch()(url)); vi.stubGlobal('fetch', mock); return mock }

  it('opens a selected existing Vault conversation without creating a new tab', async () => {
    healthy(); await renderSurface()
    chatRuntime.state.chatTabs.saved = { tabId: 'saved', sessionId: 'saved-session', metadata: { agentProfileId: 'caplayer', isViewOnly: true } }
    await act(async () => { requestServiceChat('mcp-gateway', 'saved') })
    expect(container!.querySelector('[aria-label="Vault chat"]')?.hasAttribute('hidden')).toBe(false)
    expect(chatRuntime.hydrate).toHaveBeenCalledWith('saved-session', expect.objectContaining({ workspacePath: 'Chats/CapLayer' }))
    expect(chatRuntime.resolve).not.toHaveBeenCalled()
    expect(chatRuntime.state.createChatTab).not.toHaveBeenCalled()
    await act(async () => button(container!, 'New assistant conversation').click())
    expect(container!.querySelector('[data-testid="shared-chat-area"]')?.getAttribute('data-tab-id')).toBe('tab-new')
    await act(async () => { requestServiceChat('mcp-gateway', 'saved') })
    expect(container!.querySelector('[data-testid="shared-chat-area"]')?.getAttribute('data-tab-id')).toBe('saved')
    expect(chatRuntime.state.createChatTab).toHaveBeenCalledTimes(1)
  })

  it('starts with full-width Connected MCPs and all destinations in the header, without preparing a chat', async () => {
    healthy(); await renderSurface()
    expect(main().querySelector('[aria-label="Connected servers"]')).not.toBeNull()
    expect(container!.querySelector('aside[aria-label="Vault overview"]')).toBeNull()
    expect(container!.querySelector('[aria-label="Workspace context"]')).toBeNull()
    expect(container!.querySelector('[aria-label="Show workspace"]')).toBeNull()
    for (const label of ['Access', 'Connected MCPs', 'Available MCPs', 'Secrets', 'People', 'Models', 'Audit logs', 'Vault MCP endpoint', 'Vault assistant']) expect(button(await headerMenu(), label)).toBeDefined()
    expect(button(await headerMenu(), 'Connected MCPs').getAttribute('aria-current')).toBe('page')
    expect(container!.querySelector('[aria-label="Vault chat"]')).toBeNull()
    expect(chatRuntime.resolve).not.toHaveBeenCalled(); expect(chatRuntime.hydrate).not.toHaveBeenCalled()
    expect(container!.querySelector('[aria-label="Vault sections"]')).toBeNull()
    expect(container!.querySelector('[aria-label="Collapse workspace panel"]')).toBeNull()
    expect(container!.querySelector('[aria-label="More workspace tools"]')).toBeNull()
    expect(button(main(), 'Connect MCP')).toBeDefined()
  })
  it('opens My vaults within Vault and keeps it unavailable to Run-only accounts', async () => {
    healthy(); await renderSurface()
    await shortcut('My vaults')
    expect(main().querySelector('[data-testid="my-vaults"]')?.getAttribute('data-embedded')).toBe('true')
    expect(chatRuntime.resolve).not.toHaveBeenCalled()
    auth.user = { id: 'run-person', is_admin: false, is_vault_reader: true, can_create: false }
    await act(async () => surfaceRoot!.render(<GatewaySurface />))
    expect(button(await headerMenu(), 'My vaults')).toBeUndefined()
    expect(main().querySelector('[data-testid="my-vaults"]')).toBeNull()
  })
  it('navigates MCP inventories in main without starting the assistant', async () => {
    healthy(); await renderSurface(); await shortcut('Connected MCPs')
    expect(main().querySelector('[aria-label="Connected servers"]')).not.toBeNull()
    await act(async () => button(main(), 'Connect MCP').click()); expect(main().querySelector('[aria-label="Available servers"]')).not.toBeNull()
    expect(button(await headerMenu(), 'Available MCPs').getAttribute('aria-current')).toBe('page')
    expect(chatRuntime.resolve).not.toHaveBeenCalled()
  })
  it('keeps Users and Groups in People, including Ctrl+K section requests', async () => {
    healthy(); await renderSurface(); await shortcut('People')
    expect(main().querySelector('[data-testid="gateway-users"]')).not.toBeNull()
    await act(async () => usePanelSwitcherStore.getState().entries['mcp-gateway']!.open('people', 'groups'))
    expect(main().querySelector('[data-testid="gateway-groups"]')).not.toBeNull()
    await act(async () => usePanelSwitcherStore.getState().entries['mcp-gateway']!.open('people', 'users'))
    expect(main().querySelector('[data-testid="gateway-users"]')).not.toBeNull(); expect(chatRuntime.resolve).not.toHaveBeenCalled()
  })
  it('keeps audit/analysis and endpoint in main with the bubble, returning to the same management view', async () => {
    healthy(); await renderSurface(); await shortcut('Connected MCPs'); await shortcut('Audit logs')
    expect(main().querySelector('[data-testid="audit-content"]')!.textContent).toBe('logs')
    await act(async () => container!.querySelector<HTMLButtonElement>('[role="tab"][title="Analysis"]')!.click())
    expect(main().querySelector('[data-testid="audit-content"]')!.textContent).toBe('analysis')
    await shortcut('Vault MCP endpoint'); expect(main().querySelector('[data-testid="endpoint-content"]')).not.toBeNull()
    expect(button(await headerMenu(), 'Vault MCP endpoint').getAttribute('aria-current')).toBe('page')
    expect(container!.querySelectorAll('[data-product-navigation-slot]')).toHaveLength(1)
    expect(useProductNavigationToolbarStore.getState().target).toBe(container!.querySelector('[data-product-navigation-slot]'))
    expect(container!.querySelector('[aria-label="Back to Vault management"]')).toBeNull()
    await act(async () => container!.querySelector<HTMLButtonElement>('h1 button')!.click())
    expect(main().querySelector('[aria-label="Connected servers"]')).not.toBeNull(); expect(chatRuntime.resolve).not.toHaveBeenCalled()
  })
  it('opens the existing assistant on the right and retains its draft and management selection', async () => {
    healthy(); await renderSurface(); await shortcut('People'); await openAssistant()
    expect(chatRuntime.resolve).toHaveBeenCalledExactlyOnceWith('caplayer', { conversation_key: 'main' }, null)
    expect(chatRuntime.hydrate).toHaveBeenCalledWith('session', expect.objectContaining({ workspacePath: 'Chats/CapLayer' }))
    expect(main().querySelector('[data-testid="gateway-users"]')).not.toBeNull()
    const chat = container!.querySelector<HTMLElement>('[aria-label="Vault chat"]')!, draft = chat.querySelector('textarea')!
    expect(container!.querySelector('aside[aria-label="Vault assistant panel"]')!.contains(chat)).toBe(true)
    draft.value = 'Unsent setup request'; await closeAssistant(); expect(chat.hidden).toBe(true)
    expect(container!.querySelector<HTMLElement>('aside[aria-label="Vault assistant panel"]')?.hidden).toBe(true); await openAssistant()
    expect(container!.querySelector('[aria-label="Vault chat"]')).toBe(chat); expect(draft.value).toBe('Unsent setup request')
    expect(chatRuntime.resolve).toHaveBeenCalledOnce(); expect(chatRuntime.draft).not.toHaveBeenCalled()
  })
  it('retries an assistant failure while keeping management usable', async () => {
    healthy(); await renderSurface(); chatRuntime.resolve.mockRejectedValueOnce(new Error('Assistant unavailable')); await openAssistant()
    expect(main().querySelector('[aria-label="Connected servers"]')).not.toBeNull()
    expect(container!.querySelector('[role="alert"]')?.textContent).toBe('Assistant unavailable')
    await act(async () => button(container!, 'Retry assistant').click())
    expect(container!.querySelector('[data-testid="shared-chat-area"]')).not.toBeNull(); expect(chatRuntime.resolve).toHaveBeenCalledTimes(2)
  })
  it('prepares Models only on request while leaving the assistant closed', async () => {
    healthy(); await renderSurface(); await shortcut('Models')
    expect(main().textContent).toContain('Shared provider model settings'); expect(chatRuntime.resolve).toHaveBeenCalledOnce()
    expect(container!.querySelector('[aria-label="Vault chat"]')).toBeNull(); await openAssistant(); expect(chatRuntime.resolve).toHaveBeenCalledOnce()
  })
  it('prepares a custom-server draft on first use without sending it', async () => {
    healthy(); await renderSurface(); await shortcut('Available MCPs')
    await act(async () => button(main(), 'Add custom server').click())
    expect(chatRuntime.resolve).toHaveBeenCalledOnce()
    expect(chatRuntime.draft).toHaveBeenCalledExactlyOnceWith({ tabId: 'tab-session', message: 'Help me connect a custom MCP server. Ask me for its name and URL.' })
    expect(container!.querySelector('[aria-label="Vault chat"]')?.hasAttribute('hidden')).toBe(false)
  })
  it('keeps audit and setup visible and removes irrelevant preview sizes', async () => {
    healthy(); await renderSurface()
    expect((await headerMenu()).querySelector('details')).toBeNull()
    for (const label of ['Audit logs', 'Vault MCP endpoint', 'Models']) expect(button(await headerMenu(), label)).toBeDefined()
    expect((await headerMenu()).textContent).not.toContain('Preview size')
    expect((await headerMenu()).textContent).not.toContain('Mobile preview')
    expect(main().querySelector('[data-vault-preview]')).toBeNull()
    expect(chatRuntime.resolve).not.toHaveBeenCalled()
  })
  it('retains new-conversation rotation in the shared assistant', async () => {
    healthy(); await renderSurface(); await openAssistant(); await act(async () => button(container!, 'New assistant conversation').click())
    expect(chatRuntime.startNew).toHaveBeenCalledWith('caplayer', { conversation_key: 'main' })
    expect(chatRuntime.state.setTabMetadata).toHaveBeenCalledWith('tab-session', { isViewOnly: true })
    expect(container!.querySelector('[data-testid="shared-chat-area"]')?.getAttribute('data-tab-id')).toBe('tab-new')
  })
  it('can retry a failed conversation rotation after an earlier chat was ready', async () => {
    healthy(); await renderSurface(); await openAssistant()
    chatRuntime.startNew.mockRejectedValueOnce(new Error('Could not rotate conversation'))
    await act(async () => button(container!, 'New assistant conversation').click())
    expect(container!.querySelector('[role="alert"]')?.textContent).toBe('Could not rotate conversation')
    await act(async () => button(container!, 'Retry assistant').click())
    expect(container!.querySelector('[data-testid="shared-chat-area"]')).not.toBeNull()
    expect(container!.querySelector('[role="alert"]')).toBeNull()
  })
  it.each(['audit', 'connect'])('retains the existing requested %s destination with chat closed', async page => {
    healthy(); sessionStorage.setItem('vault.requested-panel', page); await renderSurface()
    expect(main().querySelector(`[data-testid="${page === 'audit' ? 'audit' : 'endpoint'}-content"]`)).not.toBeNull()
    expect(await headerMenu()).not.toBeNull(); expect(chatRuntime.resolve).not.toHaveBeenCalled()
    await act(async () => window.dispatchEvent(new CustomEvent('vault-open-panel', { detail: 'servers' })))
    expect(main().querySelector('[aria-label="Connected servers"]')).not.toBeNull()
  })
  it('keeps an explicitly requested Access screen available despite the new default', async () => {
    healthy(); sessionStorage.setItem('vault.requested-panel', 'access'); await renderSurface()
    expect(main().querySelector('[data-testid="gateway-groups"]')).not.toBeNull()
    expect(button(await headerMenu(), 'Access').getAttribute('aria-current')).toBe('page')
    expect(chatRuntime.resolve).not.toHaveBeenCalled()
  })
  it.each(['users', 'providers'])('does not dismiss %s when a requested assistant finishes restoring', async page => {
    healthy(); await renderSurface(); let finish!: () => void
    chatRuntime.hydrate.mockImplementationOnce(() => new Promise<void>(resolve => { finish = resolve })); await openAssistant()
    globalPage.adminPage = page === 'users' ? 'users' : null; globalPage.showLLMModal = page === 'providers'
    await act(async () => surfaceRoot!.render(<GatewaySurface />)); await act(async () => finish())
    expect(chatRuntime.activate).not.toHaveBeenCalled(); expect(container!.querySelector('[data-testid="gateway-setup-panel"]')!.closest('.hidden')).not.toBeNull()
  })
  it('ignores a delayed assistant response after access is revoked', async () => {
    healthy(); await renderSurface(); let finish!: (value: any) => void
    chatRuntime.resolve.mockImplementationOnce(() => new Promise(resolve => { finish = resolve })); await openAssistant()
    auth.user = { id: 'admin', is_admin: false }; await act(async () => surfaceRoot!.render(<GatewaySurface />))
    await act(async () => finish({ conversation_key: 'main', conversation_id: 'old', session_id: 'old' }))
    expect(chatRuntime.state.createChatTab).not.toHaveBeenCalled(); expect(container!.querySelector('[aria-label="Vault chat"]')).toBeNull()
  })
  it('preserves reader bounds across shortcuts without exposing mutation controls', async () => {
    auth.user = { id: 'reader', is_admin: false, is_vault_reader: true }; const fetchMock = healthy()
    await renderSurface(); await shortcut('Connected MCPs'); expect(button(main(), 'Add custom server')).toBeUndefined()
    await shortcut('People'); expect(main().textContent).toContain('Accounts are managed by administrators'); expect(main().querySelector('[data-testid="gateway-users"]')).toBeNull()
    await shortcut('Secrets'); expect(main().textContent).toContain('Secret values are managed by Vault managers'); expect(main().querySelector('[data-testid="secret-management"]')).toBeNull()
    await shortcut('Models'); expect(main().textContent).toContain('model is set by Vault managers'); expect(chatRuntime.resolve).not.toHaveBeenCalled()
    expect(fetchMock.mock.calls.every(([, init]) => !init?.method || init.method === 'GET')).toBe(true)
    await openAssistant(); expect(chatRuntime.resolve).toHaveBeenCalledOnce()
  })
  it('uses existing product credentials and revalidates a 401 without a new login', async () => {
    const fetchMock = healthy(); await renderSurface()
    expect(fetchMock.mock.calls.every(([, init]) => (init?.headers as Record<string, string>)?.Authorization === 'Bearer product-jwt')).toBe(true)
    expect(container!.querySelector('[data-testid="gateway-admin-login"]')).toBeNull()
    fetchMock.mockResolvedValue(jsonResponse(401, { error: 'unauthorized' })); await shortcut('Access'); expect(auth.checkAuth).toHaveBeenCalled()
  })
  it('keeps unreachable-gateway retry in main with navigation available', async () => {
    stubGatewayUrl(BASE); const mock = vi.fn().mockRejectedValue(new TypeError('Failed to fetch')); vi.stubGlobal('fetch', mock)
    await renderSurface(); expect(main().textContent).toContain('Gateway is unreachable'); mock.mockImplementation(healthyFetch())
    await act(async () => button(main(), 'Retry').click()); expect(main().querySelector('[aria-label="Connected servers"]')).not.toBeNull()
    expect(await headerMenu()).not.toBeNull(); expect(chatRuntime.resolve).not.toHaveBeenCalled()
  })
  it('denies accounts without Vault access without calling gateway or chat', async () => {
    auth.user = { id: 'ordinary', is_admin: false }; const mock = healthy(); await renderSurface()
    expect(container!.textContent).toContain('needs an administrator or a Vault reader account'); expect(mock).not.toHaveBeenCalled(); expect(chatRuntime.resolve).not.toHaveBeenCalled()
    expect(container!.querySelector('[aria-label="Workspace context"]')).toBeNull()
  })
  it('uses the shared management layout in the standalone Vault build', async () => {
    stubGatewayUrl(null); vi.stubGlobal('fetch', vi.fn(healthyFetch())); await renderSurface(true)
    expect(main()).not.toBeNull(); expect(await headerMenu()).not.toBeNull(); expect(container!.querySelector('[aria-label="Switch product"]')).not.toBeNull(); expect(chatRuntime.resolve).not.toHaveBeenCalled()
  })
  it('explains when a gateway is not configured', async () => {
    stubGatewayUrl(null); vi.stubGlobal('fetch', vi.fn(healthyFetch())); await renderSurface()
    expect(container!.textContent).toContain('Vault needs an MCP Gateway endpoint'); expect(container!.querySelector('[aria-label="Workspace context"]')).toBeNull()
  })
})
