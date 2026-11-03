import { useEffect, useState } from 'react'
import { CalendarClock, ChevronRight, RefreshCw, Settings2 } from 'lucide-react'
import { useWorkspaceStatus } from './useWorkspaceStatus'
import { ChatChangesRow } from '../../components/workspace/ChatChangesRow'
import { WorkspaceContextCard, WorkspaceContextRow as Row, WorkspaceContextGroup as Group } from '../../components/workspace/WorkspaceContextCard'
import { WorkspaceDashboardMenu } from '../../components/workspace/WorkspaceDashboardMenu'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { WorkspaceCompactGroup } from '../../components/workspace/WorkspaceCompactGroup'
import { WorkspaceConnectionsMenu } from '../../components/workspace/WorkspaceConnectionsMenu'
import { WorkspaceBrowserRow } from '../../components/workspace/WorkspaceBrowserRow'
import { connectionSummary } from '../../components/workspace/workspaceConnectionStatus'
import { cloudStateIcon, cloudStateLabel, useCrewLivesOnMac, useCrewSharedAgentCount, useYourComputerSummary } from './YourComputerPanel'
import { selectReportDocument, useReportDocuments } from '../../components/workflow/reportDocuments'
import type { DashboardChatTarget } from '../../components/workflow/DashboardEmptyState'
import { setWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import api from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { projectMenuHistoryScope } from '../../stores/useProjectMenuHistoryStore'
import { useAgentInboxEnabled } from '../../components/integrations/AgentInboxPanel'
import { CREW_MENU_GROUPS, crewMenuTarget, crewProjectMenuItems, type CrewMenuItem } from './crewProjectMenu'
import type { ProductPanel } from '../productPanels'
import type { WorkWorkspaceView } from './WorkWorkspacePane'

export function WorkContextPanel({ workspacePath, projectId = '', profileId = 'work', panels, onOpen, enabledPanels, dashboardChatTarget, reviewSessionId, mode = 'cowork' }: {
  workspacePath: string
  mode?: 'dev' | 'cowork'
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
  const canReadSharing = allowed('dashboard') && allowed('mcp')
  const [sharing, setSharing] = useState<{ scope: string; allowed: boolean; owner: boolean; detail?: string } | null>(null)
  useEffect(() => {
    if (!canReadSharing) return
    const controller = new AbortController()
    void api.post<{ can_share: boolean; owner?: boolean; mode?: string; people?: { id: string }[] }>('/api/dashboards', { workspace: workspacePath, action: 'share_status' }, { signal: controller.signal })
      .then(({ data }) => {
        if (controller.signal.aborted) return
        const count = new Set((data.people || []).map(person => person.id)).size
        // Whose Crew it is comes from the server's owner flag, never from can_share (a Mac Crew blocks sharing).
        const owner = data.owner === true || data.can_share === true
        setSharing({ scope, allowed: data.can_share === true, owner, detail: !owner ? 'Shared with you' : data.mode === 'everyone' ? 'Everyone' : data.mode === 'people' && count > 0 ? `Shared with ${count}` : 'Only you' })
      })
      .catch(() => { if (!controller.signal.aborted) setSharing({ scope, allowed: false, owner: false }) })
    return () => controller.abort()
  }, [scope, workspacePath, canReadSharing])
  const { documents } = useReportDocuments(allowed('dashboard') ? workspacePath : '')
  const menuOptions = { mode, panels, enabledPanels, incomingEmail, projectId, canShare: sharing?.scope === scope && sharing.allowed }
  const inventory = crewProjectMenuItems({ ...menuOptions, allowedProducts: user?.allowed_products })
  const byId = new Map(inventory.map(item => [item.id, item]))
  const accessItems = inventory.filter(item => item.group === 'Share' && item.view === 'mcp')
  const { snapshot, refresh } = useWorkspaceStatus({ scope, workspacePath, projectId, admitted: inventory.map(item => item.id) })
  const integrations = inventory.filter(item => item.id === 'browser' || item.group === 'Integrations' || (item.view === 'mcp' && item.group === 'Use'))
  const automationItems = inventory.filter(item => item.view === 'schedules')
  const [menu, setMenu] = useState<{ scope: string; view: 'full' | 'integrations' | 'automations' }>()
  const computerState = useYourComputerSummary(accessItems.some(item => item.id === 'mcp:access'), projectId)
  const sharedAgents = useCrewSharedAgentCount(accessItems.some(item => item.id === 'mcp:access') ? projectId : undefined)
  const macOnMacHere = useCrewLivesOnMac(projectId)
  const showMenu = (view: 'full' | 'integrations' | 'automations') => {
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
  if (menu?.scope === scope) {
    const renderItem = (item: CrewMenuItem) => {
      if (item.id === 'dashboard') return <WorkspaceDashboardMenu key={item.id} documents={documents} createTarget={dashboardChatTarget} onSelect={path => open({ ...item, reportPath: path })}/>
      if (item.id === 'browser') return <WorkspaceBrowserRow key={item.id} workspacePath={workspacePath} profileId={profileId} onClick={() => open(item)}/>
      const connections = menu.view === 'integrations' ? (snapshot?.apps || []).filter(row => row.destination === item.id) : []
      const status = connectionSummary(connections, item.id === 'mcp:connected' && snapshot?.loaded.includes(item.id) === true && !snapshot.errors.includes('Connections could not be loaded.'))
      const color = connections.some(row => row.attention) ? 'text-amber-600 dark:text-amber-400' : connections.some(row => row.connected) ? 'text-emerald-600 dark:text-emerald-400' : undefined
      return <Row key={item.id} icon={item.icon} hint={connections.length && item.id !== 'mcp:connected' ? connections.map(row => row.detail).join('\n') : undefined} detail={menu.view === 'integrations' && status ? <span className={color}>{status}</span> : undefined} onClick={() => open(item)}>{item.label}</Row>
    }
    return <WorkspaceContextCard title={{ full: 'Full menu', integrations: 'Integrations', automations: 'Automations' }[menu.view]} onBack={() => setMenu(undefined)}>
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
  const automations = snapshot?.automations || []
  const nextAutomation = automations.filter(item => item.nextRunAt && Date.parse(item.nextRunAt) > Date.now()).sort((a, b) => Date.parse(a.nextRunAt!) - Date.parse(b.nextRunAt!))[0]
  const changes = reviewSessionId && allowed('files') ? <ChatChangesRow key={`${scope}:${reviewSessionId}`} workspacePath={workspacePath} sessionId={reviewSessionId} onOpen={() => { if (useAuthStore.getState().user?.id !== user?.id || useWorkspaceConnectionStore.getState().activeWorkspaceId !== server) return; setWorkspaceViewTarget('files', 'review'); onOpen('files') }}/> : null
  // One Access row (owner, 3 Nov): where it lives · who can use it, both as the server reports them.
  const who = sharing?.scope === scope ? sharing.detail : undefined
  const theirs = sharing?.scope === scope && !sharing.owner
  // On the website, a Crew that lives on its owner's Mac has only an empty online record: show none of its own
  // changes, dashboards, connections or automations (they would mislead, and could run on the server).
  const macElsewhere = macOnMacHere
  const sharedText = sharedAgents ? `${sharedAgents} ${sharedAgents === 1 ? 'agent' : 'agents'} shared` : undefined
  // Offline is the whole answer: on this Mac, reached from nowhere else.
  const accessDetail = [computerState ? cloudStateLabel[computerState] : undefined, computerState === 'offline' ? undefined : sharedText || who].filter(Boolean).join(' · ') || undefined
  const accessRow = (item: CrewMenuItem) => <Row key={item.id} icon={item.id === 'mcp:access' ? cloudStateIcon(computerState, theirs) : item.icon} detail={item.id === 'mcp:access' ? accessDetail : undefined} onClick={() => open(item)}>{item.label}</Row>
  return <WorkspaceContextCard onFullMenu={() => showMenu('full')} footerContent={accessItems.length > 0 && <div className="space-y-0.5">{accessItems.map(accessRow)}</div>}>
    {macElsewhere ? <div role="note" className="space-y-1.5 px-1 py-1 text-xs text-muted-foreground">
      <p>This Crew lives on your Mac. Its changes, connections and automations are set on your Mac.</p>
      <p>Dashboards work once this Crew is online. Move it online from your Mac.</p>
    </div> : <>
    {changes}
    {byId.has('dashboard') && <WorkspaceDashboardMenu key={`${scope}:dashboards`} documents={documents} createTarget={dashboardChatTarget} onSelect={path => { const dashboard = byId.get('dashboard'); if (dashboard) open({ ...dashboard, reportPath: path }) }}/>}
    <WorkspaceConnectionsMenu key={scope} workspacePath={workspacePath} profileId={profileId} browserAllowed={byId.has('browser')} mcpAllowed={byId.has('mcp:connected')} apps={(snapshot?.apps || []).filter(item => byId.has(item.destination))} loading={snapshot?.loaded.includes('mcp:connected') !== true} unavailable={snapshot?.errors.includes('Connections could not be loaded.') === true} onOpen={openID} onManage={integrations.length ? () => showMenu('integrations') : undefined}/>
    {automationItems.length > 0 && <WorkspaceCompactGroup key={`${scope}:automations`} title="Automations" icon={CalendarClock} detail={automations.length === 1 ? automations[0].detail : automations.length > 1 ? `${automations.length}${nextAutomation ? ` · ${nextAutomation.detail}` : ''}` : undefined}>
      {automations.map(item => <Row key={item.id} icon={byId.get(item.destination)?.icon || CalendarClock} hint={byId.get(item.destination)?.label} detail={item.detail} onClick={() => openID(item.destination)}>{item.label}</Row>)}
      <Row icon={Settings2} detail={<ChevronRight aria-hidden="true" className="h-3 w-3" />} onClick={() => showMenu('automations')}>Manage automations</Row>
    </WorkspaceCompactGroup>}
    </>}
    {snapshot?.errors.length ? <div role="status" className="space-y-1 px-1 text-xs text-muted-foreground"><p>Some workspace status is unavailable.</p><button type="button" onClick={refresh} className="inline-flex items-center gap-1 py-1 hover:text-foreground"><RefreshCw className="h-3 w-3"/>Retry</button></div> : null}
  </WorkspaceContextCard>
}
