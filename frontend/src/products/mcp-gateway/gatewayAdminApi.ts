/**
 * Typed client for the MCP Gateway admin API (`/api/admin/*`).
 *
 * Shapes mirror the gateway's Go structs (capitalized JSON keys). Locally
 * the gateway trusts loopback callers as admin, so no token is attached;
 * a 401 here means a remote gateway the local user cannot reach.
 */
export interface GatewayUser {
  ID: string
  WorkspaceID: string
  Email: string
}

export interface GatewayGroup {
  ID: string
  WorkspaceID: string
  Name: string
}

export interface GatewayConnector {
  ID: string
  WorkspaceID: string
  Provider: string
  InstanceSlug: string
  Label: string
  UpstreamURL: string
  Status: string
}

export interface GatewayTool {
  ConnectorID: string
  WorkspaceID: string
  UpstreamName: string
  PublicName: string
  Description: string
  /** Raw JSON schema, base64-encoded by Go ([]byte); null when the upstream sent none. */
  InputSchema: string | null
  Status: string
  DiscoveredAt: string
}

export interface GatewayAuditEvent {
  ID: string
  CallID: string
  Timestamp: string
  UserID: string
  ConnectorID: string
  PublicName: string
  UpstreamName: string
  Decision: string
  Outcome: string
  DurationMs: number
  ErrorText: string
}

export interface GatewayProvider {
  Name: string
  Key: string
  URL: string
  OAuth: boolean
}

export class GatewayApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'GatewayApiError'
    this.status = status
  }
}

async function request<T>(base: string, path: string, init?: RequestInit): Promise<T> {
  let resp: Response
  try {
    resp = await fetch(`${base}${path}`, {
      ...init,
      headers: { Accept: 'application/json', ...(init?.headers ?? {}) },
    })
  } catch {
    throw new GatewayApiError(0, 'Gateway is unreachable. Start the backend with the gateway enabled and retry.')
  }
  if (resp.status === 204) return undefined as T
  let data: unknown = null
  try {
    data = await resp.json()
  } catch {
    // Non-JSON body; fall through to the status check below.
  }
  if (!resp.ok) {
    const detail =
      typeof data === 'object' && data !== null && 'error' in data && typeof (data as { error: unknown }).error === 'string'
        ? (data as { error: string }).error
        : `Request failed (${resp.status})`
    throw new GatewayApiError(resp.status, detail)
  }
  return data as T
}

function post<T>(base: string, path: string, body: unknown): Promise<T> {
  return request<T>(base, path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

export function listUsers(base: string): Promise<{ users: GatewayUser[] }> {
  return request(base, '/api/admin/users')
}

export function createUser(base: string, id: string, email: string): Promise<{ id: string }> {
  return post(base, '/api/admin/users', { ID: id, Email: email })
}

export function listGroups(base: string): Promise<{ groups: GatewayGroup[] }> {
  return request(base, '/api/admin/groups')
}

export function createGroup(base: string, id: string, name: string): Promise<{ id: string }> {
  return post(base, '/api/admin/groups', { ID: id, Name: name })
}

export function listMembers(base: string, groupId: string): Promise<{ members: string[] }> {
  return request(base, `/api/admin/groups/${encodeURIComponent(groupId)}/members`)
}

export function addMember(base: string, groupId: string, userId: string): Promise<{ status: string }> {
  return post(base, `/api/admin/groups/${encodeURIComponent(groupId)}/members`, { user_id: userId })
}

export function removeMember(base: string, groupId: string, userId: string): Promise<void> {
  return request(base, `/api/admin/groups/${encodeURIComponent(groupId)}/members/${encodeURIComponent(userId)}`, {
    method: 'DELETE',
  })
}

export function listGroupServers(base: string, groupId: string): Promise<{ servers: string[] }> {
  return request(base, `/api/admin/groups/${encodeURIComponent(groupId)}/servers`)
}

export function attachGroupServer(base: string, groupId: string, connectorId: string): Promise<{ status: string }> {
  return post(base, `/api/admin/groups/${encodeURIComponent(groupId)}/servers`, { connector_id: connectorId })
}

export function detachGroupServer(base: string, groupId: string, connectorId: string): Promise<void> {
  return request(base, `/api/admin/groups/${encodeURIComponent(groupId)}/servers/${encodeURIComponent(connectorId)}`, {
    method: 'DELETE',
  })
}

export function listConnectors(base: string): Promise<{ connectors: GatewayConnector[] }> {
  return request(base, '/api/admin/connectors')
}

export function createConnector(base: string, input: { Provider: string; Label: string; Slug: string; URL: string }): Promise<GatewayConnector> {
  return post(base, '/api/admin/connectors', input)
}

export function syncConnector(base: string, id: string): Promise<{ status: string }> {
  return post(base, `/api/admin/connectors/${encodeURIComponent(id)}/sync`, {})
}

export function deleteConnector(base: string, id: string): Promise<void> {
  return request(base, `/api/admin/connectors/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export function listTools(base: string): Promise<{ tools: GatewayTool[] }> {
  return request(base, '/api/admin/tools')
}

export function getGrants(base: string, subject: { user: string } | { group: string }): Promise<string[]> {
  const query = 'user' in subject ? `user=${encodeURIComponent(subject.user)}` : `group=${encodeURIComponent(subject.group)}`
  return request<{ user_grants?: string[]; group_grants?: string[] }>(base, `/api/admin/grants?${query}`).then(
    (data) => ('user' in subject ? (data.user_grants ?? []) : (data.group_grants ?? [])),
  )
}

export function setGrant(
  base: string,
  subject: { user: string } | { group: string },
  tool: string,
  grant: boolean,
): Promise<{ status: string }> {
  return post(base, '/api/admin/grants', {
    ...('user' in subject ? { user_id: subject.user } : { group_id: subject.group }),
    tool,
    grant,
  })
}

export function listAudit(base: string, limit: number): Promise<{ events: GatewayAuditEvent[] }> {
  return request(base, `/api/admin/audit?limit=${limit}`)
}

export function listCatalog(base: string): Promise<{ providers: GatewayProvider[] }> {
  return request(base, '/api/admin/catalog')
}
