export interface ParsedOAuthClient {
  clientId: string
  clientSecret: string
  redirectUris: string[]
  projectId?: string
}

/**
 * Reads the client_secret_*.json Google Cloud downloads for an OAuth client
 * ({"web": {...}} or {"installed": {...}}), so nobody copies two values by
 * hand. Returns null for anything else.
 */
export function parseOAuthClientJson(text: string): ParsedOAuthClient | null {
  let data: unknown
  try { data = JSON.parse(text) } catch { return null }
  if (!data || typeof data !== 'object') return null
  const root = data as Record<string, unknown>
  const body = (root.web ?? root.installed) as Record<string, unknown> | undefined
  if (!body || typeof body !== 'object') return null
  const clientId = typeof body.client_id === 'string' ? body.client_id.trim() : ''
  const clientSecret = typeof body.client_secret === 'string' ? body.client_secret.trim() : ''
  if (!clientId || !clientSecret) return null
  const redirectUris = Array.isArray(body.redirect_uris) ? body.redirect_uris.filter((uri): uri is string => typeof uri === 'string') : []
  return { clientId, clientSecret, redirectUris, projectId: typeof body.project_id === 'string' ? body.project_id : undefined }
}
