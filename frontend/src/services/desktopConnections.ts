import { getDesktopApiBaseUrl, getAuthToken } from './api'
import type { AuthUser } from './api'

export type DesktopConnection = { id: string; name: string; server: string; user?: AuthUser }
export type DesktopProject = { id: string; name: string; kind: 'crew' | 'goal'; mode?: 'builder' | 'run' }
export type DesktopInventory = { status: 'connected' | 'sign_in' | 'unavailable'; projects: DesktopProject[] }

export async function desktopRequest<T>(path = '', method = 'GET', body?: unknown): Promise<T> {
  const response = await fetch(`${getDesktopApiBaseUrl()}/api/desktop-connections${path}`, {
    method, headers: { Authorization: `Bearer ${getAuthToken() || ''}`, 'Content-Type': 'application/json' },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  })
  if (!response.ok) throw new Error(response.status === 410 ? 'Sign-in expired. Try again.' : 'Could not reach this connection. Try again.')
  return response.status === 204 ? undefined as T : response.json()
}
