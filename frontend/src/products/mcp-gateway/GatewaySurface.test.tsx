// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { GatewaySurface } from './GatewaySurface'
const auth = vi.hoisted(() => ({ user: { is_admin: true } as { is_admin: boolean } | null, checkAuth: vi.fn() }))

const chatRuntime = vi.hoisted(() => {
  const state = { chatTabs: {} as Record<string, any>, tabEvents: {}, createChatTab: vi.fn(), setTabConfig: vi.fn(), setTabMetadata: vi.fn() }
  return { state, resolve: vi.fn(), startNew: vi.fn(), hydrate: vi.fn(), activate: vi.fn() }
})
vi.mock('../../services/api', () => ({
  getApiBaseUrl: () => 'http://127.0.0.1:18161', getAuthToken: () => 'product-jwt',
  authApi: { listAdminUsers: async () => ({ users: [], products: [] }) },
  agentApi: { getToolDetail: vi.fn(), resolveAgentProfileConversation: chatRuntime.resolve, startNewAgentProfileConversation: chatRuntime.startNew },
}))
vi.mock('../../components/ModePresetBar', () => ({ ModePresetBar: ({ productActions }: any) => <header data-testid="shared-header"><button aria-label="Switch product">Vault</button>{productActions}</header> }))
vi.mock('../../components/ChatArea', () => ({ default: ({ tabId, landingContent, composerPlaceholder }: any) => <div data-testid="shared-chat-area" data-tab-id={tabId}>{landingContent}<div data-testid="tour-chat-input-area"><textarea data-testid="chat-input-textarea" placeholder={composerPlaceholder} /></div></div> }))
vi.mock('../../components/GlobalHumanFeedbackPrompt', () => ({ GlobalHumanFeedbackPrompt: () => null }))
vi.mock('../../components/UpdateProgressToast', () => ({ UpdateProgressToast: () => null }))
vi.mock('../../components/topbar/LlmModalHost', () => ({ default: () => null }))
vi.mock('../../components/SchedulesPage', () => ({ default: () => null }))
vi.mock('../../components/AdminPages', () => ({ default: () => null }))
vi.mock('./GatewayAuditPanel', () => ({ GatewayAuditPanel: ({ tab }: any) => <div data-testid="audit-content">{tab}</div> }))
vi.mock('./GatewayConnectPanel', () => ({ GatewayConnectPanel: () => <div data-testid="endpoint-content">Endpoint connection</div> }))
vi.mock('./GatewayModelSettings', () => ({ GatewayModelSettings: () => <span>Shared provider model settings</span> }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (selector: (state: any) => unknown) => selector(auth) }))
vi.mock('../../stores/useAppStore', () => ({ useAppStore: Object.assign((selector: (state: any) => unknown) => selector({}), { getState: () => ({ setAdminPage: vi.fn(), setShowSchedulesOverview: vi.fn(), setShowWorkflowsOverview: vi.fn() }) }) }))
vi.mock('../../stores/useLLMStore', () => ({ useLLMStore: Object.assign((selector: (state: any) => unknown) => selector({}), { getState: () => ({ setShowLLMModal: vi.fn() }) }) }))
vi.mock('../../stores/useChatStore', () => ({
  useChatStore: Object.assign((selector: (state: any) => unknown) => selector(chatRuntime.state), { getState: () => chatRuntime.state }),
  waitForChatStoreHydration: () => Promise.resolve(),
}))
vi.mock('../../utils/sessionRestore', () => ({ hydrateTabEvents: chatRuntime.hydrate }))
vi.mock('../../utils/activateTab', () => ({ activateTab: chatRuntime.activate }))
vi.mock('../../stores/useProductSurfaceStore', () => ({
  useProductSurfaceStore: (selector: (state: Record<string, unknown>) => unknown) => selector({ productSurface: 'mcp-gateway', setProductSurface: () => {} }),
}))
vi.mock('../../stores/useMCPStore', () => ({
  useMCPStore: (selector: (state: Record<string, unknown>) => unknown) => selector({ toolList: [], refreshTools: async () => {}, isLoadingTools: false, toolsError: null }),
}))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const BASE = 'http://127.0.0.1:18161'

