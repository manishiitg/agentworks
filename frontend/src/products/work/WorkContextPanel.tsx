import { useEffect, useState, type ReactNode } from 'react'
import { CalendarClock, ChevronRight, Eye, FolderSync, Globe, LayoutDashboard, Link2, Mail, RefreshCw, Route, TriangleAlert, Users, Webhook, Zap, type LucideIcon } from 'lucide-react'
import { useCrewPanel } from './useCrewPanel'
import { ChatChangesRow } from '../../components/workspace/ChatChangesRow'
import { WorkspaceContextCard, WorkspaceContextRow as Row, WorkspaceContextGroup as Group } from '../../components/workspace/WorkspaceContextCard'
import { WorkspaceDashboardMenu } from '../../components/workspace/WorkspaceDashboardMenu'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { WorkspaceConnectionsMenu, ConnectionIcon } from '../../components/workspace/WorkspaceConnectionsMenu'
import { TooltipProvider } from '../../components/ui/tooltip'
import { WorkspaceCompactGroup } from '../../components/workspace/WorkspaceCompactGroup'
import { WorkspaceBrowserRow } from '../../components/workspace/WorkspaceBrowserRow'
import { type CrewAccessLine } from './YourComputerPanel'
import { selectReportDocument } from '../../components/workflow/reportDocuments'
import type { DashboardChatTarget } from '../../components/workflow/DashboardEmptyState'
import { setWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { projectMenuHistoryScope } from '../../stores/useProjectMenuHistoryStore'
import { CREW_MENU_GROUPS, crewMenuTarget, crewProjectMenuItems, type CrewMenuItem } from './crewProjectMenu'
import type { ProductPanel } from '../productPanels'
import type { WorkWorkspaceView } from './WorkWorkspacePane'
import { openCrewAccess } from './MacSharedAgents'
import { CrewOnlineRow } from './CrewOnlineRow'
import { useChatStore } from '../../stores/useChatStore'
import { useTeamTab, type TeamTabState } from './teamApi'
import { teamAgentChatTab } from './AgentSharingRow'
import { activateTab } from '../../utils/activateTab'
import { sidebarExpansionKey, useSidebarExpansionStore } from '../../stores/useSidebarExpansionStore'
import { fillAskFromMyAgentDraft } from '../../utils/agentPurposeDraft'

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
  const { data, error, refresh } = useCrewPanel(scope, projectId, workspacePath)
  // The Team's agents and activity, from the same Team read every screen shares (teamApi watchTeamTab).
  const team = useTeamTab(projectId || undefined)
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
  // Open or closed, remembered per Crew in this browser (a per-person convenience; open by default).
  const dashboardsKey = `agentworks.menu-dashboards-closed:${projectId}`
  const [dashboardsOpen, setDashboardsOpen] = useState(() => { try { return localStorage.getItem(dashboardsKey) !== '1' } catch { return true } })
  useEffect(() => { try { setDashboardsOpen(localStorage.getItem(dashboardsKey) !== '1') } catch { setDashboardsOpen(true) } }, [dashboardsKey])
  const toggleDashboards = () => setDashboardsOpen(current => {
    try { if (current) localStorage.setItem(dashboardsKey, '1'); else localStorage.removeItem(dashboardsKey) } catch { /* kept for this visit only */ }
    return !current
  })
  const computerState = access?.place
  const accessTree = access?.tree || []
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
    if (item.id === 'mcp:team') { openCrewAccess(projectId, '', 'team'); return }
    if (item.external) { openProductWorkspace(item.external); return }
    if (!item.view) return
    if (item.reportPath) selectReportDocument(workspacePath, item.reportPath)
    if (item.view === 'files') setWorkspaceViewTarget('files', 'browse')
    else if (item.target) setWorkspaceViewTarget(crewMenuTarget(item.view), item.target)
    onOpen(item.view)
  }
  const openID = (id: string) => { const item = byId.get(id); if (item) open(item) }
  // One page, opened at the tab the row names (the Automations page listens for the target).
  const automationKinds = [{ target: 'schedules', section: 'schedules', label: 'Schedules', icon: CalendarClock }, { target: 'triggers', section: 'webhooks', label: 'Webhooks', icon: Webhook }, { target: 'conditions', section: 'watchers', label: 'Watchers', icon: Eye }] as const
  const allRows = (data?.sections || []).flatMap(item => item.rows)
  // The server sends an Inbox row whenever it can do inboxes, On or Off (the server decides, not a browser flag).
  const inboxRow = allRows.find(row => row.destination === 'schedules:email')
  // The collapsed row's stacked icons: one per kind that has something, with how many (Connections look): the workflow,
  // each start, and the Inbox while it is on. Each hint is the server's one-line summary.
  const automationChips = [
    { key: 'workflow', detailKey: 'workflow', target: 'workflow', label: 'Workflow', icon: Route, count: section('workflow')?.rows.filter(row => row.destination === 'workflow').length ?? 0 },
    ...automationKinds.map(kind => ({ key: kind.target, detailKey: `schedules:${kind.target}`, target: kind.target, label: kind.label, icon: kind.icon, count: section(kind.section)?.rows.length ?? 0 })),
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
  const changes = reviewSessionId && allowed('files') ? <ChatChangesRow key={`${scope}:${reviewSessionId}`} section={section('changes')} storageKey={`agentworks.changes-panel.${workspacePath}`} onOpen={(repoRoot) => { if (useAuthStore.getState().user?.id !== user?.id || useWorkspaceConnectionStore.getState().activeWorkspaceId !== server) return; setWorkspaceViewTarget('files', repoRoot === undefined ? 'review-uncommitted' : `review-uncommitted:${encodeURIComponent(repoRoot)}`); onOpen('files') }}/> : null
  const theirs = access?.role === 'member' || access?.role === 'shared'
  const macElsewhere = data?.mac_elsewhere === true
  // Team and Online (owner, 11 Nov: the Share row split in two): Team says Off or how many people, Online where the
  // Crew lives (its state icon); the server words both.
  const accessRow = (item: CrewMenuItem) => item.id === 'mcp:online'
    ? <CrewOnlineRow key={item.id} state={computerState} theirs={theirs} onOpen={() => open(item)} />
    : <Row key={item.id} icon={item.icon} onClick={() => open(item)}>{item.id === 'mcp:team' ? accessTree.find(line => line.kind === 'team')?.title || item.label : item.label}</Row>
  // Team things sit under Access as its sub-list (owner, 5 Nov: "put inside access only like a sub menu"): the shared
  // folder's line, then who asked; each opens Access at its place.
  const lineIcon: Record<CrewAccessLine['kind'], LucideIcon> = { mac: Globe, team: Users, team_live: Users, dashboard: LayoutDashboard, public: Link2, folder: FolderSync, clash: TriangleAlert }
  // Each line opens the Access page (WorkSurface listens) at its tab, or at Sync (TeamSyncPanel scrolls to it).
  const openLine = (line: CrewAccessLine) => {
    // The dashboard line (owner, 6 Nov) opens the Artifacts page, where the server lists them, or says
    // there are none and offers its Ask.
    if (line.kind === 'team_live') { openCrewAccess(projectId, '', 'team', 'activity'); return }
    if (line.kind === 'dashboard') { openCrewAccess(projectId, '', 'artifacts'); return }
    if (line.kind === 'folder' || line.kind === 'clash') openCrewAccess(projectId, '', 'team', 'sync')
    else openCrewAccess(projectId, '', line.kind === 'mac' || line.kind === 'public' ? 'mac' : 'team')
  }
  const lineButton = 'flex w-full items-start gap-2 rounded-md px-1.5 py-1 text-left text-[11px] hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
  const treeLines = (lines: CrewAccessLine[]) => lines.map((line, index) => {
    const Icon = lineIcon[line.kind] || Globe
    // Dashboards (owner, 9 Nov mock): a header that opens and closes the list, each dashboard on one line (no
    // "Manage dashboards" link, owner 10 Nov). With none, the server's one line and its Ask, as before.
    if (line.kind === 'dashboard' && line.dashboards?.length) {
      const dashboard = byId.get('dashboard')
      return <div key={`${line.kind}:${index}`} role="listitem">
        {/* Owner, 15 Nov: the label opens the Artifacts pane like the other rows; only the caret, at the right end
            as elsewhere in this panel, opens or closes the list. */}
        <div className="flex items-center">
        <button type="button" onClick={() => { const artifacts = byId.get('mcp:artifacts'); if (artifacts) open(artifacts); else openLine(line) }} className={`${lineButton} min-w-0 flex-1 items-center`}>
          <span className="font-medium text-foreground">{line.title}</span>
          {line.count ? <span className="text-muted-foreground">{line.count}</span> : null}
          {line.tag && <span className="ml-auto shrink-0 rounded-full border border-border px-1.5 text-[10px] leading-4 text-muted-foreground">{line.tag}</span>}
        </button>
        <button type="button" aria-label={dashboardsOpen ? 'Hide artifacts' : 'Show artifacts'} aria-expanded={dashboardsOpen} onClick={toggleDashboards} className="flex h-8 w-6 shrink-0 items-center justify-center rounded text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
          <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${dashboardsOpen ? 'rotate-90' : ''}`} />
        </button>
        </div>
        {dashboardsOpen && <div className="ml-3 border-l border-border pl-1.5">
          {dashboard && line.dashboards.map(item => <button key={item.path} type="button" title={item.name} onClick={() => open({ ...dashboard, reportPath: item.path })} className={`${lineButton} group/dash items-center`}>
            <span aria-hidden="true" className="h-1.5 w-1.5 shrink-0 rounded-sm bg-muted-foreground/60" />
            <span className="min-w-0 flex-1 truncate text-foreground">{item.name}</span>
            <span aria-hidden="true" className="shrink-0 text-muted-foreground opacity-0 group-hover/dash:opacity-100">Open</span>
          </button>)}
        </div>}
      </div>
    }
    return <div key={`${line.kind}:${index}`} role="listitem">
      <button type="button" onClick={() => openLine(line)} className={lineButton}>
        {line.kind === 'team_live' ? <span aria-hidden="true" className={`mt-1 h-1.5 w-1.5 shrink-0 rounded-full ${line.tag === 'waiting' ? 'bg-amber-500' : line.tag === 'decision' ? 'rotate-45 rounded-none bg-blue-500' : line.tag === 'finding' ? 'rounded-sm bg-blue-500' : 'bg-emerald-500'}`} /> : <Icon aria-hidden="true" className={`mt-0.5 h-3 w-3 shrink-0 ${line.kind === 'clash' || line.kind === 'folder' ? 'text-amber-600' : 'text-muted-foreground'}`} />}
        <span className="min-w-0 flex-1"><span className="block break-words font-medium text-foreground">{line.title}</span>
          {line.detail && <span className="block break-words text-muted-foreground">{line.detail}</span>}
          {line.progress && line.progress.bytes_total > 0 && <span aria-hidden="true" className="mt-1 block h-1 overflow-hidden rounded-full bg-muted"><span className="block h-full rounded-full bg-primary" style={{ width: `${Math.min(100, Math.round(line.progress.bytes_done * 100 / line.progress.bytes_total))}%` }} /></span>}
        </span>
      </button>
    </div>
  })
  // Team keeps its details; Mac online is one row. Artifacts remains a sibling
  // with its own document list (owner, 15 Nov).
  const teamLines = accessTree.filter(line => line.group === 'team' && line.kind !== 'team')
  const artifactLines = accessTree.filter(line => line.kind === 'dashboard')
  const activityLines = teamLines.filter(line => line.kind === 'team_live')
  const otherTeamLines = teamLines.filter(line => line.kind !== 'team_live')
  const teamTree = (teamLines.length > 0 || !!team.view) && <div role="list" aria-label="Team details" className="ml-3 border-l border-border pl-2 pb-1">
    <TeamAgentsGroup projectId={projectId} scope={scope} team={team} />
    {activityLines.length > 0 && <TeamPanelGroup scope={scope} id="activity" title="Activity" count={`${activityLines.length} new`} preview={activityLines[0].title}>
      {treeLines(activityLines.slice(0, 2))}
      <button type="button" onClick={() => openCrewAccess(projectId, '', 'team', 'activity')} className={`${lineButton} text-primary`}>See all activity</button>
    </TeamPanelGroup>}
    {treeLines(otherTeamLines)}
  </div>
  return <WorkspaceContextCard onFullMenu={() => showMenu('full')} footerContent={accessItems.length > 0 && <div className="space-y-0.5">{accessItems.map(item => <div key={item.id}>{item.id === 'mcp:artifacts' && artifactLines.length ? treeLines(artifactLines) : accessRow(item)}{item.id === 'mcp:team' ? teamTree : null}</div>)}</div>}>
    {macElsewhere ? <div role="note" className="space-y-1.5 px-1 py-1 text-xs text-muted-foreground">
      <p>This Crew lives on your Mac. Its changes, connections and automations are set on your Mac.</p>
      <p>Artifacts work once this Crew is online. Move it online from your Mac.</p>
    </div> : <>
    {changes}
    {/* Artifacts has its own top-level footer row (owner, 14 Nov); Run mode keeps this menu. */}
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

/** One collapsible group under Team (owner, 15 Nov mock): closed by default, one line with a count and a short preview;
 * each person's open or close is kept. */
function TeamPanelGroup({ scope, id, title, count, preview, children }: { scope: string; id: string; title: string; count: string; preview?: string; children: ReactNode }) {
  const key = sidebarExpansionKey(scope, 'team-panel', id)
  const open = useSidebarExpansionStore(state => state.expanded[key] ?? false)
  const toggle = useSidebarExpansionStore(state => state.toggle)
  return <div role="listitem">
    <button type="button" aria-expanded={open} onClick={() => toggle(key, false)} className="flex w-full items-center gap-1.5 rounded-md px-1.5 py-1 text-left text-[11px] hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <span className="font-medium text-foreground">{title}</span>
      <span className="shrink-0 text-muted-foreground">{count}</span>
      {!open && preview && <span className="min-w-0 flex-1 truncate text-muted-foreground">{preview}</span>}
      <ChevronRight aria-hidden="true" className={`ml-auto h-3 w-3 shrink-0 text-muted-foreground transition-transform ${open ? 'rotate-90' : ''}`} />
    </button>
    {open && <div role="list" className="pb-1">{children}</div>}
  </div>
}

const ageOf = (at?: string) => {
  const seconds = at ? Math.max(0, Math.floor((Date.now() - new Date(at).getTime()) / 1000)) : NaN
  if (!Number.isFinite(seconds)) return ''
  return seconds < 60 ? 'now' : seconds < 3600 ? `${Math.floor(seconds / 60)}m` : seconds < 86400 ? `${Math.floor(seconds / 3600)}h` : `${Math.floor(seconds / 86400)}d`
}

type PanelAgent = { key: string; name: string; who: string; online: boolean; activity: string; line: string; mark: string; onClick: () => void }

/** The Team's agents (owner, 15 Nov mock): the Team agent, teammates' In-team agents, then the person's own,
 * collapsed. Every row and line is the server's (the Team read); an agent whose chat is not Live arrives with its
 * Doing line only, never chat text. Clicking a teammate's agent starts an ask from the person's own Main; the server
 * sends it to the Team agent while that agent's Mac is off. The left menu keeps the person's own agents. */
function TeamAgentsGroup({ projectId, scope, team }: { projectId: string; scope: string; team: TeamTabState }) {
  const [showMine, setShowMine] = useState(false)
  const view = team.view
  if (!view) return null
  const ask = (owner: string, name: string, address?: string) => {
    if (!fillAskFromMyAgentDraft(projectId, owner, name, address)) useChatStore.getState().addToast('Open your Main chat in this Crew, then try again.', 'error')
  }
  const online = new Map((view.live?.people || []).map(person => [person.user_id, person.online]))
  const rows: PanelAgent[] = []
  if (view.team_agent) {
    const home = view.team_agent
    rows.push({ key: 'team-agent', name: home.name, who: home.line, online: true, activity: '', mark: 'T', line: home.last_message ? `${ageOf(home.last_message_at)} · ${home.last_message}` : '', onClick: () => ask('the Team', 'Team', home.address) })
  }
  for (const agent of [...(view.live?.agents || []), ...(view.live?.idle || [])]) {
    const listed = view.agents.find(item => item.id === agent.id)
    const isOn = online.get(agent.owner_id) ?? false
    const text = agent.last_message || agent.doing || ''
    rows.push({ key: agent.id, name: agent.name, who: `${agent.person}${listed?.place === 'mac' ? ' · Mac' : ''}`, online: isOn, activity: agent.activity, mark: agent.person.slice(0, 1).toUpperCase(),
      line: agent.activity === 'running' ? `typing… ${text}`.trim() : agent.activity === 'waiting' ? agent.waiting || 'Waiting' : text ? `${agent.last_message_at ? `${ageOf(agent.last_message_at)} · ` : ''}${text}` : '',
      onClick: () => ask(agent.person, agent.name, listed?.address) })
  }
  const mine = view.agents.filter(agent => agent.mine)
  const mineRows: PanelAgent[] = mine.map(agent => ({ key: agent.key, name: agent.name, who: 'You', online: true, activity: agent.activity || '', mark: 'Y', line: agent.activity === 'running' ? 'Working' : '',
    onClick: () => { const tab = teamAgentChatTab(team, projectId, agent.key); if (tab) activateTab(tab.tabId) } }))
  const total = rows.length + mineRows.length
  if (!total) return null
  const onlineCount = rows.filter(row => row.online).length + mineRows.length
  const firstMine = mineRows.filter(row => row.activity === 'running').slice(0, 1)
  const restMine = mineRows.filter(row => !firstMine.includes(row))
  const row = (agent: PanelAgent) => <button key={agent.key} type="button" onClick={agent.onClick} className="flex w-full items-start gap-2 rounded-md px-1.5 py-1 text-left text-[11px] hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
    <span aria-hidden="true" className="relative mt-0.5 grid h-4 w-4 shrink-0 place-items-center rounded-full bg-muted text-[9px] font-semibold text-foreground">{agent.mark}
      <span className={`absolute -bottom-0.5 -right-0.5 h-1.5 w-1.5 rounded-full border border-background ${agent.online ? 'bg-emerald-500' : 'bg-muted-foreground/40'}`} />
    </span>
    <span className="min-w-0 flex-1">
      <span className="flex min-w-0 items-baseline gap-1.5"><span className="truncate font-medium text-foreground">{agent.name}</span>
        {agent.activity === 'running' && <span className="shrink-0 text-[10px] text-blue-600 dark:text-blue-400">Working</span>}
        {!agent.online && <span className="shrink-0 text-[10px] text-muted-foreground">Offline</span>}</span>
      <span className="block truncate text-muted-foreground">{agent.who}</span>
      {agent.line && <span className="block truncate text-muted-foreground">{agent.line}</span>}
    </span>
  </button>
  const preview = [...rows, ...mineRows].slice(0, 2).map(agent => agent.name).join(', ')
  return <TeamPanelGroup scope={scope} id="agents" title="Agents" count={`${total} · ${onlineCount} online`} preview={preview ? `${preview}…` : undefined}>
    {rows.map(row)}
    {firstMine.map(row)}
    {showMine ? restMine.map(row) : restMine.length > 0 && <button type="button" onClick={() => setShowMine(true)} className="w-full rounded-md px-1.5 py-1 text-left text-[11px] text-muted-foreground hover:bg-background/40">+ {restMine.length} more of yours</button>}
  </TeamPanelGroup>
}
