import api from '../../services/api'

/**
 * A person who only runs someone else's product lands on its dashboard. When
 * the owner built none, the server writes the default one from what the
 * product declares; a failed request just leaves the person on today's view.
 */
export async function ensureDefaultDashboard(workspacePath: string): Promise<void> {
  try {
    await api.post('/api/dashboards', { workspace: workspacePath, action: 'list', limit: 1 })
  } catch { /* the usual landing view still works */ }
}
