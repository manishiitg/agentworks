import { useEffect, useState } from 'react'
import { CalendarClock, RefreshCw } from 'lucide-react'
import { useWorkspaceStatus } from './useWorkspaceStatus'
import { ChatChangesRow } from '../../components/workspace/ChatChangesRow'
import { WorkspaceContextCard, WorkspaceContextRow as Row, WorkspaceContextGroup as Group } from '../../components/workspace/WorkspaceContextCard'
import { WorkspaceDashboardMenu } from '../../components/workspace/WorkspaceDashboardMenu'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { WorkspaceCompactGroup } from '../../components/workspace/WorkspaceCompactGroup'
import { WorkspaceConnectionsMenu } from '../../components/workspace/WorkspaceConnectionsMenu'
import { WorkspaceBrowserRow } from '../../components/workspace/WorkspaceBrowserRow'
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
  const [sharing, setSharing] = useState<{ scope: string; allowed: boolean; detail?: string } | null>(null)
  useEffect(() => {
    if (!canReadSharing) return
    const controller = new AbortController()
    void api.post<{ can_share: boolean; mode?: string; people?: { id: string }[] }>('/api/dashboards', { workspace: workspacePath, action: 'share_status' }, { signal: controller.signal })
      .then(({ data }) => {
        if (controller.signal.aborted) return
        const count = new Set((data.people || []).map(person => person.id)).size
        setSharing({ scope, allowed: data.can_share === true, detail: data.mode === 'everyone' ? 'Everyone' : data.mode === 'private' ? 'Private' : data.mode === 'people' ? `${count} ${count === 1 ? 'person' : 'people'}` : undefined })
      })
      .catch(() => { if (!controller.signal.aborted) setSharing({ scope, allowed: false }) })
    return () => controller.abort()
  }, [scope, workspacePath, canReadSharing])
  const { documents } = useReportDocuments(allowed('dashboard') ? workspacePath : '')
  const menuOptions = { mode, panels, enabledPanels, incomingEmail, projectId, canShare: sharing?.scope === scope && sharing.allowed }
  const inventory = crewProjectMenuItems({ ...menuOptions, allowedProducts: user?.allowed_products })
  const byId = new Map(inventory.map(item => [item.id, item]))
  const { snapshot, refresh } = useWorkspaceStatus({ scope, workspacePath, projectId, admitted: inventory.map(item => item.id) })
  const integrations = inventory.filter(item => item.group === 'Integrations' || (item.view === 'mcp' && item.group === 'Use'))
  const [menu, setMenu] = useState<{ scope: string; view: 'full' | 'integrations' }>()
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
    if (item.target) setWorkspaceViewTarget(crewMenuTarget(item.view), item.target)
    onOpen(item.view)
  }
  const openID = (id: string) => { const item = byId.get(id); if (item) open(item) }
  if (menu?.scope === scope) {
    const renderItem = (item: CrewMenuItem) => item.id === 'dashboard'
      ? <WorkspaceDashboardMenu key={item.id} documents={documents} createTarget={dashboardChatTarget} onSelect={path => open({ ...item, reportPath: path })}/>
      : item.id === 'browser' ? <WorkspaceBrowserRow key={item.id} workspacePath={workspacePath} profileId={profileId} onClick={() => open(item)}/>
      : <Row key={item.id} icon={item.icon} onClick={() => open(item)}>{item.label}</Row>
    return <WorkspaceContextCard title={menu.view === 'integrations' ? 'Integrations' : 'Full menu'} onBack={() => setMenu(undefined)}>
      {menu.view === 'integrations' ? <div className="space-y-0.5">{integrations.map(renderItem)}</div> : CREW_MENU_GROUPS.map(group => {
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
  const changes = reviewSessionId && allowed('files') ? <ChatChangesRow key={`${scope}:${reviewSessionId}`} workspacePath={workspacePath} sessionId={reviewSessionId} onOpen={reply => { if (useAuthStore.getState().user?.id !== user?.id || useWorkspaceConnectionStore.getState().activeWorkspaceId !== server) return; setWorkspaceViewTarget('files', reply ? `reply:${JSON.stringify(reply)}` : 'review-all-chat'); onOpen('files') }}/> : null
  const share = byId.get('mcp:share')
  return <WorkspaceContextCard onFullMenu={() => showMenu('full')} footerContent={share && <Row icon={share.icon} detail={sharing?.scope === scope ? sharing.detail : undefined} onClick={() => open(share)}>{share.label}</Row>}>
    {mode === 'dev' && changes}
    {byId.has('dashboard') && <WorkspaceDashboardMenu key={`${scope}:dashboards`} documents={documents} createTarget={dashboardChatTarget} onSelect={path => { const dashboard = byId.get('dashboard'); if (dashboard) open({ ...dashboard, reportPath: path }) }}/>}
    <WorkspaceConnectionsMenu key={scope} workspacePath={workspacePath} profileId={profileId} browserAllowed={byId.has('browser')} mcpAllowed={byId.has('mcp:connected')} apps={(snapshot?.apps || []).filter(item => byId.has(item.destination))} loading={!snapshot} unavailable={snapshot?.errors.includes('Connections could not be loaded.') === true} onOpen={openID} onManage={integrations.length ? () => showMenu('integrations') : undefined}/>
    {automations.length > 0 && <WorkspaceCompactGroup key={`${scope}:automations`} title="Automations" icon={CalendarClock} detail={`${automations.length}${nextAutomation ? ` · ${nextAutomation.detail}` : ''}`}>
      {automations.map(item => <Row key={item.id} icon={byId.get(item.destination)?.icon || CalendarClock} hint={byId.get(item.destination)?.label} detail={item.detail} onClick={() => openID(item.destination)}>{item.label}</Row>)}
    </WorkspaceCompactGroup>}
    {snapshot?.errors.length ? <div className="space-y-1 px-1 text-xs text-muted-foreground">{snapshot.errors.map(error => <p key={error}>{error}</p>)}<button type="button" onClick={refresh} className="inline-flex items-center gap-1 py-1 hover:text-foreground"><RefreshCw className="h-3 w-3"/>Retry</button></div> : null}
  </WorkspaceContextCard>
}
