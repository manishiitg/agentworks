import { useEffect, useState } from 'react'
import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import type { AgentProfileSideChat } from '../../services/api-types'
import { desktopRequest } from '../../services/desktopConnections'
import { liveFeed } from '../../services/liveFeed'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { registerRemoteSession } from '../../utils/remoteSessions'
import { hasDesktopConnections, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import type { PublicLink, PublicLinkMode } from '../../components/workflow/PublicLinkControl'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'

export type Team = { change_on_mac?: string; project_id: string; name: string; owner_id: string; owner_name: string; on: boolean; people: { user_id: string; invited?: boolean }[]; folder: { id?: string; whole?: boolean; folders?: string[] }; can_manage: boolean; folder_access: boolean; server_data?: { name: string; size_bytes: number }; lent?: TeamLent; where?: 'mac' | 'web' }
// What teammates' agents use from the owner, as the server writes it (crew_team.go crewTeamLent).
export type TeamLent = { title: string; line: string; foot?: string; empty?: string; yours: boolean; items: { name: string; kind: string }[]; groups: { title: string; line: string; key?: boolean; names: string[] }[] }
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
  if (!response.ok) { const data = await response.json().catch(() => null); throw Object.assign(new Error(data?.error?.message || 'The shared folder is unavailable. Check your Team access.'), { code: data?.error?.code as string | undefined }) }
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
export type TeamTabPerson = { user_id: string; name: string; note?: 'owner' | 'you' | 'invited'; active: boolean; active_label?: string; can_remove: boolean; presence?: TeamTabPresence }
// A teammate's Mac, as the server words it (crew_team_tab.go); absent when their agents work on the server.
// tone picks the dot's colour: on (Mac on), server (Mac off, shared agents answer from the server), away (they wait).
export type TeamTabPresence = { online: boolean; line: string; tone?: 'on' | 'server' | 'away' }
// mine: the viewer's own agent (the Agents card lists only these). can_unshare is false on their last shared agent;
// fixed then says why.
export type TeamTabAgent = { key: string; name: string; detail: string; in_team: boolean; can_change: boolean; can_unshare: boolean; fixed?: string; address?: string; owner_id: string; owner_name: string; mine: boolean; place: 'mac' | 'server'; status_label?: string; tab: string
  // Always on (crew_agent_always_on.go): where an agent of the owner's Mac answers now and where its server copy
  // stands, worded by the server; place_tone only picks the chip's colour. not_shared_line: the muted line of the
  // viewer's own agent that is not shared.
  place_label?: string; place_tone?: 'ok' | 'wait' | 'server' | ''; not_shared_line?: string
  // activity: "running" while a turn is going in the agent now, as the server reads it (their Mac's relayed events, or its own queue).
  activity?: 'running' | ''
  // When others ask (crew_when_others_ask.go): names only; personal marks the viewer's own sign-ins (sent only to them).
  uses: { name: string; kind: string; personal?: boolean }[]; uses_note?: string; when_others_ask: string; has_rule: boolean; can_make_changes?: boolean; can_edit_rule: boolean; when_others_line: string
  // Teammates see (crew_team_chat_private.go): chat_share_label and chat_share_options are the choice on the viewer's own
  // agent; on someone else's, chat_private_line replaces the chat. watch_key is its
  // tab's key where that differs.
  chat_share: string; chat_share_label?: string; chat_share_options?: { value: string; label: string }[]; chat_private_line?: string; watch_key?: string
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
  sync?: TeamTabSync; can_switch: boolean; agents_line?: string; can_leave: boolean; leave_confirm?: string
  can_choose: boolean; can_add_folder: boolean; can_remove_folder: boolean
  // The cards' Ask messages, written by the server and sent as given (crew_team_tab.go crewTeamAskPrompts).
  asks: Record<string, string>
  // The Crew has no Team yet and the viewer owns it: open the first-time wizard (crew_team_wizard.go).
  new_team?: boolean
  // The agent menu's "How this works" card, worded by the server for the states that exist in this Crew; help_ask is the
  // draft its button opens in Main (absent when the viewer changes nothing here, then there is no button).
  help?: { title: string; text: string }[]; help_ask?: string
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

/** The People list (crew_people.go): the Team and the Share as one list with two levels. The server words every line and
 * says what each person may be moved to; the card shows them and sends the person's choice. */
export type CrewPeopleLevelKey = 'works' | 'dashboards'
export type CrewPeoplePerson = { user_id: string; name: string; email?: string; level: CrewPeopleLevelKey | 'owner'; level_label: string; note?: string; invited?: boolean; owner?: boolean; you?: boolean; moves?: (CrewPeopleLevelKey | 'none')[] }
export type CrewPeopleLevel = { key: CrewPeopleLevelKey; label: string; line: string; available: boolean; why?: string }
export type CrewPeopleView = {
  kind: 'server' | 'mac_online' | 'mac_never_online'; team_on: boolean; intro: string; blocked?: string; put_online?: string; add_everyone?: string
  can_add: boolean; can_share: boolean; owner: CrewPeoplePerson; people: CrewPeoplePerson[]; levels: CrewPeopleLevel[]
  gets: { title: string; tagline: string; items: { text: string; yes: boolean }[] }[]
}
export type CrewPeoplePreview = { person: string; from: string; to: string; summary: string; gains: string[]; loses: string[]; notes: string[] }
export type CrewPeopleChange = { person: string; level: CrewPeopleLevelKey | 'none'; confirm?: boolean; create_dashboard?: boolean }
export type CrewPeopleReply = { confirm_needed?: boolean; preview?: CrewPeoplePreview; changed?: boolean; note?: string }

/** The server's own words for a refusal, and its code (the one the card acts on is dashboard_needed). */
export function teamFailure(cause: unknown): { code: string; message: string } {
  const data = (cause as { response?: { data?: { error?: { code?: string; message?: string } | string } } })?.response?.data
  const error = typeof data?.error === 'object' ? data.error : undefined
  return { code: error?.code || (cause as { code?: string })?.code || '', message: error?.message || (typeof data?.error === 'string' ? data.error : '') || teamError(cause) }
}

export function crewPeople(target: TeamTarget): Promise<CrewPeopleView> {
  return teamRequest<CrewPeopleView>({ project: target.project, connection: target.connection }, '/people')
}

/** One person's level: first what they gain and lose (confirm_needed), saved only with confirm=true (crew_people.go). */
export async function changeCrewPerson(target: TeamTarget, change: CrewPeopleChange): Promise<CrewPeopleReply> {
  const reply = await teamRequest<CrewPeopleReply>({ project: target.project, connection: target.connection }, '/people', 'POST', change)
  if (!reply.confirm_needed) changed()
  return reply
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
  return agents?.find(item => item.key === chatKey || item.watch_key === chatKey || chatKey.endsWith(`:team:${item.key}`))
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
/** The Dashboards tab (crew_dashboards_tab.go): everything the Crew publishes, each with who can open it and, on a
 * server Crew, its public link. Every line is the server's; the card shows them. */
export type CrewDashboardsTab = {
  workspace: string; intro: string; can_change: boolean; on_mac?: boolean; put_online?: boolean; note?: string; filter: string
  filters: { key: string; label: string; count: number; hint?: string; ask?: string }[]
  items: { id: string; title: string; path: string; kind: 'dashboard' | 'artifact' | 'motion'; line: string; who: string; public_link?: PublicLink }[]
  empty?: { title: string; line: string; kinds?: { name: string; line: string }[]; ask?: string; example?: string }
}
export function crewDashboardsTab(target: TeamTarget, kind: string): Promise<CrewDashboardsTab> {
  return teamRequest<CrewDashboardsTab>({ project: target.project, connection: target.connection }, `/dashboards-tab?kind=${encodeURIComponent(kind)}`)
}
export function teamPublicLink(target: TeamTarget, id: string, mode: PublicLinkMode): Promise<PublicLink> {
  return teamRequest<PublicLink>({ project: target.project, connection: target.connection }, '/dashboards/public-link', 'POST', { dashboard_id: id, mode })
}
export function teamFolders(target: TeamTarget, query: string): Promise<TeamFolderListing> {
  return teamRequest<TeamFolderListing>({ project: target.project, connection: target.connection }, `/folders?${query}`)
}

/** The Share page (crew_share_page.go, owner 9 Nov mocks): four options with the server's status and chips, the
 * viewer's agents (the share switch with its permissions) and the owner's actions, the who-can-use line and the dashboards
 * count. Every word and every "may change" is the server's; the page prints them and sends the person's change. */
export type CrewShareOptionKey = 'team' | 'agents'
export type CrewShareOption = { key: CrewShareOptionKey; title: string; line: string; chips: string[]; status: string; on: boolean; open: boolean }
export type CrewShareAsk = { on: boolean; can_change: boolean; fixed?: string; note?: string; can_edit: boolean; can_make_changes: boolean
  title: string; line: string; changes_label: string; changes_options: { value: boolean; label: string; line: string }[]
  chat_share: string; chat_share_label: string; chat_share_options: { value: string; label: string }[]; rules_label: string }
export type CrewShareAction = { name: string; title: string; line?: string; inputs: string[]; on: boolean; can_change: boolean; fixed?: string; target: string; starts: string; who: string }
export type CrewShareAgent = { key: string; name: string; line?: string; ask?: CrewShareAsk }
/** The one "Actions" group (crew_share_page.go crewShareActionsGroup). */
export type CrewShareActionsGroup = { title: string; line: string; rows: CrewShareAction[]; setup_ask?: string; wizard_for?: string }
/** What other people share with the viewer (crew_share_page.go crewShareSharedWithYou): people, then their agents and actions. */
export type CrewShareShared = { name: string; role: string; items: { name: string; line: string; can: string }[] }
/** Share › Slack's Who can talk to it (crew_share_page.go crewShareSlack): one line per rule, then a note. */
export type CrewShareSlack = { rules: { allowed: boolean; where?: string; line: string }[]; note?: string }
export type CrewSharePage = { title: string; lead: string; options: CrewShareOption[]; slack?: CrewShareSlack; agents: CrewShareAgent[]; agents_intro: string; agents_empty?: string; who_can_use: string; actions_group: CrewShareActionsGroup; shared_with_you: CrewShareShared[] }
export function crewSharePage(target: TeamTarget): Promise<CrewSharePage> {
  return teamRequest<CrewSharePage>({ project: target.project, connection: target.connection }, '/share')
}
/** One action's own switch (POST action, the Crew's owner only): target is '' for the Crew's own actions, else the workflow. */
export async function setCrewAction(target: TeamTarget, action: { target: string; name: string; on: boolean }): Promise<void> {
  await teamRequest<unknown>({ project: target.project, connection: target.connection }, '/action', 'POST', action)
  changed()
}

/** The Folders / Whole Crew confirm box, worded by the server (crew_team_folder_switch.go); refusal when too big. */
export type TeamFolderChange = { whole: boolean; title: string; sub: string; people: string[]; folders_added?: string[]; folders_kept?: string[]; means: string[]; note?: string; confirm_label?: string; refusal?: string }
export function teamFolderChange(target: TeamTarget, whole: boolean): Promise<TeamFolderChange> {
  return teamRequest<TeamFolderChange>({ project: target.project, connection: target.connection }, `/folder-change?whole=${whole ? 1 : 0}`)
}

/** The Team members' own agents in the owner's Mac app (owner, 5 Nov): the online copy's side-chat rows the person
 * does not write, as real tabs of this Crew (agents only: a read-only schedule chat of the viewer's own has a :schedule: key and stays out). Their chats are read from the server through this Mac's sign-in
 * (utils/remoteSessions.ts); the key carries the server's after ":team:". Empty outside the Mac app's own Crew. */
export async function teamSideChats(profileId: string, projectId: string): Promise<AgentProfileSideChat[]> {
  const binding = await macBinding(projectId)
  if (!binding || binding.side !== 'local') return []
  const response = await fetch(`${getDesktopApiBaseUrl()}/api/connections/${encodeURIComponent(binding.connection)}/api/agent-profiles/${encodeURIComponent(profileId)}/side-chats?project_id=${encodeURIComponent(binding.peer)}`, { headers: { Authorization: `Bearer ${getAuthToken() || ''}` } })
  if (!response.ok) return []
  const rows = await response.json() as AgentProfileSideChat[]
  return (rows || []).filter(row => row.can_write === false && !row.on_your_mac && row.tab_owner_id && row.session_id && !row.is_main && row.conversation_key.includes(':chat:')).map(row => {
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
  // The connections list is rebuilt (new objects) on every refresh of the connected servers; only its ids matter here.
  const connectionKey = connections.map(connection => connection.id).join(',')
  // A different person or workspace starts from nothing; a refresh of the same ones never empties the list.
  useEffect(() => { setInvites([]) }, [user, workspace])
  useEffect(() => {
    let active = true
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
      // The same invites keep the same list, so the card is not re-rendered by every poll.
      if (active) setInvites(current => JSON.stringify(current) === JSON.stringify(found) ? current : found)
    }
    void load()
    const timer = window.setInterval(() => { if (!document.hidden) void load() }, 15000)
    window.addEventListener('agentworks:team-updated', load)
    return () => { active = false; window.clearInterval(timer); window.removeEventListener('agentworks:team-updated', load) }
  }, [user, workspace, connectionKey]) // eslint-disable-line react-hooks/exhaustive-deps -- connections is read through its ids
  return invites
}

/** The first-time Team wizard (crew_team_wizard.go): the server's options for each step, and one save at Finish. */
export type TeamWizardView = {
  new_team: boolean; default_level: CrewPeopleLevelKey; levels: CrewPeopleLevel[]
  people: { user_id: string; name: string; email?: string }[]
  folders: { default: string; folders: string[]; offline?: string }
  agents: { key: string; name: string; main?: boolean }[]
}
export type TeamWizardChoice = { people: { user_id: string; level: CrewPeopleLevelKey }[]; whole: boolean; folder: string; agents: string[]; create_dashboard?: boolean }
export function teamWizard(target: TeamTarget): Promise<TeamWizardView> {
  return teamRequest<TeamWizardView>({ project: target.project, connection: target.connection }, '/setup')
}
export async function finishTeamWizard(target: TeamTarget, choice: TeamWizardChoice): Promise<void> {
  try {
    await teamRequest<unknown>({ project: target.project, connection: target.connection }, '/setup', 'POST', choice)
    changed()
  }
  catch (cause) {
    // A partly saved Team reloads the tab (the wizard is gone), so the server's line goes to the tab to keep showing.
    const failure = teamFailure(cause)
    if (failure.code === 'setup_partial') {
      window.dispatchEvent(new CustomEvent('agentworks:team-notice', { detail: failure.message }))
      changed()
    }
    throw cause
  }
}
