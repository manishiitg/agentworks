// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { GatewayGroupsPanel } from './GatewayGroupsPanel'

vi.mock('../../services/api', () => ({ getApiBaseUrl: () => 'http://127.0.0.1:18745', getAuthToken: () => 'product-jwt' }))

const secretAPI = vi.hoisted(() => ({ getGlobalSecrets: vi.fn(), setVaultSecretAccess: vi.fn() }))
vi.mock('../../api/secrets', () => ({ secretsApi: secretAPI }))
vi.mock('../../components/integrations/OpenVaultButton', () => ({ OpenVaultButton: () => null }))
vi.mock('../../hooks/useCanWriteWorkflow', () => ({ useCanWriteWorkflow: () => true }))
beforeEach(() => {
  secretAPI.getGlobalSecrets.mockResolvedValue([{ name: 'TEAM_KEY' }, { name: 'SECOND_KEY' }, { name: 'THIRD_KEY' }])
  secretAPI.setVaultSecretAccess.mockResolvedValue(undefined)
})

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
          tools: [{ ConnectorID: 'c1', WorkspaceID: 'w1', UpstreamName: 'search', PublicName: 'notion__search', Description: '', InputSchema: btoa(JSON.stringify({ type: 'object', properties: { project: { type: 'string', description: 'Project to search' } }, required: ['project'] })), Status: 'active', DiscoveredAt: '' }],
        }),
      )
    }
    if (url.includes('/permissions')) return Promise.resolve(jsonResponse(200, { permissions: [{ public_name: 'notion__search', allowed: false, governed: false, source: '' }] }))
    if (url.endsWith('/api/admin/access/packages')) return Promise.resolve(jsonResponse(200, { packages: [] }))
    if (url.endsWith('/api/admin/access/history')) return Promise.resolve(jsonResponse(200, { events: [] }))
    if (url.endsWith('/secrets')) return Promise.resolve(jsonResponse(200, { secrets: [] }))
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

  async function renderPanel(fetchMock?: ReturnType<typeof vi.fn>, directoryOnly = false, allowLegacyAPIKeys = false): Promise<ReturnType<typeof vi.fn>> {
    const mock = fetchMock ?? vi.fn(healthyFetch())
    vi.stubGlobal('fetch', mock)
    container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => {
      root.render(<GatewayGroupsPanel base={BASE} directoryOnly={directoryOnly} allowLegacyAPIKeys={allowLegacyAPIKeys} />)
    })
    await act(async () => {})
    await act(async () => {})
    return mock
  }

  it('shows the selected group with users and MCP/tool tabs', async () => {
    await renderPanel()

    expect(container!.textContent).toContain('Engineering')
    expect(container!.querySelector('[role=tab][title=Users]')).not.toBeNull()
    expect(container!.querySelector('[role=tab][title="Permissions"]')).not.toBeNull()
    expect(container!.querySelector('[aria-label="Group users"]')).toBeNull()
    expect(container!.textContent).toContain('Permissions')
    expect(container!.textContent).not.toContain('Whole server')
    expect(container!.querySelector('[aria-label="Remove Notion from group"]')).not.toBeNull()
    expect(container!.querySelector('[data-testid="gateway-permissions"]')).not.toBeNull()
  })

  it('keeps secret controls mounted and the add list open while refreshing after a saved grant', async () => {
    let refreshSecrets: ((value: Response) => void) | undefined
    let reads = 0
    const fetchMock = vi.fn((url: string) => {
      if (url.endsWith('/secrets')) {
        reads += 1
        if (reads === 1) return Promise.resolve(jsonResponse(200, { secrets: [{ name: 'TEAM_KEY' }] }))
        return new Promise<Response>(resolve => { refreshSecrets = resolve })
      }
      return healthyFetch()(url)
    })
    await renderPanel(fetchMock)
    const section = container!.querySelector('[aria-label="Group secrets"]')
    expect(section).not.toBeNull()
    expect(container!.querySelector('[aria-label="Allow TEAM_KEY"]')?.getAttribute('aria-checked')).toBe('true')
    await act(async () => { ([...container!.querySelectorAll('button')].find(b => b.textContent === 'Add secrets') as HTMLButtonElement).click() })
    await act(async () => { (container!.querySelector('[aria-label="Allow SECOND_KEY"]') as HTMLButtonElement).click() })
    expect(secretAPI.setVaultSecretAccess).toHaveBeenCalledWith('eng', 'SECOND_KEY', true)
    expect(reads).toBe(2)
    expect(container!.querySelector('[aria-label="Group secrets"]')).toBe(section)
    expect(container!.textContent).not.toContain('Loading secret permissions')
    expect(container!.querySelector('[aria-label="Available secrets"]')).not.toBeNull()
    expect(container!.querySelector('[aria-label="Allow SECOND_KEY"]')?.getAttribute('aria-checked')).toBe('true')
    await act(async () => { refreshSecrets!(jsonResponse(200, { secrets: [{ name: 'TEAM_KEY' }, { name: 'SECOND_KEY' }] })) })
    expect(container!.querySelector('[aria-label="Group secrets"]')).toBe(section)
    expect(container!.querySelector('[aria-label="Allow THIRD_KEY"]')).not.toBeNull()
  })

  it('shows Platform as built-in with automatic platform membership and an immutable name', async () => {
    const fetchMock = vi.fn((url: string) => {
      if (url.endsWith('/api/admin/groups')) return Promise.resolve(jsonResponse(200, { groups: [{ ID: 'everyone-w1', WorkspaceID: 'w1', Name: 'Platform', BuiltIn: true, Description: 'Share tools across products' }] }))
      return healthyFetch()(url)
    })
    await renderPanel(fetchMock, true)
    expect(container!.textContent).toContain('Built-in')
    expect(container!.textContent).toContain('All platform users · membership is automatic')
    expect(container!.textContent).toContain('a@x.com')
    expect(container!.querySelector('[aria-label="Remove alice from everyone-w1"]')).toBeNull()
    expect(container!.querySelector('[aria-label="Add member to everyone-w1"]')).toBeNull()
    await act(async () => { (container!.querySelector('[aria-label="Edit group"]') as HTMLButtonElement).click() })
    expect((container!.querySelector('[aria-label="Group name"]') as HTMLInputElement).disabled).toBe(true)
    expect((container!.querySelector('[aria-label="Group description"]') as HTMLTextAreaElement).disabled).toBe(false)
  })

  it('offers creation and membership management only in the group directory', async () => {
    const fetchMock = await renderPanel()
    expect(container!.textContent).not.toContain('New group')
    expect(container!.querySelector('[data-testid="gateway-group-name-input"]')).toBeNull()
    await act(async () => { (container!.querySelector('[role=tab][title=Users]') as HTMLButtonElement).click() })
    expect(container!.textContent).toContain('a@x.com')
    expect(container!.querySelector('[aria-label="Remove alice from eng"]')).toBeNull()
    expect(fetchMock.mock.calls.filter(([url]) => url.includes('/members'))).toHaveLength(1)
  })

  it('keeps policies and conditions scoped to the selected group and shows them inside a tool', async () => {
    const fetchMock = vi.fn((url: string) => {
      if (url.endsWith('/api/admin/groups')) return Promise.resolve(jsonResponse(200, { groups: [{ ID: 'eng', WorkspaceID: 'w1', Name: 'Engineering' }, { ID: 'sales', WorkspaceID: 'w1', Name: 'Sales' }] }))
      if (url.endsWith('/api/admin/access/packages')) return Promise.resolve(jsonResponse(200, { packages: [
        { id: 'p-eng', group_id: 'eng', name: 'Engineering scoped access', status: 'published', version: 1, rules: [{ public_name: 'notion__search', fingerprint: 'v1', conditions: [{ path: '/project', op: 'equals', value: 'eng-project' }] }] },
        { id: 'p-sales', group_id: 'sales', name: 'Sales scoped access', status: 'draft', version: 1, rules: [{ public_name: 'notion__search', fingerprint: 'v1', conditions: [{ path: '/project', op: 'equals', value: 'sales-project' }] }] },
      ] }))
      if (url.includes('/permissions')) return Promise.resolve(jsonResponse(200, { permissions: [{ public_name: 'notion__search', allowed: url.includes('/eng/'), governed: true, source: 'policy' }] }))
      return healthyFetch()(url)
    })
    await renderPanel(fetchMock)
    expect(container!.textContent).toContain('Engineering scoped access')
    expect(container!.textContent).not.toContain('Sales scoped access')
    // Memberships load only for the selected group.
    expect(fetchMock.mock.calls.some(([url]) => url.includes('/sales/members'))).toBe(false)
    await act(async () => { (container!.querySelector('[aria-label="Show tools on Notion"]') as HTMLButtonElement).click() })
    expect((container!.querySelector('[aria-label="Grant notion__search"]') as HTMLButtonElement).disabled).toBe(true)
    expect(container!.textContent).toContain('Allowed with restrictions')
    expect(container!.querySelector('[aria-label="search permission details"]')!.textContent).toContain('project must equal eng-project')
    await act(async () => { setInputValue(container!.querySelector('[aria-label="Find a group"]') as HTMLInputElement, 'sales') })
    expect(container!.querySelector('[aria-label="Select group Engineering"]')).toBeNull()
    await act(async () => { (container!.querySelector('[aria-label="Select group Sales"]') as HTMLButtonElement).click() })
    await act(async () => {})
    expect(container!.querySelector('[aria-label="Select group Sales"]')?.getAttribute('aria-pressed')).toBe('true')
    expect(container!.textContent).toContain('Sales scoped access')
    expect(container!.textContent).not.toContain('Engineering scoped access')
    expect(container!.textContent).not.toContain('eng-project')
  })

  it('confirms removing all access to a server from only the selected group', async () => {
    const fetchMock = vi.fn((url: string, init?: RequestInit) => {
      if (url.endsWith('/permissions')) return Promise.resolve(jsonResponse(200, { permissions: [{ public_name: 'notion__search', allowed: true, assigned: true, governed: false, source: 'tool' }] }))
      if (init?.method === 'DELETE') return Promise.resolve(jsonResponse(204, {}))
      return healthyFetch()(url)
    })
    await renderPanel(fetchMock)
    await act(async () => { (container!.querySelector('[aria-label="Remove Notion from group"]') as HTMLButtonElement).click() })
    expect(document.body.textContent).toContain('individual tool grants')
    expect(document.body.textContent).toContain('The server stays connected')
    expect(fetchMock.mock.calls.some(([, init]) => init?.method === 'DELETE')).toBe(false)
    const confirm = [...document.body.querySelectorAll('button')].find(button => button.textContent === 'Remove from group' && !button.hasAttribute('aria-label')) as HTMLButtonElement
    await act(async () => { confirm.click() })
    expect(fetchMock).toHaveBeenCalledWith(`${BASE}/api/caplayer/api/admin/groups/eng/servers/c1/access`, expect.objectContaining({ method: 'DELETE' }))
  })

  it('expands a server to grant specific tools', async () => {
    await renderPanel()

    expect(container!.textContent).not.toContain('notion__search')
    await act(async () => {
      ;(container!.querySelector('[aria-label="Show tools on Notion"]') as HTMLButtonElement).click()
    })
    await act(async () => {})

    expect(container!.querySelector('[aria-label="Grant notion__search"]')).not.toBeNull()
    await act(async () => { (container!.querySelector('[aria-label="Arguments for search"]') as HTMLButtonElement).click() })
    const args = container!.querySelector('[aria-label="search arguments"]')!
    expect(args.textContent).toContain('project')
    expect(args.textContent).toContain('string')
    expect(args.textContent).toContain('"required"')
    expect(args.textContent).toContain('Project to search')
    expect(args.querySelector('pre')?.getAttribute('aria-label')).toBe('Input JSON schema')
    expect(container!.querySelector('[aria-label="Permissions for search"]')).toBeNull()
  })

  it('creates a group from a single name with a derived id', async () => {
    const fetchMock = vi.fn(healthyFetch())
    await renderPanel(fetchMock, true)
    await act(async () => { ([...container!.querySelectorAll('button')].find(b => b.textContent === 'New group') as HTMLButtonElement).click() })

    await act(async () => {
      setInputValue(container!.querySelector('[data-testid="gateway-group-name-input"]') as HTMLInputElement, 'Data Science')
      const field = container!.querySelector('[aria-label="New group description"]') as HTMLTextAreaElement
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(field, 'Data team tools')
      field.dispatchEvent(new Event('input', { bubbles: true }))
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
      `${BASE}/api/caplayer/api/admin/groups`,
      expect.objectContaining({ method: 'POST', body: JSON.stringify({ ID: 'data-science', Name: 'Data Science', Description: 'Data team tools' }) }),
    )
  })

  it('renames the selected group inline', async () => {
    const fetchMock = vi.fn(healthyFetch())
    await renderPanel(fetchMock, true)

    fetchMock.mockImplementation((url: string, init?: RequestInit) => {
      if (init?.method === 'POST' && url.endsWith('/api/admin/groups/eng')) {
        return Promise.resolve(jsonResponse(200, { status: 'renamed' }))
      }
      return healthyFetch()(url)
    })
    await act(async () => {
      ;(container!.querySelector('[aria-label="Edit group"]') as HTMLButtonElement).click()
    })
    await act(async () => {
      setInputValue(container!.querySelector('[data-testid="gateway-group-rename-input"]') as HTMLInputElement, 'Platform')
    })
    await act(async () => {
      ;(container!.querySelector('[aria-label="Save group details"]') as HTMLButtonElement).click()
    })
    await act(async () => {})

    expect(fetchMock).toHaveBeenCalledWith(
      `${BASE}/api/caplayer/api/admin/groups/eng`,
      expect.objectContaining({ method: 'POST', body: JSON.stringify({ Name: 'Platform', Description: '' }) }),
    )
  })

  it('shows group descriptions in Access and can update or clear them without losing the name', async () => {
    let description = 'Support read tools'
    const fetchMock = vi.fn((url: string, init?: RequestInit) => {
      if (url.endsWith('/api/admin/groups')) return Promise.resolve(jsonResponse(200, { groups: [{ ID: 'eng', WorkspaceID: 'w1', Name: 'Engineering', Description: description }] }))
      if (url.endsWith('/api/admin/groups/eng') && init?.method === 'POST') {
        description = JSON.parse(init.body as string).Description
        return Promise.resolve(jsonResponse(200, { status: 'renamed' }))
      }
      return healthyFetch()(url)
    })
    await renderPanel(fetchMock)
    expect(container!.querySelector('[data-testid="gateway-group-description"]')?.textContent).toBe('Support read tools')
    for (const value of ['Tools for on-call support', '']) {
      await act(async () => { (container!.querySelector('[aria-label="Edit group"]') as HTMLButtonElement).click() })
      expect((container!.querySelector('[aria-label="Group name"]') as HTMLInputElement).value).toBe('Engineering')
      await act(async () => {
        const field = container!.querySelector('[aria-label="Group description"]') as HTMLTextAreaElement
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(field, value)
        field.dispatchEvent(new Event('input', { bubbles: true }))
      })
      await act(async () => { (container!.querySelector('[aria-label="Save group details"]') as HTMLButtonElement).click() })
      expect(fetchMock).toHaveBeenCalledWith(`${BASE}/api/caplayer/api/admin/groups/eng`, expect.objectContaining({ method: 'POST', body: JSON.stringify({ Name: 'Engineering', Description: value }) }))
      expect(container!.querySelector('[data-testid="gateway-group-description"]')?.textContent ?? '').toBe(value)
    }
  })

  it('uses SSO by default without advertising group API keys', async () => {
    await renderPanel(undefined, true)
    expect(container!.textContent).not.toContain('Group API keys')
  })

  it('creates a legacy local API key only when explicitly enabled', async () => {
    const fetchMock = vi.fn(healthyFetch())
    await renderPanel(fetchMock, true, true)
    await act(async () => { ([...container!.querySelectorAll('button')].find(b => b.textContent === 'Group API keys') as HTMLButtonElement).click() })

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
