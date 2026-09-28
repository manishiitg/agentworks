// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { GatewaySurface } from './GatewaySurface'
import { gatewayAdminToken, setGatewayAdminToken } from './gatewayAdminApi'

vi.mock('../../services/api', () => ({ agentApi: { getToolDetail: vi.fn() } }))

// The surface embeds the product switcher and the AgentWorks MCP store. Mock
// the stores (notably useAuthStore, which pulls services/api's eager side
// effect) the same way other component tests in this repo do.
vi.mock('../../stores/useAuthStore', () => ({
  useAuthStore: (selector: (state: Record<string, unknown>) => unknown) => selector({ user: undefined }),
}))
vi.mock('../../stores/useAppStore', () => ({
  useAppStore: { getState: () => ({}) },
}))
vi.mock('../../stores/useProductSurfaceStore', () => ({
  useProductSurfaceStore: (selector: (state: Record<string, unknown>) => unknown) =>
    selector({ productSurface: 'mcp-gateway', setProductSurface: () => {} }),
}))
vi.mock('../../stores/useMCPStore', () => ({
  useMCPStore: (selector: (state: Record<string, unknown>) => unknown) => selector({
    toolList: [], refreshTools: async () => {}, isLoadingTools: false, toolsError: null,
  }),
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
    sessionStorage.clear()
  })

  async function renderSurface(): Promise<void> {
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => {
      root.render(<GatewaySurface />)
    })
    await act(async () => {})
  }

  it('opens the servers panel without an extra user-list request', async () => {
    stubGatewayUrl(BASE)
    const fetchMock = vi.fn(healthyFetch())
    vi.stubGlobal('fetch', fetchMock)

    await renderSurface()

    expect(container!.querySelector('[data-testid="gateway-surface"]')).not.toBeNull()
    const menu = container!.querySelector('[aria-label="CapLayer sections"]')
    expect(menu).not.toBeNull()
    expect(container!.textContent).toContain('CapLayer')
    expect(menu!.textContent).toContain('MCP Gateway')
    expect(menu!.textContent).toContain('Groups')
    expect(menu!.textContent).toContain('Users')
    expect(menu!.textContent).toContain('Audit')
    expect(menu!.textContent).toContain('Connect')
    expect(menu!.textContent).not.toContain('Tools & Grants')
    expect(container!.textContent).not.toContain('admin token')
    expect(container!.textContent).not.toContain('Sign in')
    expect(container!.querySelector('[data-testid="gateway-servers"]')).not.toBeNull()
    expect(fetchMock.mock.calls.map(([url]) => url)).not.toContain(`${BASE}/api/admin/users`)
  })

  it('shows an error with retry instead of hanging when the gateway is down', async () => {
    stubGatewayUrl(BASE)
    const fetchMock = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'))
    vi.stubGlobal('fetch', fetchMock)

    await renderSurface()

    expect(container!.textContent).toContain('Gateway is unreachable')
    expect(container!.querySelector('[aria-label="CapLayer sections"]')).not.toBeNull()
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
    expect(container!.querySelector('[data-testid="gateway-servers"]')).not.toBeNull()
  })

  it('prompts for the admin token after a 401 and sends it as a bearer credential', async () => {
    stubGatewayUrl(BASE)
    const fetchMock = vi.fn((url: string, init?: RequestInit) => {
      const headers = init?.headers as Record<string, string> | undefined
      if (headers?.Authorization !== 'Bearer test-admin-secret') {
        return Promise.resolve(jsonResponse(401, { error: 'unauthorized' }))
      }
      return healthyFetch()(url)
    })
    vi.stubGlobal('fetch', fetchMock)
    await renderSurface()

    const form = container!.querySelector('[data-testid="gateway-admin-login"]') as HTMLFormElement
    expect(form).not.toBeNull()
    const input = form.querySelector('input') as HTMLInputElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'test-admin-secret')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => { form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })
    await act(async () => {})

    expect(container!.querySelector('[data-testid="gateway-admin-login"]')).toBeNull()
    expect(container!.querySelector('[data-testid="gateway-servers"]')).not.toBeNull()
    expect(fetchMock.mock.calls.some(([, init]) => (init?.headers as Record<string, string>)?.Authorization === 'Bearer test-admin-secret')).toBe(true)
  })

  it('clears the browser token and shows sign-in when the admin signs out', async () => {
    stubGatewayUrl(BASE)
    setGatewayAdminToken(BASE, 'local-test-secret')
    vi.stubGlobal('fetch', vi.fn(healthyFetch()))
    await renderSurface()

    const signOut = [...container!.querySelectorAll('button')].find(button => button.textContent === 'Sign out')
    expect(signOut).toBeDefined()
    await act(async () => { signOut!.click() })
    expect(gatewayAdminToken(BASE)).toBe('')
    expect(container!.querySelector('[data-testid="gateway-admin-login"]')).not.toBeNull()
  })

  it('explains itself when no gateway is configured', async () => {
    stubGatewayUrl(null)
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))

    await renderSurface()

    expect(container!.textContent).toContain('CapLayer needs an MCP Gateway endpoint')
  })
})
