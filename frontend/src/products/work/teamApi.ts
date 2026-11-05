import { useEffect, useState } from 'react'
import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { desktopRequest } from '../../services/desktopConnections'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { hasDesktopConnections, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'

export type Team = { project_id: string; name: string; owner_id: string; owner_name: string; on: boolean; people: { user_id: string; invited?: boolean }[]; folder: { id?: string; whole?: boolean; folders?: string[] }; can_manage: boolean; folder_access: boolean; server_data?: { name: string; size_bytes: number }; lent?: { apps: string[]; secrets: string[] }; where?: 'mac' | 'web' }
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

/** Where this Crew's Team is kept: on the website the Crew itself; in the Mac app its online copy, through the Mac's
 * saved sign-in (undefined until Mac online pairs it). */
async function teamTarget(project: string): Promise<TeamTarget | undefined> {
  if (!(hasDesktopConnections() && getRawActiveWorkspaceId() === 'local')) return { project }
  const { data } = await api.get<{ binding?: { connection_id: string; peer_project_id: string } }>(`/api/crew-sync/${encodeURIComponent(project)}`, { skipSessionContext: true })
  if (!data.binding?.connection_id) return undefined
  return { connection: data.binding.connection_id, project: data.binding.peer_project_id, localProject: project }
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
        const target = await teamTarget(project)
        if (!target) return
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

/** The Team tab as the server writes it for the signed-in person (crew_team_tab.go): every row, and what they may
 * change. The screen shows it and sends the person's choices; it decides nothing itself. */
export type TeamTabPerson = { user_id: string; name: string; note?: 'owner' | 'you' | 'invited'; active: boolean; active_label?: string; can_remove: boolean }
export type TeamTabAgent = { key: string; name: string; detail: string; in_team: boolean; can_change: boolean; fixed?: string; chosen_on_mac?: boolean; address?: string }
export type TeamTabSync = { line: string; problem?: boolean; last?: string; progress?: { direction: 'upload' | 'download'; files: number; bytes_done: number; bytes_total: number } }
export type TeamTabView = {
  team: Team; intro: string; people: TeamTabPerson[]; agents: TeamTabAgent[]
  mac?: { on: boolean; line: string; note: string; yours: boolean }
  sync?: TeamTabSync; switch: 'server' | 'mac'; can_leave: boolean
}

export function useTeamTab(project: string) {
  const workspace = useWorkspaceConnectionStore(s => s.activeWorkspaceId)
  const user = useAuthStore(s => s.user?.id)
  const [result, setResult] = useState<{ view: TeamTabView; target: TeamTarget }>()
  const [error, setError] = useState('')
  useEffect(() => {
    let active = true
    setResult(undefined)
    const load = async () => {
      try {
        const target = await teamTarget(project)
        if (!target) return
        const view = await teamRequest<TeamTabView>(target, '/tab')
        if (active) { setResult({ view, target: { ...target, folderId: view.team.folder.id } }); setError('') }
      } catch (e) { if (active) setError(teamError(e)) }
    }
    if (project) void load()
    const timer = window.setInterval(() => { if (!document.hidden) void load() }, 15000)
    window.addEventListener('agentworks:team-updated', load)
    return () => { active = false; window.clearInterval(timer); window.removeEventListener('agentworks:team-updated', load) }
  }, [project, workspace, user])
  return { ...result, error }
}

export async function saveTeam(target: TeamTarget, team: Team): Promise<Team> {
  const saved = target.connection && target.localProject
    ? await desktopRequest<Team>(`/${encodeURIComponent(target.connection)}/crew-teams/${encodeURIComponent(target.localProject)}`, 'PUT', team)
    : await teamRequest<Team>(target, '', 'PUT', team)
  window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
  return saved
}

export type TeamSwitchResult = { team: Team; shared?: string[]; unshared?: number; folder_created?: boolean; note?: string }

/** The Team tab's switch: the Mac shares the ready agents, makes team/ when no folder is chosen, or turns it all off. */
export async function switchTeam(target: TeamTarget, on: boolean, serverCrew = false): Promise<TeamSwitchResult> {
  // A Crew that lives on the server (owner, 5 Nov): the same Team, switched on the server; team/ is shared by default.
  if (serverCrew && !target.connection) {
    const team = await teamRequest<Team>(target)
    const chosen = team.folder.whole || Boolean(team.folder.folders?.length)
    const saved = await teamRequest<Team>(target, '', 'PUT', { ...team, on, folder: on && !chosen ? { folders: ['team'] } : team.folder })
    window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
    return { team: saved }
  }
  if (!target.connection || !target.localProject) throw new Error('Turn on Use from phone and web for this Crew first.')
  const result = await desktopRequest<TeamSwitchResult>(`/${encodeURIComponent(target.connection)}/crew-teams/${encodeURIComponent(target.localProject)}/switch`, 'POST', { on })
  window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
  return result
}

/** A Team invite as the invited person sees it (owner, 5 Nov): accept on the web or on this Mac, or decline. */
export type TeamInvite = { project_id: string; name: string; owner_name: string; connection?: string }

export async function answerTeamInvite(invite: TeamInvite, accept: boolean) {
  await teamRequest<unknown>({ project: invite.project_id, connection: invite.connection }, '/invite', 'POST', { accept })
  window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
}

export function useTeamInvites() { return useTeamLists().invites }

function useTeamLists() {
  const user = useAuthStore(s => s.user?.id)
  const workspace = useWorkspaceConnectionStore(s => s.activeWorkspaceId)
  const connections = useConnectedWorkStore(s => s.connections)
  const [lists, setLists] = useState<{ teams: { team: Team; target: TeamTarget }[]; invites: TeamInvite[] }>({ teams: [], invites: [] })
  useEffect(() => {
    let active = true
    setLists({ teams: [], invites: [] })
    const load = async () => {
      const results: { team: Team; target: TeamTarget }[] = []
      const invites: TeamInvite[] = []
      const sources = hasDesktopConnections() && getRawActiveWorkspaceId() === 'local' ? connections.filter(c => c.id !== 'local').map(c => c.id) : [undefined]
      await Promise.all(sources.map(async connection => {
        try {
          let data: { teams: Team[]; invites?: TeamInvite[] }
          if (connection) {
            const response = await fetch(`${getDesktopApiBaseUrl()}/api/connections/${encodeURIComponent(connection)}/api/crew-teams`, { headers: { Authorization: `Bearer ${getAuthToken() || ''}` } })
            if (!response.ok) return
            data = await response.json()
          } else data = (await api.get<{ teams: Team[]; invites?: TeamInvite[] }>('/api/crew-teams', { skipSessionContext: true })).data
          for (const team of data.teams || []) if (team.folder_access) results.push({ team, target: { project: team.project_id, connection, folderId: team.folder.id } })
          for (const invite of data.invites || []) invites.push({ ...invite, connection })
        } catch { /* An offline network keeps its copies on the computer. */ }
      }))
      if (active) setLists({ teams: results, invites })
    }
    void load()
    const timer = window.setInterval(() => { if (!document.hidden) void load() }, 15000)
    window.addEventListener('agentworks:team-updated', load)
    return () => { active = false; window.clearInterval(timer); window.removeEventListener('agentworks:team-updated', load) }
  }, [user, workspace, connections])
  return lists
}
