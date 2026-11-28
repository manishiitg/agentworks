import { useCallback, useContext, useEffect, useState } from 'react'
import { Activity, CalendarClock, Mail, Plus, RefreshCw, Route, Zap } from 'lucide-react'
import { WorkspacePanelVisibilityContext } from '../../components/workspace/WorkspacePanelToggle'
import { useCrewPanel } from './useCrewPanel'
import { ChatChangesRow } from '../../components/workspace/ChatChangesRow'
import { ChatContextSection } from '../../components/workspace/ChatContextSection'
import { WorkspaceContextCard, WorkspaceContextRow as Row } from '../../components/workspace/WorkspaceContextCard'
import { WorkspaceDashboardMenu } from '../../components/workspace/WorkspaceDashboardMenu'
import { WorkspaceConnectionsMenu, ConnectionIcon } from '../../components/workspace/WorkspaceConnectionsMenu'
import { TooltipProvider } from '../../components/ui/tooltip'
import { WorkspaceCompactGroup } from '../../components/workspace/WorkspaceCompactGroup'
import type { DashboardChatTarget } from '../../components/workflow/DashboardEmptyState'
import { setWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { useAuthStore } from '../../stores/useAuthStore'
import { useChatStore } from '../../stores/useChatStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { projectMenuHistoryScope } from '../../stores/useProjectMenuHistoryStore'
import { crewProjectMenuItems, type CrewMenuItem } from './crewProjectMenu'
import { findAdmittedCrewMenuItem, isSameRightPanelScope, navigateCrewMenuItem } from './crewMenuNavigation'
import type { ProductPanel } from '../productPanels'
import type { WorkWorkspaceView } from './WorkWorkspacePane'
import type { TeamPanelView } from './TeamPanel'
import { openCrewWorkflow } from './workflowRunNavigation'
import { openWorkspacePaneChatWithDraft, sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { useTeamTab } from './teamApi'
import { CrewTeamPanel } from './CrewTeamPanel'
import { CrewActivitySection } from './ActivitySection'
import { useTeamSetupsList } from './useTeamSetupsList'
import { ProjectActivityRows, projectActivityLine, projectActivitySetupMessage } from './ProjectActivityRows'
import { ProjectTaskPage } from './ProjectTasksRow'
import { openCrewAccess } from './MacSharedAgents'
import { useSetupPromptHidden } from '../../components/workspace/SetupPrompt'
import { WorkContextSubmenu } from './WorkContextSubmenu'
import { crewTeamTitle, TEAM_SETUP_LINE, WorkContextAccessFooter } from './WorkContextAccessFooter'

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
  const panelVisibility = useContext(WorkspacePanelVisibilityContext)
  const panelVisible = panelVisibility ? panelVisibility.open : true
  const user = useAuthStore(state => state.user)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = projectMenuHistoryScope(server || 'local', user?.id || 'pending', profileId, projectId || workspacePath)
  const allowed = (view: WorkWorkspaceView) => panels.some(item => item.id === view)
  const { data, error, refresh } = useCrewPanel(scope, projectId, workspacePath, panelVisible)
  // Teammates' shared agents, from the same Team read every screen shares (teamApi watchTeamTab).
  const teamState = useTeamTab(panelVisible ? (projectId || undefined) : undefined)
  const setups = useTeamSetupsList(teamState.target, projectId, panelVisible && !!teamState.target && !!teamState.view?.team.on && !teamState.error)

  const collapseKey = JSON.stringify(['agentworks.team-collapsed', user?.id, server, teamState.target?.connection, teamState.target?.project || projectId])
  const readCollapsed = () => { try { return localStorage.getItem(collapseKey) === '1' } catch { return false } }
  const [collapse, setCollapse] = useState<{ key: string; value: boolean }>(() => ({ key: collapseKey, value: readCollapsed() }))
  const collapsed = collapse.key === collapseKey ? collapse.value : readCollapsed()
  const showTeam = !teamState.view?.team.on || !collapsed
  const saveCollapsed = (value: boolean) => {
    setCollapse({ key: collapseKey, value })
    try { localStorage.setItem(collapseKey, value ? '1' : '0') } catch { /* Optional view preference. */ }
  }

  const [needs, setNeeds] = useState<{ key: string; count: number }>()
  const onNeedsCount = useCallback((count: number) => setNeeds(current => current?.key === collapseKey && current.count === count ? current : { key: collapseKey, count }), [collapseKey])
  const [teamPane, setTeamPane] = useState<{ scope: string; view: TeamPanelView; agent?: string }>()
  const [taskPage, setTaskPage] = useState<{ scope: string; id: string }>()
  const teamView = teamPane?.scope === scope ? teamPane.view : 'live'
  const [teamPerson, setTeamPerson] = useState<{ scope: string; id?: string }>()
  const selectedPerson = teamPerson?.scope === scope ? teamPerson.id : undefined
  const selectPerson = (id?: string) => {
    console.info('[TEAM_VIEW]', { project: projectId, event: 'person_selected', person: id || '', outcome: id ? 'opened' : 'closed' })
    saveCollapsed(false)
    setTeamPerson({ scope, id })
    setTeamPane({ scope, view: 'live' })
  }
  // The Ask view names its agent here, so the panel that shows it (the card, not the menu row) knows which one (#3084).
  const openTeamPane = (view: TeamPanelView, agent?: string) => setTeamPane({ scope, view, agent })
  const askAgent = teamPane?.scope === scope ? teamPane.agent : undefined

  const section = (id: string) => data?.sections.find(item => item.id === id)
  const access = section('access')?.access
  const accessTree = access?.tree || []
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
  const [activityHidden, hideActivity] = useSetupPromptHidden(`agentworks.project-activity-hidden:${projectId}`)
  const [teamHidden, hideTeam] = useSetupPromptHidden(`agentworks.team-prompt-hidden:${projectId}`)
  const [remoteHidden, hideRemote, showRemote] = useSetupPromptHidden(`agentworks.remote-control-hidden:${projectId}`)
  const [bridgeHidden, hideBridge, showBridge] = useSetupPromptHidden(`agentworks.agentbridge-hidden:${projectId}`)
  const addToast = useChatStore(state => state.addToast)

  const showMenu = (view: 'full' | 'integrations') => {
    if (!isSameRightPanelScope(user?.id, server)) return
    setMenu({ scope, view })
  }
  const open = (item: CrewMenuItem) => {
    // Recheck account/product admission if access changed while the menu was open.
    const admitted = findAdmittedCrewMenuItem(item.id, { ...menuOptions, userId: user?.id, serverId: server })
    if (!admitted) return
    navigateCrewMenuItem({ ...admitted, target: item.target ?? admitted.target, reportPath: item.reportPath ?? admitted.reportPath }, {
      projectId,
      workspacePath,
      onOpen,
      directTeamAccess: true,
    })
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
  // An online row lives on the Team, not on this Mac: it opens the workflows page, where it is listed.
  const openWorkflow = (rowId: string) => { if (rowId.startsWith('workflow:')) openCrewWorkflow(projectId, rowId.replace(/^workflow:/, '')); openID('workflow') }
  const newWorkflow = dashboardChatTarget ? () => { void openWorkspacePaneChatWithDraft({ ...dashboardChatTarget, message: 'I want to create a new workflow. Ask me what it should do, then build it.' }).catch(() => openID('workflow')) } : undefined
  const openAutomation = (target: string) => { const item = byId.get('schedules'); if (item) open({ ...item, target }) }
  const activityAgent = dashboardChatTarget ? (message: string) => { void openWorkspacePaneChatWithDraft({ ...dashboardChatTarget, message }).catch(() => openAutomation('conditions')) } : undefined
  const workflowMenuRow = byId.has('workflow') && (workflowRows.length === 0
    ? <Row icon={Route} detail={data?.details.workflow} onClick={() => openID('workflow')}>Workflow</Row>
    : <WorkspaceCompactGroup key={`${scope}:workflows`} title="Workflows" icon={Route} detail={String(workflowRows.length)}>
      {workflowRows.map(row => <Row key={row.id} icon={Route} detail={row.detail} onClick={() => openWorkflow(row.id)}>{row.label}</Row>)}
      {newWorkflow && <Row icon={Plus} onClick={newWorkflow}>New workflow</Row>}
    </WorkspaceCompactGroup>)

  // Access is one full-panel page in the Mac app and on the website (owner, 5 Nov: the same Team screens everywhere),
  // with Who can use it where the server lists it; it is never a smaller copy inside this menu.
  if (menu?.scope === scope) {
    return <WorkContextSubmenu
      view={menu.view}
      scope={scope}
      workspacePath={workspacePath}
      profileId={profileId}
      inventory={inventory}
      integrations={integrations}
      apps={apps}
      details={data?.details}
      documents={documents}
      browser={browser}
      dashboardChatTarget={dashboardChatTarget}
      workflowMenuRow={workflowMenuRow}
      hasInboxRow={Boolean(inboxRow)}
      onOpen={open}
      onOpenAutomation={openAutomation}
      onBack={() => setMenu(undefined)}
    />
  }

  // A Project task opens its own page in the side panel (#3788), like a Team person.
  const tasksRow = section('activity')?.rows.find(row => row.group === 'project-tasks')
  const openTask = taskPage?.scope === scope ? tasksRow?.tasks?.find(task => task.id === taskPage.id) : undefined
  if (tasksRow && openTask) return <WorkspaceContextCard title="Project tasks" onBack={() => setTaskPage(undefined)}><ProjectTaskPage row={tasksRow} task={openTask} onAgent={activityAgent}/></WorkspaceContextCard>
  if (teamView !== 'live' && teamState.view?.team.on) return <WorkspaceContextCard><CrewTeamPanel projectId={projectId} scope={scope} team={teamState} panelView={teamView} onPanelView={openTeamPane} askAgent={askAgent}/></WorkspaceContextCard>

  const teamLines = accessTree.filter(line => line.group === 'team' && line.kind !== 'team' && line.kind !== 'team_live')
  const noTeamYet = !teamState.view?.team.on && !access?.co_owners && !teamState.error && !teamLines.length
  const teamItem = accessItems.find(item => item.id === 'mcp:team')
  const remoteItem = accessItems.find(item => item.id === 'mcp:online')
  const bridgeMenuItem = accessItems.find(item => item.id === 'mcp:agentbridge')
  const githubRow = section('github')?.rows[0]
  const changesStorageKey = JSON.stringify(['agentworks.changes-panel', server || 'local', user?.id || 'pending', workspacePath])
  const changes = reviewSessionId && allowed('files') ? <ChatChangesRow
    key={`${scope}:${reviewSessionId}`}
    workspacePath={workspacePath}
    onAskAgent={dashboardChatTarget ? message => sendWorkspacePaneMessageToChat({ ...dashboardChatTarget, message }).then(() => undefined) : undefined}
    section={section('changes')}
    storageKey={changesStorageKey}
    onTerminal={allowed('shell') ? () => onOpen('shell') : undefined}
    github={byId.has('mcp:github') && githubRow ? { connected: githubRow.connected === true, login: githubRow.login, orgs: githubRow.orgs, onOpen: () => openID('mcp:github') } : undefined}
    onOpen={(repoRoot, changed) => {
      if (!isSameRightPanelScope(user?.id, server)) return
      setWorkspaceViewTarget('files', repoRoot === undefined ? 'review-uncommitted' : changed === false ? `folder:${encodeURIComponent(repoRoot)}` : `review-uncommitted:${encodeURIComponent(repoRoot)}`)
      onOpen('files')
    }}
  /> : null
  const macElsewhere = data?.mac_elsewhere === true
  // Needs you sits at the very top of the panel, only while something waits (owner, 23 Nov).
  const activitySection = projectId && !selectedPerson && <CrewActivitySection projectId={projectId} team={teamState} enabled={panelVisible} onNeedsCount={onNeedsCount} onAsk={dashboardChatTarget ? message => { void openWorkspacePaneChatWithDraft({ ...dashboardChatTarget, message }).catch(() => undefined) } : undefined}/>

  // This chat's context, cost and plan use sit at the very end, after Team and Artifacts (owner, 22 Nov); the header shows the short line.
  return <WorkspaceContextCard
    onFullMenu={() => showMenu('full')}
    endContent={!macElsewhere && reviewSessionId ? <ChatContextSection key={`${scope}:context:${reviewSessionId}`} sessionId={reviewSessionId} enabled={panelVisible} /> : undefined}
    footerContent={accessItems.length > 0 && <WorkContextAccessFooter
      projectId={projectId}
      scope={scope}
      access={access}
      accessItems={accessItems}
      byId={byId}
      teamState={teamState}
      selectedPerson={selectedPerson}
      showTeam={showTeam}
      needsCount={needs?.key === collapseKey ? needs.count : 0}
      askAgent={askAgent}
      noTeamYet={noTeamYet}
      teamHidden={teamHidden}
      remoteHidden={remoteHidden}
      bridgeHidden={bridgeHidden}
      dashboardsOpen={dashboardsOpen}
      setupsCount={setups.data?.setups.length ?? 0}
      setupsToDo={setups.data?.to_do}
      onOpen={open}
      onSelectPerson={selectPerson}
      onToggleTeamCollapsed={() => { saveCollapsed(!collapsed); setTeamPerson({ scope }); setTeamPane({ scope, view: 'live' }) }}
      onOpenTeamPane={openTeamPane}
      onHideTeam={() => { hideTeam(); addToast('Team hidden. Start one any time from Connections.', 'info') }}
      onHideRemote={() => { hideRemote(); addToast('Remote Control hidden. Find it in Connections.', 'info') }}
      onHideBridge={() => { hideBridge(); addToast('AgentBridge hidden. Find it in Connections.', 'info') }}
      onToggleDashboards={toggleDashboards}
    />}
  >
    {activitySection}
    {macElsewhere ? <div role="note" className="space-y-1.5 px-1 py-1 text-xs text-muted-foreground">
      <p>This project lives on your Mac. Its changes, connections and automations are set on your Mac.</p>
      <p>Artifacts work in projects on the server.</p>
    </div> : <>
      {changes}
      {/* Project activity sits right under Git (owner, 21 Nov, #3476). */}
      <ProjectActivityRows key={`${scope}:activity`} section={section('activity')} onAgent={activityAgent} onWatchers={() => openAutomation('conditions')} onOpenTask={id => setTaskPage({ scope, id })} onOpenTasks={() => openCrewAccess(projectId, '', 'tasks')} tasksStorageKey={`agentworks.task-groups-folded:${projectId || workspacePath}`} hidden={activityHidden} onHide={() => { hideActivity(); addToast('Project activity hidden. Set it up any time from Connections.', 'info') }}/>
      {/* Artifacts has its own top-level footer row (owner, 14 Nov); Run mode keeps this menu. */}
      {byId.has('dashboard') && accessItems.length === 0 && <WorkspaceDashboardMenu key={`${scope}:dashboards`} documents={documents} createTarget={dashboardChatTarget} onSelect={path => { const dashboard = byId.get('dashboard'); if (dashboard) open({ ...dashboard, reportPath: path }) }}/>}
      <WorkspaceConnectionsMenu key={scope} browser={browser} details={data?.details || {}} browserAllowed={byId.has('browser')} mcpAllowed={byId.has('mcp:connected')} apps={apps.filter(item => byId.has(item.destination!))} loading={!section('connections')} unavailable={!!section('connections')?.error} onOpen={openID} onManage={integrations.length ? () => showMenu('integrations') : undefined} setups={[
        ...(activityHidden && activityAgent && section('activity')?.rows.length === 0 ? [{ id: 'activity', icon: Activity, title: 'Project activity', line: projectActivityLine, action: 'Set up', onClick: () => activityAgent(projectActivitySetupMessage) }] : []),
        ...(teamHidden && noTeamYet && teamItem && !access?.co_owners ? [{ id: 'team', icon: teamItem.icon, title: crewTeamTitle(accessTree, teamItem), line: TEAM_SETUP_LINE, action: 'Start a Team', onClick: () => open(teamItem) }] : []),
        ...(remoteHidden && remoteItem ? [{ id: 'remote', icon: remoteItem.icon, title: 'Remote Control', line: 'Use this project from your phone and the website', action: 'Open', onClick: () => open(remoteItem), onShow: showRemote }] : []),
        ...(bridgeHidden && bridgeMenuItem ? [{ id: 'agentbridge', icon: bridgeMenuItem.icon, title: bridgeMenuItem.label, line: 'Your agents and discovery', action: 'Open', onClick: () => open(bridgeMenuItem), onShow: showBridge }] : []),
      ]}/>
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
