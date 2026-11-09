import { useState } from 'react'
import { Bot, CalendarClock, Eye, FolderSync, Globe, Laptop, LayoutDashboard, Link2, Mail, RefreshCw, Route, TriangleAlert, Users, Webhook, Zap, type LucideIcon } from 'lucide-react'
import { useCrewPanel } from './useCrewPanel'
import { ChatChangesRow } from '../../components/workspace/ChatChangesRow'
import { WorkspaceContextCard, WorkspaceContextRow as Row, WorkspaceContextGroup as Group } from '../../components/workspace/WorkspaceContextCard'
import { WorkspaceDashboardMenu } from '../../components/workspace/WorkspaceDashboardMenu'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { WorkspaceConnectionsMenu, ConnectionIcon } from '../../components/workspace/WorkspaceConnectionsMenu'
import { TooltipProvider } from '../../components/ui/tooltip'
import { WorkspaceCompactGroup } from '../../components/workspace/WorkspaceCompactGroup'
import { WorkspaceBrowserRow } from '../../components/workspace/WorkspaceBrowserRow'
import { cloudStateIcon, type CrewAsk, openCrewAskThread, type CrewAccessLine } from './YourComputerPanel'
import { selectReportDocument } from '../../components/workflow/reportDocuments'
import type { DashboardChatTarget } from '../../components/workflow/DashboardEmptyState'
import { setWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { projectMenuHistoryScope } from '../../stores/useProjectMenuHistoryStore'
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
  const allowed = (view: WorkWorkspaceView) => panels.some(item => item.id === view)
  const { data, error, refresh } = useCrewPanel(scope, projectId, workspacePath, reviewSessionId)
  const section = (id: string) => data?.sections.find(item => item.id === id)
  const access = section('access')?.access
  const apps = (data?.sections || []).flatMap(item => item.rows).filter(row => row.destination?.startsWith('mcp:'))
  const documents = (section('dashboards')?.rows || []).map((row, order) => ({ id: row.id, title: row.label, path: row.path!, order, motion: row.motion, artifact: row.artifact }))
  const browserRow = section('browser')?.rows[0]
  const browser = { connected: browserRow?.connected === true, name: browserRow?.label === 'Browser' ? undefined : browserRow?.label, label: browserRow?.detail || (section('browser')?.error ? 'Unable to check' : 'Checking…') }
  const menuOptions = { panels, enabledPanels, projectId }
  const inventory = crewProjectMenuItems({ ...menuOptions, allowedProducts: user?.allowed_products })
  const byId = new Map(inventory.map(item => [item.id, item]))
  const accessItems = inventory.filter(item => item.group === 'Share' && item.view === 'mcp')
  const integrations = inventory.filter(item => item.id === 'browser' || item.group === 'Integrations' || (item.view === 'mcp' && item.group === 'Use'))
  const [menu, setMenu] = useState<{ scope: string; view: 'full' | 'integrations' }>()
  const computerState = access?.place
  const accessTree = access?.tree || []
  const asking = access?.asking || []
  const showMenu = (view: 'full' | 'integrations') => {
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
  // One page, opened at the tab the row names (the Automations page listens for the target).
  const automationKinds = [{ target: 'schedules', label: 'Schedules', icon: CalendarClock }, { target: 'triggers', label: 'Webhooks', icon: Webhook }, { target: 'conditions', label: 'Watchers', icon: Eye }] as const
  const allRows = (data?.sections || []).flatMap(item => item.rows)
  // The server sends an Inbox row whenever it can do inboxes, On or Off (the server decides, not a browser flag).
  const inboxRow = allRows.find(row => row.destination === 'schedules:email')
  // The collapsed row's stacked icons: one per kind that has something, with how many (Connections look): the workflow,
  // each start, and the Inbox while it is on. Each hint is the server's one-line summary.
  const automationChips = [
    { key: 'workflow', detailKey: 'workflow', target: 'workflow', label: 'Workflow', icon: Route, count: allRows.filter(row => row.destination === 'workflow').length },
    ...automationKinds.map(kind => ({ key: kind.target, detailKey: `schedules:${kind.target}`, target: kind.target, label: kind.label, icon: kind.icon, count: allRows.filter(row => row.destination === `schedules:${kind.target}`).length })),
    { key: 'email', detailKey: 'schedules:email', target: 'email', label: 'Inbox', icon: Mail, count: inboxRow?.connected ? 1 : 0 },
  ].filter(chip => chip.count > 0)
  const openAutomation = (target: string) => { const item = byId.get('schedules'); if (item) open({ ...item, target }) }
  // Access is one full-panel page in the Mac app and on the website (owner, 5 Nov: the same Team screens everywhere),
  // with Who can use it where the server lists it; it is never a smaller copy inside this menu.
  if (menu?.scope === scope) {
    const renderItem = (item: CrewMenuItem) => {
      if (item.id === 'dashboard') return <WorkspaceDashboardMenu key={item.id} documents={documents} createTarget={dashboardChatTarget} onSelect={path => open({ ...item, reportPath: path })}/>
      if (item.id === 'browser') return <WorkspaceBrowserRow key={item.id} workspacePath={workspacePath} profileId={profileId} status={browser} onClick={() => open(item)}/>
      const connections = menu.view === 'integrations' ? apps.filter(row => row.destination === item.id) : []
      const status = data?.details[item.id]
      const color = connections.some(row => row.attention) ? 'text-amber-600 dark:text-amber-400' : connections.some(row => row.connected) ? 'text-emerald-600 dark:text-emerald-400' : undefined
      return <Row key={item.id} icon={item.icon} hint={connections.length && item.id !== 'mcp:connected' ? connections.map(row => row.detail).join('\n') : undefined} detail={menu.view === 'integrations' && status ? <span className={color}>{status}</span> : undefined} onClick={() => open(item)}>{item.label}</Row>
    }
    return <WorkspaceContextCard title={{ full: 'Full menu', integrations: 'Integrations' }[menu.view]} onBack={() => setMenu(undefined)}>
      {menu.view !== 'full' ? <div className="space-y-0.5">{integrations.map(renderItem)}</div> : CREW_MENU_GROUPS.map(group => {
        const items = inventory.filter(item => item.group === group && !(item.id === 'dashboard' && accessItems.length > 0))
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
  const accessRow = (item: CrewMenuItem) => <Row key={item.id} icon={item.id === 'mcp:access' ? cloudStateIcon(computerState, theirs) : item.icon} detail={item.id === 'mcp:access' ? accessDetail : undefined} onClick={() => open(item)}>{item.label}</Row>
  const openAsk = (ask: CrewAsk) => { void openCrewAskThread(projectId, ask) }
  // Team things sit under Access as its sub-list (owner, 5 Nov: "put inside access only like a sub menu"): the shared
  // folder's line, then who asked; each opens Access at its place.
  const lineIcon: Record<CrewAccessLine['kind'], LucideIcon> = { mac: Globe, team: Users, agents: Bot, dashboard: LayoutDashboard, public: Link2, folder: FolderSync, clash: TriangleAlert, where: access?.where === 'mac' ? Laptop : Globe }
  // Each line opens the Access page (WorkSurface listens) at its tab, or at Sync (TeamSyncPanel scrolls to it).
  const openLine = (line: CrewAccessLine) => {
    // The dashboard line (owner, 6 Nov) opens the Share page's Dashboards tab, where the server lists them, or says
    // there are none and offers its Ask.
    if (line.kind === 'dashboard') { openCrewAccess(projectId, '', 'dashboards'); return }
    if (line.kind === 'folder' || line.kind === 'clash') openCrewSync(projectId)
    else openCrewAccess(projectId, '', line.kind === 'mac' || line.kind === 'public' ? 'mac' : 'team')
  }
  // A person in the Team, by account: their agent in this Crew's tabs (watch only), else Access at the Team tab.
  const openPerson = (userId: string) => {
    const tab = Object.values(useChatStore.getState().chatTabs).find(item => item.metadata?.agentProfileProjectId === projectId && item.metadata?.agentProfileTabCanWrite === false && item.metadata?.agentProfileTabOwnerID === userId)
    if (tab) activateTab(tab.tabId); else openCrewAccess(projectId, '', 'team')
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
      {line.people?.map(person => <button key={person.user_id} type="button" onClick={() => openPerson(person.user_id)} title={person.online_label} className={`${lineButton} pl-6`}>
        <span aria-hidden="true" className={`mt-1 h-1.5 w-1.5 shrink-0 rounded-full ${person.online ? 'bg-emerald-500' : 'bg-muted-foreground/40'}`} />
        <span className="min-w-0 flex-1 break-words text-muted-foreground"><span className="text-foreground">{person.name}</span>{person.status_label && <span> · {person.status_label}</span>}{person.agents_label && ` · ${person.agents_label}`}</span>
      </button>)}
    </div>
  })
  // Team things sit under Access as its sub-list (owner, 5 Nov: "put inside access only like a sub menu"): the server's
  // tree (or, until it has one, the shared folder's line), then who asked; each opens Access at its place.
  const asksTree = (asking.length > 0 || accessTree.length > 0) && <div role="list" aria-label="Share details" className="ml-3 border-l border-border pl-2 pb-1">
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
    {/* With a Share row the dashboard is a line in its tree (owner, 5 Nov: one place); Run mode keeps this menu. */}
    {byId.has('dashboard') && accessItems.length === 0 && <WorkspaceDashboardMenu key={`${scope}:dashboards`} documents={documents} createTarget={dashboardChatTarget} onSelect={path => { const dashboard = byId.get('dashboard'); if (dashboard) open({ ...dashboard, reportPath: path }) }}/>}
    <WorkspaceConnectionsMenu key={scope} browser={browser} details={data?.details || {}} browserAllowed={byId.has('browser')} mcpAllowed={byId.has('mcp:connected')} apps={apps.filter(item => byId.has(item.destination!))} loading={!section('connections')} unavailable={!!section('connections')?.error} onOpen={openID} onManage={integrations.length ? () => showMenu('integrations') : undefined}/>
    {(byId.has('workflow') || byId.has('schedules')) && <TooltipProvider delayDuration={200}><WorkspaceCompactGroup key={`${scope}:automations`} title="Automations" icon={Zap}
      detail={automationChips.length ? undefined : data?.details.automations}
      preview={automationChips.length ? <div className="isolate flex -space-x-1.5">{automationChips.map(chip =>
        <ConnectionIcon key={chip.key} icon={chip.icon} hint={`${chip.label} · ${data?.details[chip.detailKey] ?? chip.count}`} count={chip.count} onClick={() => chip.target === 'workflow' ? openID('workflow') : openAutomation(chip.target)} />)}</div> : undefined}>
      {/* Everything that sets work off, inside one row like Connections (owner, 8 Nov): the workflow, then its starts,
          each with the server's one-line summary. */}
      {byId.has('workflow') && <Row icon={Route} detail={data?.details.workflow} onClick={() => openID('workflow')}>Workflow</Row>}
      {byId.has('schedules') && <>
        {automationKinds.map(({ target, label, icon: Icon }) =>
          <Row key={target} icon={Icon} detail={data?.details[`schedules:${target}`]} onClick={() => openAutomation(target)}>{label}</Row>)}
        {inboxRow && <Row icon={Mail} detail={data?.details['schedules:email']} onClick={() => openAutomation('email')}>Inbox</Row>}
      </>}
    </WorkspaceCompactGroup></TooltipProvider>}
    </>}
    {error ? <div role="status" className="space-y-1 px-1 text-xs text-muted-foreground"><p>{error}</p><button type="button" onClick={refresh} className="inline-flex items-center gap-1 py-1 hover:text-foreground"><RefreshCw className="h-3 w-3"/>Retry</button></div> : null}
  </WorkspaceContextCard>
}
