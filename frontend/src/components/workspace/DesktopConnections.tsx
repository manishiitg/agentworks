import { useTerminalConversationSelection } from '../../products/work/terminalConversationSelection'
import { revealSidebarTarget, sidebarProjectShortcuts } from '../../utils/sidebarProjectOrder'
import { OwnTerminalAgents } from '../../products/work/OwnTerminalAgents'
import type { TeamLiveView } from './TeamLiveAgents'
import { agentSetupForTab, teamAgentForTab, teamSideChats, useTeamTab } from '../../products/work/teamApi'
import { queueTeammateAgent } from '../../products/work/openTeammateAgent'
import { ProjectAgentRow } from './SidebarCrewAgentMark'
import { matchesWorkConversation, workConversationAgent } from '../../utils/workConversationKey'
import { openAgentTab } from '../../utils/openAgentTab'
import { agentApi } from '../../services/api'
import type { AgentProfileSideChat } from '../../services/api-types'
import { openAppPage, openAppProduct } from '../../utils/appNavigation'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
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
import { SidebarAgentLayout, SidebarCrewAgents, SidebarHiddenAgents } from './SidebarCrewAgents'
import { sidebarAgentDisclosureClass, sidebarAgentListClass } from './sidebarAgentLayout'
import { ChevronRight, Folder, Pin, Search } from 'lucide-react'
import { mostInTeam } from '../chat/AgentInTeamMark'
import { useSidebarExpansionStore, sidebarExpansionKey } from '../../stores/useSidebarExpansionStore'
import { runtimeNeedsUserInput } from '../../utils/runtimeActivity'
import { hasActiveSessionWork } from '../../utils/activitySessions'
import { useChatStore } from '../../stores/useChatStore'
import { useShallow } from 'zustand/react/shallow'
import { isSharedProject, useMenuFilter } from './ProjectFilter'


type SidebarProject = { team_live?: TeamLiveView; id: string; name: string; shared_line?: string; kind: 'crew' | 'goal'; icon?: string; selected?: boolean; mode?: 'builder' | 'run'; owned_by_caller?: boolean; owner_name?: string; shared_agents?: number } & CrewLocationFields

/** Crews you build or work on. Dashboards shared with you are not listed here: they have their own page (owner,
 * 10 Nov). */
