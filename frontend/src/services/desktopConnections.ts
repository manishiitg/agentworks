import { getDesktopApiBaseUrl, getAuthToken } from './api'
import type { AuthUser } from './api'

export type DesktopConnection = { id: string; name: string; server: string; user?: AuthUser }
export type DesktopProject = { id: string; name: string; kind: 'crew' | 'goal'; icon?: string; mode?: 'builder' | 'run' }
export type DesktopInventory = { status: 'connected' | 'sign_in' | 'unavailable'; projects: DesktopProject[] }

export async function desktopRequest<T>(path = '', method = 'GET', body?: unknown): Promise<T> {
  const response = await fetch(`${getDesktopApiBaseUrl()}/api/desktop-connections${path}`, {
    method, headers: { Authorization: `Bearer ${getAuthToken() || ''}`, 'Content-Type': 'application/json' },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  }).catch(() => { throw new Error('Could not reach the local app. Check that the backend is running, then try again.') })
  if (!response.ok) {
    const body = (await response.text()).trim()
    let message = ''
    try {
      const data = JSON.parse(body)
      if (typeof data.error === 'string') message = data.error
      else if (typeof data.error?.message === 'string') message = data.error.message
    } catch {
      // The local backend also uses plain-text validation errors.
      if (body && !body.startsWith('<')) message = body
    }
    message ||= response.status === 410 ? 'Sign-in expired. Try again.' : 'Could not complete this request. Try again.'
    throw new Error(`${message} (HTTP ${response.status})`)
  }
  return response.status === 204 ? undefined as T : response.json()
}
