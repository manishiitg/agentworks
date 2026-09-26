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
    return Promise.resolve(jsonResponse(404, { error: 'nope' }))
  }
}

describe('GatewayGroupsPanel', () => {
  let container: HTMLDivElement | null = null

  afterEach(() => {
    container?.remove()
    container = null
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  async function renderPanel(): Promise<void> {
    vi.stubGlobal('fetch', vi.fn(healthyFetch()))
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => {
      root.render(<GatewayGroupsPanel base={BASE} />)
    })
    await act(async () => {})
    await act(async () => {})
  }

  it('shows members and AWS-style server permissions for the selected group', async () => {
    await renderPanel()

    expect(container!.textContent).toContain('Engineering')
    expect(container!.textContent).toContain('alice')
    expect(container!.textContent).toContain('Permissions for Engineering')
    expect(container!.textContent).toContain('Full access')
    expect(container!.querySelector('[data-testid="gateway-permissions"]')).not.toBeNull()
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
})
