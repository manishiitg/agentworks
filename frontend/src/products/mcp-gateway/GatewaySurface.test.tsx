// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { GatewaySurface } from './GatewaySurface'

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
  })

  async function renderSurface(): Promise<void> {
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => {
      root.render(<GatewaySurface />)
    })
    // Flush the ping fetch, then the panel fetch it unlocks.
    await act(async () => {})
    await act(async () => {})
  }

  it('renders the console tabs with no token prompt when the gateway answers', async () => {
    stubGatewayUrl(BASE)
    vi.stubGlobal('fetch', vi.fn(healthyFetch()))

    await renderSurface()

    expect(container!.querySelector('[data-testid="gateway-surface"]')).not.toBeNull()
    expect(container!.textContent).toContain('Connections')
    expect(container!.textContent).toContain('Tools & Grants')
    expect(container!.textContent).toContain('Audit')
    expect(container!.textContent).toContain(`${BASE}/mcp`)
    expect(container!.textContent).toContain('Connected')
    expect(container!.textContent).not.toContain('admin token')
    expect(container!.textContent).not.toContain('Sign in')
  })

  it('shows an error with retry instead of hanging when the gateway is down', async () => {
    stubGatewayUrl(BASE)
    const fetchMock = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'))
    vi.stubGlobal('fetch', fetchMock)

    await renderSurface()

    expect(container!.textContent).toContain('Unreachable')
    expect(container!.textContent).toContain('Gateway is unreachable')
    const callsBefore = fetchMock.mock.calls.length
    expect(callsBefore).toBeGreaterThan(0)

    const retry = [...container!.querySelectorAll('button')].find((b) => b.textContent === 'Retry')
    expect(retry).toBeDefined()
    fetchMock.mockImplementation(healthyFetch())
    await act(async () => {
      ;(retry as HTMLButtonElement).click()
    })
    await act(async () => {})
    await act(async () => {})

    expect(fetchMock.mock.calls.length).toBeGreaterThan(callsBefore)
    expect(container!.textContent).toContain('Connected')
  })

  it('explains itself when no gateway is configured', async () => {
    stubGatewayUrl(null)
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))

    await renderSurface()

    expect(container!.textContent).toContain('No MCP Gateway is configured')
  })
})
