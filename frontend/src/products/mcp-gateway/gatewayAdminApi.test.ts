import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  GatewayApiError,
  attachGroupServer,
  createConnector,
  createUser,
  detachGroupServer,
  getGrants,
  listAudit,
  listConnectors,
  listGroupServers,
  listMembers,
  setGrant,
} from './gatewayAdminApi'

const BASE = 'http://127.0.0.1:18161'

function jsonResponse(status: number, body: unknown): Response {
  return { ok: status >= 200 && status < 300, status, json: () => Promise.resolve(body) } as Response
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('gatewayAdminApi', () => {
  it('lists connectors from the admin API', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { connectors: [{ ID: 'c1' }] }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(listConnectors(BASE)).resolves.toEqual({ connectors: [{ ID: 'c1' }] })
    expect(fetchMock).toHaveBeenCalledWith(
      `${BASE}/api/admin/connectors`,
      expect.objectContaining({ headers: expect.objectContaining({ Accept: 'application/json' }) }),
    )
  })

  it('creates users with the gateway field names', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(201, { id: 'u9' }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(createUser(BASE, 'u9', 'u9@example.com')).resolves.toEqual({ id: 'u9' })
    expect(fetchMock).toHaveBeenCalledWith(
      `${BASE}/api/admin/users`,
      expect.objectContaining({ method: 'POST', body: JSON.stringify({ ID: 'u9', Email: 'u9@example.com' }) }),
    )
  })

  it('creates catalog connectors without a URL and custom ones with a URL', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(201, { ID: 'c2' }))
    vi.stubGlobal('fetch', fetchMock)

    await createConnector(BASE, { Provider: 'notion', Label: 'Notion', Slug: '', URL: '' })
    expect(fetchMock).toHaveBeenLastCalledWith(
      `${BASE}/api/admin/connectors`,
      expect.objectContaining({ body: JSON.stringify({ Provider: 'notion', Label: 'Notion', Slug: '', URL: '' }) }),
    )
  })

  it('reads user and group grants from the matching response field', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(200, { user_grants: ['notion__search'] }))
      .mockResolvedValueOnce(jsonResponse(200, { group_grants: ['linear__list'] }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(getGrants(BASE, { user: 'u1' })).resolves.toEqual(['notion__search'])
    await expect(getGrants(BASE, { group: 'g1' })).resolves.toEqual(['linear__list'])
    expect(fetchMock).toHaveBeenNthCalledWith(1, `${BASE}/api/admin/grants?user=u1`, expect.anything())
    expect(fetchMock).toHaveBeenNthCalledWith(2, `${BASE}/api/admin/grants?group=g1`, expect.anything())
  })

  it('sets grants with exactly one subject key', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { status: 'ok' }))
    vi.stubGlobal('fetch', fetchMock)

    await setGrant(BASE, { user: 'u1' }, 'notion__search', true)
    expect(fetchMock).toHaveBeenCalledWith(
      `${BASE}/api/admin/grants`,
      expect.objectContaining({
        body: JSON.stringify({ user_id: 'u1', tool: 'notion__search', grant: true }),
      }),
    )
  })

  it('attaches and detaches whole servers to groups', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(200, { servers: ['c1'] }))
      .mockResolvedValueOnce(jsonResponse(200, { status: 'attached' }))
      .mockResolvedValueOnce({ ok: true, status: 204, json: () => Promise.reject(new Error('no body')) } as Response)
    vi.stubGlobal('fetch', fetchMock)

    await expect(listGroupServers(BASE, 'g1')).resolves.toEqual({ servers: ['c1'] })
    await attachGroupServer(BASE, 'g1', 'c1')
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      `${BASE}/api/admin/groups/g1/servers`,
      expect.objectContaining({ method: 'POST', body: JSON.stringify({ connector_id: 'c1' }) }),
    )
    await detachGroupServer(BASE, 'g1', 'c1')
    expect(fetchMock).toHaveBeenNthCalledWith(3, `${BASE}/api/admin/groups/g1/servers/c1`, expect.objectContaining({ method: 'DELETE' }))
  })

  it('lists members and audit events', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(200, { members: ['u1'] }))
      .mockResolvedValueOnce(jsonResponse(200, { events: [] }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(listMembers(BASE, 'g1')).resolves.toEqual({ members: ['u1'] })
    await expect(listAudit(BASE, 50)).resolves.toEqual({ events: [] })
  })

  it('surfaces the gateway error message on failure', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(400, { error: 'user exists' })))

    const err = await listConnectors(BASE).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(GatewayApiError)
    expect((err as GatewayApiError).message).toBe('user exists')
    expect((err as GatewayApiError).status).toBe(400)
  })

  it('reports an unreachable gateway without hanging', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockRejectedValue(new TypeError('Failed to fetch')),
    )

    const err = await listConnectors(BASE).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(GatewayApiError)
    expect((err as GatewayApiError).status).toBe(0)
    expect((err as GatewayApiError).message).toContain('unreachable')
  })
})
