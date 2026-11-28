import { ChevronRight, FolderSync, X, Globe, LayoutDashboard, Link2, ListChecks, TriangleAlert, Users, type LucideIcon } from 'lucide-react'
import { PendingAgentForks } from '../../components/chat/PendingAgentForks'
import { SetupPrompt } from '../../components/workspace/SetupPrompt'
import { WorkspaceContextRow as Row } from '../../components/workspace/WorkspaceContextCard'
import { CrewOnlineRow } from './CrewOnlineRow'
import { CrewTeamPanel } from './CrewTeamPanel'
import { openCrewAccess } from './MacSharedAgents'
import type { TeamPanelView } from './TeamPanel'
import { TeamPanelHeader } from './TeamPanelHeader'
import type { CrewMenuItem } from './crewProjectMenu'
import type { TeamTabState } from './teamApi'
import type { CrewPanelAccess } from './useCrewPanel'
import type { CrewAccessLine } from './YourComputerPanel'

export const TEAM_SETUP_LINE = 'Work with teammates and their agents'

const lineIcon: Record<CrewAccessLine['kind'], LucideIcon> = {
  mac: Globe,
  team: Users,
  team_live: Users,
  in_team: Users,
  dashboard: LayoutDashboard,
  public: Link2,
  folder: FolderSync,
  clash: TriangleAlert,
}

const lineButton = 'flex w-full items-start gap-2 rounded-md px-1.5 py-1 text-left text-[11px] hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'

export function crewTeamTitle(accessTree: CrewAccessLine[], item: CrewMenuItem): string {
  return accessTree.find(line => line.kind === 'team')?.title || item.label
}

