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
              { Name: 'Slack', Key: 'slack', URL: 'https://z/mcp', OAuth: true },
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

  it('expands a gateway server to show its tools with args and descriptions', async () => {
    const withTools = (url: string) => {
      if (url.endsWith('/api/admin/tools')) {
        const schema = btoa(JSON.stringify({
          properties: { q: { type: 'string', description: 'Search query' } },
          required: ['q'],
        }))
        return Promise.resolve(
          jsonResponse(200, {
            tools: [
              { ConnectorID: 'c1', WorkspaceID: 'w1', UpstreamName: 'search', PublicName: 'notion__search', Description: 'Searches notes', InputSchema: schema, Status: 'active', DiscoveredAt: '' },
            ],
          }),
        )
      }
      return healthyFetch()(url)
    }
    await renderPanel(vi.fn(withTools))

    expect(container!.textContent).not.toContain('notion__search')
    await act(async () => {
      ;(container!.querySelector('[aria-label="Show 1 tool on Notion"]') as HTMLButtonElement).click()
    })
    await act(async () => {})

    expect(container!.textContent).toContain('notion__search')
    expect(container!.textContent).toContain('Searches notes')
    expect(container!.textContent).toContain('q: string*')
  })

  it('reviews and approves the exact quarantined tool version', async () => {
    const tool = {
      ConnectorID: 'c1', WorkspaceID: 'w1', UpstreamName: 'search', PublicName: 'notion__search',
      Description: 'Search notes', InputSchema: btoa('{"type":"object"}'), Status: 'quarantined',
      Version: 2, Fingerprint: 'fingerprint-2', ApprovedFingerprint: 'fingerprint-1', DiscoveredAt: '',
    }
    const fetchMock = vi.fn((url: string, init?: RequestInit) => {
      if (url.endsWith('/api/admin/tools')) return Promise.resolve(jsonResponse(200, { tools: [tool] }))
      if (url.endsWith('/api/admin/tools/notion__search/versions')) return Promise.resolve(jsonResponse(200, { versions: [{ ...tool, Version: 1, Status: 'active' }] }))
      if (url.endsWith('/api/admin/tools/notion__search/approve') && init?.method === 'POST') return Promise.resolve(jsonResponse(200, { ...tool, Status: 'active' }))
      return healthyFetch()(url)
    })
    await renderPanel(fetchMock)
    await act(async () => { (container!.querySelector('[aria-label="Show 1 tool on Notion"]') as HTMLButtonElement).click() })
    await act(async () => { ([...container!.querySelectorAll('button')].find(button => button.textContent === 'Review details') as HTMLButtonElement).click() })
    await act(async () => {})
    expect(container!.textContent).toContain('Current input schema')
    expect(container!.textContent).toContain('Previous v1')

    await act(async () => { ([...container!.querySelectorAll('button')].find(button => button.textContent?.includes('Approve v2')) as HTMLButtonElement).click() })
    expect(fetchMock).toHaveBeenCalledWith(`${BASE}/api/admin/tools/notion__search/approve`, expect.objectContaining({
      method: 'POST', body: JSON.stringify({ fingerprint: 'fingerprint-2', version: 2 }),
    }))
  })

  it('adds custom servers from pasted mcpServers JSON', async () => {
    const fetchMock = vi.fn(healthyFetch())
    await renderPanel(fetchMock)

    fetchMock.mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === 'POST' && url.endsWith('/api/admin/connectors')) {
        return Promise.resolve(jsonResponse(201, { ID: 'c9' }))
      }
      return healthyFetch()(url)
    })

    const box = container!.querySelector('[data-testid="gateway-add-json"]') as HTMLTextAreaElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(
        box,
        '{"mcpServers": {"acme": {"url": "https://acme.example.com/mcp"}}}',
      )
      box.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      ;(container!.querySelector('[data-testid="gateway-add-submit"]') as HTMLButtonElement).click()
    })
    await act(async () => {})

    expect(fetchMock).toHaveBeenCalledWith(
      `${BASE}/api/admin/connectors`,
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ Provider: 'acme', Label: '', Slug: '', URL: 'https://acme.example.com/mcp' }),
      }),
    )
    expect(container!.textContent).toContain('Added 1 server: acme')
  })

  it('offers catalog-only servers with one-click add on a fresh workspace', async () => {
    await renderPanel(vi.fn(healthyFetch()))

    // Slack is in neither AgentWorks nor the gateway: it still gets a row.
    expect(container!.textContent).toContain('Slack')
    const add = container!.querySelector('[data-testid="gateway-add-slack"]')
    expect(add).not.toBeNull()
    expect(add!.textContent).toContain('Add to gateway')
  })

  it('adds a custom server by name and URL', async () => {
    const fetchMock = vi.fn(healthyFetch())
    await renderPanel(fetchMock)

    fetchMock.mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === 'POST' && url.endsWith('/api/admin/connectors')) {
        return Promise.resolve(jsonResponse(201, { ID: 'c9' }))
      }
      return healthyFetch()(url)
    })

    const name = container!.querySelector('[data-testid="gateway-add-name"]') as HTMLInputElement
    const address = container!.querySelector('[data-testid="gateway-add-url"]') as HTMLInputElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(name, 'acme')
      name.dispatchEvent(new Event('input', { bubbles: true }))
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(address, 'https://acme.example.com/mcp')
      address.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      ;(container!.querySelector('[data-testid="gateway-add-custom-submit"]') as HTMLButtonElement).click()
    })
    await act(async () => {})

    expect(fetchMock).toHaveBeenCalledWith(
      `${BASE}/api/admin/connectors`,
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ Provider: 'acme', Label: '', Slug: '', URL: 'https://acme.example.com/mcp' }),
      }),
    )
  })

  it('rejects non-https custom URLs without calling the API', async () => {
    const fetchMock = vi.fn(healthyFetch())
    await renderPanel(fetchMock)
    const callsBefore = fetchMock.mock.calls.length

    const name = container!.querySelector('[data-testid="gateway-add-name"]') as HTMLInputElement
    const address = container!.querySelector('[data-testid="gateway-add-url"]') as HTMLInputElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(name, 'acme')
      name.dispatchEvent(new Event('input', { bubbles: true }))
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(address, 'http://acme.example.com/mcp')
      address.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      ;(container!.querySelector('[data-testid="gateway-add-custom-submit"]') as HTMLButtonElement).click()
    })
    await act(async () => {})

    expect(fetchMock.mock.calls.length).toBe(callsBefore)
    expect(container!.textContent).toContain('must be https')
  })

  it('warns on stale data when a refetch fails instead of hiding it', async () => {
    const fetchMock = vi.fn(healthyFetch())
    await renderPanel(fetchMock)
    expect(container!.textContent).toContain('Notion')

    fetchMock.mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === 'POST' && url.endsWith('/api/admin/connectors/c1/sync')) {
        return Promise.resolve(jsonResponse(200, { status: 'synced' }))
      }
      if (url.endsWith('/api/admin/tools')) {
        return Promise.resolve(jsonResponse(500, { error: 'boom' }))
      }
      return healthyFetch()(url)
    })
    await act(async () => {
      ;(container!.querySelector('[aria-label="Sync Notion"]') as HTMLButtonElement).click()
    })
    await act(async () => {})
    await act(async () => {})

    expect(container!.textContent).toContain('Could not refresh')
    expect(container!.textContent).toContain('may be outdated')
    expect(container!.textContent).toContain('Notion')
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
