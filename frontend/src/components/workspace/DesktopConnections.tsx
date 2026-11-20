import { useTerminalConversationSelection } from '../../products/work/terminalConversationSelection'
import { revealSidebarTarget, sidebarProjectShortcuts } from '../../utils/sidebarProjectOrder'
import { OwnTerminalAgents } from '../../products/work/OwnTerminalAgents'
import type { TeamLiveView } from './TeamLiveAgents'
import { agentSetupForTab, teamAgentForTab, teamSideChats, useTeamTab } from '../../products/work/teamApi'
import { queueTeammateAgent } from '../../products/work/openTeammateAgent'
import { AgentPlaceMark, AgentSetupMark } from '../chat/AgentIcon'
import { ProjectAgentMark } from './SidebarCrewAgentMark'
import { matchesWorkConversation, workConversationAgent } from '../../utils/workConversationKey'
import { openAgentTab } from '../../utils/openAgentTab'
import { agentApi, getAuthToken, getDesktopApiBaseUrl, getLocalApiBaseUrl } from '../../services/api'
import type { AgentProfileSideChat } from '../../services/api-types'
import { openAppPage, openAppProduct } from '../../utils/appNavigation'
import { productRoutePath } from '../../utils/productRoutes'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { openConnectedServers } from './ConnectedServersMenuItem'
import { useProjectMenuHistoryStore, connectedWorkHistoryScope } from '../../stores/useProjectMenuHistoryStore'
import { connectedProjectKey } from '../../utils/connectedWorkPresentation'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { Fragment, useEffect, useRef, useState, type ReactNode } from 'react'
import { connectedLocations, hasDesktopConnections, useConnectedWorkPolling, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import type { DesktopConnection } from '../../services/desktopConnections'
import { SidebarProjectRow, openSidebarProject } from './SidebarServerGroup'
import { macCrewStateText, type CrewLocationFields } from './macCrews'
import { PinnedCrewOrderMenu } from './PinnedCrewOrderMenu'
import { TeamInvites } from './TeamInviteRow'
import { useCrewAgentsStore } from '../chat/AgentCard'
import { PersonInitial, SidebarAgentLayout, SidebarCrewAgents, SidebarHiddenAgents } from './SidebarCrewAgents'
import { sidebarAgentDisclosureClass, sidebarAgentListClass, sidebarAgentRowClass } from './sidebarAgentLayout'
import { ChevronRight, Folder, Laptop, Pin, Search } from 'lucide-react'
import { AgentInTeamMark, mostInTeam } from '../chat/AgentInTeamMark'
import { useSidebarExpansionStore, sidebarExpansionKey } from '../../stores/useSidebarExpansionStore'
import { runtimeNeedsUserInput } from '../../utils/runtimeActivity'
import { hasActiveSessionWork } from '../../utils/activitySessions'
import { useChatStore } from '../../stores/useChatStore'
import { useShallow } from 'zustand/react/shallow'
import { ProjectFilterMenu, ProjectGroupsMenu, isSharedProject, matchesMenuFilter, useMenuGroups } from './MenuGroups'


type SidebarProject = { team_live?: TeamLiveView; id: string; name: string; shared_line?: string; kind: 'crew' | 'goal'; icon?: string; selected?: boolean; mode?: 'builder' | 'run'; owned_by_caller?: boolean; owner_name?: string; shared_agents?: number; personal_agent?: boolean } & CrewLocationFields

/** Crews you build or work on. Dashboards shared with you are not listed here: they have their own page (owner,
 * 10 Nov). The personal AgentWorks agent is on the Home page only. */
const menuListed = (project: SidebarProject) => !project.personal_agent && !(project.owned_by_caller === false && project.mode === 'run')
function useWorkHistory() {
  const historyScope = connectedWorkHistoryScope()
  const pinned = useProjectMenuHistoryStore(state => state.pinned[historyScope])
  const recent = useProjectMenuHistoryStore(state => state.recent[historyScope])
  const setPinned = useProjectMenuHistoryStore(state => state.setPinned)
  const movePinned = useProjectMenuHistoryStore(state => state.movePinned)
  return { pinned, recent, movePinned: (key: string, target: string) => movePinned(historyScope, key, target), canPin: Boolean(historyScope), setPinned: (key: string, value: boolean) => setPinned(historyScope, key, value) }
}
type ProjectMenuProps = {
  children?: ReactNode
  top?: ReactNode
  projects?: SidebarProject[]
  directory?: boolean
  showBrowse?: boolean
  loading?: boolean
}

export function DesktopConnections({ projects = [], ...props }: ProjectMenuProps) {
  return <>
    {!props.directory && <TeamInvites />}
    <ProjectMenu key={connectedWorkHistoryScope()} {...props} projects={projects} />
  </>
}

/** Reuse the same admitted inventory and pin store in both views. */
function ProjectMenu({ children, top, projects, directory, showBrowse, loading }: ProjectMenuProps & { projects: SidebarProject[] }) {
  const creationProjectId = useCrewAgentsStore(state => state.projectId)
  const canAddAgent = useCrewAgentsStore(state => state.canAdd)
  const canImportChat = useCrewAgentsStore(state => state.canImportChat)
  const canDiscoverSkills = useCrewAgentsStore(state => state.canDiscoverSkills)
  const desktop = hasDesktopConnections()
  const active = getRawActiveWorkspaceId()
  const state = useConnectedWorkStore()
  useConnectedWorkPolling(desktop)
  // A teammate starting or stopping work on another network reaches this Mac as a note, then a sessions notice: read
  // the networks' lists again then, not only on the minute. The store reuses a read from the last 30 s: with many
  // local agents, sessions notices arrive constantly and each full read costs the server seconds.
  useLiveRefetch(() => { void useConnectedWorkStore.getState().refresh() }, { kinds: ['sessions'], fallbackMs: 0, safetyMs: 0, minIntervalMs: 5_000, enabled: desktop && !directory })
  const current = desktop && isChatIdentityCurrent(state.identity)
  const connections: DesktopConnection[] = desktop ? connectedLocations(state) : [{ id: active, name: '', server: '' }]
  const inventory = current ? state.inventory : {}
  const directoryError = desktop && current ? state.error : ''
  const { pinned, recent, canPin, setPinned, movePinned } = useWorkHistory()
  const [query, setQuery] = useState('')
  const { groups, filter, network, pick } = useMenuGroups(Boolean(directory))
  const [groupScope, setGroupScope] = useState<'all' | 'mine' | 'shared'>('all')
  // The filter is a personal presentation preference, never a connection or access change.
  const locationId = network === 'all' || connections.some(connection => connection.id === network)
    ? network
    : (connections.find(connection => connection.id === active) || connections[0])?.id
  const open = (project: typeof rows[number]) => {
    if (project.connection.id === active) {
      openSidebarProject(project.kind, project.id)
      return
    }
    void window.electronAPI?.openDesktopConnection?.(project.connection.id, productRoutePath({ surface: 'work', id: project.id }))
      .catch(() => useConnectedWorkStore.setState({ error: 'Could not open this server. Try again.' }))
  }
  // The website has no delete for a Mac copy (owner, 12 Nov): its row offers the existing project delete instead.
  const removeOnlineCopy = (project: typeof rows[number]) => {
    void agentApi.deleteAgentProfileProject('work', project.id)
      .then(() => {
        useChatStore.getState().addToast(`Removed the online copy of “${project.name}”. Your Mac Crew is unchanged.`, 'success')
        void state.refresh()
      })
      .catch(() => {
        useChatStore.getState().addToast('Could not remove the online copy. Try again.', 'error')
      })
  }
  const rows = connections.flatMap(connection => (connection.id === active ? projects : (inventory[connection.id]?.projects || []) as SidebarProject[])
    .filter(project => project.kind === 'crew' && menuListed(project))
    .map(project => ({ ...project, connection })))
    .sort((a, b) => a.name.localeCompare(b.name) || a.connection.name.localeCompare(b.connection.name) || a.connection.id.localeCompare(b.connection.id) || a.id.localeCompare(b.id))
  const keyFor = (project: typeof rows[number]) => connectedProjectKey(project.connection.id, project.kind, project.id)
  const { pinnedRows, recentRows } = sidebarProjectShortcuts(rows, keyFor, pinned ?? [], recent ?? [])
  const pinnedKeys = new Set(pinned ?? [])
  const renderProject = (project: typeof rows[number], pinnedShortcut: boolean) => {
    const { location: where, mac, state: crewState, connection, ...rest } = project
    const key = keyFor(project)
    const here = connection.id === active
    const pinnedIndex = pinnedRows.findIndex(item => keyFor(item) === key)
    const move = (offset: number) => {
      const target = pinnedRows[pinnedIndex + offset]
      if (target) movePinned(key, keyFor(target))
    }
    const crew = project.kind === 'crew' && !project.personal_agent
    const currentCrew = here && crew && project.selected && creationProjectId === project.id
    const importFromCrew = (kind: 'chat' | 'skills') => () => window.dispatchEvent(new CustomEvent('crew-agent-import', { detail: { projectId: project.id, kind } }))
    const row = (agentsExpanded?: boolean, onToggleAgents?: () => void, activityLabel?: string, activityTitle?: string, expand?: () => void) => <SidebarProjectRow {...rest} agentsExpanded={agentsExpanded} onToggleAgents={onToggleAgents} activityLabel={agentsExpanded === false || activityTitle ? activityLabel : undefined} activityTitle={activityTitle}
      showIcon={project.kind !== 'crew' || project.personal_agent || project.mode === 'run'}
      onMoveUp={pinnedShortcut && canPin && pinnedIndex > 0 ? () => move(-1) : undefined} onMoveDown={pinnedShortcut && canPin && pinnedIndex >= 0 && pinnedIndex < pinnedRows.length - 1 ? () => move(1) : undefined}
      selected={here && project.selected}
      location={desktop ? connection.id === 'local' ? 'On your Mac' : connection.name : undefined} remote={desktop && connection.id !== 'local'} showLocation={desktop}
      pinned={pinnedKeys.has(key)} onTogglePin={canPin && crew ? () => setPinned(key, !pinnedKeys.has(key)) : undefined}
      onNewAgent={here && crew && creationProjectId === project.id && canAddAgent ? () => window.dispatchEvent(new CustomEvent('crew-new-agent', { detail: { projectId: project.id } })) : undefined}
      onImportLocalChat={currentCrew && canImportChat ? importFromCrew('chat') : undefined}
      onDiscoverSkills={currentCrew && canDiscoverSkills ? importFromCrew('skills') : undefined}
      onRemoveOnlineCopy={!desktop && crew && where === 'mac' && project.owned_by_caller !== false ? () => removeOnlineCopy(project) : undefined}
      groupsMenu={crew ? <ProjectGroupsMenu name={project.name} projectKey={key} inline /> : undefined}
      stateLine={where === 'mac' ? macCrewStateText(crewState) : undefined} offline={where === 'mac' && mac?.online === false} onMac={where === 'mac' ? (mac?.online ? 'on' : 'off') : undefined}
      onOpen={() => {
        // Opening a project shows its agents here too, wherever it lives (owner, 10 Nov).
        expand?.()
        open(project)
      }} />
    return crew
      ? <ExpandableCrew key={key} projectId={project.id} connectionId={connection.id} selected={here && !!project.selected} initialOpen={pinnedShortcut || (here && !!project.selected)}>{row}</ExpandableCrew>
      : <Fragment key={key}>{row()}</Fragment>
  }
  if (!directory) {
    return <div className="flex min-h-0 flex-1 flex-col" aria-label="Connected work">
      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto px-2 pb-3" data-sidebar-project-scroll>
        {top}
        {pinnedRows.length > 0 ? <section aria-label="Pinned projects" className="mb-3 shrink-0 space-y-1">
          {pinnedRows.map(project => renderProject(project, true))}
        </section> : recentRows.length > 0 ? <section aria-labelledby="recent-projects-heading" className="mb-3 shrink-0 space-y-1">
          <h2 id="recent-projects-heading" className="px-2 pb-1 pt-2 text-xs font-medium text-muted-foreground">Recent projects</h2>
          {recentRows.map(project => renderProject(project, false))}
        </section> : showBrowse && !loading && <button type="button" onClick={() => { openAppPage('/projects'); window.dispatchEvent(new Event('close-app-sidebar')) }} className="flex items-center gap-2 rounded-lg px-2 py-2 text-left text-xs text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
          <Folder className="h-4 w-4" aria-hidden="true" />Browse projects
        </button>}
      </div>
    </div>
  }

  const locations = connections.filter(connection => locationId === 'all' || connection.id === locationId)
  const includesActive = locations.some(connection => connection.id === active)
  const normalizedQuery = query.trim().toLocaleLowerCase()
  const visible = rows.filter(row => (locationId === 'all' || row.connection.id === locationId) && row.name.toLocaleLowerCase().includes(normalizedQuery))
  const groupFilter = filter === 'mine' || filter === 'shared' ? 'all' : filter
  const scope = filter === 'mine' || filter === 'shared' ? filter : groupFilter === 'all' ? 'all' : groupScope
  const shown = visible.filter(row => matchesMenuFilter(scope, groups, keyFor(row), row) && matchesMenuFilter(groupFilter, groups, keyFor(row), row))
  const sections = locations.map(connection => ({
    connection,
    name: desktop ? (connection.id === 'local' ? 'Mac' : connection.name) : 'On this server',
    projects: shown.filter(row => row.connection.id === connection.id),
  }))
  return <div aria-label="Project directory" className="space-y-3">
    <div className="flex items-center gap-2">
      <label className="flex min-w-0 flex-1 items-center gap-2 rounded-lg border border-border bg-card px-3 focus-within:ring-2 focus-within:ring-ring">
        <Search className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        <input type="search" aria-label="Search projects" value={query} onChange={event => setQuery(event.target.value)} placeholder="Search projects…" className="h-9 min-w-0 flex-1 bg-transparent text-sm outline-none" />
      </label>
      <ProjectFilterMenu locations={connections.map(c => ({ id: c.id, name: c.id === 'local' ? 'Mac' : c.name }))}
        locationId={locationId} onLocation={id => pick(filter, id)}
        onGroup={next => { setGroupScope(scope); pick(next === 'all' ? scope : next) }}
        onClear={() => pick(scope, 'all')} />
    </div>
    <div role="group" aria-label="Project ownership" className="flex gap-1 border-b border-border">
      {([['all', 'All'], ['mine', 'Mine'], ['shared', 'Shared with me']] as const).map(([id, label]) =>
        <button key={id} type="button" aria-pressed={scope === id} onClick={() => { if (groupFilter === 'all') pick(id); else setGroupScope(id) }}
          className={`min-h-9 border-b-2 px-2 text-xs hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${scope === id ? 'border-primary font-semibold text-foreground' : 'border-transparent text-muted-foreground'}`}>{label}</button>)}
    </div>
    {sections.filter(section => section.projects.length > 0).map(section => <section key={section.connection.id} aria-label={section.name}>
      <h2 className="flex items-center gap-1.5 px-1 pb-1.5 text-xs font-medium text-muted-foreground">{section.name} <span>· {section.projects.length}</span></h2>
      <ul className="rounded-lg border border-border bg-card text-card-foreground">
        {section.projects.map(project => {
          const key = keyFor(project)
          const pinned = pinnedKeys.has(key)
          const shared = isSharedProject(project)
          const macStatus = project.where === 'mac'
            ? [project.mac?.name || 'On a Mac', project.mac?.online === false ? 'Mac off' : macCrewStateText(project.state)].filter(Boolean).join(' · ')
            : ''
          return <li key={key} data-theme-row="project" className="group/crew flex min-h-9 items-center gap-1 rounded-md px-1 hover:bg-muted max-md:min-h-11">
            <button type="button" title={project.name} onClick={() => open(project)} className="flex min-w-0 flex-1 items-center gap-2 rounded-md px-2 py-1.5 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
              <span className="min-w-0 flex-1 truncate text-sm font-medium">{project.name}</span>
              {macStatus && <span role="img" aria-label={macStatus} title={macStatus} className="shrink-0 text-muted-foreground"><Laptop aria-hidden="true" className="h-3.5 w-3.5" /></span>}
              {shared && <span title={project.shared_line} className="shrink-0 text-xs text-muted-foreground">Shared</span>}
            </button>
            {canPin && <button type="button" onClick={() => setPinned(key, !pinned)} title={pinned ? 'Unpin from sidebar' : 'Pin to sidebar'} aria-pressed={pinned} aria-label={`${pinned ? 'Unpin' : 'Pin'} ${project.name}`}
              className={`grid h-8 w-8 shrink-0 place-items-center rounded-md hover:bg-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:h-10 max-md:w-10 ${pinned ? 'text-primary' : 'text-muted-foreground [@media(hover:hover)]:opacity-0 group-hover/crew:opacity-100 group-focus-within/crew:opacity-100'}`}>
              <Pin aria-hidden="true" className={`h-4 w-4 ${pinned ? 'fill-current' : ''}`} />
            </button>}
            <PinnedCrewOrderMenu name={project.name} groupsMenu={<ProjectGroupsMenu name={project.name} projectKey={key} inline />}
              onRemoveOnlineCopy={!desktop && project.where === 'mac' && project.owned_by_caller !== false ? () => removeOnlineCopy(project) : undefined} />
          </li>
        })}
      </ul>
    </section>)}
    {includesActive && children}
    {locations.filter(connection => connection.id !== active).map(connection => {
      const status = inventory[connection.id]?.status
      if (!status) return <p key={connection.id} role="status" className="text-sm text-muted-foreground">Loading {connection.name}…</p>
      if (status === 'unavailable') return <button key={connection.id} type="button" className="text-sm text-muted-foreground underline" onClick={() => void state.refresh(true)}>{connection.name} unavailable · Retry</button>
      if (status === 'sign_in') return <button key={connection.id} type="button" className="text-sm text-muted-foreground underline" onClick={() => openConnectedServers(connection.server)}>{connection.name} · Sign in again</button>
      return null
    })}
    {!shown.length && (!includesActive || !children) && locations.every(connection => connection.id === active || inventory[connection.id]?.status === 'connected') &&
      <div className="space-y-2 rounded-lg border border-border bg-card p-4 text-sm text-muted-foreground">
        <p>{normalizedQuery || scope !== 'all' || groupFilter !== 'all' ? 'No matching projects.' : 'No projects here yet.'}</p>
        {(normalizedQuery || scope !== 'all' || groupFilter !== 'all' || (connections.length > 1 && locationId !== 'all')) &&
          <button type="button" className="underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" onClick={() => { setQuery(''); pick('all', 'all') }}>Clear search and filters</button>}
      </div>}
    {directoryError && <p role="alert" className="text-sm text-destructive">{directoryError}</p>}
  </div>
}

/** Each expanded Crew loads its own admitted chats, including on other connections. */
function ExpandableCrew({ projectId, connectionId, selected, initialOpen, children }: { projectId: string; connectionId: string; selected: boolean; initialOpen: boolean; children: (expanded: boolean, onToggle: () => void, activityLabel?: string, activityTitle?: string, expand?: () => void) => ReactNode }) {
  const key = sidebarExpansionKey(connectedWorkHistoryScope(), 'crew', connectionId, projectId)
  const saved = useSidebarExpansionStore(state => state.expanded[key])
  const toggle = useSidebarExpansionStore(state => state.toggle)
  const expanded = saved ?? initialOpen
  // Opening a project shows its agents; they stay open when another project is opened, and the caret closes them
  // (owner, 10 Nov), so several projects' agents can be open at once.
  const selectedRef=useRef<HTMLDivElement>(null)
  const activeNative=useTerminalConversationSelection(state=>state.selection?.agent.id)
  const activeAgent=useChatStore(state=>state.activeTabId)
  useEffect(()=>{if(!selected)return;const box=selectedRef.current,viewport=box?.closest<HTMLElement>('[data-sidebar-project-scroll]');if(box&&viewport)revealSidebarTarget(box,viewport)},[selected,projectId,activeAgent,activeNative,saved])
  useEffect(() => { if (selected && saved !== true) toggle(key, false) }, [selected,activeAgent,activeNative]) // eslint-disable-line react-hooks/exhaustive-deps
  const [loadedChats, setLoadedChats] = useState<{ identity: number; chats: AgentProfileSideChat[] }>({ identity: captureChatIdentity(), chats: [] })
  const chats = isChatIdentityCurrent(loadedChats.identity) ? loadedChats.chats : []
  const team = useTeamTab(expanded ? projectId : undefined, connectionId)
  const liveProjectId = useCrewAgentsStore(state => state.projectId)
  const liveAgentCount = useCrewAgentsStore(state => state.agents.filter(agent => !agent.ownerId).length)
  const [error, setError] = useState(false)
  const activity = useConnectedWorkStore(state => isChatIdentityCurrent(state.identity) ? state.activity[connectionId]?.sessions : undefined)
  // The active-session cache is already refreshed for the current connection, including outside the desktop app.
  // Keep observing it while collapsed; no need to open/load the project's chat list to know it is working.
  const localSessions = useChatStore(state => state.activeSessionsCache)
  const sessions = (connectionId === getRawActiveWorkspaceId() ? localSessions : activity || [])
    .filter(session => session.crew_project_id === projectId)
  const running = sessions.some(hasActiveSessionWork)
  const ownActivityLabel = running ? 'Running' : sessions.some(runtimeNeedsUserInput) ? 'Needs your input' : undefined
  // Only the person's own work: teammates' agents are in the Monitor's Team view (PLAT-746-926).
  const activityLabel = ownActivityLabel
  // This network's chat changes arrive as live notices; another network's list is checked every minute.
  const here = connectionId === getRawActiveWorkspaceId()
  const [reload, setReload] = useState(0)
  useLiveRefetch(() => setReload(value => value + 1), { kinds: ['sessions', 'crew_panel'], project: projectId, fallbackMs: 60_000, minIntervalMs: 5_000, enabled: expanded && here })
  useEffect(() => {
    if (!expanded) return
    let cancelled = false
    const identity = captureChatIdentity()
    const load = async () => {
      try {
        const current = connectionId === getRawActiveWorkspaceId()
        let result: AgentProfileSideChat[]
        if (current) result = await agentApi.listAgentProfileSideChats('work', projectId, true)
        else {
          const base = connectionId === 'local' ? getLocalApiBaseUrl() : `${getDesktopApiBaseUrl()}/api/connections/${connectionId}`
          const response = await fetch(`${base}/api/agent-profiles/work/side-chats?project_id=${encodeURIComponent(projectId)}&with_main=1`, { headers: { Authorization: `Bearer ${getAuthToken() || ''}` }, signal: AbortSignal.timeout(15000) })
          if (!response.ok) throw new Error('Chats unavailable')
          result = await response.json()
        }
        // A failed Team read leaves the person's own agents listed (owner, 11 Nov: a Team failure hid them all).
        const teammates = await teamSideChats('work', projectId, connectionId).catch(() => [] as AgentProfileSideChat[])
        const rows = [...(Array.isArray(result) ? result : []), ...teammates]
        const unique = rows.filter((row, index) => rows.findIndex(other => other.conversation_key === row.conversation_key) === index)
        if (!cancelled && isChatIdentityCurrent(identity)) { setLoadedChats({ identity, chats: unique }); setError(false) }
      } catch { if (!cancelled && isChatIdentityCurrent(identity)) setError(true) }
    }
    void load()
    const timer = here ? undefined : window.setInterval(() => { if (!document.hidden) void load() }, 60_000)
    return () => { cancelled = true; window.clearInterval(timer) }
  }, [expanded, projectId, connectionId, here, reload])
  const open = (chat: AgentProfileSideChat) => {
    // A teammate's agent: open the project and let its strip open the agent once it has read its list.
    if (chat.can_write === false && connectionId === getRawActiveWorkspaceId()) {
      queueTeammateAgent({ projectId, key: chat.conversation_key, owner: chat.tab_owner_id })
      openAppProduct({ surface: 'work', id: projectId })
      window.dispatchEvent(new Event('close-app-sidebar'))
      return
    }
    const agent = chat.is_main || chat.can_write === false ? undefined : workConversationAgent(chat.conversation_key)
    const route = { surface: 'work' as const, id: projectId, agent }
    if (connectionId === getRawActiveWorkspaceId()) {
      const existing = Object.values(useChatStore.getState().chatTabs).find(tab => matchesWorkConversation(tab, 'work', projectId, chat.conversation_key))
      if (existing) openAgentTab(existing.tabId)
      else openAppProduct(route)
    }
    else void window.electronAPI?.openDesktopConnection?.(connectionId, productRoutePath(route))
    window.dispatchEvent(new Event('close-app-sidebar'))
  }
  const inTeam = (chat: AgentProfileSideChat) => { const row = teamAgentForTab(team, chat.conversation_key, !!chat.is_main, true); return row?.mine === true && !!row.in_team }
  const own = chats.filter(chat => chat.can_write !== false)
  // The same setup warning the open project's rows show (the Team tab's reading), without its reach line.
  const setupOf = (chat: AgentProfileSideChat) => { const setup = agentSetupForTab(team, chat.conversation_key, !!chat.is_main, chat.can_write !== false); return setup && { ...setup, reach: '' as const } }
  const teammateChats = chats.filter(chat => chat.can_write === false)
  const mostIn = mostInTeam(own.map(inTeam))
  const anyPrivate = own.some(chat => !inTeam(chat))
  const chatRow = (chat: AgentProfileSideChat) => {
    const session = sessions.find(item => item.session_id === chat.session_id)
    const teamAgent = teamAgentForTab(team, chat.conversation_key, !!chat.is_main, chat.can_write !== false)
    return <button key={chat.conversation_key} data-theme-row="agent" type="button" role="listitem" onClick={() => open(chat)} className={`${sidebarAgentRowClass} group gap-2 text-muted-foreground hover:bg-muted hover:text-foreground focus:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:pr-11`}>{chat.can_write === false && <PersonInitial name={chat.tab_owner_name || 'Teammate'} />}<span className="min-w-0 flex-1 truncate">{chat.name || (chat.is_main ? 'Main' : 'Agent')}</span>{chat.can_write !== false && <AgentInTeamMark on={inTeam(chat)} mostIn={mostIn}/>}<AgentSetupMark setup={setupOf(chat)} /><AgentPlaceMark place={chat.chat_location === 'mac' ? 'mac' : undefined} /><ProjectAgentMark projectId={projectId} sessionId={chat.session_id} session={session} local={connectionId === getRawActiveWorkspaceId()} activity={teamAgent?.activity} status={teamAgent?.status_label} /></button>
  }
  // Use the matching tab's latest server-confirmed preference when switching from the live list back to this one.
  const openPins = useChatStore(useShallow(state => {
    const result: Record<string, boolean | undefined> = {}
    if (!here) return result
    const tabs = Object.values(state.chatTabs)
    for (const chat of own) {
      const tab = tabs.find(tab => matchesWorkConversation(tab, 'work', projectId, chat.conversation_key))
      if (tab) result[chat.conversation_key] = tab.metadata?.agentProfileTabPinned
    }
    return result
  }))
  const openKeys = new Set(Object.keys(openPins))
  const orderedOwn = [...own.filter(chat => chat.is_main), ...own.filter(chat => !chat.is_main)]
  const isHidden = (chat: AgentProfileSideChat) => !chat.is_main && (openPins[chat.conversation_key] ?? chat.pinned) === false
  const visible = orderedOwn.filter(chat => !isHidden(chat))
  const hidden = orderedOwn.filter(isHidden)
  // Another network's project has no tabs here: its visible agents stay listed as before.
  const shown = here ? visible.filter(chat => openKeys.has(chat.conversation_key)) : visible
  const closedAgents = here ? visible.filter(chat => !openKeys.has(chat.conversation_key)) : []
  const [showClosed, setShowClosed] = useState(false)
  const hiddenExpansion = useState(false)
  const [showHidden, setShowHidden] = hiddenExpansion
  // The open project switches to the live list once that list has its agents; until then the loaded rows stay, so
  // switching projects does not blank and refill the menu (owner, 18 Nov).
  const live = selected && liveProjectId === projectId && (liveAgentCount > 0 || chats.length === 0)
  return <div ref={selectedRef}>{children(expanded, () => toggle(key, initialOpen), activityLabel, undefined, () => { if (!expanded) toggle(key, initialOpen) })}{expanded && (live ? <SidebarCrewAgents projectId={projectId} hiddenExpansion={hiddenExpansion} /> : <div role="list" aria-label="Agents" className={sidebarAgentListClass}>{error ? <span className="px-2 text-xs text-muted-foreground">Could not load agents</span> : <SidebarAgentLayout projectId={here ? projectId : undefined} rows={[...shown, ...teammateChats]} known={new Set(chats.map(chat => chat.conversation_key))} keyOf={chat => chat.conversation_key} forkedFromOf={chat => chat.forked_from} teammate={chat => chat.can_write === false} render={chatRow} />}
    {closedAgents.length > 0 && <button type="button" aria-expanded={showClosed} onClick={() => setShowClosed(value => !value)} className={sidebarAgentDisclosureClass}>
      <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${showClosed ? 'rotate-90' : ''}`} />Closed agents ({closedAgents.length})
    </button>}
    {showClosed && closedAgents.map(chatRow)}
    <SidebarHiddenAgents count={hidden.length} expanded={showHidden} onToggle={() => setShowHidden(!showHidden)}>
      {showHidden && hidden.map(chatRow)}
    </SidebarHiddenAgents>
    <OwnTerminalAgents projectId={projectId} connection={connectionId} nested mostIn={mostIn} />
    {mostIn && <p className="px-2 pt-1 text-[11px] text-muted-foreground/80">{anyPrivate ? 'In team unless marked with a lock' : 'All in team'}</p>}
    </div>)}</div>
}

