import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'

/** Open the accepted Team publication, not source HTML with a local DB. The
 * destination's ordinary report runtime owns all queries, buttons and writes.
 * Explicit revisions (including drafts) remain authoring previews on the Mac.
 */
export async function openTeamOnlineReport(workspace: string, document: string, isCurrent = () => true, notice: (message: string) => void = () => {}, selected?: { team_project_id: string; source_project_id: string; item_kind: string; dashboard_id: string; connection?: string }): Promise<boolean> {
  if (!/^db\/reports\/managed\/[A-Za-z0-9][A-Za-z0-9_-]*\/index\.html$/.test(document)) return false
  const { connection: saved, ...selector } = selected || {}
  const active = selected ? getRawActiveWorkspaceId() : 'local'
  const connection = saved || (active !== 'local' ? active : undefined)
  type Location = { found: boolean; short_path?: string; connection?: string; notice?: string }
  const payload = { action: 'online_location', workspace, document_path: document, ...selector }
  // Saved-connection requests go to the desktop backend directly, not through
  // api's currently selected workspace prefix (which could be another server).
  const request = connection
    ? fetch(`${getDesktopApiBaseUrl()}/api/connections/${encodeURIComponent(connection)}/api/dashboards`, { method: 'POST', headers: { Authorization: `Bearer ${getAuthToken() || ''}`, 'Content-Type': 'application/json' }, body: JSON.stringify(payload) }).then(async response => {
      const data = await response.json()
      if (!response.ok) throw new Error(data?.error?.message || 'The Team dashboard could not be opened. Try again.')
      return { data: data as Location }
    })
    : api.post<Location>('/api/dashboards', payload)
  const { data } = await request.catch((cause: unknown) => {
    const body = (cause as { response?: { data?: { error?: string | { message?: string } } } })?.response?.data
    const message = typeof body?.error === 'string' ? body.error : body?.error?.message
    throw new Error(message || (cause instanceof Error ? cause.message : 'The Team dashboard could not be opened. Try again.'))
  })
  if (!isCurrent()) return true
  if (!data.found) {
    if (data.notice) notice(data.notice)
    return false
  }
  if (!data.short_path || !/^\/d\/[23456789abcdefghjkmnpqrstuvwxyz]{10}$/.test(data.short_path)) throw new Error('The Team dashboard link is unavailable. Try again.')
  const savedConnection = connection || data.connection
  if (savedConnection) {
    if (!window.electronAPI?.openDesktopConnection) throw new Error('Open this dashboard from its Team on the server.')
    await window.electronAPI.openDesktopConnection(savedConnection, data.short_path)
  } else {
    window.location.assign(data.short_path)
  }
  return true
}
