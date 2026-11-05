import { useEffect, useState } from 'react'
import { Bot, CalendarClock, ChevronRight, FolderSync, Globe, Laptop, Link2, RefreshCw, Settings2, TriangleAlert, Users, type LucideIcon } from 'lucide-react'
import { useCrewPanel } from './useCrewPanel'
import { ChatChangesRow } from '../../components/workspace/ChatChangesRow'
import { WorkspaceContextCard, WorkspaceContextRow as Row, WorkspaceContextGroup as Group } from '../../components/workspace/WorkspaceContextCard'
import { WorkspaceDashboardMenu } from '../../components/workspace/WorkspaceDashboardMenu'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { WorkspaceCompactGroup } from '../../components/workspace/WorkspaceCompactGroup'
import { WorkspaceConnectionsMenu } from '../../components/workspace/WorkspaceConnectionsMenu'
import { WorkspaceBrowserRow } from '../../components/workspace/WorkspaceBrowserRow'
import { cloudStateIcon, type CrewAsk, openCrewAskThread, takeCrewAccess, type CrewAccessLine, YourComputerPanel } from './YourComputerPanel'
import { isLocalDeployment } from '../productSurfaceConfig'
import { useContextMenuWideStore } from '../../stores/useContextMenuWideStore'
import { selectReportDocument } from '../../components/workflow/reportDocuments'
import type { DashboardChatTarget } from '../../components/workflow/DashboardEmptyState'
import { setWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { projectMenuHistoryScope } from '../../stores/useProjectMenuHistoryStore'
import { useAgentInboxEnabled } from '../../components/integrations/AgentInboxPanel'
import { CREW_MENU_GROUPS, crewMenuTarget, crewProjectMenuItems, type CrewMenuItem } from './crewProjectMenu'
import type { ProductPanel } from '../productPanels'
import type { WorkWorkspaceView } from './WorkWorkspacePane'
import { openCrewAccess, openCrewSync } from './MacSharedAgents'
import { useChatStore } from '../../stores/useChatStore'
import { activateTab } from '../../utils/activateTab'

export function WorkContextPanel({ workspacePath, projectId = '', profileId = 'work', panels, onOpen, enabledPanels, dashboardChatTarget, reviewSessionId }: {
  workspacePath: string
  reviewSessionId?: string
  projectId?: string
  profileId?: 'work'
  panels: ProductPanel<WorkWorkspaceView>[]
  onOpen: (view: WorkWorkspaceView) => void
  dashboardChatTarget?: DashboardChatTarget
  enabledPanels?: Set<string>
}) {
  const user = useAuthStore(state => state.user)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = projectMenuHistoryScope(server || 'local', user?.id || 'pending', profileId, projectId || workspacePath)
  const incomingEmail = useAgentInboxEnabled()
  const allowed = (view: WorkWorkspaceView) => panels.some(item => item.id === view)
  const { data, error, refresh } = useCrewPanel(scope, projectId, workspacePath, reviewSessionId)
  const section = (id: string) => data?.sections.find(item => item.id === id)
  const access = section('access')?.access
  const apps = (data?.sections || []).flatMap(item => item.rows).filter(row => row.destination?.startsWith('mcp:'))
  const allAutomations = (data?.sections || []).flatMap(item => item.rows).filter(row => row.destination?.startsWith('schedules:'))
  const documents = (section('dashboards')?.rows || []).map((row, order) => ({ id: row.id, title: row.label, path: row.path!, order, motion: row.motion, artifact: row.artifact }))
  const browserRow = section('browser')?.rows[0]
  const browser = { connected: browserRow?.connected === true, name: browserRow?.label === 'Browser' ? undefined : browserRow?.label, label: browserRow?.detail || (section('browser')?.error ? 'Unable to check' : 'Checking…') }
  const menuOptions = { panels, enabledPanels, incomingEmail, projectId }
  const inventory = crewProjectMenuItems({ ...menuOptions, allowedProducts: user?.allowed_products })
  const byId = new Map(inventory.map(item => [item.id, item]))
  const automations = allAutomations.filter(item => byId.has(item.destination!))
  const accessItems = inventory.filter(item => item.group === 'Share' && item.view === 'mcp')
  const integrations = inventory.filter(item => item.id === 'browser' || item.group === 'Integrations' || (item.view === 'mcp' && item.group === 'Use'))
  const automationItems = inventory.filter(item => item.view === 'schedules')
  const [menu, setMenu] = useState<{ scope: string; view: 'full' | 'integrations' | 'automations' | 'access' }>()
  const computerState = access?.place
  const accessTree = access?.tree || []
  const asking = access?.asking || []
  const showMenu = (view: 'full' | 'integrations' | 'automations' | 'access') => {
    if (useAuthStore.getState().user?.id !== user?.id || useWorkspaceConnectionStore.getState().activeWorkspaceId !== server) return
    setMenu({ scope, view })
  }
  const open = (item: CrewMenuItem) => {
    // Recheck account/product admission if access changed while the menu was open.
    const current = useAuthStore.getState().user
    if (current?.id !== user?.id || useWorkspaceConnectionStore.getState().activeWorkspaceId !== server) return
    const admitted = crewProjectMenuItems({ ...menuOptions, allowedProducts: current?.allowed_products })
    if (!admitted.some(candidate => candidate.id === item.id)) return
    if (item.external) { openProductWorkspace(item.external); return }
    if (!item.view) return
    if (item.reportPath) selectReportDocument(workspacePath, item.reportPath)
    if (item.view === 'files') setWorkspaceViewTarget('files', 'browse')
    else if (item.target) setWorkspaceViewTarget(crewMenuTarget(item.view), item.target)
    onOpen(item.view)
  }
  const openID = (id: string) => { const item = byId.get(id); if (item) open(item) }
  // A Crew on this Mac opens Access inside this same menu card, grown, with a back arrow (owner, 3 Nov: "same this just
  // gets bigger"), not as the full side panel.
  // Only the Mac app's own Crews (isLocalDeployment): the website's raw workspace id is 'local' too, and there the
  // Access page is the server's (WorkIntegrationsPanel AccessPage), like for a Crew opened from a server in the Mac app.
  const accessInMenu = isLocalDeployment() && computerState !== undefined && computerState !== 'online'
  const accessOpen = menu?.scope === scope && menu.view === 'access'
  // An agent's tab asked for its sharing: open Access here (the card then scrolls to that agent).
  useEffect(() => {
    const open = () => { if (accessInMenu && !accessOpen && takeCrewAccess(projectId)) showMenu('access') }
    open()
    window.addEventListener('agentworks:open-crew-access', open)
    return () => window.removeEventListener('agentworks:open-crew-access', open)
  })
  useEffect(() => { useContextMenuWideStore.getState().setWide(accessOpen); return () => useContextMenuWideStore.getState().setWide(false) }, [accessOpen])
  // Access opens straight on its tabs, without a title row (owner, 4 Nov); the side panel's own toggle closes it.
  if (menu?.scope === scope && menu.view === 'access') return <WorkspaceContextCard noFooter page>
    <YourComputerPanel projectId={projectId} sessionId={reviewSessionId} canEdit onBack={() => setMenu(undefined)} />
  </WorkspaceContextCard>
  if (menu?.scope === scope) {
    const renderItem = (item: CrewMenuItem) => {
      if (item.id === 'dashboard') return <WorkspaceDashboardMenu key={item.id} documents={documents} createTarget={dashboardChatTarget} onSelect={path => open({ ...item, reportPath: path })}/>
      if (item.id === 'browser') return <WorkspaceBrowserRow key={item.id} workspacePath={workspacePath} profileId={profileId} status={browser} onClick={() => open(item)}/>
      const connections = menu.view === 'integrations' ? apps.filter(row => row.destination === item.id) : []
      const status = data?.details[item.id]
      const color = connections.some(row => row.attention) ? 'text-amber-600 dark:text-amber-400' : connections.some(row => row.connected) ? 'text-emerald-600 dark:text-emerald-400' : undefined
      return <Row key={item.id} icon={item.icon} hint={connections.length && item.id !== 'mcp:connected' ? connections.map(row => row.detail).join('\n') : undefined} detail={menu.view === 'integrations' && status ? <span className={color}>{status}</span> : undefined} onClick={() => open(item)}>{item.label}</Row>
    }
    return <WorkspaceContextCard title={{ full: 'Full menu', integrations: 'Integrations', automations: 'Automations', access: 'Access' }[menu.view]} onBack={() => setMenu(undefined)}>
      {menu.view !== 'full' ? <div className="space-y-0.5">{(menu.view === 'integrations' ? integrations : automationItems).map(renderItem)}</div> : CREW_MENU_GROUPS.map(group => {
        const items = inventory.filter(item => item.group === group)
        if (!items.length) return null
        const subgroups = [...new Set(items.flatMap(item => item.subgroup ? [item.subgroup] : []))]
        return <Group key={`${scope}:${group}`} title={group} collapsed defaultOpen={group === 'Build'}>
          {items.filter(item => !item.subgroup).map(renderItem)}
          {subgroups.map(subgroup => <div key={subgroup} role="group" aria-label={subgroup} className="space-y-1"><h3 className="px-1 pb-1 pt-2 text-[10px] font-medium text-muted-foreground">{subgroup}</h3>{items.filter(item => item.subgroup === subgroup).map(renderItem)}</div>)}
        </Group>
      })}
    </WorkspaceContextCard>
  }
  const changes = reviewSessionId && allowed('files') ? <ChatChangesRow key={`${scope}:${reviewSessionId}`} section={section('changes')} onOpen={(repoRoot) => { if (useAuthStore.getState().user?.id !== user?.id || useWorkspaceConnectionStore.getState().activeWorkspaceId !== server) return; setWorkspaceViewTarget('files', repoRoot === undefined ? 'review-uncommitted' : `review-uncommitted:${encodeURIComponent(repoRoot)}`); onOpen('files') }}/> : null
  const theirs = access?.role === 'member' || access?.role === 'shared'
  const macElsewhere = data?.mac_elsewhere === true
  const accessDetail = section('access')?.detail || section('sharing')?.detail
  const accessRow = (item: CrewMenuItem) => <Row key={item.id} icon={item.id === 'mcp:access' ? cloudStateIcon(computerState, theirs) : item.icon} detail={item.id === 'mcp:access' ? accessDetail : undefined} onClick={() => item.id === 'mcp:access' && accessInMenu ? showMenu('access') : open(item)}>{item.label}</Row>
  const openAsk = (ask: CrewAsk) => { void openCrewAskThread(projectId, ask) }
  // Team things sit under Access as its sub-list (owner, 5 Nov: "put inside access only like a sub menu"): the shared
  // folder's line, then who asked; each opens Access at its place.
  const lineIcon: Record<CrewAccessLine['kind'], LucideIcon> = { mac: Globe, team: Users, agents: Bot, public: Link2, folder: FolderSync, clash: TriangleAlert, where: access?.where === 'mac' ? Laptop : Globe }
  // Where Access is not inside this menu (the website, a Crew from a server), it opens as the Access page, the same
  // Team tab and its Sync section (TeamSyncPanel scrolls to it after openCrewSync).
  const openAccessPage = () => { if (!accessInMenu) openID('mcp:access') }
  const openLine = (line: CrewAccessLine) => {
    if (line.kind === 'folder' || line.kind === 'clash') openCrewSync(projectId)
    else openCrewAccess(projectId, '', line.kind === 'mac' || line.kind === 'public' ? 'mac' : 'team')
    openAccessPage()
  }
  // A person in the Team: their agent in this Crew's tabs (watch only), else Access at the Team tab.
  const openPerson = (name: string) => {
    const tab = Object.values(useChatStore.getState().chatTabs).find(item => item.metadata?.agentProfileProjectId === projectId && item.metadata?.agentProfileTabCanWrite === false && (item.metadata?.agentProfileTabOwnerName || '').split('@')[0] === name.split('@')[0])
    if (tab) activateTab(tab.tabId); else { openCrewAccess(projectId, '', 'team'); openAccessPage() }
  }
  const lineButton = 'flex w-full items-start gap-2 rounded-md px-1.5 py-1 text-left text-[11px] hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
  const treeLines = accessTree.map((line, index) => {
    const Icon = lineIcon[line.kind] || Globe
    return <div key={`${line.kind}:${index}`} role="listitem">
      <button type="button" onClick={() => openLine(line)} className={lineButton}>
        <Icon aria-hidden="true" className={`mt-0.5 h-3 w-3 shrink-0 ${line.kind === 'clash' ? 'text-amber-600' : 'text-muted-foreground'}`} />
        <span className="min-w-0 flex-1"><span className="block break-words font-medium text-foreground">{line.title}</span>
          {line.detail && <span className="block break-words text-muted-foreground">{line.detail}</span>}
          {line.progress && line.progress.bytes_total > 0 && <span aria-hidden="true" className="mt-1 block h-1 overflow-hidden rounded-full bg-muted"><span className="block h-full rounded-full bg-primary" style={{ width: `${Math.min(100, Math.round(line.progress.bytes_done * 100 / line.progress.bytes_total))}%` }} /></span>}
        </span>
      </button>
      {line.people?.map(person => <button key={person.name} type="button" onClick={() => openPerson(person.name)} title={person.online_label} className={`${lineButton} pl-6`}>
        <span aria-hidden="true" className={`mt-1 h-1.5 w-1.5 shrink-0 rounded-full ${person.online ? 'bg-emerald-500' : 'bg-muted-foreground/40'}`} />
        <span className="min-w-0 flex-1 break-words text-muted-foreground"><span className="text-foreground">{person.name}</span>{person.status_label && <span> · {person.status_label}</span>}{person.agents?.length ? ` · ${person.agents.join(', ')}` : ''}</span>
      </button>)}
    </div>
  })
  // Team things sit under Access as its sub-list (owner, 5 Nov: "put inside access only like a sub menu"): the server's
  // tree (or, until it has one, the shared folder's line), then who asked; each opens Access at its place.
  const asksTree = (asking.length > 0 || accessTree.length > 0) && <div role="list" aria-label="Access details" className="ml-3 border-l border-border pl-2 pb-1">
    {treeLines}
    {asking.slice(0, 5).map(ask => <button key={`${ask.tab}:${ask.sender}`} role="listitem" type="button" onClick={() => openAsk(ask)} title={`Open ${ask.sender}'s chat with ${ask.agent}`} className="flex w-full items-center gap-2 rounded-md px-1.5 py-1 text-left text-[11px] hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <span aria-hidden="true" className={`h-1.5 w-1.5 shrink-0 rounded-full ${ask.active ? 'bg-emerald-500 motion-safe:animate-pulse' : 'bg-muted-foreground/40'}`} />
      <span className="min-w-0 flex-1 truncate">{ask.label}</span>
      <span className="shrink-0 text-muted-foreground">{ask.detail}</span>
    </button>)}
    {asking.length > 5 && <p className="px-1.5 text-[11px] text-muted-foreground">+{asking.length - 5} more</p>}
  </div>
  return <WorkspaceContextCard onFullMenu={() => showMenu('full')} footerContent={accessItems.length > 0 && <div className="space-y-0.5">{accessItems.map(accessRow)}{asksTree}</div>}>
    {macElsewhere ? <div role="note" className="space-y-1.5 px-1 py-1 text-xs text-muted-foreground">
      <p>This Crew lives on your Mac. Its changes, connections and automations are set on your Mac.</p>
      <p>Dashboards work once this Crew is online. Move it online from your Mac.</p>
    </div> : <>
    {changes}
    {byId.has('dashboard') && <WorkspaceDashboardMenu key={`${scope}:dashboards`} documents={documents} createTarget={dashboardChatTarget} onSelect={path => { const dashboard = byId.get('dashboard'); if (dashboard) open({ ...dashboard, reportPath: path }) }}/>}
    <WorkspaceConnectionsMenu key={scope} browser={browser} details={data?.details || {}} browserAllowed={byId.has('browser')} mcpAllowed={byId.has('mcp:connected')} apps={apps.filter(item => byId.has(item.destination!))} loading={!section('connections')} unavailable={!!section('connections')?.error} onOpen={openID} onManage={integrations.length ? () => showMenu('integrations') : undefined}/>
    {automationItems.length > 0 && <WorkspaceCompactGroup key={`${scope}:automations`} title="Automations" icon={CalendarClock} detail={data?.details.automations}>
      {automations.map(item => <Row key={item.id} icon={byId.get(item.destination!)?.icon || CalendarClock} hint={byId.get(item.destination!)?.label} detail={item.detail} onClick={() => openID(item.destination!)}>{item.label}</Row>)}
      <Row icon={Settings2} detail={<ChevronRight aria-hidden="true" className="h-3 w-3" />} onClick={() => showMenu('automations')}>Manage automations</Row>
    </WorkspaceCompactGroup>}
    </>}
    {error || data?.sections.some(item => item.error || item.stale) ? <div role="status" className="space-y-1 px-1 text-xs text-muted-foreground"><p>{error || (data?.sections.some(item => item.stale) ? 'Showing the last update for some sections.' : 'Some workspace status is unavailable.')}</p><button type="button" onClick={refresh} className="inline-flex items-center gap-1 py-1 hover:text-foreground"><RefreshCw className="h-3 w-3"/>Retry</button></div> : null}
  </WorkspaceContextCard>
}
