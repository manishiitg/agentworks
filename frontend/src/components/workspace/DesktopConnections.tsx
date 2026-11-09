import { AgentMark } from '../chat/AgentIcon'
import { useAgentMenuState } from '../chat/agentMenuState'
import type { ActiveSessionInfo } from '../../services/api-types'
import { openConnectedServers } from './ConnectedServersMenuItem'
import { useProjectMenuHistoryStore, connectedWorkHistoryScope } from '../../stores/useProjectMenuHistoryStore'
import { connectedProjectKey } from '../../utils/connectedWorkPresentation'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { Fragment, useEffect, useState, type ReactNode } from 'react'
import { connectedLocations, hasDesktopConnections, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { SidebarProjectRow, openSidebarProject } from './SidebarServerGroup'
import { groupByMac, macCrewStateText, macStatusText, type CrewLocationFields } from './macCrews'
import { useTeamInviteRows } from './TeamInviteRow'
import { useCrewAgentsStore } from '../chat/AgentCard'
import { openAgentTab } from '../../utils/openAgentTab'
import { SidebarCrewAgents } from './SidebarCrewAgents'
import { ChevronRight, Laptop } from 'lucide-react'
import { ServerIdentityIcon } from './ServerIdentityIcon'
import { useSidebarExpansionStore, sidebarExpansionKey } from '../../stores/useSidebarExpansionStore'
import { agentApi, getAuthToken, getDesktopApiBaseUrl, getLocalApiBaseUrl } from '../../services/api'
import type { AgentProfileSideChat } from '../../services/api-types'
import { openAppProduct } from '../../utils/appNavigation'
import { productRoutePath } from '../../utils/productRoutes'
import { runtimeHasBackgroundAgents, runtimeNeedsUserInput, sessionRuntimeStatus } from '../../utils/runtimeActivity'
import { useChatStore } from '../../stores/useChatStore'


type SidebarProject = { id: string; name: string; kind: 'crew' | 'goal'; icon?: string; selected?: boolean; mode?: 'builder' | 'run'; owned_by_caller?: boolean; owner_name?: string; shared_agents?: number; personal_agent?: boolean } & CrewLocationFields

/** Crews first, then a flat shared-dashboard list. Each row names its owner once.
 * The personal AgentWorks agent is rendered by its network group. */
function ProjectList<T extends SidebarProject>({ projects, render, invites }: { projects: T[]; render: (project: T) => ReactNode; invites: ReactNode[] }) {
  const rest = projects.filter(project => !project.personal_agent)
  const mine = rest.filter(project => project.owned_by_caller === true || (project.owned_by_caller === false && project.mode !== 'run'))
  const dashboards = rest.filter(project => project.owned_by_caller === false && project.mode === 'run')
  return <>{invites.length > 0 && <div className="mb-2 space-y-1">{invites}</div>}
    {mine.map(render)}
    {rest.filter(project => project.owned_by_caller === undefined).map(render)}
    {dashboards.length > 0 && <section aria-label="Dashboards shared with me" className="mb-3 space-y-1">
      <h2 className="px-3 pb-1 pt-2 text-xs font-medium text-muted-foreground">Shared</h2>
      {dashboards.map(render)}
    </section>}
    </>
}

function useWorkHistory(projects: SidebarProject[]) {
  const active = getRawActiveWorkspaceId()
  const historyScope = connectedWorkHistoryScope()
  const recent = useProjectMenuHistoryStore(state => state.recent[historyScope])
  const pinned = useProjectMenuHistoryStore(state => state.pinned[historyScope])
  const setPinned = useProjectMenuHistoryStore(state => state.setPinned)
  const movePinned = useProjectMenuHistoryStore(state => state.movePinned)
  const visit = useProjectMenuHistoryStore(state => state.visit)
  const selectedProject = projects.find(project => project.selected)
  const selectedKey = selectedProject ? connectedProjectKey(active, selectedProject.kind, selectedProject.id) : ''
  useEffect(() => { if (selectedKey) visit(historyScope, selectedKey) }, [historyScope, selectedKey, visit])
  return { recent, pinned, movePinned: (key: string, target: string) => movePinned(historyScope, key, target), canPin: Boolean(historyScope), setPinned: (key: string, value: boolean) => setPinned(historyScope, key, value), visit: (key: string) => visit(historyScope, key) }
}
export function DesktopConnections({ children, projects = [] }: { children?: ReactNode; projects?: SidebarProject[] }) {
  const crews = projects.filter(project => project.kind === 'crew')
  return hasDesktopConnections() ? <ConnectedServers projects={crews}>{children}</ConnectedServers> : <StandaloneWork projects={crews}>{children}</StandaloneWork>
}
function AllProjects({ children }: { children: ReactNode }) {
  const key = sidebarExpansionKey(connectedWorkHistoryScope(), 'all-projects')
  const expanded = useSidebarExpansionStore(state => state.expanded[key] ?? false)
  const toggle = useSidebarExpansionStore(state => state.toggle)
  return <section aria-label="All projects">
    <button type="button" aria-expanded={expanded} onClick={() => toggle(key)} className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <span className="min-w-0 flex-1">All projects</span>
      <ChevronRight aria-hidden="true" className={`h-4 w-4 shrink-0 transition-transform ${expanded ? 'rotate-90' : ''}`} />
    </button>
    {expanded && <div className="mt-1">{children}</div>}
  </section>
}
function StandaloneWork({ children, projects }: { children?: ReactNode; projects: SidebarProject[] }) {
  const { recent, pinned, canPin, setPinned, movePinned, visit } = useWorkHistory(projects)
  const orderedProjects = [...projects].sort((a, b) => {
    const rank = (project: SidebarProject) => {
      const index = recent?.indexOf(connectedProjectKey(getRawActiveWorkspaceId(), project.kind, project.id)) ?? -1
      return project.selected && index < 0 ? -1 : index < 0 ? Infinity : index
    }
    return rank(a) - rank(b) || a.name.localeCompare(b.name)
  })
  // The open Crew lists its agents under it (owner, 4 Nov, "Left menu").
  const renderProject = ({ location, mac, state, ...project }: SidebarProject) => {
    const pinnedIndex = pinnedProjects.findIndex(item => item.id === project.id && item.kind === project.kind)
    const move = (offset: number) => {
      const target = pinnedProjects[pinnedIndex + offset]
      if (target) movePinned(connectedProjectKey(getRawActiveWorkspaceId(), project.kind, project.id), connectedProjectKey(getRawActiveWorkspaceId(), target.kind, target.id))
    }
    const row = (agentsExpanded?: boolean, onToggleAgents?: () => void, activityLabel?: string) => <SidebarProjectRow {...project} agentsExpanded={agentsExpanded} onToggleAgents={onToggleAgents} activityLabel={agentsExpanded === false ? activityLabel : undefined}
    onMoveUp={canPin && pinnedIndex > 0 ? () => move(-1) : undefined} onMoveDown={canPin && pinnedIndex >= 0 && pinnedIndex < pinnedProjects.length - 1 ? () => move(1) : undefined}
    showIcon={project.kind !== 'crew' || project.personal_agent || project.mode === 'run'}
    pinned={pinned?.includes(connectedProjectKey(getRawActiveWorkspaceId(), project.kind, project.id))} onTogglePin={canPin && !project.personal_agent ? () => { const key = connectedProjectKey(getRawActiveWorkspaceId(), project.kind, project.id); setPinned(key, !pinned?.includes(key)) } : undefined}
    stateLine={location === 'mac' ? macCrewStateText(state) : undefined} offline={location === 'mac' && mac?.online === false} onMac={location === 'mac' ? (mac?.online ? 'on' : 'off') : undefined} onOpen={() => {
    visit(connectedProjectKey(getRawActiveWorkspaceId(), project.kind, project.id))
    openSidebarProject(project.kind, project.id)
  }} />
    return project.kind === 'crew' && !project.personal_agent
      ? <ExpandableCrew key={`${project.kind}:${project.id}`} projectId={project.id} connectionId={getRawActiveWorkspaceId()} selected={!!project.selected} initialOpen={!!project.selected || !!pinned?.includes(connectedProjectKey(getRawActiveWorkspaceId(), project.kind, project.id))}>{row}</ExpandableCrew>
      : <Fragment key={`${project.kind}:${project.id}`}>{row()}</Fragment>
  }
  const invites = useTeamInviteRows()
  const pinnedProjects = (pinned || []).flatMap(key => {
    const project = orderedProjects.find(item => !item.personal_agent && connectedProjectKey(getRawActiveWorkspaceId(), item.kind, item.id) === key)
    return project ? [project] : []
  })
  const { macs, server } = groupByMac(orderedProjects.filter(project => !pinnedProjects.includes(project)))
  return <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-3">
    {pinnedProjects.length > 0 && <section aria-label="Pinned Crews" className="mb-3 space-y-1">{pinnedProjects.map(renderProject)}</section>}
    <AllProjects>
    {macs.map(({ mac, rows }) => <section key={mac.device_id} aria-label={`On your Mac: ${mac.name}`} className="mb-3 space-y-1">
      <h2 className="flex min-w-0 items-center gap-1.5 px-3 pb-1 pt-2 text-xs font-medium text-muted-foreground">
        <span className="shrink-0">On your Mac</span>
        <span role="img" aria-label={`${mac.name} · ${macStatusText(mac)}`} title={`${mac.name} · ${macStatusText(mac)}`} className={`h-1.5 w-1.5 shrink-0 rounded-full ${mac.online ? 'bg-emerald-500' : 'bg-muted-foreground/40'}`} />
      </h2>
      {rows.map(renderProject)}
    </section>)}
    {orderedProjects.filter(project => project.personal_agent).map(renderProject)}
    {macs.length > 0 && server.length > 0 && <h2 className="px-3 pb-1 pt-2 text-xs font-medium text-muted-foreground">On the server</h2>}
    <div className="space-y-1"><ProjectList projects={server} render={renderProject} invites={invites} /></div>
    {children}
    {window.electronAPI?.getDesktopConnection && <p className="px-2.5 py-2 text-xs text-muted-foreground">Start the app with the desktop launcher to connect another server.</p>}
    </AllProjects>
  </div>
}

function ConnectedServers({ children, projects }: { children?: ReactNode; projects: SidebarProject[] }) {
  const active = getRawActiveWorkspaceId()
  const state = useConnectedWorkStore()
  const connections = connectedLocations(state)
  const inventory = isChatIdentityCurrent(state.identity) ? state.inventory : {}
  const { refresh, refreshActivity, error: directoryError } = state
  const { recent, pinned, canPin, setPinned, movePinned, visit } = useWorkHistory(projects)
  const [selectedLocations, setSelectedLocations] = useState<string[]>([active])
  const invites = useTeamInviteRows()
  useEffect(() => {
    if (!state.loaded || !isChatIdentityCurrent(state.identity) || !connections.length) return
    setSelectedLocations(previous => {
      const remaining = previous.filter(id => connections.some(connection => connection.id === id))
      const next = remaining.length ? remaining : [connections.find(connection => connection.id === active)?.id || connections[0].id]
      return next.length === previous.length && next.every((id, index) => id === previous[index]) ? previous : next
    })
  }, [connections, active, state.loaded, state.identity])
  useEffect(() => {
    void refresh().then(() => refreshActivity())
    const onFocus = () => { void refresh(); void refreshActivity() }
    const timer = window.setInterval(() => { if (!document.hidden) { void refresh(); void refreshActivity() } }, 15_000)
    window.addEventListener('focus', onFocus)
    return () => { window.clearInterval(timer); window.removeEventListener('focus', onFocus) }
  }, [refresh, refreshActivity])
  const open = (id: string, path: string) => {
    void window.electronAPI?.openDesktopConnection?.(id, path).catch(() => useConnectedWorkStore.setState({ error: 'Could not open this server. Try again.' }))
  }
  const rows = connections.flatMap(connection => {
    const byId = new Map<string, SidebarProject>()
    for (const project of inventory[connection.id]?.projects || []) {
      if (project.kind === 'crew') byId.set(`${project.kind}:${project.id}`, project)
    }
    if (connection.id === active) for (const project of projects) byId.set(`${project.kind}:${project.id}`, { ...byId.get(`${project.kind}:${project.id}`), ...project })
    return [...byId.values()].map(project => ({ ...project, connection }))
  }).sort((a, b) => {
    const rank = (row: typeof a) => { const index = recent?.indexOf(connectedProjectKey(row.connection.id, row.kind, row.id)) ?? -1; return index < 0 ? Infinity : index }
    return rank(a) - rank(b) || a.name.localeCompare(b.name) || a.connection.name.localeCompare(b.connection.name) || a.id.localeCompare(b.id)
  })
  // Location buttons only filter navigation; the current chat keeps its connection.
  const visible = rows.filter(row => selectedLocations.includes(row.connection.id))
  const keyFor = (project: typeof rows[number]) => connectedProjectKey(project.connection.id, project.kind, project.id)
  // Saved shortcuts contain IDs only. Resolve them against the current admitted inventory on every render.
  const crewByKey = new Map(rows.filter(row => row.kind === 'crew' && !row.personal_agent).map(row => [keyFor(row), row]))
  const pinnedKeys = new Set(pinned || [])
  const pinnedRows = (pinned || [])
    .flatMap(key => { const row = crewByKey.get(key); return row ? [row] : [] })
  const renderProject = (project: typeof rows[number], shortcut = false) => {
    const key = keyFor(project)
    const pinnedIndex = pinnedRows.findIndex(item => keyFor(item) === key)
    const move = (offset: number) => {
      const target = pinnedRows[pinnedIndex + offset]
      if (target) movePinned(key, keyFor(target))
    }
    const row = (agentsExpanded?: boolean, onToggleAgents?: () => void, activityLabel?: string) => <SidebarProjectRow {...project} agentsExpanded={agentsExpanded} onToggleAgents={onToggleAgents} activityLabel={agentsExpanded === false ? activityLabel : undefined}
      showIcon={project.kind !== 'crew' || project.personal_agent || project.mode === 'run'}
      onMoveUp={shortcut && canPin && pinnedIndex > 0 ? () => move(-1) : undefined} onMoveDown={shortcut && canPin && pinnedIndex >= 0 && pinnedIndex < pinnedRows.length - 1 ? () => move(1) : undefined}
      selected={project.connection.id === active && project.selected}
      location={project.connection.id === 'local' ? 'On your Mac' : project.connection.name} remote={project.connection.id !== 'local'} showLocation={shortcut || selectedLocations.length > 1}
      pinned={pinnedKeys.has(key)} onTogglePin={canPin && project.kind === 'crew' && !project.personal_agent ? () => setPinned(key, !pinnedKeys.has(key)) : undefined}
      onOpen={() => {
        visit(key)
        if (project.connection.id === active) openSidebarProject(project.kind, project.id)
        else open(project.connection.id, project.kind === 'crew' ? `/crew/projects/${encodeURIComponent(project.id)}` : `/workflows/${encodeURIComponent(project.id)}`)
      }} />
    return project.kind === 'crew' && !project.personal_agent
      ? <ExpandableCrew key={key} projectId={project.id} connectionId={project.connection.id} selected={project.connection.id === active && !!project.selected} initialOpen={shortcut || (project.connection.id === active && !!project.selected)}>{row}</ExpandableCrew>
      : <Fragment key={key}>{row()}</Fragment>
  }
  const locations = connections.filter(connection => selectedLocations.includes(connection.id))
  const unpinnedRows = visible.filter(project => !pinnedKeys.has(keyFor(project)))
  return <div className="flex min-h-0 flex-1 flex-col" aria-label="Connected work">
    <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-3">
      {pinnedRows.length > 0 && <section aria-label="Pinned Crews" className="mb-3 space-y-1">
        {pinnedRows.map(project => renderProject(project, true))}
      </section>}
      <AllProjects>
      {connections.length > 1 && <div role="group" aria-label="Show Crews from" className="mb-2 flex items-center gap-1 overflow-x-auto px-1 py-2">
        {connections.map(connection => {
          const selected = selectedLocations.includes(connection.id)
          const name = connection.id === 'local' ? 'Mac' : connection.name
          return <button key={connection.id} type="button" aria-pressed={selected} title={connection.id === 'local' ? 'On your Mac' : connection.name}
            onClick={() => setSelectedLocations(previous => previous.includes(connection.id)
              ? previous.length > 1 ? previous.filter(id => id !== connection.id) : previous
              : [...previous, connection.id])}
            className={`flex h-9 shrink-0 items-center gap-1.5 rounded-lg px-2.5 text-xs font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${selected ? 'bg-foreground/[0.08] text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
            {connection.id === 'local' ? <Laptop className="h-3.5 w-3.5 shrink-0" aria-hidden="true" /> : <ServerIdentityIcon connectionId={connection.id} hidden className="h-3.5 w-3.5 shrink-0" />}
            <span className="max-w-28 truncate">{name}</span>
          </button>
        })}
      </div>}
      {invites.length > 0 && <div className="mb-2 space-y-1">{invites}</div>}
      {unpinnedRows.filter(project => project.personal_agent).map(project => renderProject(project))}
      <div className="space-y-1"><ProjectList projects={unpinnedRows} render={project => renderProject(project)} invites={[]} /></div>
      {selectedLocations.includes(active) && children}
        {locations.map(connection => {
        const status = inventory[connection.id]?.status
        if (!status) return <p key={connection.id} role="status" className="px-2.5 py-2 text-xs text-muted-foreground">Loading {connection.name}…</p>
        if (status === 'unavailable') return <button key={connection.id} className="block px-2.5 py-2 text-left text-xs text-muted-foreground underline" onClick={() => void refresh()}>{connection.name} unavailable · Retry</button>
        if (status === 'sign_in') return <button key={connection.id} className="block px-2.5 py-2 text-left text-xs text-muted-foreground underline" onClick={() => openConnectedServers(connection.server)}>{connection.name} · Sign in again</button>
        return null
      })}
      {!visible.length && locations.length > 0 && locations.every(connection => inventory[connection.id]?.status === 'connected') && <p className="px-2.5 py-2 text-xs text-muted-foreground">No Crews here yet.</p>}
      </AllProjects>
    </div>
    {directoryError && <p role="alert" className="px-3 text-xs text-destructive">{directoryError}</p>}

  </div>
}

/** Each expanded Crew loads its own admitted chats, including on other connections. */
function ExpandableCrew({ projectId, connectionId, selected, initialOpen, children }: { projectId: string; connectionId: string; selected: boolean; initialOpen: boolean; children: (expanded: boolean, onToggle: () => void, activityLabel?: string) => ReactNode }) {
  const key = sidebarExpansionKey(connectedWorkHistoryScope(), 'crew', connectionId, projectId)
  const saved = useSidebarExpansionStore(state => state.expanded[key])
  const toggle = useSidebarExpansionStore(state => state.toggle)
  const expanded = saved ?? initialOpen
  const [loadedChats, setLoadedChats] = useState<{ identity: number; chats: AgentProfileSideChat[] }>({ identity: captureChatIdentity(), chats: [] })
  const chats = isChatIdentityCurrent(loadedChats.identity) ? loadedChats.chats : []
  const liveProjectId = useCrewAgentsStore(state => state.projectId)
  const [error, setError] = useState(false)
  const activity = useConnectedWorkStore(state => isChatIdentityCurrent(state.identity) ? state.activity[connectionId]?.sessions : undefined)
  // The active-session cache is already refreshed for the current connection, including outside the desktop app.
  // Keep observing it while collapsed; no need to open/load the project's chat list to know it is working.
  const localSessions = useChatStore(state => state.activeSessionsCache)
  const sessions = (connectionId === getRawActiveWorkspaceId() ? localSessions : activity || [])
    .filter(session => session.crew_project_id === projectId)
  const running = sessions.some(session => sessionRuntimeStatus(session) === 'busy' || runtimeHasBackgroundAgents(session))
  const activityLabel = running ? 'Running' : sessions.some(runtimeNeedsUserInput) ? 'Needs your input' : undefined
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
        if (!cancelled && isChatIdentityCurrent(identity)) { setLoadedChats({ identity, chats: Array.isArray(result) ? result : [] }); setError(false) }
      } catch { if (!cancelled && isChatIdentityCurrent(identity)) setError(true) }
    }
    void load()
    const timer = window.setInterval(() => { if (!document.hidden) void load() }, 60000)
    return () => { cancelled = true; window.clearInterval(timer) }
  }, [expanded, projectId, connectionId])
  const open = (chat: AgentProfileSideChat) => {
    const agent = chat.is_main ? undefined : chat.conversation_key.split(':chat:')[1]
    const route = { surface: 'work' as const, id: projectId, agent }
    if (connectionId === getRawActiveWorkspaceId()) {
      const existing = Object.values(useChatStore.getState().chatTabs).find(tab => tab.sessionId === chat.session_id && tab.metadata?.agentProfileProjectId === projectId)
      if (existing) openAgentTab(existing.tabId)
      else openAppProduct(route)
    }
    else void window.electronAPI?.openDesktopConnection?.(connectionId, productRoutePath(route))
    window.dispatchEvent(new Event('close-app-sidebar'))
  }
  return <>{children(expanded, () => toggle(key, initialOpen), activityLabel)}{expanded && (selected && liveProjectId === projectId ? <SidebarCrewAgents projectId={projectId} /> : <div role="list" aria-label="Agents" className="ml-6 space-y-0.5 border-l border-border pb-1 pl-1.5">{error ? <span className="px-2 text-xs text-muted-foreground">Could not load agents</span> : chats.filter(chat => chat.pinned !== false).map(chat => {
      const session = sessions.find(item => item.session_id === chat.session_id)
      return <button key={chat.conversation_key} type="button" role="listitem" onClick={() => open(chat)} className="flex w-full min-w-0 items-center gap-2 rounded-md py-1 pl-2 pr-7 text-left text-[13px] text-muted-foreground hover:bg-muted hover:text-foreground focus:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:min-h-10 max-md:pr-11"><span className="min-w-0 flex-1 truncate">{chat.name || (chat.is_main ? 'Main' : 'Agent')}</span><ProjectAgentMark projectId={projectId} sessionId={chat.session_id} session={session} local={connectionId === getRawActiveWorkspaceId()} /></button>
    })}</div>)}</>
}

/** Inactive project rows use the same marks as the open Crew, including unread completions. */
function ProjectAgentMark({ projectId, sessionId, session, local }: { projectId: string; sessionId: string; session?: ActiveSessionInfo; local: boolean }) {
  const tabId = useChatStore(state => local ? Object.values(state.chatTabs).find(tab => tab.sessionId === sessionId && tab.metadata?.agentProfileProjectId === projectId)?.tabId : undefined)
  const state = useAgentMenuState(tabId || `sidebar:${projectId}:${sessionId}`, undefined, local ? { sessionId } : undefined)
  const remoteState = runtimeNeedsUserInput(session) ? 'waiting' : sessionRuntimeStatus(session) === 'busy' || runtimeHasBackgroundAgents(session) ? 'running' : null
  return <AgentMark state={local ? state : remoteState} />
}