function stubGatewayUrl(url: string | null) {
  const w = window as unknown as { __APP_RUNTIME_CONFIG__?: { gatewayUrl?: string } }
  if (url === null) delete w.__APP_RUNTIME_CONFIG__
  else w.__APP_RUNTIME_CONFIG__ = { gatewayUrl: url }
}

function jsonResponse(status: number, body: unknown): Response {
  return { ok: status >= 200 && status < 300, status, json: () => Promise.resolve(body) } as Response
}

function healthyFetch(): (url: string) => Promise<Response> {
  return (url: string) => {
    if (url.endsWith('/api/admin/setup/status')) return Promise.resolve(jsonResponse(200, { configured: false }))
    if (url.endsWith('/api/admin/access/packages')) return Promise.resolve(jsonResponse(200, { packages: [] }))
    if (url.endsWith('/api/admin/access/history')) return Promise.resolve(jsonResponse(200, { events: [] }))
    if (url.endsWith('/api/admin/users')) return Promise.resolve(jsonResponse(200, { users: [] }))
    if (url.endsWith('/api/admin/connectors')) return Promise.resolve(jsonResponse(200, { connectors: [] }))
    if (url.endsWith('/api/admin/catalog')) return Promise.resolve(jsonResponse(200, { providers: [] }))
    if (url.endsWith('/api/admin/tools')) return Promise.resolve(jsonResponse(200, { tools: [] }))
    if (url.includes('/api/admin/groups')) return Promise.resolve(jsonResponse(200, { groups: [] }))
    if (url.includes('/api/admin/audit')) return Promise.resolve(jsonResponse(200, { events: [] }))
    return Promise.resolve(jsonResponse(404, { error: 'nope' }))
  }
}

