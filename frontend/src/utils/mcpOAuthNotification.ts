// Start bounded callback watching only while an MCP sign-in is outstanding.
export const MCP_OAUTH_STARTED_EVENT = 'mcp-oauth-started'
const pending = new Map<string, { count: number; until: number }>()
export function announceMcpOAuthStart(sessionId?: string) {
  if (!sessionId) return
  const previous = pending.get(sessionId)
  pending.set(sessionId, { count: (previous?.count ?? 0) + 1, until: Date.now() + 330_000 })
  window.dispatchEvent(new CustomEvent(MCP_OAUTH_STARTED_EVENT, { detail: sessionId }))
}
export function isWatchingMcpOAuth(sessionId: string) {
  const current = pending.get(sessionId)
  if (!current || current.until < Date.now()) { pending.delete(sessionId); return false }
  return true
}
export function finishMcpOAuthNotifications(sessionId: string, count: number) {
  const current = pending.get(sessionId)
  if (!current) return
  current.count -= count
  if (current.count <= 0) pending.delete(sessionId)
}
