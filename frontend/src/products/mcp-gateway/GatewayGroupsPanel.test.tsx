// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { GatewayGroupsPanel } from './GatewayGroupsPanel'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const BASE = 'http://127.0.0.1:18745'

function jsonResponse(status: number, body: unknown): Response {
  return { ok: status >= 200 && status < 300, status, json: () => Promise.resolve(body) } as Response
}

function healthyFetch(): (url: string) => Promise<Response> {
  return (url: string) => {
    if (url.endsWith('/api/admin/groups')) {
      return Promise.resolve(jsonResponse(200, { groups: [{ ID: 'eng', WorkspaceID: 'w1', Name: 'Engineering' }] }))
    }
    if (url.endsWith('/api/admin/users')) {
      return Promise.resolve(jsonResponse(200, { users: [{ ID: 'alice', WorkspaceID: 'w1', Email: 'a@x.com' }] }))
    }
    if (url.endsWith('/api/admin/connectors')) {
      return Promise.resolve(
        jsonResponse(200, {
          connectors: [{ ID: 'c1', WorkspaceID: 'w1', Provider: 'notion', InstanceSlug: '', Label: 'Notion', UpstreamURL: 'https://x/mcp', Status: 'active' }],
        }),
      )
    }
    if (url.endsWith('/api/admin/tools')) {
      return Promise.resolve(
        jsonResponse(200, {
          tools: [{ ConnectorID: 'c1', WorkspaceID: 'w1', UpstreamName: 'search', PublicName: 'notion__search', Description: '', InputSchema: null, Status: 'active', DiscoveredAt: '' }],
        }),
      )
    }
    if (url.includes('/members')) return Promise.resolve(jsonResponse(200, { members: ['alice'] }))
    if (url.includes('/servers')) return Promise.resolve(jsonResponse(200, { servers: [] }))
    if (url.includes('/api/admin/grants')) return Promise.resolve(jsonResponse(200, { group_grants: [] }))
    if (url.includes('/keys')) return Promise.resolve(jsonResponse(200, { keys: [] }))
    return Promise.resolve(jsonResponse(404, { error: 'nope' }))
  }
}

function setInputValue(el: HTMLInputElement, value: string) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(el, value)
  el.dispatchEvent(new Event('input', { bubbles: true }))
}

describe('GatewayGroupsPanel', () => {
  let container: HTMLDivElement | null = null

  afterEach(() => {
    container?.remove()
    container = null
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  async function renderPanel(fetchMock?: ReturnType<typeof vi.fn>): Promise<ReturnType<typeof vi.fn>> {
    const mock = fetchMock ?? vi.fn(healthyFetch())
    vi.stubGlobal('fetch', mock)
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => {
      root.render(<GatewayGroupsPanel base={BASE} />)
    })
    await act(async () => {})
    await act(async () => {})
    return mock
  }

  it('shows members and AWS-style server permissions for the selected group', async () => {
    await renderPanel()

    expect(container!.textContent).toContain('Engineering')
    expect(container!.textContent).toContain('alice')
    expect(container!.textContent).toContain('Permissions for Engineering')
    expect(container!.textContent).toContain('Whole server')
    expect(container!.querySelector('[data-testid="gateway-permissions"]')).not.toBeNull()
  })

  it('confirms the scope before granting a whole server', async () => {
    const fetchMock = await renderPanel()
    await act(async () => { (container!.querySelector('[aria-label="Full access to Notion"]') as HTMLButtonElement).click() })
    expect(document.body.textContent).toContain('1 approved tool(s) available now')
    expect(fetchMock.mock.calls.some(([url, init]) => url.includes('/servers') && init?.method === 'POST')).toBe(false)
    const confirm = [...document.body.querySelectorAll('button')].find(button => button.textContent === 'Grant server') as HTMLButtonElement
    await act(async () => { confirm.click() })
    expect(fetchMock.mock.calls.some(([url, init]) => url.includes('/servers') && init?.method === 'POST')).toBe(true)
  })

  it('expands a server to grant specific tools', async () => {
    await renderPanel()

    expect(container!.textContent).not.toContain('notion__search')
    await act(async () => {
      ;(container!.querySelector('[aria-label="Show tools on Notion"]') as HTMLButtonElement).click()
    })
    await act(async () => {})

    expect(container!.textContent).toContain('notion__search')
    expect(container!.querySelector('[aria-label="Grant notion__search"]')).not.toBeNull()
  })

  it('creates a group from a single name with a derived id', async () => {
    const fetchMock = vi.fn(healthyFetch())
    await renderPanel(fetchMock)

    await act(async () => {
      setInputValue(container!.querySelector('[data-testid="gateway-group-name-input"]') as HTMLInputElement, 'Data Science')
    })
    await act(async () => {})
    expect(container!.textContent).toContain('data-science')

    fetchMock.mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === 'POST' && url.endsWith('/api/admin/groups')) {
        return Promise.resolve(jsonResponse(201, { id: 'data-science' }))
      }
      return healthyFetch()(url)
    })
    await act(async () => {
      ;(container!.querySelector('[data-testid="gateway-group-add-submit"]') as HTMLButtonElement).click()
    })
    await act(async () => {})

    expect(fetchMock).toHaveBeenCalledWith(
      `${BASE}/api/admin/groups`,
      expect.objectContaining({ method: 'POST', body: JSON.stringify({ ID: 'data-science', Name: 'Data Science' }) }),
    )
  })

  it('renames the selected group inline', async () => {
    const fetchMock = vi.fn(healthyFetch())
    await renderPanel(fetchMock)

    fetchMock.mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === 'POST' && url.endsWith('/api/admin/groups/eng')) {
        return Promise.resolve(jsonResponse(200, { status: 'renamed' }))
      }
      return healthyFetch()(url)
    })
    await act(async () => {
      ;(container!.querySelector('[aria-label="Rename group"]') as HTMLButtonElement).click()
    })
    await act(async () => {
      setInputValue(container!.querySelector('[data-testid="gateway-group-rename-input"]') as HTMLInputElement, 'Platform')
    })
    await act(async () => {
      ;(container!.querySelector('[aria-label="Save group name"]') as HTMLButtonElement).click()
    })
    await act(async () => {})

    expect(fetchMock).toHaveBeenCalledWith(
      `${BASE}/api/admin/groups/eng`,
      expect.objectContaining({ method: 'POST', body: JSON.stringify({ Name: 'Platform' }) }),
    )
  })

  it('creates an API key and shows the token once', async () => {
    const fetchMock = vi.fn(healthyFetch())
    await renderPanel(fetchMock)

    fetchMock.mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === 'POST' && url.endsWith('/api/admin/groups/eng/keys')) {
        return Promise.resolve(
          jsonResponse(201, { ID: 'key_1', GroupID: 'eng', Label: 'share', Token: 'gwk_secret', CreatedAt: '', LastUsedAt: '' }),
        )
      }
      return healthyFetch()(url)
    })
    await act(async () => {
      setInputValue(container!.querySelector('[data-testid="gateway-key-label"]') as HTMLInputElement, 'share')
    })
    await act(async () => {
      ;(container!.querySelector('[data-testid="gateway-key-create"]') as HTMLButtonElement).click()
    })
    await act(async () => {})
    await act(async () => {})

    const fresh = container!.querySelector('[data-testid="gateway-key-fresh"]')
    expect(fresh).not.toBeNull()
    expect(fresh!.textContent).toContain('gwk_secret')
    expect(fresh!.textContent).toContain('shown once')
  })
})