describe('GatewaySurface', () => {
  let container: HTMLDivElement | null = null

  afterEach(() => {
    container?.remove()
    container = null
    stubGatewayUrl(null)
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    auth.user = { is_admin: true }
    auth.checkAuth.mockClear()
    sessionStorage.clear()
    window.localStorage?.removeItem(`caplayer_setup_model:${BASE}`)
  })

  async function renderSurface(standalone = false): Promise<void> {
    container = document.createElement('div')
    document.body.appendChild(container)
    chatRuntime.state.chatTabs = {}
    chatRuntime.resolve.mockResolvedValue({ conversation_id: 'conversation', conversation_key: 'main', session_id: 'session' })
    chatRuntime.startNew.mockResolvedValue({ conversation_id: 'conversation-new', conversation_key: 'main', session_id: 'session-new' })
    chatRuntime.state.createChatTab.mockImplementation(async (name: string, metadata: any, sessionId: string) => {
      const id = `tab-${sessionId}`
      chatRuntime.state.chatTabs[id] = { name, metadata, sessionId, tabId: id, isStreaming: false, hasRunningBgAgents: false }
      return id
    })
    const root = createRoot(container)
    await act(async () => {
      root.render(<GatewaySurface standalone={standalone} />)
    })
    await act(async () => {})
  }

  it('opens audit and MCP endpoint as full-width rail pages and returns to the same builder', async () => {
    stubGatewayUrl(BASE)
    vi.stubGlobal('fetch', vi.fn(healthyFetch()))
    await renderSurface()
    const chatId = container!.querySelector('[data-testid="shared-chat-area"]')!.getAttribute('data-tab-id')
    await act(async () => { (container!.querySelector('[aria-label="Audit logs"]') as HTMLButtonElement).click() })
    expect(container!.querySelector('[aria-label="Vault audit page"]')).not.toBeNull()
    expect(container!.querySelector('[data-testid="audit-content"]')!.textContent).toBe('logs')
    expect(container!.querySelector('[data-testid="gateway-setup-panel"]')!.closest('.hidden')).not.toBeNull()
    await act(async () => { (container!.querySelector('[role="tab"][title="Analysis"]') as HTMLButtonElement).click() })
    expect(container!.querySelector('[data-testid="audit-content"]')!.textContent).toBe('analysis')
    await act(async () => { (container!.querySelector('[aria-label="Vault MCP endpoint"]') as HTMLButtonElement).click() })
    expect(container!.querySelector('[aria-label="Vault MCP endpoint page"]')).not.toBeNull()
    expect(container!.querySelector('[aria-label="Vault audit page"]')).toBeNull()
    const back = [...container!.querySelectorAll('button')].find(el => el.textContent?.includes('Back to Vault'))!
    await act(async () => { back.click() })
    expect(container!.querySelector('[aria-label="Vault MCP endpoint page"]')).toBeNull()
    expect(container!.querySelector('[data-testid="gateway-setup-panel"]')!.closest('.hidden')).toBeNull()
    expect(container!.querySelector('[data-testid="shared-chat-area"]')!.getAttribute('data-tab-id')).toBe(chatId)
  })

  it('opens group-based access in the shared workspace', async () => {
    stubGatewayUrl(BASE)
    const fetchMock = vi.fn((url: string, _init?: RequestInit) => healthyFetch()(url))
    vi.stubGlobal('fetch', fetchMock)

    await renderSurface()

    expect(container!.querySelector('[data-testid="gateway-surface"]')).not.toBeNull()
    const menu = container!.querySelector('[aria-label="Vault sections"]')
    expect(menu).not.toBeNull()
    expect(menu!.closest('[aria-label="Vault workspace toolbar"]')).not.toBeNull()
    expect(container!.textContent).toContain('Vault')
    for (const label of ['Connected MCPs', 'Available MCPs', 'People', 'Models']) {
      expect(menu!.querySelector(`[aria-label="${label}"]`)).not.toBeNull()
    }
    expect(menu!.querySelector('[aria-label="Audit"]')).toBeNull()
    expect(menu!.querySelector('[aria-label="Connect"]')).toBeNull()
    expect(container!.querySelector('[aria-label="Vault pages"] [aria-label="Audit logs"]')).not.toBeNull()
    expect(container!.querySelector('[aria-label="Vault pages"] [aria-label="Vault MCP endpoint"]')).not.toBeNull()
    expect(menu!.querySelector('[aria-label="Groups"]')).toBeNull()
    expect(menu!.querySelector('[aria-label="Users"]')).toBeNull()
    expect(menu!.textContent).not.toContain('Tools & Grants')
    expect(container!.textContent).not.toContain('admin token')
    expect(container!.textContent).not.toContain('Sign in')
    expect(container!.querySelector('[data-testid="gateway-setup-panel"]')).not.toBeNull()
    for (const label of ['Mobile preview', 'Tablet preview', 'Laptop preview']) {
      expect(container!.querySelector(`[aria-label="${label}"]`)).not.toBeNull()
    }
    expect(container!.querySelector('[aria-label="Vault chat"]')).not.toBeNull()
    const composer = container!.querySelector('[data-testid="tour-chat-input-area"]')!
    expect(composer).not.toBeNull()
    expect(composer.querySelector('[aria-label="New conversation"]')).toBeNull()
    expect(composer.querySelector('[aria-label="Model and reasoning effort"]')).toBeNull()
    expect(composer.textContent).not.toContain('Choose model')
    expect(composer.textContent).not.toContain('New chat')
    expect(container!.querySelector('[aria-label="Chat tabs"]')!.textContent).toContain('Chat')
    expect(container!.textContent).not.toContain('Vault assistant')
    expect(fetchMock.mock.calls.filter(([url]) => url.endsWith('/api/admin/users'))).toHaveLength(1)
  })

  it('keeps section navigation only in the right workspace toolbar', async () => {
    stubGatewayUrl(BASE)
    vi.stubGlobal('fetch', vi.fn(healthyFetch()))
    await renderSurface()

    expect(container!.querySelector('[aria-label="Gateway workspace"]')).toBeNull()
    const sections = container!.querySelector('[aria-label="Vault sections"]')!
    const servers = sections.querySelector('[aria-label="Connected MCPs"]') as HTMLButtonElement
    expect(container!.querySelectorAll('[aria-label="Connected MCPs"]')).toHaveLength(1)
    await act(async () => { servers.click() })
    await act(async () => {})
    expect(container!.querySelector('[data-testid="mcp-connections-panel"]')).not.toBeNull()
    expect(container!.querySelector('[aria-label="Available servers"]')).toBeNull()
    const chat = container!.querySelector('[aria-label="Vault chat"]')
    await act(async () => { (sections.querySelector('[aria-label="Available MCPs"]') as HTMLButtonElement).click() })
    await act(async () => {})
    expect(container!.querySelector('[aria-label="Available servers"]')).not.toBeNull()
    expect(container!.querySelector('[aria-label="Connected servers"]')).toBeNull()
    expect(container!.querySelector('[aria-label="Vault chat"]')).toBe(chat)
  })

  it('shows users and groups as tabs inside People', async () => {
    stubGatewayUrl(BASE)
    vi.stubGlobal('fetch', vi.fn(healthyFetch()))
    await renderSurface()

    const sections = container!.querySelector('[aria-label="Vault sections"]')!
    await act(async () => { (sections.querySelector('[aria-label="People"]') as HTMLButtonElement).click() })
    await act(async () => {})
    const tabs = container!.querySelector('[role="tablist"][aria-label="People tabs"]')!
    expect(tabs.querySelectorAll('[role="tab"]')).toHaveLength(2)
    expect(container!.querySelector('[data-testid="gateway-users"]')).not.toBeNull()
    await act(async () => { (tabs.querySelector('[role="tab"][title="Groups"]') as HTMLButtonElement).click() })
    await act(async () => {})
    expect(container!.querySelector('[data-testid="gateway-groups"]')).not.toBeNull()
  })

  it('keeps chat visible when the right workspace changes panels', async () => {
    stubGatewayUrl(BASE)
    vi.stubGlobal('fetch', vi.fn(healthyFetch()))
    await renderSurface()

    const chat = container!.querySelector('[aria-label="Vault chat"]')
    const servers = container!.querySelector('[aria-label="Connected MCPs"]') as HTMLButtonElement
    expect(servers).toBeDefined()
    await act(async () => { servers!.click() })
    await act(async () => {})

    expect(container!.querySelector('[aria-label="Vault chat"]')).toBe(chat)
    expect(container!.querySelector('[data-testid="chat-input-textarea"]')).not.toBeNull()
    expect(container!.querySelector('[data-testid="mcp-connections-panel"]')).not.toBeNull()
  })

  it('uses the same product shell for the independent Vault build', async () => {
    stubGatewayUrl(null)
    vi.stubGlobal('fetch', vi.fn(healthyFetch()))
    await renderSurface(true)

    expect(container!.querySelector('[data-testid="gateway-surface"]')).not.toBeNull()
    expect(container!.querySelector('[aria-label="Switch product"]')).not.toBeNull()
    expect(container!.querySelector('[aria-label="Vault chat"]')).not.toBeNull()
    expect(container!.querySelector('[aria-label="Vault sections"]')).not.toBeNull()
  })

  it('shows an error with retry instead of hanging when the gateway is down', async () => {
    stubGatewayUrl(BASE)
    const fetchMock = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'))
    vi.stubGlobal('fetch', fetchMock)

    await renderSurface()

    expect(container!.textContent).toContain('Gateway is unreachable')
    expect(container!.querySelector('[aria-label="Vault sections"]')).not.toBeNull()
    const callsBefore = fetchMock.mock.calls.length
    expect(callsBefore).toBeGreaterThan(0)

    const retry = [...container!.querySelectorAll('button')].find((b) => b.textContent === 'Retry')
    expect(retry).toBeDefined()
    fetchMock.mockImplementation(healthyFetch())
    await act(async () => {
      ;(retry as HTMLButtonElement).click()
    })
    await act(async () => {})

    expect(fetchMock.mock.calls.length).toBeGreaterThan(callsBefore)
    expect(container!.querySelector('[data-testid="gateway-setup-panel"]')).not.toBeNull()
  })

  it('uses the existing product credential and does not offer a separate token login', async () => {
    stubGatewayUrl(BASE)
    const fetchMock = vi.fn((url: string, _init?: RequestInit) => healthyFetch()(url))
    vi.stubGlobal('fetch', fetchMock)
    await renderSurface()
    expect(fetchMock.mock.calls.every(([, init]) => (init?.headers as Record<string, string>)?.Authorization === 'Bearer product-jwt')).toBe(true)
    expect(container!.querySelector('[data-testid="gateway-admin-login"]')).toBeNull()
    expect(container!.textContent).not.toContain('Disconnect gateway')
  })

  it('revalidates the product session after a 401 without creating another login', async () => {
    stubGatewayUrl(BASE)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(401, { error: 'unauthorized' })))
    await renderSurface()
    expect(auth.checkAuth).toHaveBeenCalled()
    expect(container!.querySelector('[data-testid="gateway-admin-login"]')).toBeNull()
  })

  it('denies management to a non-admin without calling the gateway', async () => {
    stubGatewayUrl(BASE)
    auth.user = { is_admin: false }
    const fetchMock = vi.fn((url: string, _init?: RequestInit) => healthyFetch()(url))
    vi.stubGlobal('fetch', fetchMock)
    await renderSurface()
    expect(container!.textContent).toContain('requires an administrator account')
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('keeps access group-based and user account management in People', async () => {
    stubGatewayUrl(BASE)
    const fetchMock = vi.fn(healthyFetch())
    vi.stubGlobal('fetch', fetchMock)
    await renderSurface()
    expect(container!.textContent).not.toContain('Select a group to view its users')
    expect(container!.querySelector('[data-testid="gateway-groups"]')).not.toBeNull()
    expect(container!.textContent).not.toContain('Access packages')
    expect(fetchMock.mock.calls.some(([url]) => url.endsWith('/api/admin/access/packages'))).toBe(false)
    await act(async () => { (container!.querySelector('[aria-label="People"]') as HTMLButtonElement).click() })
    expect(container!.querySelector('[data-testid="gateway-users"]')).not.toBeNull()
    expect(container!.querySelector('[data-testid="gateway-groups"]')).toBeNull()
  })

  it('collapses and reopens each pane without losing the selected workspace', async () => {
    stubGatewayUrl(BASE)
    vi.stubGlobal('fetch', vi.fn(healthyFetch()))
    await renderSurface()
    await act(async () => { (container!.querySelector('[aria-label="Collapse chat panel"]') as HTMLButtonElement).click() })
    expect(container!.querySelector('[aria-label="Vault chat"]')).toBeNull()
    await act(async () => { (container!.querySelector('[aria-label="Show chat panel"]') as HTMLButtonElement).click() })
    expect(container!.querySelector('[aria-label="Vault chat"]')).not.toBeNull()
    await act(async () => { (container!.querySelector('[aria-label="Collapse workspace panel"]') as HTMLButtonElement).click() })
    expect(container!.querySelector('[aria-label="Vault workspace"]')).toBeNull()
    await act(async () => { (container!.querySelector('[aria-label="Show workspace"]') as HTMLButtonElement).click() })
    await act(async () => { (container!.querySelector('[aria-label="Connected MCPs"]') as HTMLButtonElement).click() })
    expect(container!.querySelector('[data-testid="mcp-connections-panel"]')).not.toBeNull()
  })

  it('opens the shared durable profile chat and does not use the legacy setup transport', async () => {
    stubGatewayUrl(BASE)
    const fetchMock = vi.fn(healthyFetch())
    vi.stubGlobal('fetch', fetchMock)
    await renderSurface()
    expect(chatRuntime.resolve).toHaveBeenCalledWith('caplayer', { conversation_key: 'main' }, null)
    expect(chatRuntime.hydrate).toHaveBeenCalledWith('session', expect.objectContaining({ workspacePath: 'Chats/CapLayer' }))
    expect(container!.querySelector('[data-testid="shared-header"]')).not.toBeNull()
    expect(container!.querySelector('[data-testid="shared-chat-area"]')?.getAttribute('data-tab-id')).toBe('tab-session')
    expect(fetchMock.mock.calls.some(([url]) => url.includes('/setup/chat') || url.includes('/setup/status'))).toBe(false)
    expect(container!.querySelector('[aria-label="New conversation"]')).toBeNull()
  })

  it('explains itself when no gateway is configured', async () => {
    stubGatewayUrl(null)
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))

    await renderSurface()

    expect(container!.textContent).toContain('Vault needs an MCP Gateway endpoint')
  })
})
