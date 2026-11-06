import { useEffect, useState } from 'react'
import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import type { AgentProfileSideChat } from '../../services/api-types'
import { desktopRequest } from '../../services/desktopConnections'
import { liveFeed } from '../../services/liveFeed'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { registerRemoteSession } from '../../utils/remoteSessions'
import { hasDesktopConnections, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'

export type Team = { project_id: string; name: string; owner_id: string; owner_name: string; on: boolean; people: { user_id: string; invited?: boolean }[]; folder: { id?: string; whole?: boolean; folders?: string[] }; can_manage: boolean; folder_access: boolean; server_data?: { name: string; size_bytes: number }; lent?: TeamLent; where?: 'mac' | 'web' }
// What teammates' agents use from the owner, as the server writes it (crew_team.go crewTeamLent).
export type TeamLent = { title: string; line: string; foot?: string; empty?: string; yours: boolean; items: { name: string; kind: string }[] }
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

/** The Mac app's own Crew is kept online through Mac online (its crew-sync binding); undefined elsewhere. */
async function macBinding(project: string): Promise<{ connection: string; peer: string; side?: string } | undefined> {
  if (!(hasDesktopConnections() && getRawActiveWorkspaceId() === 'local')) return undefined
  const { data } = await api.get<{ binding?: { connection_id: string; peer_project_id: string; side?: string } }>(`/api/crew-sync/${encodeURIComponent(project)}`, { skipSessionContext: true })
  return data.binding?.connection_id ? { connection: data.binding.connection_id, peer: data.binding.peer_project_id, side: data.binding.side } : undefined
}

/** Where this Crew's Team is kept: on the website the Crew itself; in the Mac app its online copy, through the Mac's
 * saved sign-in (undefined until Mac online pairs it). */
async function teamTarget(project: string): Promise<TeamTarget | undefined> {
  if (!(hasDesktopConnections() && getRawActiveWorkspaceId() === 'local')) return { project }
  const binding = await macBinding(project)
  return binding ? { connection: binding.connection, project: binding.peer, localProject: project } : undefined
}

/** The Team tab as the server writes it for the signed-in person (crew_team_tab.go): every row, and what they may
 * change. Every screen shows it and sends the person's choices; none decides anything itself. */
export type TeamTabPerson = { user_id: string; name: string; note?: 'owner' | 'you' | 'invited'; active: boolean; active_label?: string; can_remove: boolean }
// mine: the viewer's own agent (the Agents card lists only these). can_unshare is false on their last shared agent;
// fixed then says why.
export type TeamTabAgent = { key: string; name: string; detail: string; in_team: boolean; can_change: boolean; can_unshare: boolean; fixed?: string; address?: string; owner_id: string; owner_name: string; mine: boolean; place: 'mac' | 'server'; status_label?: string; tab: string
  // Always on (crew_agent_always_on.go): where an agent of the owner's Mac answers now and where its server copy
  // stands, worded by the server; place_tone only picks the chip's colour. not_shared_line: the muted line of the
  // viewer's own agent that is not shared.
  place_label?: string; place_tone?: 'ok' | 'wait' | 'server' | ''; not_shared_line?: string
  // When others ask (crew_when_others_ask.go): names only; personal marks the viewer's own sign-ins (sent only to them).
  uses: { name: string; kind: string; personal?: boolean }[]; when_others_ask: string; has_rule: boolean; can_make_changes?: boolean; can_edit_rule: boolean; when_others_line: string
  // The rule editor's Ask message (the viewer's own agent).
  permissions_prompt?: string
  // The sharing list (crew_team_tab.go): its purpose, whether the viewer edits it (POST agent-rule {purpose}), and its Public on
  // AgentBridge (a Mac Crew's owner's own rows): needs_purpose shows Set purpose instead of the switch; state is the
  // pill while it is public ("public · online").
  purpose: string; can_set_purpose: boolean
  // copy_line is the owner's coding-app line for this one agent; copy_line_why says why there is none yet.
  agentbridge?: { public: boolean; can_change: boolean; needs_purpose?: boolean; state?: string; copy_line?: string; copy_line_why?: string }
  // The tab's setup icon and card rows (crew_tab_setup.go), worded by the server, for everyone who sees the agent.
  setup?: TeamTabSetup }
export type TeamTabSetup = { reach: 'agentbridge' | 'team' | ''; starts_on_own: boolean; agentbridge_line?: string; automations: { kind: string; name: string; line: string; on: boolean }[] }
export type TeamTabSync = { line: string; problem?: boolean; last?: string; progress?: { direction: 'upload' | 'download'; files: number; bytes_done: number; bytes_total: number } }
export type TeamTabView = {
  team: Team; intro: string; people: TeamTabPerson[]; agents: TeamTabAgent[]
  sync?: TeamTabSync; can_switch: boolean; agents_line?: string; can_leave: boolean
  can_choose: boolean; can_add_folder: boolean; can_remove_folder: boolean
  // The cards' Ask messages, written by the server and sent as given (crew_team_tab.go crewTeamAskPrompts).
  asks: Record<string, string>
}
/** unbound: the Mac app's own Crew before Mac online pairs it, so there is no Team on a server yet (Team is off). */
export type TeamTabState = { view?: TeamTabView; target?: TeamTarget; unbound?: boolean; error: string }

// One read of the Team tab per Crew for the whole page (the Team tab, the left menu, tabs, watching lines): every
// screen shares it. A live notice (crew_panel) or a Team change reads it again at once; otherwise every 15 s.
type TabEntry = { state: TeamTabState; listeners: Set<(state: TeamTabState) => void>; load: () => Promise<void>; stop: () => void }
const tabCache = new Map<string, TabEntry>()

function watchTeamTab(project: string, key: string, listener: (state: TeamTabState) => void) {
  let entry = tabCache.get(key)
  if (!entry) {
    // A turn sends many crew_panel notices: one read at a time (a notice during a read reads once more after it), and an
    // unchanged reply is not published, so the tab strip, left menu and agent cards only re-render on a real change.
    let reading = false
    let again = false
    let last = ''
    const created: TabEntry = { state: { error: '' }, listeners: new Set(), load: async () => {}, stop: () => {} }
    const publish = (state: TeamTabState) => {
      const text = JSON.stringify(state)
      if (text === last) return
      last = text
      created.state = state
      created.listeners.forEach(notify => notify(state))
    }
    created.load = async () => {
      if (reading) { again = true; return }
      reading = true
      try {
        const target = await teamTarget(project)
        if (!target) publish({ unbound: true, error: '' })
        else {
          const view = await teamRequest<TeamTabView>(target, '/tab')
          publish({ view, target: { ...target, folderId: view.team.folder.id }, error: '' })
        }
      } catch (e) { publish({ ...created.state, error: teamError(e) }) }
      reading = false
      if (again) { again = false; void created.load() }
    }
    const reload = () => { void created.load() }
    const timer = window.setInterval(() => { if (!document.hidden) reload() }, 15000)
    window.addEventListener('agentworks:team-updated', reload)
    const unlive = liveFeed.subscribe(['crew_panel'], null, reload, project)
    created.stop = () => { window.clearInterval(timer); window.removeEventListener('agentworks:team-updated', reload); unlive() }
    tabCache.set(key, created)
    entry = created
    reload()
  }
  const watched = entry
  watched.listeners.add(listener)
  listener(watched.state)
  return () => {
    watched.listeners.delete(listener)
    if (watched.listeners.size) return
    watched.stop()
    tabCache.delete(key)
  }
}

export function useTeamTab(project?: string): TeamTabState {
  const workspace = useWorkspaceConnectionStore(s => s.activeWorkspaceId)
  const user = useAuthStore(s => s.user?.id)
  const [state, setState] = useState<TeamTabState>({ error: '' })
  useEffect(() => {
    setState({ error: '' })
    if (!project || !user) return
    return watchTeamTab(project, `${workspace}\x00${user}\x00${project}`, setState)
  }, [project, workspace, user])
  return state
}

const changed = () => window.dispatchEvent(new CustomEvent('agentworks:team-updated'))

export async function saveTeam(target: TeamTarget, team: Team): Promise<Team> {
  const saved = target.connection && target.localProject
    ? await desktopRequest<Team>(`/${encodeURIComponent(target.connection)}/crew-teams/${encodeURIComponent(target.localProject)}`, 'PUT', team)
    : await teamRequest<Team>(target, '', 'PUT', team)
  changed()
  return saved
}

/** Adds or removes one person; the server changes its record (crew_team_people.go). */
export async function changeTeamPeople(target: TeamTarget, change: { add: string } | { remove: string }) {
  await teamRequest<Team>({ project: target.project, connection: target.connection }, '/people', 'POST', change)
  changed()
}

export type TeamSwitchResult = { team: Team; folder_created?: boolean }

/** The Team tab's switch is the server's record (crew_team.go decides the default folder and All agents in team). The
 * owner's own Mac app switches through its Mac, which also turns Mac online on and applies the record at once. */
export async function switchTeam(target: TeamTarget, on: boolean, ownMac = false): Promise<TeamSwitchResult> {
  if (!ownMac || !target.connection || !target.localProject) {
    const team = await teamRequest<Team>(target)
    const saved = await teamRequest<Team>(target, '', 'PUT', { ...team, on })
    changed()
    return { team: saved }
  }
  const result = await desktopRequest<TeamSwitchResult>(`/${encodeURIComponent(target.connection)}/crew-teams/${encodeURIComponent(target.localProject)}/switch`, 'POST', { on })
  changed()
  return result
}

/** One of the Team's agents by its chat key. A Mac app tab of a teammate's agent carries the server's key after
 * ":team:" (teamSideChats). */
export function teamAgentFor(agents: TeamTabAgent[] | undefined, chatKey?: string) {
  if (!chatKey) return undefined
  return agents?.find(item => item.key === chatKey || chatKey.endsWith(`:team:${item.key}`))
}

/** The Team tab's row for one chat tab here: someone else's by its key; one of the person's own by its key on the
 * website, by its Mac tab ("main" or the tab id) in the Mac app, whose own tabs use this Mac's keys. */
export function teamAgentForTab(state: TeamTabState, key: string | undefined, main: boolean, own: boolean) {
  const agents = state.view?.agents
  if (!agents || !key) return undefined
  if (!own || !state.target?.localProject) return teamAgentFor(agents, key)
  const tab = main ? 'main' : key.split(':chat:')[1]
  return agents.find(agent => agent.can_set_purpose && agent.tab === tab)
}

/** One folder of the Crew for the Team's "+ Share this folder" and the shared folders' sizes (path=, or whole=1),
 * the same for the Mac app and the website: the server reads it where the Crew is (its own folder, or the owner's
 * Mac over Mac online) and, with that Mac off, says so in offline (crew_team_folders.go). */
export type TeamFolderListing = { folders: string[]; path: string; files: number; bytes: number; offline?: string }
export function teamFolders(target: TeamTarget, query: string): Promise<TeamFolderListing> {
  return teamRequest<TeamFolderListing>({ project: target.project, connection: target.connection }, `/folders?${query}`)
}

/** The Folders / Whole Crew confirm box, worded by the server (crew_team_folder_switch.go); refusal when too big. */
export type TeamFolderChange = { whole: boolean; title: string; sub: string; people: string[]; folders_added?: string[]; folders_kept?: string[]; means: string[]; note?: string; confirm_label?: string; refusal?: string }
export function teamFolderChange(target: TeamTarget, whole: boolean): Promise<TeamFolderChange> {
  return teamRequest<TeamFolderChange>({ project: target.project, connection: target.connection }, `/folder-change?whole=${whole ? 1 : 0}`)
}

/** The Team members' own agents in the owner's Mac app (owner, 5 Nov): the online copy's side-chat rows the person
 * does not write, as real tabs of this Crew. Their chats are read from the server through this Mac's sign-in
 * (utils/remoteSessions.ts); the key carries the server's after ":team:". Empty outside the Mac app's own Crew. */
export async function teamSideChats(profileId: string, projectId: string): Promise<AgentProfileSideChat[]> {
  const binding = await macBinding(projectId)
  if (!binding || binding.side !== 'local') return []
  const response = await fetch(`${getDesktopApiBaseUrl()}/api/connections/${encodeURIComponent(binding.connection)}/api/agent-profiles/${encodeURIComponent(profileId)}/side-chats?project_id=${encodeURIComponent(binding.peer)}`, { headers: { Authorization: `Bearer ${getAuthToken() || ''}` } })
  if (!response.ok) return []
  const rows = await response.json() as AgentProfileSideChat[]
  return (rows || []).filter(row => row.can_write === false && row.tab_owner_id && row.session_id && !row.is_main).map(row => {
    registerRemoteSession(row.session_id, binding.connection)
    return { ...row, conversation_key: `${projectId}:team:${row.conversation_key}`, chat_location: undefined, agent_id: undefined }
  })
}

/** A Team invite as the invited person sees it (owner, 5 Nov): accept on the web or on this Mac, or decline. */
export type TeamInvite = { project_id: string; name: string; owner_name: string; connection?: string }

export async function answerTeamInvite(invite: TeamInvite, accept: boolean) {
  await teamRequest<unknown>({ project: invite.project_id, connection: invite.connection }, '/invite', 'POST', { accept })
  changed()
}

/** Open Team invites for the signed-in person, from each connected server in the Mac app, else this one. */
export function useTeamInvites() {
  const user = useAuthStore(s => s.user?.id)
  const workspace = useWorkspaceConnectionStore(s => s.activeWorkspaceId)
  const connections = useConnectedWorkStore(s => s.connections)
  const [invites, setInvites] = useState<TeamInvite[]>([])
  useEffect(() => {
    let active = true
    setInvites([])
    const load = async () => {
      const found: TeamInvite[] = []
      const sources = hasDesktopConnections() && getRawActiveWorkspaceId() === 'local' ? connections.filter(c => c.id !== 'local').map(c => c.id) : [undefined]
      await Promise.all(sources.map(async connection => {
        try {
          let data: { invites?: TeamInvite[] }
          if (connection) {
            const response = await fetch(`${getDesktopApiBaseUrl()}/api/connections/${encodeURIComponent(connection)}/api/crew-teams`, { headers: { Authorization: `Bearer ${getAuthToken() || ''}` } })
            if (!response.ok) return
            data = await response.json()
          } else data = (await api.get<{ invites?: TeamInvite[] }>('/api/crew-teams', { skipSessionContext: true })).data
          for (const invite of data.invites || []) found.push({ ...invite, connection })
        } catch { /* An offline network keeps its copies on the computer. */ }
      }))
      if (active) setInvites(found)
    }
    void load()
    const timer = window.setInterval(() => { if (!document.hidden) void load() }, 15000)
    window.addEventListener('agentworks:team-updated', load)
    return () => { active = false; window.clearInterval(timer); window.removeEventListener('agentworks:team-updated', load) }
  }, [user, workspace, connections])
  return invites
}