export function WorkContextAccessFooter({
  projectId,
  scope,
  access,
  accessItems,
  byId,
  teamState,
  selectedPerson,
  showTeam,
  needsCount,
  askAgent,
  noTeamYet,
  teamHidden,
  remoteHidden,
  dashboardsOpen,
  setupsCount,
  setupsToDo,
  onOpen,
  onSelectPerson,
  onToggleTeamCollapsed,
  onOpenTeamPane,
  onHideTeam,
  onHideRemote,
  onToggleDashboards,
}: {
  projectId: string
  scope: string
  access?: CrewPanelAccess
  accessItems: CrewMenuItem[]
  byId: Map<string, CrewMenuItem>
  teamState: TeamTabState
  selectedPerson?: string
  showTeam: boolean
  needsCount: number
  askAgent?: string
  noTeamYet: boolean
  teamHidden: boolean
  remoteHidden: boolean
  dashboardsOpen: boolean
  setupsCount: number
  setupsToDo?: number
  onOpen: (item: CrewMenuItem) => void
  onSelectPerson: (id?: string) => void
  onToggleTeamCollapsed: () => void
  onOpenTeamPane: (view: TeamPanelView, agent?: string) => void
  onHideTeam: () => void
  onHideRemote: () => void
  onToggleDashboards: () => void
}) {
  if (accessItems.length === 0) return null

  const accessTree = access?.tree || []
  const computerState = access?.place
  const theirs = access?.role === 'member' || access?.role === 'shared'
  // Team keeps its details; Mac online is one row. Artifacts remains a sibling
  // with its own document list (owner, 15 Nov).
  const teamLines = accessTree.filter(line => line.group === 'team' && line.kind !== 'team' && line.kind !== 'team_live')
  const artifactLines = accessTree.filter(line => line.kind === 'dashboard')

  // Each line opens the Access page (WorkSurface listens) at its tab, or at Sync (TeamSyncPanel scrolls to it).
  const openLine = (line: CrewAccessLine) => {
    // The dashboard line (owner, 6 Nov) opens the Artifacts page, where the server lists them, or says
    // there are none and offers its Ask.
    if (line.kind === 'team_live') { openCrewAccess(projectId, '', 'team', 'activity'); return }
    if (line.kind === 'dashboard') { openCrewAccess(projectId, '', 'artifacts'); return }
    if (line.kind === 'folder' || line.kind === 'clash') openCrewAccess(projectId, '', 'team', 'sync')
    else openCrewAccess(projectId, '', line.kind === 'mac' || line.kind === 'public' ? 'mac' : 'team')
  }

  // Team things sit under Access as its sub-list (owner, 5 Nov: "put inside access only like a sub menu"): the shared
  // folder's line, then who asked; each opens Access at its place.
  const renderTreeLines = (lines: CrewAccessLine[]) => lines.map((line, index) => {
    const Icon = lineIcon[line.kind] || Globe
    // Dashboards (owner, 9 Nov mock): a header that opens and closes the list, each dashboard on one line (no
    // "Manage dashboards" link, owner 10 Nov). With none, the server's one line and its Ask, as before.
    if (line.kind === 'dashboard' && line.dashboards?.length) {
      const own = line.dashboards || []
      const dashboard = byId.get('dashboard')
      return <div key={`${line.kind}:${index}`} role="listitem">
        {/* Owner, 15 Nov: the label opens the Artifacts pane like the other rows; only the caret, at the right end
            as elsewhere in this panel, opens or closes the list. */}
        <div className="flex items-center">
          <button type="button" onClick={() => { const artifacts = byId.get('mcp:artifacts'); if (artifacts) onOpen(artifacts); else openLine(line) }} className={`${lineButton} min-w-0 flex-1 items-center`}>
            <span className="font-medium text-foreground">{line.title}</span>
            {line.count ? <span className="text-muted-foreground">{line.count}</span> : null}
          </button>
          <button type="button" aria-label={dashboardsOpen ? 'Hide artifacts' : 'Show artifacts'} aria-expanded={dashboardsOpen} onClick={onToggleDashboards} className="flex h-8 w-6 shrink-0 items-center justify-center rounded text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
            <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${dashboardsOpen ? 'rotate-90' : ''}`} />
          </button>
        </div>
        {dashboardsOpen && <div className="ml-3 border-l border-border pl-1.5">
          {dashboard && own.map(item => <button key={item.path} type="button" title={item.name} onClick={() => onOpen({ ...dashboard, reportPath: item.path })} className={`${lineButton} items-center`}>
            <span aria-hidden="true" className="h-1.5 w-1.5 shrink-0 rounded-sm bg-muted-foreground/60" />
            <span className="min-w-0 flex-1 truncate text-foreground">{item.name}</span>
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

  // Team and Online (owner, 11 Nov: the Share row split in two): Team says Off or how many people, Online where the
  // Crew lives (its state icon); the server words both.
  // Remote Control can be hidden into Connections (#4486), like Team and Project activity before setup. AgentBridge's
  // row and page are gone (#4510): each agent's menu says how other agents message it.
  const hideButton = (label: string, onHide: () => void) => <button type="button" onClick={onHide} aria-label={`Hide ${label}`} title={`Hide ${label}. It stays in Connections.`} className="flex h-5 w-4 shrink-0 items-center justify-center rounded text-muted-foreground opacity-0 hover:text-foreground focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring group-hover/hide:opacity-100"><X aria-hidden="true" className="h-3 w-3" /></button>
  const renderAccessRow = (item: CrewMenuItem) => item.id === 'mcp:online'
    ? <div key={item.id} className="group/hide flex items-center"><div className="min-w-0 flex-1"><CrewOnlineRow state={computerState} theirs={theirs} onOpen={() => onOpen(item)} /></div>{hideButton('Remote Control', onHideRemote)}</div>
    : item.id === 'mcp:team' && teamState.view?.team.on ? <div key={item.id}><TeamPanelHeader onName={() => openCrewAccess(projectId, '', 'team')} view={teamState.view} connection={teamState.target?.connection} selectedPerson={selectedPerson} onPersonSelect={onSelectPerson} expanded={showTeam} onToggle={onToggleTeamCollapsed} needsCount={needsCount} /><PendingAgentForks state={teamState} place="menu" /></div>
    : noTeamYet && item.id === 'mcp:team' ? (teamHidden ? null : <div key={item.id}>
      {/* No Team yet (owner, 18 Nov; ⓘ card and Hide into Connections, #3780). */}
      <SetupPrompt icon={item.icon} title={crewTeamTitle(accessTree, item)} line={TEAM_SETUP_LINE} action="Start a Team" onAction={() => onOpen(item)} onHide={onHideTeam}
        what="Bring teammates and their agents into this project."
        points={['Everyone works from the same project, tasks and files', 'Agents can hand work to each other and to people', 'See who is working on what, live']} />
    </div>)
    : <Row key={item.id} icon={item.icon} onClick={() => onOpen(item)}>{item.id === 'mcp:team' ? crewTeamTitle(accessTree, item) : item.label}</Row>

  const teamTree = (teamLines.length > 0 || teamState.view?.team.on || !!teamState.error) && <div role="list" aria-label="Team details" className={showTeam ? 'pb-1' : undefined}><CrewTeamPanel projectId={projectId} scope={scope} team={teamState} collapsed={!showTeam} selectedPerson={selectedPerson} onPersonSelect={onSelectPerson} onPanelView={onOpenTeamPane} askAgent={askAgent} />{!teamState.view?.team.on && renderTreeLines(teamLines)}</div>

  const visibleItems = accessItems.filter(item => (item.id !== 'mcp:artifacts' || artifactLines.some(line => (line.dashboards?.length || 0) > 0)) 
    && !(item.id === 'mcp:online' && remoteHidden))

  return <div className="space-y-0.5">
    {visibleItems.map(item => <div key={item.id}>
      {item.id === 'mcp:artifacts' && artifactLines.length ? renderTreeLines(artifactLines) : renderAccessRow(item)}
      {item.id === 'mcp:team' ? (access?.co_owners ? renderTreeLines(teamLines) : teamTree) : null}
      {item.id === 'mcp:team' && !access?.co_owners && !selectedPerson && setupsCount > 0 ? <Row icon={ListChecks} detail={setupsToDo ? `${setupsToDo} to do` : 'All done'} onClick={() => { console.info('[TEAM_SETUP]', { event: 'open', project: projectId }); openCrewAccess(projectId, '', 'team', 'setups') }}>Team kit</Row> : null}
    </div>)}
  </div>
}
