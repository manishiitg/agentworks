import { TeamPanelHeader } from './TeamPanelHeader'
import type { TeamPanelView } from './TeamPanel'
import { useCallback, useEffect, useRef, useState } from 'react'
import { CalendarClock, ChevronRight, Eye, FolderSync, Globe, LayoutDashboard, Link2, ListChecks, Mail, RefreshCw, Route, TriangleAlert, Users, Webhook, Zap, type LucideIcon } from 'lucide-react'
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
import { crewDashboardsTab, useTeamTab } from './teamApi'
import { Avatar } from '../../components/ui/Avatar'
import { CrewTeamPanel } from './CrewTeamPanel'
import { CrewActivitySection } from './ActivitySection'
import { useTeamSetupsList } from './useTeamSetupsList'
import { FeatureMenuBlock } from './WorkFeaturePanel'

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
  // Teammates' shared agents, from the same Team read every screen shares (teamApi watchTeamTab).
  const teamState = useTeamTab(projectId || undefined)
  const setups = useTeamSetupsList(teamState.target, projectId, !!teamState.target && !!teamState.view?.team.on && !teamState.error)
  const collapseKey=JSON.stringify(['agentworks.team-collapsed',user?.id,server,teamState.target?.connection,teamState.target?.project||projectId])
  const readCollapsed=()=>{try{return localStorage.getItem(collapseKey)==='1'}catch{return false}}
  const [collapse,setCollapse]=useState<{key:string;value:boolean}>(()=>({key:collapseKey,value:readCollapsed()}))
  const collapsed=collapse.key===collapseKey?collapse.value:readCollapsed()
  const showTeam=!teamState.view?.team.on||!collapsed
  const saveCollapsed=(value:boolean)=>{setCollapse({key:collapseKey,value});try{localStorage.setItem(collapseKey,value?'1':'0')}catch{/* Optional view preference. */}}
  const [needs,setNeeds]=useState<{key:string;count:number}>()
  const onNeedsCount=useCallback((count:number)=>setNeeds(current=>current?.key===collapseKey&&current.count===count?current:{key:collapseKey,count}),[collapseKey])
  const [teamPane,setTeamPane]=useState<{scope:string;view:TeamPanelView;agent?:string}>()
  const teamView=teamPane?.scope===scope?teamPane.view:'live'
  const [teamPerson,setTeamPerson]=useState<{scope:string;id?:string}>()
  const selectedPerson=teamPerson?.scope===scope?teamPerson.id:undefined
  const selectPerson=(id?:string)=>{console.info('[TEAM_VIEW]',{project:projectId,event:'person_selected',person:id||'',outcome:id?'opened':'closed'});saveCollapsed(false);setTeamPerson({scope,id});setTeamPane({scope,view:'live'})}
  // The Ask view names its agent here, so the panel that shows it (the card, not the menu row) knows which one (#3084).
  const openTeamPane=(view:TeamPanelView,agent?:string)=>setTeamPane({scope,view,agent})
  const askAgent=teamPane?.scope===scope?teamPane.agent:undefined
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
  // Which artifacts are on the Team (owner, 19 Nov, #2993): the Team's own dashboards list marks them with an online
  // selector naming the artifact's ID; each such row shows who it is shared with, the Team's people.
  const teamOn = !!teamState.view?.team.on && !teamState.error
  const artifactCount = (section('dashboards')?.rows || []).length
  const [teamArtifacts, setTeamArtifacts] = useState<{ key: string; ids: Set<string> }>()
  const teamArtifactsKey = JSON.stringify([teamState.target?.connection, teamState.target?.project, artifactCount])
  // The Team read updates often (presence); refetch only when the Team or the artifact count changes.
  const teamTarget = useRef(teamState.target)
  teamTarget.current = teamState.target
  useEffect(() => {
    const target = teamTarget.current
    if (!teamOn || !target) return
    let live = true
    crewDashboardsTab(target, 'all').then(tab => {
      if (live) setTeamArtifacts({ key: teamArtifactsKey, ids: new Set(tab.items.flatMap(item => item.online ? [item.online.dashboard_id] : [])) })
    }).catch(() => { /* rows stay unmarked until the next read */ })
    return () => { live = false }
  }, [teamOn, teamArtifactsKey])
  const onTeam = teamOn && teamArtifacts?.key === teamArtifactsKey ? teamArtifacts.ids : undefined
  const teamPeople = (teamState.view?.people || []).filter(person => person.note !== 'invited' && !person.you && person.note !== 'you')
  const sharedWith = (size: 'row' | 'header') => teamPeople.length > 0 && <span role="img" aria-label={`On the Team · shared with ${teamPeople.map(person => person.name).join(', ')}`} title={`On the Team · ${teamPeople.map(person => person.name).join(', ')}`} className="inline-flex shrink-0 items-center -space-x-1">
    {teamPeople.slice(0, size === 'row' ? 2 : 3).map(person => <Avatar key={person.user_id} {...person} name={person.name} connection={teamState.target?.connection} className="h-3.5 w-3.5 shrink-0 aspect-square border border-background leading-none text-[7px]" />)}
    {teamPeople.length > (size === 'row' ? 2 : 3) && <span className="pl-1.5 text-[10px] text-muted-foreground">+{teamPeople.length - (size === 'row' ? 2 : 3)}</span>}
  </span>
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
    : item.id === 'mcp:team' && teamState.view?.team.on ? <TeamPanelHeader key={item.id} onName={()=>openCrewAccess(projectId,'','team')} view={teamState.view} connection={teamState.target?.connection} selectedPerson={selectedPerson} onPersonSelect={selectPerson} expanded={showTeam} onToggle={()=>{saveCollapsed(!collapsed);setTeamPerson({scope});setTeamPane({scope,view:'live'})}} needsCount={needs?.key===collapseKey?needs.count:0}/>
    : item.id === 'mcp:team' && !teamState.error && !teamLines.length ? <div key={item.id}>
      <Row icon={item.icon} onClick={() => open(item)}>{accessTree.find(line=>line.kind==='team')?.title||item.label}</Row>
      {/* No Team yet (owner, 18 Nov): say what a Team is for and offer the one next step. */}
      <div className="flex items-center gap-2 pb-1 pl-7 pr-1 text-[10px] text-muted-foreground"><span className="min-w-0 flex-1">Work with teammates and their agents</span><button type="button" onClick={() => open(item)} className="shrink-0 rounded-full border border-border px-2 py-0.5 text-[10px] font-medium text-primary hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">Start a Team</button></div>
    </div>
    : <Row key={item.id} icon={item.icon} onClick={() => open(item)}>{item.id==='mcp:team'?accessTree.find(line=>line.kind==='team')?.title||item.label:item.label}</Row>
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
          {/* Closed: who the Team ones are shared with, stacked (#2993). */}
          {!dashboardsOpen && line.dashboards.some(item => item.id && onTeam?.has(item.id)) && <span className="ml-auto">{sharedWith('header')}</span>}
        </button>
        <button type="button" aria-label={dashboardsOpen ? 'Hide artifacts' : 'Show artifacts'} aria-expanded={dashboardsOpen} onClick={toggleDashboards} className="flex h-8 w-6 shrink-0 items-center justify-center rounded text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
          <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${dashboardsOpen ? 'rotate-90' : ''}`} />
        </button>
        </div>
        {dashboardsOpen && <div className="ml-3 border-l border-border pl-1.5">
          {dashboard && line.dashboards.map(item => <button key={item.path} type="button" title={item.name} onClick={() => open({ ...dashboard, reportPath: item.path })} className={`${lineButton} group/dash items-center`}>
            <span aria-hidden="true" className="h-1.5 w-1.5 shrink-0 rounded-sm bg-muted-foreground/60" />
            {item.id && onTeam?.has(item.id) && sharedWith('row')}
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
  const teamLines = accessTree.filter(line => line.group === 'team' && line.kind !== 'team' && line.kind !== 'team_live')
  const artifactLines = accessTree.filter(line => line.kind === 'dashboard')
  const teamTree = (teamLines.length > 0 || teamState.view?.team.on || !!teamState.error) && <div role="list" aria-label="Team details" className={showTeam?'pb-1':undefined}><CrewTeamPanel projectId={projectId} scope={scope} team={teamState} collapsed={!showTeam} selectedPerson={selectedPerson} onPersonSelect={selectPerson} onPanelView={openTeamPane} askAgent={askAgent} />{!teamState.view?.team.on&&treeLines(teamLines)}</div>
  // Activity is its own section, not part of the Team (owner, 16 Nov): with the Team off it shows your own agents.
  const activitySection = projectId && !selectedPerson && <CrewActivitySection projectId={projectId} scope={scope} team={teamState} onNeedsCount={onNeedsCount}/>
  if(teamView!=='live' && teamState.view?.team.on) return <WorkspaceContextCard><CrewTeamPanel projectId={projectId} scope={scope} team={teamState} panelView={teamView} onPanelView={openTeamPane} askAgent={askAgent}/></WorkspaceContextCard>
  return <WorkspaceContextCard onFullMenu={() => showMenu('full')} footerContent={accessItems.length > 0 && <div className="space-y-0.5">{accessItems.filter(item => item.id !== 'mcp:artifacts' || artifactLines.some(line => (line.dashboards?.length || 0) > 0)).map(item => <div key={item.id}>{item.id === 'mcp:artifacts' && artifactLines.length ? treeLines(artifactLines) : accessRow(item)}{item.id === 'mcp:team' ? teamTree : null}{item.id === 'mcp:team' && !selectedPerson && setups.data?.setups.length ? <Row icon={ListChecks} detail={setups.data.to_do ? `${setups.data.to_do} to do` : 'All done'} onClick={() => { console.info('[TEAM_SETUP]', { event: 'open', project: projectId }); openCrewAccess(projectId, '', 'team', 'setups') }}>Team kit</Row> : null}{item.id === 'mcp:team' ? activitySection : null}</div>)}</div>}>
    {macElsewhere ? <div role="note" className="space-y-1.5 px-1 py-1 text-xs text-muted-foreground">
      <p>This Crew lives on your Mac. Its changes, connections and automations are set on your Mac.</p>
      <p>Artifacts work once this Crew is online. Move it online from your Mac.</p>
    </div> : <>
    {/* The open agent's feature, or its own tasks, first (#3210; owner, 19 Nov). */}
    {projectId && !selectedPerson && <FeatureMenuBlock projectId={projectId} onOpen={() => onOpen('work')} />}
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
