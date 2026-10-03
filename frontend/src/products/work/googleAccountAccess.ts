import type { GmailConnection } from '../../services/api-types'

export type GoogleAccessLevel = 'off' | 'read' | 'write'

export const GOOGLE_SERVICES: { key: string; label: string }[] = [
  { key: 'drive', label: 'Drive' },
  { key: 'calendar', label: 'Calendar' },
  { key: 'docs', label: 'Docs' },
  { key: 'sheets', label: 'Sheets' },
  { key: 'slides', label: 'Slides' },
]

/** The access a connection has, in the Connect form's terms. */
export function googleAccessLevels(conn: GmailConnection): { gmail: GoogleAccessLevel; levels: Record<string, GoogleAccessLevel> } {
  const gmail: GoogleAccessLevel = conn.allow_agent_write_access ? 'write' : conn.allow_read_access ? 'read' : 'off'
  const levels: Record<string, GoogleAccessLevel> = {}
  for (const grant of conn.services || []) levels[grant.service] = grant.write ? 'write' : 'read'
  return { gmail, levels }
}

/** What the agent may do with the account, in one line of plain words. */
export function googleAccessSummary(conn: GmailConnection): string {
  const { gmail, levels } = googleAccessLevels(conn)
  const parts = [gmail === 'write' ? 'Gmail: read, draft and send' : gmail === 'read' ? 'Gmail: read' : 'Gmail: send notifications only']
  const label = (key: string) => GOOGLE_SERVICES.find(service => service.key === key)?.label || key
  const read = Object.keys(levels).filter(key => levels[key] === 'read').map(label)
  const edit = Object.keys(levels).filter(key => levels[key] === 'write').map(label)
  if (read.length) parts.push(`${read.join(', ')}: read`)
  if (edit.length) parts.push(`${edit.join(', ')}: read and edit`)
  return parts.join(' · ')
}

export const CHANGE_GOOGLE_ACCESS_EVENT = 'google-account-change-access'

/** Opens this workspace's form with the existing account and its exact access. */
export function changeGoogleAccountAccess(conn: GmailConnection, workspacePath?: string | null) {
  window.dispatchEvent(new CustomEvent(CHANGE_GOOGLE_ACCESS_EVENT, { detail: { id: conn.id, workspacePath, email: conn.email || conn.display_name, ...googleAccessLevels(conn) } }))
}
