// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { GatewayServersPanel } from './GatewayServersPanel'

vi.mock('../../stores/useMCPStore', () => ({
  useMCPStore: (selector: (state: Record<string, unknown>) => unknown) =>
    selector({
      toolList: [
        { name: 't1', server: 'Notion', connection: 'connected', status: 'ready', function_names: ['a', 'b'] },
        { name: 't2', server: 'Linear', connection: 'available', function_names: [] },
      ],
    }),
}))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const BASE = 'http://127.0.0.1:18161'

function jsonResponse(status: number, body: unknown): Response {
  return { ok: status >= 200 && status < 300, status, json: () => Promise.resolve(body) } as Response
}

describe('GatewayServersPanel', () => {
  let container: HTMLDivElement | null = null

  afterEach(() => {
    container?.remove()
    container = null
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  async function renderPanel(fetchMock: ReturnType<typeof vi.fn>): Promise<void> {
    vi.stubGlobal('fetch', fetchMock)
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => {
      root.render(<GatewayServersPanel base={BASE} />)
    })
    await act(async () => {})
  }

  function healthyFetch(): (url: string, init?: RequestInit) => Promise<Response> {
    return (url: string) => {
      if (url.endsWith('/api/admin/connectors')) {
        return Promise.resolve(
          jsonResponse(200, {
            connectors: [
              { ID: 'c1', WorkspaceID: 'w1', Provider: 'notion', InstanceSlug: '', Label: 'Notion', UpstreamURL: 'https://x/mcp', Status: 'active' },
            ],
          }),
        )
      }
      if (url.endsWith('/api/admin/catalog')) {
        return Promise.resolve(
          jsonResponse(200, {
            providers: [
              { Name: 'Notion', Key: 'notion', URL: 'https://x/mcp', OAuth: false },
              { Name: 'Linear', Key: 'linear', URL: 'https://y/mcp', OAuth: false },
            ],
          }),
        )
      }
      if (url.endsWith('/api/admin/tools')) return Promise.resolve(jsonResponse(200, { tools: [] }))
      return Promise.resolve(jsonResponse(404, { error: 'nope' }))
    }
  }

  it('centralizes AgentWorks servers and gateway connectors in one list', async () => {
    await renderPanel(vi.fn(healthyFetch()))

    expect(container!.querySelector('[data-testid="gateway-servers"]')).not.toBeNull()
    // Notion: connected in AgentWorks (2 tools) and present in the gateway.
    expect(container!.textContent).toContain('Notion')
    expect(container!.textContent).toContain('2 tools')
    // Linear: in AgentWorks but not the gateway, with a catalog match → one-click add.
    const add = container!.querySelector('[data-testid="gateway-add-linear"]')
    expect(add).not.toBeNull()
    expect(add!.textContent).toContain('Add to gateway')
  })

  it('filters the list by search text', async () => {
    await renderPanel(vi.fn(healthyFetch()))

    const search = container!.querySelector('[data-testid="gateway-servers-search"]') as HTMLInputElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(search, 'linear')
      search.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {})

    expect(container!.textContent).toContain('Linear')
    expect(container!.textContent).not.toContain('Notion')
    expect(container!.textContent).toContain('1 server')
  })

  it('adds an AgentWorks server to the gateway from its catalog template', async () => {
    const fetchMock = vi.fn(healthyFetch())
    await renderPanel(fetchMock)

    fetchMock.mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === 'POST' && url.endsWith('/api/admin/connectors')) {
        return Promise.resolve(jsonResponse(201, { ID: 'c2' }))
      }
      return healthyFetch()(url)
    })

    await act(async () => {
      ;(container!.querySelector('[data-testid="gateway-add-linear"]') as HTMLButtonElement).click()
    })
    await act(async () => {})

    expect(fetchMock).toHaveBeenCalledWith(
      `${BASE}/api/admin/connectors`,
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ Provider: 'Linear', Label: '', Slug: '', URL: '' }),
      }),
    )
  })
})
