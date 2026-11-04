import { useEffect, useState } from 'react'
import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { desktopRequest } from '../../services/desktopConnections'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { hasDesktopConnections, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'

export type Team = { project_id: string; name: string; owner_id: string; owner_name: string; on: boolean; people: { user_id: string; can_edit_shared_folder: boolean }[]; folder: { id?: string; path: string; label: string }; can_manage: boolean; can_edit_shared_folder: boolean; server_data?: { name: string; size_bytes: number } }
export type TeamTarget = { project: string; connection?: string; localProject?: string; folderId?: string }
export type TeamClash = { path: string; source: string; winning_revision: number; losing_revision: number; winning_author: string; losing_author: string }
export type TeamEvent = { path: string; kind: string; revision: number; author: string; time: string }
export const teamError = (e: unknown) => e instanceof Error ? e.message : 'Could not open the shared folder. Try again.'

export async function teamRequest<T>(target: TeamTarget, suffix = '', method = 'GET', body?: unknown, raw: boolean | 'blob' = false): Promise<T> {
  const route = `/api/crew-teams/${encodeURIComponent(target.project)}${suffix}${suffix && target.folderId ? `${suffix.includes('?') ? '&' : '?'}folder_id=${encodeURIComponent(target.folderId)}` : ''}`
  if (!target.connection) {
    const { data } = await api.request<T>({ url: route, method, data: body, skipSessionContext: true, ...(raw ? { responseType: raw === 'blob' ? 'blob' : 'text', headers: { 'Content-Type': 'application/octet-stream' } } : {}) })
    return data
  }
  const response = await fetch(`${getDesktopApiBaseUrl()}/api/connections/${encodeURIComponent(target.connection)}${route}`, {
    method, headers: { Authorization: `Bearer ${getAuthToken() || ''}`, 'Content-Type': raw ? 'application/octet-stream' : 'application/json' },
    ...(body === undefined ? {} : { body: typeof body === 'string' || body instanceof Blob ? body : JSON.stringify(body) }),
  })
  if (!response.ok) { const data = await response.json().catch(() => null); throw new Error(data?.error?.message || 'The shared folder is unavailable. Check your Team access.') }
  return (response.status === 204 ? '' : raw === 'blob' ? await response.blob() : raw ? await response.text() : await response.json()) as T
}

export function useProjectTeam(project: string) {
  const workspace = useWorkspaceConnectionStore(s => s.activeWorkspaceId)
  const user = useAuthStore(s => s.user?.id)
  const [result, setResult] = useState<{ team: Team; target: TeamTarget }>()
  const [error, setError] = useState('')
  const [version, setVersion] = useState(0)
  useEffect(() => {
    let active = true
    setResult(undefined)
    const load = async () => {
      try {
        let target: TeamTarget = { project }
        if (hasDesktopConnections() && getRawActiveWorkspaceId() === 'local') {
          const { data } = await api.get<{ binding?: { connection_id: string; peer_project_id: string } }>(`/api/crew-sync/${encodeURIComponent(project)}`, { skipSessionContext: true })
          if (!data.binding?.connection_id) return
          target = { connection: data.binding.connection_id, project: data.binding.peer_project_id, localProject: project }
        }
        const team = await teamRequest<Team>(target)
        if (active) { setResult({ team, target: { ...target, folderId: team.folder.id } }); setError('') }
      } catch (e) { if (active) setError(teamError(e)) }
    }
    if (project) void load()
    const timer = window.setInterval(() => { if (!document.hidden) void load() }, 15000)
    const refresh = () => void load()
    window.addEventListener('agentworks:team-updated', refresh)
    return () => { active = false; window.clearInterval(timer); window.removeEventListener('agentworks:team-updated', refresh) }
  }, [project, workspace, user, version])
  return { ...result, error, refresh: () => setVersion(v => v + 1) }
}

export async function saveTeam(target: TeamTarget, team: Team): Promise<Team> {
  const saved = target.connection && target.localProject
    ? await desktopRequest<Team>(`/${encodeURIComponent(target.connection)}/crew-teams/${encodeURIComponent(target.localProject)}`, 'PUT', team)
    : await teamRequest<Team>(target, '', 'PUT', team)
  window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
  return saved
}

export type TeamSwitchResult = { team: Team; shared?: string[]; needs_purpose?: string[]; unshared?: number; folder_created?: boolean; note?: string }

/** The Team tab's switch: the Mac shares the ready agents, makes team/ when no folder is chosen, or turns it all off. */
export async function switchTeam(target: TeamTarget, on: boolean): Promise<TeamSwitchResult> {
  if (!target.connection || !target.localProject) throw new Error('Turn on Use from phone and web for this Crew first.')
  const result = await desktopRequest<TeamSwitchResult>(`/${encodeURIComponent(target.connection)}/crew-teams/${encodeURIComponent(target.localProject)}/switch`, 'POST', { on })
  window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
  return result
}

export function useTeams() {
  const user = useAuthStore(s => s.user?.id)
  const workspace = useWorkspaceConnectionStore(s => s.activeWorkspaceId)
  const connections = useConnectedWorkStore(s => s.connections)
  const [teams, setTeams] = useState<{ team: Team; target: TeamTarget }[]>([])
  useEffect(() => {
    let active = true
    setTeams([])
    const load = async () => {
      const results: { team: Team; target: TeamTarget }[] = []
      const sources = hasDesktopConnections() && getRawActiveWorkspaceId() === 'local' ? connections.filter(c => c.id !== 'local').map(c => c.id) : [undefined]
      await Promise.all(sources.map(async connection => {
        try {
          let data: { teams: Team[] }
          if (connection) {
            const response = await fetch(`${getDesktopApiBaseUrl()}/api/connections/${encodeURIComponent(connection)}/api/crew-teams`, { headers: { Authorization: `Bearer ${getAuthToken() || ''}` } })
            if (!response.ok) return
            data = await response.json()
          } else data = (await api.get<{ teams: Team[] }>('/api/crew-teams', { skipSessionContext: true })).data
          for (const team of data.teams || []) if (team.can_edit_shared_folder) results.push({ team, target: { project: team.project_id, connection, folderId: team.folder.id } })
        } catch { /* An offline network keeps its copies on the computer. */ }
      }))
      if (active) setTeams(results)
    }
    void load()
    const timer = window.setInterval(() => { if (!document.hidden) void load() }, 15000)
    window.addEventListener('agentworks:team-updated', load)
    return () => { active = false; window.clearInterval(timer); window.removeEventListener('agentworks:team-updated', load) }
  }, [user, workspace, connections])
  return teams
}
