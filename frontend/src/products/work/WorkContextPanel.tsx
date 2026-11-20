import { TeamPanelHeader } from './TeamPanelHeader'
import { PendingAgentForks } from '../../components/chat/PendingAgentForks'
import type { TeamPanelView } from './TeamPanel'
import { useCallback, useEffect, useRef, useState } from 'react'
import { CalendarClock, ChevronRight, FolderSync, Globe, LayoutDashboard, Link2, ListChecks, Mail, Plus, RefreshCw, Route, Terminal, TriangleAlert, Users, Zap, type LucideIcon } from 'lucide-react'
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
import { crewFullMenuGroups, crewMenuTarget, crewProjectMenuItems, type CrewMenuItem } from './crewProjectMenu'
import type { ProductPanel } from '../productPanels'
import type { WorkWorkspaceView } from './WorkWorkspacePane'
import { openCrewAccess } from './MacSharedAgents'
import { CrewOnlineRow } from './CrewOnlineRow'
import { openCrewWorkflow } from './workflowRunNavigation'
import { openWorkspacePaneChatWithDraft } from '../../utils/workspacePaneChat'
import { useChatStore } from '../../stores/useChatStore'
import { crewDashboardsTab, teamFailure, useTeamTab, type CrewDashboardsTab } from './teamApi'
import { openTeamOnlineReport } from '../../components/workflow/teamOnlineReport'
import { captureChatIdentity, assertChatIdentityCurrent } from '../../utils/chatIdentity'
import { Avatar } from '../../components/ui/Avatar'
import { CrewTeamPanel } from './CrewTeamPanel'
import { CrewActivitySection } from './ActivitySection'
import { useTeamSetupsList } from './useTeamSetupsList'

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
  // Who can open each artifact (owner, 19 Nov mock "who can open each Artifact"): yours show the Team's faces or
  // nothing (owner, 20 Nov: names only on shared ones); a teammate also gets "Shared with you", each with who shared it. The server's list decides both.
  const [teamArtifacts, setTeamArtifacts] = useState<{ key: string; ids: Set<string>; shared: CrewDashboardsTab['items']; workspace: string }>()
  const teamArtifactsKey = JSON.stringify([teamState.target?.connection, teamState.target?.project, artifactCount])
  // The Team read updates often (presence); refetch only when the Team or the artifact count changes.
  const teamTarget = useRef(teamState.target)
  teamTarget.current = teamState.target
  useEffect(() => {
    const target = teamTarget.current
    if (!teamOn || !target) return
    let live = true
    crewDashboardsTab(target, 'all').then(tab => {
      if (!live) return
      // Match by the artifact's own ID among this Crew's list (the Mac and the server name the source Crew differently),
      // and compare the sharer with your Team identity, not this app's local account.
      const ownIds = new Set((section('dashboards')?.rows || []).map(row => row.id))
      const me = (teamState.view?.people || []).find(person => person.you)?.user_id
      const mine = (item: CrewDashboardsTab['items'][number]) => !!item.online && (item.online.source_project_id === projectId || (ownIds.has(item.online.dashboard_id) && (!item.shared_by || item.shared_by.user_id === me)))
      setTeamArtifacts({ key: teamArtifactsKey, workspace: tab.workspace,
        ids: new Set(tab.items.flatMap(item => item.online && mine(item) ? [item.online.dashboard_id] : [])),
        shared: tab.items.filter(item => item.online && !mine(item) && (!me || item.shared_by?.user_id !== me)) })
      console.info('[CREW_ARTIFACTS]', { event: 'team_list', project: projectId, team_items: tab.items.filter(item => item.online).length, yours_on_team: tab.items.filter(mine).length })
    }).catch(() => { /* rows stay unmarked until the next read */ })
    return () => { live = false }
  }, [teamOn, teamArtifactsKey, projectId])
  const teamArtifactsNow = teamOn && teamArtifacts?.key === teamArtifactsKey ? teamArtifacts : undefined
  const onTeam = teamArtifactsNow?.ids
  const sharedWithMe = teamArtifactsNow?.shared || []
  const openShared = async (item: CrewDashboardsTab['items'][number]) => {
    const identity = captureChatIdentity()
    const current = () => { try { assertChatIdentityCurrent(identity); return true } catch { return false } }
    try {
      if (!item.online || !await openTeamOnlineReport(item.workspace || teamArtifactsNow?.workspace || '', item.path, current, undefined, { ...item.online, connection: teamState.target?.connection })) throw new Error('This artifact is unavailable. Refresh and try again.')
    } catch (cause) {
      if (current()) useChatStore.getState().addToast(teamFailure(cause).message, 'error')
    }
  }
  const sharerFace = (sharer: { user_id: string; name: string }) => { const person = (teamState.view?.people || []).find(item => item.user_id === sharer.user_id); return <Avatar {...(person || {})} name={person?.name || sharer.name} connection={teamState.target?.connection} className="h-3.5 w-3.5 shrink-0 aspect-square border border-background leading-none text-[7px]" /> }
  const teamPeople = (teamState.view?.people || []).filter(person => person.note !== 'invited' && !person.you && person.note !== 'you')
  const shortName = (name: string) => name.split(/[\s@.]/)[0] || name
  const sharedNames = teamPeople.length > 0 && <span title={`Shared with ${teamPeople.map(person => person.name).join(', ')}`} className="max-w-[45%] shrink-0 truncate text-[10px] text-muted-foreground">{teamPeople.slice(0, 2).map(person => shortName(person.name)).join(', ')}{teamPeople.length > 2 ? ` +${teamPeople.length - 2}` : ''}</span>
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
  // A Watcher is a kind of schedule (owner, 19 Nov): it has no row of its own; the server counts it in Schedules.
  // Webhooks are part of the workflow (owner, 19 Nov): the Workflow line counts them and its Starts tab lists them.
  const automationKinds = [{ target: 'schedules', section: 'schedules', label: 'Schedules', icon: CalendarClock }] as const
  const allRows = (data?.sections || []).flatMap(item => item.rows)
  // The server sends an Inbox row whenever it can do inboxes, On or Off (the server decides, not a browser flag).
  const inboxRow = allRows.find(row => row.destination === 'schedules:email')
  // The collapsed row stacks one icon per kind that has something, like Connections, with one total on the last
  // (owner, 19 Nov: "just count", then keep the stack icons). Watchers count with Schedules.
  const workflowRows = section('workflow')?.rows.filter(row => row.destination === 'workflow') ?? []
  const automationChips = [
    { key: 'workflow', detailKey: 'workflow', target: 'workflow', label: 'Workflow', icon: Route, count: workflowRows.length },
    { key: 'schedules', detailKey: 'schedules:schedules', target: 'schedules', label: 'Schedules', icon: CalendarClock, count: (section('schedules')?.rows.length ?? 0) + (section('watchers')?.rows.length ?? 0) },
    { key: 'email', detailKey: 'schedules:email', target: 'email', label: 'Inbox', icon: Mail, count: inboxRow?.connected ? 1 : 0 },
  ].filter(chip => chip.count > 0)
  const automationCount = automationChips.reduce((total, chip) => total + chip.count, 0)
  // The Workflow row opens in place and lists the Crew's workflows (owner, 19 Nov); each opens its own page.
  const openWorkflow = (rowId: string) => { openCrewWorkflow(projectId, rowId.replace(/^workflow:/, '')); openID('workflow') }
  const newWorkflow = dashboardChatTarget ? () => { void openWorkspacePaneChatWithDraft({ ...dashboardChatTarget, message: 'I want to create a new workflow. Ask me what it should do, then build it.' }).catch(() => openID('workflow')) } : undefined
  const openAutomation = (target: string) => { const item = byId.get('schedules'); if (item) open({ ...item, target }) }
  const workflowMenuRow = byId.has('workflow') && (workflowRows.length === 0
    ? <Row icon={Route} detail={data?.details.workflow} onClick={() => openID('workflow')}>Workflow</Row>
    : <WorkspaceCompactGroup key={`${scope}:workflows`} title={workflowRows.length === 1 ? 'Workflow' : 'Workflows'} icon={Route} detail={workflowRows.length === 1 ? data?.details.workflow : String(workflowRows.length)}>
      {workflowRows.map(row => <Row key={row.id} icon={Route} detail={row.detail} onClick={() => openWorkflow(row.id)}>{row.label}</Row>)}
      {newWorkflow && <Row icon={Plus} onClick={newWorkflow}>New workflow</Row>}
    </WorkspaceCompactGroup>)
  // Access is one full-panel page in the Mac app and on the website (owner, 5 Nov: the same Team screens everywhere),
  // with Who can use it where the server lists it; it is never a smaller copy inside this menu.
  if (menu?.scope === scope) {
    const renderItem = (item: CrewMenuItem) => {
      if (menu.view === 'full' && item.id === 'workflow') return <div key={item.id}>{workflowMenuRow}</div>
      if (menu.view === 'full' && item.id === 'schedules') return <div key={item.id}>
        <Row icon={CalendarClock} detail={data?.details['schedules:schedules']} onClick={() => openAutomation('schedules')}>Schedules</Row>
        {inboxRow && <Row icon={Mail} detail={data?.details['schedules:email']} onClick={() => openAutomation('email')}>Inbox</Row>}
      </div>
      if (item.id === 'dashboard') return <WorkspaceDashboardMenu key={item.id} documents={documents} createTarget={dashboardChatTarget} onSelect={path => open({ ...item, reportPath: path })}/>
      if (item.id === 'browser') return <WorkspaceBrowserRow key={item.id} workspacePath={workspacePath} profileId={profileId} status={browser} onClick={() => open(item)}/>
      const connections = menu.view === 'integrations' ? apps.filter(row => row.destination === item.id) : []
      const status = data?.details[item.id]
      const color = connections.some(row => row.attention) ? 'text-amber-600 dark:text-amber-400' : connections.some(row => row.connected) ? 'text-emerald-600 dark:text-emerald-400' : undefined
      return <Row key={item.id} icon={item.icon} hint={connections.length && item.id !== 'mcp:connected' ? connections.map(row => row.detail).join('\n') : undefined} detail={menu.view === 'integrations' && status ? <span className={color}>{status}</span> : undefined} onClick={() => open(item)}>{item.label}</Row>
    }
    return <WorkspaceContextCard title={menu.view === 'full' ? 'Full menu' : 'Integrations'} toolsAtTop={menu.view === 'full'} onBack={() => setMenu(undefined)}>
      {menu.view === 'integrations' ? <div className="space-y-0.5">{integrations.map(renderItem)}</div> : crewFullMenuGroups(inventory).map(({ title, items }, index) =>
        <Group key={`${scope}:${title}`} title={title} count={items.length + (inboxRow && items.some(item => item.id === 'schedules') ? 1 : 0)} collapsed defaultOpen={index === 0}>
          {items.map(renderItem)}
        </Group>
      )}
    </WorkspaceContextCard>
  }
  const changes = reviewSessionId && allowed('files') ? <ChatChangesRow key={`${scope}:${reviewSessionId}`} section={section('changes')} storageKey={`agentworks.changes-panel.${workspacePath}`} onOpen={(repoRoot, changed) => { if (useAuthStore.getState().user?.id !== user?.id || useWorkspaceConnectionStore.getState().activeWorkspaceId !== server) return; setWorkspaceViewTarget('files', repoRoot === undefined ? 'review-uncommitted' : changed === false ? `folder:${encodeURIComponent(repoRoot)}` : `review-uncommitted:${encodeURIComponent(repoRoot)}`); onOpen('files') }}/> : null
  const theirs = access?.role === 'member' || access?.role === 'shared'
  const macElsewhere = data?.mac_elsewhere === true
  // Team and Online (owner, 11 Nov: the Share row split in two): Team says Off or how many people, Online where the
  // Crew lives (its state icon); the server words both.
  const accessRow = (item: CrewMenuItem) => item.id === 'mcp:online'
    ? <CrewOnlineRow key={item.id} state={computerState} theirs={theirs} onOpen={() => open(item)} />
    : item.id === 'mcp:team' && teamState.view?.team.on ? <div key={item.id}><TeamPanelHeader onName={()=>openCrewAccess(projectId,'','team')} view={teamState.view} connection={teamState.target?.connection} selectedPerson={selectedPerson} onPersonSelect={selectPerson} expanded={showTeam} onToggle={()=>{saveCollapsed(!collapsed);setTeamPerson({scope});setTeamPane({scope,view:'live'})}} needsCount={needs?.key===collapseKey?needs.count:0}/><div className="pl-6"><PendingAgentForks state={teamState} /></div></div>
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
    if (line.kind === 'dashboard' && (line.dashboards?.length || sharedWithMe.length)) {
      const own = line.dashboards || []
      const sharedCount = onTeam ? own.filter(item => item.id && onTeam.has(item.id)).length : 0
      const dashboard = byId.get('dashboard')
      return <div key={`${line.kind}:${index}`} role="listitem">
        {/* Owner, 15 Nov: the label opens the Artifacts pane like the other rows; only the caret, at the right end
            as elsewhere in this panel, opens or closes the list. */}
        <div className="flex items-center">
        <button type="button" onClick={() => { const artifacts = byId.get('mcp:artifacts'); if (artifacts) open(artifacts); else openLine(line) }} className={`${lineButton} min-w-0 flex-1 items-center`}>
          <span className="font-medium text-foreground">{line.title}</span>
          {line.count ? <span className="text-muted-foreground">{line.count}</span> : null}
          {sharedCount > 0 && <span className="text-muted-foreground">· {sharedCount} shared</span>}
          {sharedWithMe.length > 0 && <span className="text-muted-foreground">· {sharedWithMe.length} shared with you</span>}
          {/* Closed: who the Team ones are shared with, stacked (#2993). */}
          {!dashboardsOpen && sharedCount > 0 && <span className="ml-auto">{sharedWith('header')}</span>}
        </button>
        <button type="button" aria-label={dashboardsOpen ? 'Hide artifacts' : 'Show artifacts'} aria-expanded={dashboardsOpen} onClick={toggleDashboards} className="flex h-8 w-6 shrink-0 items-center justify-center rounded text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
          <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${dashboardsOpen ? 'rotate-90' : ''}`} />
        </button>
        </div>
        {dashboardsOpen && <div className="ml-3 border-l border-border pl-1.5">
          {sharedWithMe.length > 0 && <>
            <p className="px-1.5 pb-0.5 pt-1 text-[10px] uppercase tracking-wide text-muted-foreground">Shared with you</p>
            {sharedWithMe.map(item => <button key={`shared:${item.id}`} type="button" title={`${item.title} · shared with you by ${item.shared_by?.name || 'a teammate'}`} onClick={() => { void openShared(item) }} className={`${lineButton} items-center`}>
              <span aria-hidden="true" className="h-1.5 w-1.5 shrink-0 rounded-sm bg-muted-foreground/60" />
              <span className="min-w-0 flex-1 truncate text-foreground">{item.title}</span>
              {item.shared_by && <span className="flex shrink-0 items-center gap-1 text-[10px] text-muted-foreground">from {sharerFace(item.shared_by)}</span>}
            </button>)}
            {own.length > 0 && <p className="px-1.5 pb-0.5 pt-1.5 text-[10px] uppercase tracking-wide text-muted-foreground">Yours</p>}
          </>}
          {dashboard && own.map(item => <button key={item.path} type="button" title={item.id && onTeam?.has(item.id) ? `${item.name} · on the Team` : `${item.name} · only you`} onClick={() => open({ ...dashboard, reportPath: item.path })} className={`${lineButton} items-center`}>
            <span aria-hidden="true" className="h-1.5 w-1.5 shrink-0 rounded-sm bg-muted-foreground/60" />
            <span className="min-w-0 flex-1 truncate text-foreground">{item.name}</span>
            {item.id && onTeam?.has(item.id) && sharedNames}
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
  const activitySection = projectId && !selectedPerson && <CrewActivitySection projectId={projectId} team={teamState} onNeedsCount={onNeedsCount}/>
  if(teamView!=='live' && teamState.view?.team.on) return <WorkspaceContextCard><CrewTeamPanel projectId={projectId} scope={scope} team={teamState} panelView={teamView} onPanelView={openTeamPane} askAgent={askAgent}/></WorkspaceContextCard>
  return <WorkspaceContextCard onFullMenu={() => showMenu('full')} footerContent={accessItems.length > 0 && <div className="space-y-0.5">{accessItems.filter(item => item.id !== 'mcp:artifacts' || artifactLines.some(line => (line.dashboards?.length || 0) > 0) || (sharedWithMe.length > 0 && artifactLines.length > 0)).map(item => <div key={item.id}>{item.id === 'mcp:artifacts' && artifactLines.length ? treeLines(artifactLines) : accessRow(item)}{item.id === 'mcp:team' ? teamTree : null}{item.id === 'mcp:team' && !selectedPerson && setups.data?.setups.length ? <Row icon={ListChecks} detail={setups.data.to_do ? `${setups.data.to_do} to do` : 'All done'} onClick={() => { console.info('[TEAM_SETUP]', { event: 'open', project: projectId }); openCrewAccess(projectId, '', 'team', 'setups') }}>Team kit</Row> : null}{item.id === 'mcp:team' ? activitySection : null}</div>)}</div>}>
    {macElsewhere ? <div role="note" className="space-y-1.5 px-1 py-1 text-xs text-muted-foreground">
      <p>This Crew lives on your Mac. Its changes, connections and automations are set on your Mac.</p>
      <p>Artifacts work once this Crew is online. Move it online from your Mac.</p>
    </div> : <>
    {changes}
    {/* One click to the terminal (owner, 20 Nov); it was only in the full menu under Setup. */}
    {allowed('shell') && <Row icon={Terminal} hint="Open a terminal in this project folder (Ctrl+`)" detail={<kbd className="font-sans text-[10px]">⌃`</kbd>} onClick={() => onOpen('shell')}>Terminal</Row>}
    {/* Artifacts has its own top-level footer row (owner, 14 Nov); Run mode keeps this menu. */}
    {byId.has('dashboard') && accessItems.length === 0 && <WorkspaceDashboardMenu key={`${scope}:dashboards`} documents={documents} createTarget={dashboardChatTarget} onSelect={path => { const dashboard = byId.get('dashboard'); if (dashboard) open({ ...dashboard, reportPath: path }) }}/>}
    <WorkspaceConnectionsMenu key={scope} browser={browser} details={data?.details || {}} browserAllowed={byId.has('browser')} mcpAllowed={byId.has('mcp:connected')} apps={apps.filter(item => byId.has(item.destination!))} loading={!section('connections')} unavailable={!!section('connections')?.error} onOpen={openID} onManage={integrations.length ? () => showMenu('integrations') : undefined}/>
    {(byId.has('workflow') || byId.has('schedules')) && <TooltipProvider delayDuration={200}><WorkspaceCompactGroup key={`${scope}:automations`} title="Automations" icon={Zap}
      detail={automationChips.length ? undefined : data?.details.automations}
      preview={automationChips.length ? <div className="isolate flex -space-x-1.5">{automationChips.map((chip, i) =>
        <ConnectionIcon key={chip.key} icon={chip.icon} hint={`${chip.label} · ${data?.details[chip.detailKey] ?? chip.count}`} count={i === automationChips.length - 1 ? automationCount : undefined} onClick={() => chip.target === 'workflow' ? openID('workflow') : openAutomation(chip.target)} />)}</div> : undefined}>
      {/* Everything that sets work off, inside one row like Connections (owner, 8 Nov): the workflow, then its starts,
          each with the server's one-line summary. */}
      {workflowMenuRow}
      {byId.has('schedules') && <>
        {automationKinds.filter(kind => section(kind.section)).map(({ target, label, icon: Icon }) =>
          <Row key={target} icon={Icon} detail={data?.details[`schedules:${target}`]} onClick={() => openAutomation(target)}>{label}</Row>)}
        {inboxRow && <Row icon={Mail} detail={data?.details['schedules:email']} onClick={() => openAutomation('email')}>Inbox</Row>}
      </>}
    </WorkspaceCompactGroup></TooltipProvider>}
    </>}
    {error ? <div role="status" className="space-y-1 px-1 text-xs text-muted-foreground"><p>{error}</p><button type="button" onClick={refresh} className="inline-flex items-center gap-1 py-1 hover:text-foreground"><RefreshCw className="h-3 w-3"/>Retry</button></div> : null}
  </WorkspaceContextCard>
}
