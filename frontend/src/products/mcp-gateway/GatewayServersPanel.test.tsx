// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { GatewayServersPanel } from './GatewayServersPanel'
import { agentApi } from '../../services/api'
import { TooltipProvider } from '../../components/ui/tooltip'

const refreshTools = vi.fn(async () => {})

vi.mock('../../services/api', () => ({ getApiBaseUrl: () => 'http://127.0.0.1:18161', getAuthToken: () => 'product-jwt', agentApi: { getToolDetail: vi.fn() } }))

vi.mock('../../components/OAuthStatusBadge', () => ({
  default: ({ serverName, connectLabel, onAuthChange }: { serverName: string; connectLabel: string; onAuthChange: (valid: boolean) => void }) =>
    <button aria-label={`OAuth ${serverName}`} onClick={() => onAuthChange(true)}>{connectLabel}</button>,
}))

vi.mock('../../stores/useMCPStore', () => ({
  useMCPStore: (selector: (state: Record<string, unknown>) => unknown) =>
    selector({
      toolList: [
        { name: 't1', server: 'Notion', connection: 'connected', status: 'ready', function_names: ['a', 'b'] },
        { name: 't2', server: 'Linear', connection: 'available', function_names: [] },
        { name: 'WorkOS', server: 'WorkOS', connection: 'connected', status: 'not_loaded', function_names: [] },
      ],
      refreshTools,
      isLoadingTools: false,
      toolsError: null,
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
    refreshTools.mockClear()
  })

  async function renderPanel(fetchMock: ReturnType<typeof vi.fn>, onAddCustom?: () => Promise<void>, view: 'connected' | 'available' = 'connected'): Promise<void> {
    vi.stubGlobal('fetch', fetchMock)
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => {
      root.render(<TooltipProvider><GatewayServersPanel base={BASE} view={view} onAddCustom={onAddCustom} /></TooltipProvider>)
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

  it('shows only connected servers in the connected panel', async () => {
    await renderPanel(vi.fn(healthyFetch()))
    const connected = container!.querySelector('[aria-label="Connected servers"]')!
    expect(connected.textContent).toContain('Notion')
    expect(connected.textContent).toContain('2 tools')
    expect(connected.textContent).toContain('WorkOS')
    expect(connected.textContent).toContain('Tools not loaded')
    expect(refreshTools).toHaveBeenCalledOnce()
    expect(container!.querySelector('[aria-label="Available servers"]')).toBeNull()
    expect(container!.querySelector('[data-testid="mcp-add-custom"]')).toBeNull()
    expect(container!.textContent).toContain('3 results')
    expect(container!.textContent).not.toContain('Linear')
  })

  it('shows only available servers and connection actions in the available panel', async () => {
    await renderPanel(vi.fn(healthyFetch()), undefined, 'available')
    const available = container!.querySelector('[aria-label="Available servers"]')!
    expect(container!.querySelector('[aria-label="Connected servers"]')).toBeNull()
    expect(available.textContent).not.toContain('WorkOS')
    expect(available.textContent).toContain('Notion')
    expect(available.textContent).toContain('Linear')
    expect(available.textContent).toContain('Add connection')
    expect(container!.querySelector('[data-testid="gateway-add-linear"]')!.textContent).toContain('Add connection')
    expect(container!.textContent).toContain('3 results')
  })

  it('discovers and shows tools for an AgentWorks-only connected server', async () => {
    const getDetail = vi.mocked(agentApi.getToolDetail).mockResolvedValue({
      name: 'WorkOS', server: 'WorkOS', status: 'ok', connection: 'connected',
      function_names: ['list_users'],
      tools: [{ name: 'list_users', description: 'List organization users', server: 'WorkOS', parameters: { org_id: { type: 'string', description: 'Organization ID' } }, required: ['org_id'] }],
    })
    await renderPanel(vi.fn(healthyFetch()))

    await act(async () => {
      ;(container!.querySelector('[aria-label="Show AgentWorks tools on WorkOS"]') as HTMLButtonElement).click()
    })
    await act(async () => {})

    expect(getDetail).toHaveBeenCalledWith('WorkOS')
    expect(container!.textContent).toContain('list_users')
    expect(container!.textContent).toContain('List organization users')
    expect(container!.querySelector('[data-tool-card="list_users"] [aria-label="Arguments for list_users"]')).not.toBeNull()
    expect(container!.querySelector('[data-tool-card="list_users"] [aria-label="Input JSON schema"]')!.textContent).toContain('"org_id"')
  })

  it('explains when a connected AgentWorks server needs authorization', async () => {
    vi.mocked(agentApi.getToolDetail).mockResolvedValue({
      name: 'WorkOS', server: 'WorkOS', status: 'error', connection: 'connected',
      error: 'transport error: authorization required', function_names: [],
    })
    await renderPanel(vi.fn(healthyFetch()))
    await act(async () => {
      ;(container!.querySelector('[aria-label="Show AgentWorks tools on WorkOS"]') as HTMLButtonElement).click()
    })
    await act(async () => {})

    expect(container!.textContent).toContain('Authorization is required to load this server’s tools.')
    expect(container!.textContent).not.toContain('transport error')
  })

  it('filters the list by search text', async () => {
    await renderPanel(vi.fn(healthyFetch()), undefined, 'available')

    const search = container!.querySelector('[data-testid="gateway-servers-search"]') as HTMLInputElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(search, 'linear')
      search.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {})

    expect(container!.textContent).toContain('Linear')
    expect(container!.textContent).not.toContain('Notion')
    expect(container!.textContent).toContain('1 result')
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

    expect(container!.querySelector('[data-tool-card="search"]')!.textContent).toContain('Approved')
    expect(container!.textContent).toContain('Searches notes')
    expect(container!.querySelector('[data-tool-card="search"] [aria-label="Arguments for search"]')).not.toBeNull()
    expect(container!.querySelector('[data-tool-card="search"] [aria-label="Input JSON schema"]')!.textContent).toContain('"q"')
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
    expect(container!.textContent).toContain('notion__search')
    expect(container!.textContent).toContain('Current input schema')
    expect(container!.textContent).toContain('Previous v1')

    await act(async () => { ([...container!.querySelectorAll('button')].find(button => button.textContent?.includes('Approve v2')) as HTMLButtonElement).click() })
    expect(fetchMock).toHaveBeenCalledWith(`${BASE}/api/caplayer/api/admin/tools/notion__search/approve`, expect.objectContaining({
      method: 'POST', body: JSON.stringify({ fingerprint: 'fingerprint-2', version: 2 }),
    }))
  })

  it('separates connection status from tool approval and labels the actions', async () => {
    const fetchMock = vi.fn((url: string) => {
      if (url.endsWith('/api/admin/connectors')) return Promise.resolve(jsonResponse(200, {
        connectors: [{ ID: 'local', Provider: 'localmemory', Label: 'Local test · Memory', UpstreamURL: 'http://127.0.0.1:18164/memory/mcp', Status: 'active' }],
      }))
      if (url.endsWith('/api/admin/tools')) return Promise.resolve(jsonResponse(200, {
        tools: ['quarantined', 'quarantined', 'active', 'disabled'].map((Status, i) => ({
          ConnectorID: 'local', UpstreamName: `tool_${i}`, PublicName: `localmemory__tool_${i}`, Status,
        })),
      }))
      return healthyFetch()(url)
    })
    await renderPanel(fetchMock)
    const server = container!.querySelector('[aria-label="Local test · Memory server"]')!
    expect(server.textContent).toContain('Connected')
    expect(server.textContent).toContain('4 tools')
    expect(server.textContent).not.toContain('need review')
    expect(server.textContent).not.toContain('approved')
    expect(server.textContent).not.toContain('http://')
    expect(server.textContent).not.toContain('AgentWorks')
    expect(server.textContent).not.toContain('—')
    expect(server.textContent!.match(/Local test · Memory/g)).toHaveLength(1)
    expect([...server.querySelectorAll('button')].map(button => button.textContent)).toEqual(['View tools', ''])
    expect(server.querySelector('[aria-label="Actions for Local test · Memory"]')).not.toBeNull()
    expect(server.textContent).not.toContain('Credentials')

    await act(async () => { (server.querySelector('[aria-label="Show 4 tools on Local test · Memory"]') as HTMLButtonElement).click() })
    expect(server.textContent).toContain('Needs review')
    expect(server.textContent).toContain('2 need review')
    expect(server.textContent).toContain('1 approved')
    expect(server.textContent).toContain('Tools changed since connection.')
    expect(server.textContent).toContain('Group access is assigned separately.')
    expect(server.textContent).not.toContain('Approve v')
    // Collapsing is local UI state and does not call the MCP server again.
    const callsBefore = fetchMock.mock.calls.length
    await act(async () => { (server.querySelector('[aria-label="Hide 4 tools on Local test · Memory"]') as HTMLButtonElement).click() })
    expect(server.textContent).not.toContain('tool_0')
    expect(fetchMock.mock.calls.length).toBe(callsBefore)
  })

  it('keeps maintenance in the actions popover and disconnect behind confirmation', async () => {
    const fetchMock = vi.fn(healthyFetch())
    await renderPanel(fetchMock)
    const actions = container!.querySelector('[aria-label="Actions for Notion"]') as HTMLButtonElement
    const action = (label: string) => [...container!.querySelectorAll('button')].find(button => button.textContent === label) as HTMLButtonElement
    expect(action('Connection settings')).toBeUndefined()
    await act(async () => { actions.click() })
    expect(action('Refresh tool list')).not.toBeUndefined()
    await act(async () => { action('Connection settings').click() })
    expect(actions.getAttribute('aria-expanded')).toBe('false')
    expect(container!.textContent).toContain('Server access token')
    expect(container!.textContent).toContain('Update this server’s connection token.')
    expect(action('Update token').disabled).toBe(true)
    await act(async () => { action('Cancel').click() })
    expect(container!.textContent).not.toContain('Server access token')
    await act(async () => { actions.click() })
    await act(async () => { action('Disconnect server').click() })
    expect(document.body.textContent).toContain('Reconnecting requires assigning permissions again.')
    expect(fetchMock.mock.calls.some(([, init]) => init?.method === 'DELETE')).toBe(false)
    await act(async () => { ([...document.body.querySelectorAll('button')].find(button => button.textContent === 'Cancel') as HTMLButtonElement).click() })
    expect(document.body.textContent).not.toContain('Reconnecting requires assigning permissions again.')
  })

  it('uses shared sign-in for OAuth connection settings without exposing a raw credential editor', async () => {
    const fetchMock = vi.fn((url: string) => url.endsWith('/api/admin/connectors')
      ? Promise.resolve(jsonResponse(200, { connectors: [{ ID: 'c1', WorkspaceID: 'w1', Provider: 'notion', Label: 'Notion', Status: 'active', UpstreamURL: 'https://x/mcp', OAuthServer: 'Notion' }] }))
      : healthyFetch()(url))
    await renderPanel(fetchMock)
    await act(async () => { (container!.querySelector('[aria-label="Actions for Notion"]') as HTMLButtonElement).click() })
    await act(async () => { ([...container!.querySelectorAll('button')].find(button => button.textContent === 'Connection settings') as HTMLButtonElement).click() })
    expect(container!.textContent).toContain('Sign in again to reconnect.')
    expect(container!.textContent).toContain('Sign in again')
    expect(container!.querySelector('input[type="password"]')).toBeNull()
    expect(container!.textContent).not.toContain('Update token')
  })

  it('puts custom setup after the catalog and opens chat without a connection form', async () => {
    const fetchMock = vi.fn(healthyFetch())
    const onAddCustom = vi.fn(async () => {})
    await renderPanel(fetchMock, onAddCustom, 'available')
    const footer = container!.querySelector('[aria-label="Add custom server"]')!
    const available = container!.querySelector('[aria-label="Available servers"]')!
    expect(available.compareDocumentPosition(footer) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(container!.querySelector('[aria-label="Custom server URL"]')).toBeNull()
    expect(container!.querySelector('[aria-label="MCP servers JSON"]')).toBeNull()
    const callsBefore = fetchMock.mock.calls.length
    await act(async () => { (footer.querySelector('button') as HTMLButtonElement).click() })
    expect(onAddCustom).toHaveBeenCalledOnce()
    expect(fetchMock.mock.calls.length).toBe(callsBefore)
  })

  it('creates a named OAuth connection before sign-in without reusing provider authentication', async () => {
    const fetchMock = vi.fn((url: string, init?: RequestInit) => init?.method === 'POST'
      ? Promise.resolve(jsonResponse(201, { ID: 'c-new', OAuthCredentialID: 'c-new', Status: 'authentication_required' })) : healthyFetch()(url))
    await renderPanel(fetchMock, undefined, 'available')
    await act(async () => { (container!.querySelector('[data-testid="gateway-add-slack"]') as HTMLButtonElement).click() })
    expect(fetchMock.mock.calls.some(([, init]) => init?.method === 'POST')).toBe(false)
    const input = container!.querySelector('[aria-label="Slack connection name"]') as HTMLInputElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'Slack · Engineering')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => { input.closest('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })
    expect(fetchMock).toHaveBeenCalledWith(`${BASE}/api/caplayer/api/admin/connectors`, expect.objectContaining({ method: 'POST', body: JSON.stringify({ Provider: 'Slack', Label: 'Slack · Engineering', Slug: '', URL: '' }) }))
    expect(container!.querySelector('[data-testid="gateway-add-slack"]')).not.toBeNull()
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
      ;(container!.querySelector('[aria-label="Actions for Notion"]') as HTMLButtonElement).click()
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
    await renderPanel(fetchMock, undefined, 'available')

    fetchMock.mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === 'POST' && url.endsWith('/api/admin/connectors')) {
        return Promise.resolve(jsonResponse(201, { ID: 'c2' }))
      }
      return healthyFetch()(url)
    })

    await act(async () => {
      ;(container!.querySelector('[data-testid="gateway-add-linear"]') as HTMLButtonElement).click()
    })
    const input = container!.querySelector('[aria-label="Linear connection name"]') as HTMLInputElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'Linear · Work')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => { input.closest('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })

    expect(fetchMock).toHaveBeenCalledWith(
      `${BASE}/api/caplayer/api/admin/connectors`,
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ Provider: 'Linear', Label: 'Linear · Work', Slug: '', URL: '' }),
      }),
    )
  })
})
