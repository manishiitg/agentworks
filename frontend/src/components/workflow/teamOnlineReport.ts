import api from '../../services/api'

/** Open the accepted Team publication, not source HTML with a local DB. The
 * destination's ordinary report runtime owns all queries, buttons and writes.
 * Explicit revisions (including drafts) remain authoring previews on the Mac.
 */
export async function openTeamOnlineReport(workspace: string, document: string, isCurrent = () => true): Promise<boolean> {
  if (!/^db\/reports\/managed\/[A-Za-z0-9][A-Za-z0-9_-]*\/index\.html$/.test(document)) return false
  const { data } = await api.post<{ found: boolean; short_path?: string; connection?: string }>('/api/dashboards', { action: 'online_location', workspace, document_path: document })
  if (!isCurrent()) return true
  if (!data.found) return false
  if (!data.short_path || !/^\/d\/[23456789abcdefghjkmnpqrstuvwxyz]{10}$/.test(data.short_path)) throw new Error('The Team dashboard link is unavailable. Try again.')
  if (data.connection) {
    if (!window.electronAPI?.openDesktopConnection) throw new Error('Open this dashboard from its Team on the server.')
    await window.electronAPI.openDesktopConnection(data.connection, data.short_path)
  } else {
    window.location.assign(data.short_path)
  }
  return true
}
