import { openWorkflowPulseChatTab } from '../../utils/pulseChatTab'
import { useChatStore } from '../../stores/useChatStore'
import { useEffect, useId, useState } from 'react'
import { ChevronRight, MessageSquareText, Power, Settings2, Target } from 'lucide-react'
import { PulseIcon } from './pulseIcon'
import { useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { projectMenuHistoryScope, useProjectMenuHistoryStore } from '../../stores/useProjectMenuHistoryStore'
import api from '../../services/api'
import { WorkspaceContextCard, WorkspaceContextRow as Row, WorkspaceContextGroup as Group } from '../workspace/WorkspaceContextCard'
import { WorkspaceAgentBridgeRows } from '../workspace/WorkspaceAgentBridgeRows'
import { useProjectStatus } from '../workspace/useProjectStatus'
import { WorkspaceBrowserRow } from '../workspace/WorkspaceBrowserRow'
import { PROJECT_MENU_GROUPS } from '../workspace/projectMenuGroups'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { ShareDashboardButton } from './ShareDashboardButton'
import { selectReportDocument, useReportDocuments } from './reportDocuments'
import { WorkspaceDashboardMenu } from '../workspace/WorkspaceDashboardMenu'
import { usePendingDecisionState } from './hooks/usePendingDecisionCount'
import { workflowProjectMenuItems, type WorkflowMenuItem } from './workflowProjectMenu'

const EMPTY_RECENT: string[] = []

/** Current product admission controls the inventory; recents are presentation only. */
export function WorkflowContextPanel({ workspacePath, relayMode = false }: { workspacePath: string | null; relayMode?: boolean }) {
  const [pulseMenuOpen, setPulseMenuOpen] = useState(true)
  const pulseMenuId = useId()
  const openView = useWorkflowStore(state => state.openWorkspaceView)
  const canWrite = useCanWriteWorkflow(workspacePath)
  const multiUser = useAuthStore(state => state.isMultiUserMode)
  const user = useAuthStore(state => state.user)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const manifest = useWorkflowManifestStore(state => state.workflows.find(item => item.workspace_path === workspacePath)?.manifest)
  const product = relayMode ? 'relays' : 'agentworks'
  const scope = projectMenuHistoryScope(server || 'local', user?.id || 'pending', product, manifest?.id ?? workspacePath ?? '')
  const storedRecent = useProjectMenuHistoryStore(state => state.recent[scope] || EMPTY_RECENT)
  const incomingEmail = false
  const { documents } = useReportDocuments(workspacePath || '')
  const decisions = usePendingDecisionState(relayMode ? null : workspacePath)
  const status = useProjectStatus({ kind: 'goal', workspacePath: workspacePath || '', profileId: product, projectId: manifest?.id ?? workspacePath ?? '', enabled: canWrite })
  const [sharing, setSharing] = useState<{ scope: string; allowed: boolean } | null>(null)
  useEffect(() => {
    if (!workspacePath || !canWrite) return
    const controller = new AbortController()
    void api.post<{ can_share: boolean }>('/api/dashboards', { workspace: workspacePath, action: 'share_status' }, { signal: controller.signal })
      .then(({ data }) => { if (!controller.signal.aborted) setSharing({ scope, allowed: data.can_share === true }) })
      .catch(() => { if (!controller.signal.aborted) setSharing({ scope, allowed: false }) })
    return () => controller.abort()
  }, [scope, workspacePath, canWrite])
  const menuOptions = { relayMode, isMultiUser: multiUser, pulseEnabled: manifest?.pulse?.enabled === true, incomingEmail, canShare: sharing?.scope === scope && sharing.allowed }
  const inventory = workflowProjectMenuItems({ ...menuOptions, allowedProducts: user?.allowed_products })
  const pulse = inventory.find(item => item.id === 'pulse')
  const pulseDecisions = pulse ? inventory.find(item => item.id === 'human-actions') : undefined
  const inPulseMenu = (id: string) => id === 'pulse' || Boolean(pulseDecisions && id === pulseDecisions.id)
  const items = inventory.filter(item => !inPulseMenu(item.id))
  const byId = new Map(inventory.map(item => [item.id, item]))
  const recent = [...new Set(storedRecent)].filter(id => !inPulseMenu(id)).flatMap(id => byId.get(id) || []).slice(0, 10)
  const recentIDs = new Set(recent.map(item => item.id))
  if (!workspacePath || !canWrite) return null
  const open = (item: WorkflowMenuItem) => {
    const current = useAuthStore.getState().user
    if (current?.id !== user?.id || useWorkspaceConnectionStore.getState().activeWorkspaceId !== server) return
    const admitted = workflowProjectMenuItems({ ...menuOptions, allowedProducts: current?.allowed_products })
    if (!admitted.some(candidate => candidate.id === item.id)) return
    if (!inPulseMenu(item.id)) useProjectMenuHistoryStore.getState().visit(scope, item.id)
    if (item.external) { openProductWorkspace(item.external); return }
    if (!item.view) return
    if (item.reportPath) selectReportDocument(workspacePath, item.reportPath)
    if (item.id === 'pulse' && item.target === 'chat') {
      void openWorkflowPulseChatTab(workspacePath).catch(error => useChatStore.getState().addToast(error instanceof Error ? error.message : 'Could not open Autopilot chat.', 'error'))
      return
    }
    openView(item.view, item.target)
  }
  const renderItem = (item: WorkflowMenuItem) => {
    if (item.id === 'report') return <WorkspaceDashboardMenu key={scope + item.id} documents={documents} createTarget={{ workspacePath }} onSelect={path => open({ ...item, reportPath: path })} />
    if (item.external) return <WorkspaceAgentBridgeRows key={item.id} onOpen={() => open(item)} />
    if (item.id === 'mcp:share') return <ShareDashboardButton key={item.id} workspacePath={workspacePath} label={item.label} variant="row" onOpen={() => open(item)} />
    if (item.id === 'browser') return <WorkspaceBrowserRow key={item.id} workspacePath={workspacePath} profileId="workflow" onClick={() => open(item)} />
    const automation = item.view === 'workshop' ? item.target : undefined
    const total = automation === 'schedules' ? status.schedules : automation === 'conditions' ? status.conditions : automation === 'triggers' ? status.triggers : automation === 'functions' ? status.functions : item.id === 'mcp:connected' ? status.mcps : undefined
    const detail = item.id === 'human-actions' ? decisions.loaded && decisions.count ? `${decisions.count} waiting` : undefined
      : item.id === 'mcp:slack' ? status.slackAccount : item.id === 'mcp:slack-bot' ? status.slackBot : item.id === 'mcp:whatsapp' ? status.whatsapp : automation === 'email' ? status.email : total === undefined ? undefined : total ? String(total) : 'None'
    return <Row key={item.id} icon={item.icon} hint={item.reportPath} detail={detail} attention={item.id === 'human-actions' && decisions.loaded && decisions.count > 0} onClick={() => open(item)}>{item.label}</Row>
  }
  return <WorkspaceContextCard>
    {pulse && <div key={scope}>
      <Row icon={Target} onClick={() => open({ ...pulse, target: 'goals' })}>Goals &amp; metrics</Row>
      {pulseDecisions && renderItem(pulseDecisions)}
      <div role="group" aria-label="Autopilot menu" className="mt-1">
        <div className="grid grid-cols-[minmax(0,1fr)_24px] items-center rounded-md bg-primary/5">
          <Row icon={PulseIcon} hint="Open the full Autopilot panel"
            detail={<span className={menuOptions.pulseEnabled ? 'text-emerald-600 dark:text-emerald-400' : undefined}>{menuOptions.pulseEnabled ? 'On' : 'Off'}</span>}
            onClick={() => open(pulse)}>Autopilot</Row>
          <button type="button" aria-label={pulseMenuOpen ? 'Hide Autopilot panels' : 'Show Autopilot panels'} aria-expanded={pulseMenuOpen} aria-controls={pulseMenuId}
            onClick={() => setPulseMenuOpen(value => !value)} className="flex h-8 w-6 items-center justify-center rounded-md text-muted-foreground hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
            <ChevronRight aria-hidden="true" className={`h-3 w-3 transition-transform ${pulseMenuOpen ? 'rotate-90' : ''}`} />
          </button>
        </div>
        <div id={pulseMenuId} hidden={!pulseMenuOpen} role="group" aria-label="Autopilot panels" className="ml-2.5 mt-1 space-y-0.5 border-l border-border pl-3">
          {!menuOptions.pulseEnabled && <Row icon={Power} onClick={() => open({ ...pulse, target: 'settings' })}>Turn Autopilot on</Row>}
          <Row icon={MessageSquareText} onClick={() => open({ ...pulse, target: 'chat' })}>Talk to Autopilot</Row>
          <Row icon={Settings2} onClick={() => open({ ...pulse, target: 'settings' })}>Autopilot settings</Row>
        </div>
      </div>
    </div>}
    {recent.length > 0 && <Group title="Recent">{recent.map(renderItem)}</Group>}
    {PROJECT_MENU_GROUPS.map(group => {
      const remaining = items.filter(item => item.group === group && !recentIDs.has(item.id))
      if (!remaining.length) return null
      const plain = remaining.filter(item => !item.subgroup)
      const subgroups = [...new Set(remaining.flatMap(item => item.subgroup ? [item.subgroup] : []))]
      return <Group key={scope + group} title={group} collapsed defaultOpen={!recent.length && group === 'Build'}>
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
