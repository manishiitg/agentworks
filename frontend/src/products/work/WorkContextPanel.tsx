import { useEffect, useState } from 'react'
import { LayoutDashboard } from 'lucide-react'
import { WorkspaceContextCard, WorkspaceContextRow as Row, WorkspaceContextGroup as Group } from '../../components/workspace/WorkspaceContextCard'
import { useProjectStatus } from '../../components/workspace/useProjectStatus'
import { WorkspaceBrowserRow } from '../../components/workspace/WorkspaceBrowserRow'
import { selectReportDocument, useReportDocuments } from '../../components/workflow/reportDocuments'
import { reportDocumentLabel } from '../../components/workflow/reportDocumentLabels'
import { setWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import api, { agentApi } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { projectMenuHistoryScope, useProjectMenuHistoryStore } from '../../stores/useProjectMenuHistoryStore'
import { useAgentInboxEnabled } from '../../components/integrations/AgentInboxPanel'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { CREW_MENU_GROUPS, crewMenuTarget, crewProjectMenuItems, type CrewMenuItem } from './crewProjectMenu'
import type { ProductPanel } from '../productPanels'
import type { WorkWorkspaceView } from './WorkWorkspacePane'

const EMPTY_RECENT: string[] = []

export function WorkContextPanel({ workspacePath, projectId = '', profileId = 'work', panels, onOpen, enabledPanels }: {
  workspacePath: string
  projectId?: string
  profileId?: 'work'
  panels: ProductPanel<WorkWorkspaceView>[]
  onOpen: (view: WorkWorkspaceView) => void
  enabledPanels?: Set<string>
}) {
  const user = useAuthStore(state => state.user)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = projectMenuHistoryScope(server || 'local', user?.id || 'pending', profileId, projectId || workspacePath)
  const storedRecent = useProjectMenuHistoryStore(state => state.recent[scope] || EMPTY_RECENT)
  const storedStartHere = useProjectMenuHistoryStore(state => state.startHere[scope] || EMPTY_RECENT)
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
  const [suggestions, setSuggestions] = useState<{ scope: string; count: number | null } | null>(null)
  const [tick, setTick] = useState(0)
  const showSuggestions = allowed('suggestions')
  useLiveRefetch(() => setTick(value => value + 1), { kinds: ['human_inputs'], workflow: workspacePath, fallbackMs: 30000, enabled: showSuggestions })
  useEffect(() => {
    if (!showSuggestions) return
    let disposed = false
    void agentApi.listReportHumanInputs(workspacePath, 'pending', 'user_suggestion').then(result => {
      if (!disposed) setSuggestions({ scope, count: result.success ? (result.inputs || []).filter(item => item.workspace_path === workspacePath && item.status === 'pending').length : null })
    }).catch(() => { if (!disposed) setSuggestions({ scope, count: null }) })
    return () => { disposed = true }
  }, [workspacePath, scope, showSuggestions, tick])
  const count = suggestions?.scope === scope ? suggestions.count : null
  const { documents, defaultPath } = useReportDocuments(allowed('dashboard') ? workspacePath : '')
  const status = useProjectStatus({ kind: 'crew', workspacePath, profileId, projectId })
  const menuOptions = { panels, enabledPanels, incomingEmail, projectId, canShare: sharing?.scope === scope && sharing.allowed }
  const inventory = crewProjectMenuItems({ ...menuOptions, allowedProducts: user?.allowed_products })
  const items: CrewMenuItem[] = [
    ...inventory.map(item => item.id === 'dashboard' ? { ...item, reportPath: defaultPath } : item),
    ...(allowed('dashboard') ? documents.filter(document => document.path !== defaultPath).map(document => ({
      id: 'dashboard:document:' + document.path, label: reportDocumentLabel(document, documents), icon: LayoutDashboard, group: 'Build' as const, view: 'dashboard' as const, reportPath: document.path,
    })) : []),
  ]
  const byId = new Map(items.map(item => [item.id, item]))
  const recent = [...new Set(storedRecent)].flatMap(id => byId.get(id) || []).slice(0, 10)
  const recentIDs = new Set(recent.map(item => item.id))
  const startHere = [...new Set(storedStartHere)].filter(id => !recentIDs.has(id)).flatMap(id => byId.get(id) || [])
  const featuredIDs = new Set([...recentIDs, ...startHere.map(item => item.id)])
  const open = (item: CrewMenuItem) => {
    // Recheck account/product admission if access changed while the menu was open.
    const current = useAuthStore.getState().user
    if (current?.id !== user?.id || useWorkspaceConnectionStore.getState().activeWorkspaceId !== server) return
    const admitted = crewProjectMenuItems({ ...menuOptions, allowedProducts: current?.allowed_products })
    if (!admitted.some(candidate => candidate.id === item.id) && !(item.reportPath && admitted.some(candidate => candidate.id === 'dashboard') && byId.has(item.id))) return
    useProjectMenuHistoryStore.getState().visit(scope, item.id)
    if (item.external) { openProductWorkspace(item.external); return }
    if (!item.view) return
    if (item.reportPath) selectReportDocument(workspacePath, item.reportPath)
    if (item.target) setWorkspaceViewTarget(crewMenuTarget(item.view), item.target)
    onOpen(item.view)
  }
  const renderItem = (item: CrewMenuItem) => {
    if (item.id === 'browser') return <WorkspaceBrowserRow key={item.id} workspacePath={workspacePath} profileId={profileId} onClick={() => open(item)} />
    const automation = item.id.startsWith('schedules:') ? item.target : undefined
    const total = item.id === 'memory:skills' ? status.skills : item.id === 'mcp:gmail' ? status.googleApps : item.id === 'mcp:secrets' ? status.secrets : item.id === 'mcp:share-owners' ? status.owners : automation === 'schedules' ? status.schedules : automation === 'conditions' ? status.conditions : automation === 'triggers' ? status.triggers : automation === 'functions' ? status.functions : item.id === 'mcp:connected' ? status.mcps : item.id === 'mcp:agents' ? status.computerChats : undefined
    const detail = item.id === 'identity:location' ? status.laptop : item.id === 'mcp:share' ? sharing?.scope === scope ? sharing.detail : undefined : item.id === 'suggestions' ? count ? count + ' waiting' : undefined
      : item.id === 'mcp:slack-account' ? status.slackAccount : item.id === 'mcp:slack-bot' ? status.slackBot
      : item.id === 'mcp:whatsapp' ? status.whatsapp : item.id === 'schedules:email' ? status.email : total === undefined ? undefined : total ? String(total) : 'None'
    return <Row key={item.id} icon={item.icon} hint={item.reportPath} detail={detail} attention={item.id === 'suggestions' && Boolean(count)} onClick={() => open(item)}>{item.label}</Row>
  }
  return <WorkspaceContextCard>
    {recent.length > 0 && <Group title="Recent">{recent.map(renderItem)}</Group>}
    {startHere.length > 0 && <Group title="Start here">{startHere.map(renderItem)}</Group>}
    {CREW_MENU_GROUPS.map(group => {
      const remaining = items.filter(item => item.group === group && !featuredIDs.has(item.id))
      if (!remaining.length) return null
      const plain = remaining.filter(item => !item.subgroup)
      const subgroups = [...new Set(remaining.flatMap(item => item.subgroup ? [item.subgroup] : []))]
      return <Group key={scope + group} title={group} collapsed defaultOpen={!recent.length && !startHere.length && group === 'Build'}>
        {group !== 'Use' && plain.map(renderItem)}
        {subgroups.map(subgroup => <div key={subgroup} role="group" aria-label={subgroup} className="space-y-1">
          <h3 className="px-1 pb-1 pt-2 text-[10px] font-medium text-muted-foreground">{subgroup}</h3>
          {remaining.filter(item => item.subgroup === subgroup).map(renderItem)}
        </div>)}
        {group === 'Use' && plain.map(renderItem)}
      </Group>
    })}
  </WorkspaceContextCard>
}
