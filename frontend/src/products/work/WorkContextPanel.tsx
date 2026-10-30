import { lazy, Suspense, useEffect, useState } from 'react'
import { ArrowLeft, Cable, CalendarClock, FileText, LayoutDashboard, RefreshCw } from 'lucide-react'
import { useWorkspaceStatus } from './useWorkspaceStatus'
import { ChatChangesRow } from '../../components/workspace/ChatChangesRow'
import { WorkspaceContextCard, WorkspaceContextRow as Row, WorkspaceContextGroup as Group } from '../../components/workspace/WorkspaceContextCard'
import { WorkspaceBrowserRow } from '../../components/workspace/WorkspaceBrowserRow'
import { selectReportDocument, useReportDocuments } from '../../components/workflow/reportDocuments'
import type { DashboardChatTarget } from '../../components/workflow/DashboardEmptyState'
import { setWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import api from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { projectMenuHistoryScope } from '../../stores/useProjectMenuHistoryStore'
import { useAgentInboxEnabled } from '../../components/integrations/AgentInboxPanel'
import { crewMenuTarget, crewProjectMenuItems, type CrewMenuItem } from './crewProjectMenu'
import type { ProductPanel } from '../productPanels'
import type { WorkWorkspaceView } from './WorkWorkspacePane'

const FilePreview = lazy(() => import('../../components/workflow/reportWidgets/FileWidget').then(module => ({ default: module.FilePreviewByPath })))

export function WorkContextPanel({ workspacePath, projectId = '', profileId = 'work', panels, onOpen, enabledPanels, dashboardChatTarget: _dashboardChatTarget, reviewSessionId, mode = 'cowork' }: {
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
    void api.post<{ can_share: boolean; mode?: string; people?: { id: string }[]; unknown_emails?: string[] }>('/api/dashboards', { workspace: workspacePath, action: 'share_status' }, { signal: controller.signal })
      .then(({ data }) => { if (!controller.signal.aborted) setSharing({ scope, allowed: data.can_share === true, detail: data.mode === 'everyone' ? 'Everyone' : data.mode === 'private' ? 'Only me' : data.mode === 'people' ? String(new Set((data.people || []).map(person => person.id)).size + (data.unknown_emails || []).length) : undefined }) })
      .catch(() => { if (!controller.signal.aborted) setSharing({ scope, allowed: false }) })
    return () => controller.abort()
  }, [scope, workspacePath, canReadSharing])
  const { documents } = useReportDocuments(allowed('dashboard') ? workspacePath : '')
  const menuOptions = { panels, enabledPanels, incomingEmail, projectId, canShare: sharing?.scope === scope && sharing.allowed }
  const inventory = crewProjectMenuItems({ ...menuOptions, allowedProducts: user?.allowed_products })
  const byId = new Map(inventory.map(item => [item.id, item]))
  const { snapshot, refresh } = useWorkspaceStatus({ scope, workspacePath, projectId, admitted: inventory.map(item => item.id) })
  const [preview, setPreview] = useState<{ scope: string; path: string; title: string }>()
  const open = (item: CrewMenuItem) => {
    // Recheck account/product admission if access changed while the menu was open.
    const current = useAuthStore.getState().user
    if (current?.id !== user?.id || useWorkspaceConnectionStore.getState().activeWorkspaceId !== server) return
    const admitted = crewProjectMenuItems({ ...menuOptions, allowedProducts: current?.allowed_products })
    if (!admitted.some(candidate => candidate.id === item.id)) return
    if (!item.view) return
    if (item.reportPath) selectReportDocument(workspacePath, item.reportPath)
    if (item.target) setWorkspaceViewTarget(crewMenuTarget(item.view), item.target)
    onOpen(item.view)
  }
  const openID = (id: string) => { const item = byId.get(id); if (item) open(item) }
  const currentPreview = preview?.scope === scope ? preview : undefined
  if (currentPreview) return <div className="flex h-full min-h-0 flex-col bg-background">
    <header className="flex items-center gap-2 border-b border-border p-3"><button type="button" aria-label="Back to workspace" onClick={() => setPreview(undefined)} className="rounded p-1 hover:bg-muted"><ArrowLeft className="h-4 w-4"/></button><h2 className="truncate text-sm font-medium">{currentPreview.title}</h2></header>
    <div className="min-h-0 flex-1"><Suspense fallback={<p className="p-4 text-sm text-muted-foreground">Loading result…</p>}><FilePreview key={currentPreview.path} path={currentPreview.path} name={currentPreview.title}/></Suspense></div>
  </div>
  const artifacts = documents.filter(item => item.artifact)
  const dashboards = documents.filter(item => !item.artifact)
  const results = <Group title="Results">
    {artifacts.map(item => <Row key={item.path} icon={FileText} detail="Artifact" onClick={() => { const dashboard = byId.get('dashboard'); if (dashboard) open({ ...dashboard, reportPath: item.path }) }}>{item.title}</Row>)}
    {(snapshot?.results || []).filter(item => byId.has('files') && !artifacts.some(artifact => artifact.path === item.path)).map(item => <Row key={item.path} icon={FileText} hint={item.path} attention={item.status !== 'ready'} detail={item.status === 'missing' ? 'Missing file' : item.status === 'unavailable' ? 'Unavailable' : undefined} onClick={() => {
      if (item.status !== 'ready' || !byId.has('files') || useAuthStore.getState().user?.id !== user?.id || useWorkspaceConnectionStore.getState().activeWorkspaceId !== server) return
      setPreview({ scope, path: `${workspacePath.replace(/\/+$/, '')}/${item.path}`, title: item.title })
    }}>{item.title}</Row>)}
    {!artifacts.length && !snapshot?.results?.length && <p className="px-1 py-2 text-xs text-muted-foreground">{snapshot ? 'Finished files will appear here.' : 'Loading results…'}</p>}
    {dashboards.map(item => <Row key={item.path} icon={LayoutDashboard} detail="Dashboard" onClick={() => { const dashboard = byId.get('dashboard'); if (dashboard) open({ ...dashboard, reportPath: item.path }) }}>{item.title}</Row>)}
  </Group>
  const changes = reviewSessionId && allowed('files') ? <Group title="Changes"><ChatChangesRow key={`${scope}:${reviewSessionId}`} workspacePath={workspacePath} sessionId={reviewSessionId} onOpen={() => { if (useAuthStore.getState().user?.id !== user?.id || useWorkspaceConnectionStore.getState().activeWorkspaceId !== server) return; setWorkspaceViewTarget('files','review'); onOpen('files') }}/></Group> : null
  return <WorkspaceContextCard>
    {mode === 'dev' && changes}
    {(mode === 'cowork' || artifacts.length > 0 || dashboards.length > 0 || Boolean(snapshot?.results?.length)) && results}
    <Group title="Connections">
      {byId.has('browser') && <WorkspaceBrowserRow key={scope} workspacePath={workspacePath} profileId={profileId} onClick={() => openID('browser')}/>}
      {(snapshot?.apps || []).filter(item => byId.has(item.destination)).map(item => <Row key={item.id} icon={byId.get(item.destination)?.icon || Cable} detail={item.detail} attention={item.attention} onClick={() => openID(item.destination)}>{item.label}</Row>)}
      {byId.has('mcp:connected') && <Row icon={Cable} onClick={() => openID('mcp:connected')}>Manage connections</Row>}
    </Group>
    {Boolean(snapshot?.automations?.length) && <Group title="Automations">{snapshot!.automations!.map(item => <Row key={item.id} icon={CalendarClock} detail={item.detail} onClick={() => openID(item.destination)}>{item.label}</Row>)}</Group>}
    {snapshot?.errors.length ? <div className="space-y-1 px-1 text-xs text-muted-foreground">{snapshot.errors.map(error => <p key={error}>{error}</p>)}<button type="button" onClick={refresh} className="inline-flex items-center gap-1 py-1 hover:text-foreground"><RefreshCw className="h-3 w-3"/>Retry</button></div> : null}
  </WorkspaceContextCard>
}
