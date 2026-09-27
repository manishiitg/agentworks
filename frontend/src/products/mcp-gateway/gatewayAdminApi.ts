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
  Title?: string
  /** Raw JSON schema, base64-encoded by Go ([]byte); null when the upstream sent none. */
  InputSchema: string | null
  OutputSchema?: string | null
  Annotations?: string | null
  Status: string
  DiscoveredAt: string
  Version: number
  Fingerprint: string
  ApprovedFingerprint: string
}

export interface GatewayAuditEvent {
  ID: string
  CallID: string
  Timestamp: string
  UserID: string
  GroupIDs?: string[]
  ClientID?: string
  ConnectorID: string
  PublicName: string
  UpstreamName: string
  Decision: string
  Outcome: string
  DurationMs: number
  ErrorText: string
  PIIAction?: string
  PIIDataTypes?: string[]
}

export interface GatewayUsageSummary {
  Total: number
  Allowed: number
  Denied: number
  UpstreamErrors: number
  AvgDurationMs: number
  ByDay: { Key: string; Count: number; Denied: number; UpstreamErrors: number }[]
  ByTool: { Key: string; Count: number; Denied: number; UpstreamErrors: number }[]
}

export interface GatewayPIIRule {
  ID: string
  WorkspaceID: string
  GroupID: string
  ConnectorID: string
  PublicName: string
  DataType: string
  Direction: string
  Action: string
}

export interface GatewayPIIReview {
  ID: string
  UserID: string
  ConnectorID: string
  PublicName: string
  Direction: string
  DataTypes: string[]
  Status: string
  CreatedAt: string
}

export interface GatewayProvider {
  Name: string
  Key: string
  URL: string
  OAuth: boolean
}

export interface GatewayAPIKey {
  ID: string
  WorkspaceID: string
  GroupID: string
  Label: string
  /** Present only in the create response; listings scrub it. */
  Token: string
  CreatedAt: string
  LastUsedAt: string
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

export function renameGroup(base: string, id: string, name: string): Promise<{ status: string }> {
  return post(base, `/api/admin/groups/${encodeURIComponent(id)}`, { Name: name })
}

export function listGroupKeys(base: string, groupId: string): Promise<{ keys: GatewayAPIKey[] }> {
  return request(base, `/api/admin/groups/${encodeURIComponent(groupId)}/keys`)
}

export function createGroupKey(base: string, groupId: string, label: string): Promise<GatewayAPIKey> {
  return post(base, `/api/admin/groups/${encodeURIComponent(groupId)}/keys`, { Label: label })
}

export function revokeGroupKey(base: string, groupId: string, keyId: string): Promise<void> {
  return request(base, `/api/admin/groups/${encodeURIComponent(groupId)}/keys/${encodeURIComponent(keyId)}`, {
    method: 'DELETE',
  })
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

export function listToolVersions(base: string, publicName: string): Promise<{ versions: GatewayTool[] }> {
  return request(base, `/api/admin/tools/${encodeURIComponent(publicName)}/versions`)
}

export function approveTool(base: string, tool: GatewayTool): Promise<GatewayTool> {
  return post(base, `/api/admin/tools/${encodeURIComponent(tool.PublicName)}/approve`, {
    fingerprint: tool.Fingerprint,
    version: tool.Version,
  })
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

export interface GatewayAuditFilter {
  user?: string
  group?: string
  client?: string
  connector?: string
  tool?: string
  decision?: string
  outcome?: string
  after?: string
  before?: string
}

export function auditPath(filter: GatewayAuditFilter, limit?: number, format?: 'csv' | 'json'): string {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(filter)) if (value) params.set(key, value)
  if (limit) params.set('limit', String(limit))
  if (format) params.set('format', format)
  return `/api/admin/audit?${params.toString()}`
}

export function listAudit(base: string, limit: number, filter: GatewayAuditFilter = {}): Promise<{ events: GatewayAuditEvent[] }> {
  return request(base, auditPath(filter, limit))
}

export function getUsage(base: string, filter: GatewayAuditFilter = {}): Promise<GatewayUsageSummary> {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(filter)) if (value) params.set(key, value)
  return request(base, `/api/admin/usage?${params.toString()}`)
}

export function listPIIRules(base: string): Promise<{ rules: GatewayPIIRule[] }> {
  return request(base, '/api/admin/pii/rules')
}

export function savePIIRule(base: string, rule: Partial<GatewayPIIRule>): Promise<GatewayPIIRule> {
  return post(base, '/api/admin/pii/rules', rule)
}

export function deletePIIRule(base: string, id: string): Promise<void> {
  return request(base, `/api/admin/pii/rules/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export function testPII(base: string, sample: string, direction: string, scope: { GroupIDs?: string[]; ConnectorID?: string; PublicName?: string }): Promise<{
  decision: { action: string; data_types: string[]; match_count: number }
  masked_preview: string
}> {
  return post(base, '/api/admin/pii/test', { Sample: sample, Direction: direction, ...scope })
}

export function listPIIReviews(base: string): Promise<{ reviews: GatewayPIIReview[] }> {
  return request(base, '/api/admin/pii/reviews')
}

export function approvePIIReview(base: string, id: string): Promise<{ status: string }> {
  return post(base, `/api/admin/pii/reviews/${encodeURIComponent(id)}/approve`, {})
}

export function listCatalog(base: string): Promise<{ providers: GatewayProvider[] }> {
  return request(base, '/api/admin/catalog')
}