const menuListed = (project: SidebarProject) => !(project.owned_by_caller === false && project.mode === 'run')
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
  const directoryError = desktop && current ? state.error : ''
  const { pinned, recent, canPin, setPinned, movePinned } = useWorkHistory()
  const [query, setQuery] = useState('')
  const { error: filterError } = useMenuFilter(Boolean(directory))
  const open = (project: typeof rows[number]) => openSidebarProject(project.kind, project.id)
  // The website has no delete for a Mac copy (owner, 12 Nov): its row offers the existing project delete instead.
  const removeOnlineCopy = (project: typeof rows[number]) => {
    void agentApi.deleteAgentProfileProject('work', project.id)
      .then(() => {
        useChatStore.getState().addToast(`Removed the online copy of “${project.name}”. Your Mac project is unchanged.`, 'success')
        void state.refresh()
      })
      .catch(() => {
        useChatStore.getState().addToast('Could not remove the online copy. Try again.', 'error')
      })
  }
  // This Mac's projects only (owner, 24 Nov, #3779): a server project reaches the Mac app as an online project in a
  // Team, and otherwise opens on its server's website.
  const here = connections.find(connection => connection.id === active) ?? { id: active, name: '', server: '' }
  const rows = projects
    .filter(project => project.kind === 'crew' && menuListed(project))
    .map(project => ({ ...project, connection: here }))
    .sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id))
  const keyFor = (project: typeof rows[number]) => connectedProjectKey(project.connection.id, project.kind, project.id)
  const isOpenProject = (project: typeof rows[number]) => Boolean(project.selected)
  const { pinnedRows, recentRows, openRow } = sidebarProjectShortcuts(rows, keyFor, pinned ?? [], recent ?? [], isOpenProject)
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
    const crew = project.kind === 'crew'
    const currentCrew = here && crew && project.selected && creationProjectId === project.id
    const importFromCrew = (kind: 'chat' | 'skills') => () => window.dispatchEvent(new CustomEvent('crew-agent-import', { detail: { projectId: project.id, kind } }))
    const row = (agentsExpanded?: boolean, onToggleAgents?: () => void, activityLabel?: string, activityTitle?: string, expand?: () => void) => <SidebarProjectRow {...rest} agentsExpanded={agentsExpanded} onToggleAgents={onToggleAgents} activityLabel={agentsExpanded === false || activityTitle ? activityLabel : undefined} activityTitle={activityTitle}
      showIcon={project.kind !== 'crew' || project.mode === 'run'}
      onMoveUp={pinnedShortcut && canPin && pinnedIndex > 0 ? () => move(-1) : undefined} onMoveDown={pinnedShortcut && canPin && pinnedIndex >= 0 && pinnedIndex < pinnedRows.length - 1 ? () => move(1) : undefined}
      selected={isOpenProject(project)}
      location={desktop ? connection.id === 'local' ? 'On your Mac' : connection.name : undefined} remote={desktop && connection.id !== 'local'} showLocation={desktop}
      pinned={pinnedKeys.has(key)} onTogglePin={canPin && crew ? () => setPinned(key, !pinnedKeys.has(key)) : undefined}
      onNewAgent={here && crew && creationProjectId === project.id && canAddAgent ? () => window.dispatchEvent(new CustomEvent('crew-new-agent', { detail: { projectId: project.id } })) : undefined}
      onImportLocalChat={currentCrew && canImportChat ? importFromCrew('chat') : undefined}
      onDiscoverSkills={currentCrew && canDiscoverSkills ? importFromCrew('skills') : undefined}
      onRemoveOnlineCopy={!desktop && crew && where === 'mac' && project.owned_by_caller !== false ? () => removeOnlineCopy(project) : undefined}
      stateLine={where === 'mac' ? macCrewStateText(crewState) : undefined} offline={where === 'mac' && mac?.online === false} onMac={where === 'mac' ? (mac?.online ? 'on' : 'off') : undefined}
      onOpen={() => {
        // Opening a project shows its agents here too, wherever it lives (owner, 10 Nov).
        expand?.()
        open(project)
      }} />
    return crew
      ? <ExpandableCrew key={key} projectId={project.id} connectionId={connection.id} selected={isOpenProject(project)} initialOpen={pinnedShortcut || isOpenProject(project)}>{row}</ExpandableCrew>
      : <Fragment key={key}>{row()}</Fragment>
  }
  if (!directory) {
    return <div className="flex min-h-0 flex-1 flex-col" aria-label="Connected work">
      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto px-2 pb-3" data-sidebar-project-scroll>
        {top}
        {pinnedRows.length > 0 && <section aria-label="Pinned projects" className="mb-3 shrink-0 space-y-1">
          {pinnedRows.map(project => renderProject(project, true))}
        </section>}
        {recentRows.length > 0 && <section aria-labelledby="recent-projects-heading" className="mb-3 shrink-0 space-y-1">
          <h2 id="recent-projects-heading" className="px-2 pb-1 pt-2 text-xs font-medium text-muted-foreground">Recent</h2>
          {recentRows.map(project => renderProject(project, false))}
        </section>}
        {!pinnedRows.length && !recentRows.length && showBrowse && !loading && <button type="button" onClick={() => { openAppPage('/projects'); window.dispatchEvent(new Event('close-app-sidebar')) }} className="flex items-center gap-2 rounded-lg px-2 py-2 text-left text-xs text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
          <Folder className="h-4 w-4" aria-hidden="true" />Browse projects
        </button>}
      </div>
    </div>
  }

  // A simple Projects page (owner, 24 Nov: "a simplified ui… something better"): two groups, yours and shared with
  // you, one grey line per row, and no place tabs or ownership chips. Where a project lives shows in its line.
  const normalizedQuery = query.trim().toLocaleLowerCase()
  const listed = rows
  const recentOrder = new Map((recent ?? []).map((key, index) => [key, index]))
  const byRecent = (a: typeof rows[number], b: typeof rows[number]) =>
    (recentOrder.get(keyFor(a)) ?? Infinity) - (recentOrder.get(keyFor(b)) ?? Infinity) || a.name.localeCompare(b.name)
  const shown = listed.filter(row => row.name.toLocaleLowerCase().includes(normalizedQuery)).sort(byRecent)
  const placeName = (connection: DesktopConnection) => connection.id === 'local' ? '' : connection.name
  // Recent first (owner, 24 Nov): the last five opened, then everything else as yours or shared with you.
  // Recent is what you worked on here, on this Mac (owner, 24 Nov), not projects opened on other networks.
  const recentShown = normalizedQuery ? [] : shown.filter(row => recentOrder.has(keyFor(row))).slice(0, 5)
  const rest = shown.filter(row => !recentShown.includes(row))
  const groups = [
    { id: 'recent', name: 'Recent', projects: recentShown },
    { id: 'yours', name: 'Yours', projects: rest.filter(row => !isSharedProject(row)) },
    { id: 'shared', name: 'Shared with you', projects: rest.filter(row => isSharedProject(row)) },
  ]
  // A network is named as in this Mac's connections ("Excellencetechnologies"), not by its host name.
  const networkName = (value: string) => {
    const host = value.replace(/^https?:\/\//, '').replace(/\/.*$/, '')
    if (!host) return ''
    return connections.find(connection => connection.id !== 'local' && connection.server && connection.server.includes(host))?.name || value
  }
  const lineFor = (project: typeof rows[number]) => {
    const shareNetwork = project.shared_line?.split(' · ').slice(1).find(part => /\./.test(part)) || ''
    const network = networkName(project.team_live?.network?.trim() || shareNetwork) || placeName(project.connection)
    if (isSharedProject(project)) {
      // A Team copy on this Mac is owned by this Mac's own account ("default"); its share line names the real owner.
      const fromLine = project.shared_line?.startsWith('Shared by ') ? project.shared_line.slice('Shared by '.length).split(' · ')[0]?.trim() : ''
      const owner = project.owner_name?.trim()
      const by = fromLine || (owner && owner !== 'default' ? owner : '')
      const theirMac = project.shared_line?.includes('On their Mac') ? 'their Mac' : ''
      // A shared project kept on this Mac is a Team copy: you joined its Team.
      const member = Boolean(project.team_live) || project.connection.id === active
      return [by, member ? 'Team member' : '', theirMac, network].filter(Boolean).join(' · ')
    }
    return project.team_live ? ['Team', network].filter(Boolean).join(' · ') : network
  }
  return <div aria-label="Project directory" className="space-y-4">
    <label className="flex min-w-0 items-center gap-2 rounded-lg border border-border bg-card px-3 focus-within:ring-2 focus-within:ring-ring">
      <Search className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
      <input type="search" aria-label="Search projects" value={query} onChange={event => setQuery(event.target.value)} placeholder="Search projects…" className="h-9 min-w-0 flex-1 bg-transparent text-sm outline-none" />
    </label>
    {groups.filter(group => group.projects.length > 0).map(group => <section key={group.id} aria-label={group.name}>
      <h2 className="px-1 pb-1.5 text-xs font-medium text-muted-foreground">{group.name} <span>· {group.projects.length}</span></h2>
      <ul className="rounded-lg border border-border bg-card text-card-foreground">
        {group.projects.map(project => {
          const key = keyFor(project)
          const pinned = pinnedKeys.has(key)
          const detail = lineFor(project)
          const macStatus = project.where === 'mac'
            ? [project.mac?.name || 'On a Mac', project.mac?.online === false ? 'Mac off' : macCrewStateText(project.state)].filter(Boolean).join(' · ')
            : ''
          return <li key={key} data-theme-row="project" className="group/crew flex min-h-9 items-center gap-1 rounded-md px-1 hover:bg-muted max-md:min-h-11">
            <button type="button" title={project.name} onClick={() => open(project)} className="flex min-w-0 flex-1 items-center gap-2 rounded-md px-2 py-1.5 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
              <span className="min-w-0 flex-1 truncate text-sm font-medium">{project.name}</span>
              {detail && <span title={[detail, macStatus].filter(Boolean).join(' · ')} className="min-w-0 max-w-[55%] shrink truncate text-xs text-muted-foreground">{detail}</span>}
            </button>
            {canPin && <button type="button" onClick={() => setPinned(key, !pinned)} title={pinned ? 'Unpin from sidebar' : 'Pin to sidebar'} aria-pressed={pinned} aria-label={`${pinned ? 'Unpin' : 'Pin'} ${project.name}`}
              className={`grid h-8 w-8 shrink-0 place-items-center rounded-md hover:bg-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:h-10 max-md:w-10 ${pinned ? 'text-primary' : 'text-muted-foreground [@media(hover:hover)]:opacity-0 group-hover/crew:opacity-100 group-focus-within/crew:opacity-100'}`}>
              <Pin aria-hidden="true" className={`h-4 w-4 ${pinned ? 'fill-current' : ''}`} />
            </button>}
            {!desktop && project.where === 'mac' && project.owned_by_caller !== false && <PinnedCrewOrderMenu name={project.name} onRemoveOnlineCopy={() => removeOnlineCopy(project)} />}
          </li>
        })}
      </ul>
    </section>)}
    {children}
    {!shown.length && !children &&
      <div className="space-y-2 rounded-lg border border-border bg-card p-4 text-sm text-muted-foreground">
        <p>{normalizedQuery ? 'No matching projects.' : 'No projects yet.'}</p>
        {normalizedQuery && <button type="button" className="underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" onClick={() => setQuery('')}>Clear search</button>}
      </div>}
    {desktop && connections.length > 1 && <p className="px-1 text-xs text-muted-foreground">Projects on a server open on its website. Add one to a project's Team to work with it here.</p>}
    {(directoryError || filterError) && <p role="alert" className="text-sm text-destructive">{directoryError || filterError}</p>}
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
        const result = await agentApi.listAgentProfileSideChats('work', projectId, true)
        // A failed Team read leaves the person's own agents listed (owner, 11 Nov: a Team failure hid them all), and
        // keeps the teammates' agents last read, so they do not drop out and come back (owner, 20 Nov).
        const teammates = await teamSideChats('work', projectId, connectionId).catch(() => undefined)
        if (!cancelled && isChatIdentityCurrent(identity)) {
          setLoadedChats(previous => {
            const kept = teammates ?? (previous.identity === identity ? previous.chats.filter(chat => chat.conversation_key.includes(':team:')) : [])
            const rows = [...(Array.isArray(result) ? result : []), ...kept]
            return { identity, chats: rows.filter((row, index) => rows.findIndex(other => other.conversation_key === row.conversation_key) === index) }
          })
          setError(false)
        }
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
    const existing = Object.values(useChatStore.getState().chatTabs).find(tab => matchesWorkConversation(tab, 'work', projectId, chat.conversation_key))
    if (existing && useProductSurfaceStore.getState().selectedWorkProjectId === projectId) openAgentTab(existing.tabId)
    else { openProductWorkspace('work'); openAppProduct(route) }
    window.dispatchEvent(new Event('close-app-sidebar'))
  }
  const inTeam = (chat: AgentProfileSideChat) => { const row = teamAgentForTab(team, chat.conversation_key, !!chat.is_main, true); return row?.mine === true && !!row.in_team }
  const own = chats.filter(chat => chat.can_write !== false)
  // The same setup warning the open project's rows show (the Team tab's reading), without its reach line.
  const setupOf = (chat: AgentProfileSideChat) => { const setup = agentSetupForTab(team, chat.conversation_key, !!chat.is_main, chat.can_write !== false); return setup && { ...setup, reach: '' as const } }
  const teammateChats = chats.filter(chat => chat.can_write === false)
  const mostIn = mostInTeam(own.map(inTeam))
  const chatRow = (chat: AgentProfileSideChat) => {
    const session = sessions.find(item => item.session_id === chat.session_id)
    const teamAgent = teamAgentForTab(team, chat.conversation_key, !!chat.is_main, chat.can_write !== false)
    return <ProjectAgentRow key={chat.conversation_key} projectId={projectId} sessionId={chat.session_id} session={session} lastMessageAt={chat.last_message_at} local={connectionId === getRawActiveWorkspaceId()}
      activity={teamAgent?.activity} status={teamAgent?.status_label} onOpen={() => open(chat)} name={chat.name || (chat.is_main ? 'Main' : 'Agent')}
      person={chat.can_write === false ? chat.tab_owner_name || 'Teammate' : undefined} inTeam={chat.can_write !== false ? { on: inTeam(chat), mostIn } : undefined}
      setup={setupOf(chat) || undefined} place={chat.chat_location === 'mac' ? 'mac' : undefined} provider={chat.provider} />
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
  return <div ref={selectedRef}>{children(expanded, () => toggle(key, initialOpen), activityLabel, undefined, () => { if (!expanded) toggle(key, initialOpen) })}{expanded && (live ? <SidebarCrewAgents projectId={projectId} hiddenExpansion={hiddenExpansion} /> : <div role="list" aria-label="Agents" className={sidebarAgentListClass}>{error ? <span className="px-2 text-xs text-muted-foreground">Could not load agents</span> : <SidebarAgentLayout projectId={here ? projectId : undefined} rows={[...shown, ...teammateChats]} keyOf={chat => chat.conversation_key} forkedFromOf={chat => chat.forked_from} teammate={chat => chat.can_write === false} render={chatRow} />}
    {closedAgents.length > 0 && <button type="button" aria-expanded={showClosed} onClick={() => setShowClosed(value => !value)} className={sidebarAgentDisclosureClass}>
      <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${showClosed ? 'rotate-90' : ''}`} />Closed agents ({closedAgents.length})
    </button>}
    {showClosed && closedAgents.map(chatRow)}
    <SidebarHiddenAgents count={hidden.length} expanded={showHidden} onToggle={() => setShowHidden(!showHidden)}>
      {showHidden && hidden.map(chatRow)}
    </SidebarHiddenAgents>
    <OwnTerminalAgents projectId={projectId} connection={connectionId} nested mostIn={mostIn} />
    </div>)}</div>
}

