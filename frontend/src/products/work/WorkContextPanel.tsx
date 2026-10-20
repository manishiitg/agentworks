import { useEffect, useState } from 'react'
import { BookOpen, Cpu, DollarSign, Files, Hand, Laptop, LayoutDashboard, MessageSquareText, Route, Settings, Terminal, Database } from 'lucide-react'
import { WorkspaceContextCard, WorkspaceContextRow as Row, WorkspaceContextGroup as Group } from '../../components/workspace/WorkspaceContextCard'
import { WorkspaceAgentBridgeRows } from '../../components/workspace/WorkspaceAgentBridgeRows'
import { WorkspaceAutomationRows, WorkspaceChannelRows, WorkspaceConnectionRows } from '../../components/workspace/WorkspaceProjectNavigation'
import { useProjectStatus } from '../../components/workspace/useProjectStatus'
import { WorkspaceBrowserRow } from '../../components/workspace/WorkspaceBrowserRow'
import { ShareDashboardButton } from '../../components/workflow/ShareDashboardButton'
import { selectReportDocument, useReportDocuments } from '../../components/workflow/reportDocuments'
import { reportDocumentLabel } from '../../components/workflow/reportDocumentLabels'
import { setWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { isWorkIdentityTabEnabled } from './workViewGating'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { agentApi } from '../../services/api'
import type { ProductPanel } from '../productPanels'
import type { WorkWorkspaceView } from './WorkWorkspacePane'

export function WorkContextPanel({ workspacePath, projectId = '', profileId = 'work', panels, onOpen, enabledPanels }: {
  workspacePath: string
  projectId?: string
  profileId?: 'work' | 'code'
  panels: ProductPanel<WorkWorkspaceView>[]
  onOpen: (view: WorkWorkspaceView) => void
  enabledPanels?: Set<string>
}) {
  const allowed = (view: WorkWorkspaceView) => panels.some(item => item.id === view)
  const [suggestions, setSuggestions] = useState<{ path: string; count: number | null } | null>(null)
  const [tick, setTick] = useState(0)
  const showSuggestions = allowed('suggestions')
  useLiveRefetch(() => setTick(value => value + 1), { kinds: ['human_inputs'], workflow: workspacePath, fallbackMs: 30000, enabled: showSuggestions })
  useEffect(() => {
    if (!showSuggestions) return
    let disposed = false
    void agentApi.listReportHumanInputs(workspacePath, 'pending', 'user_suggestion').then(result => {
      if (!disposed) setSuggestions({ path: workspacePath, count: result.success ? (result.inputs || []).filter(item => item.workspace_path === workspacePath && item.status === 'pending').length : null })
    }).catch(() => { if (!disposed) setSuggestions({ path: workspacePath, count: null }) })
    return () => { disposed = true }
  }, [workspacePath, showSuggestions, tick])
  const count = suggestions?.path === workspacePath ? suggestions.count : null
  const { documents, defaultPath } = useReportDocuments(allowed('dashboard') ? workspacePath : '')
  const openConnection = (section: string) => { setWorkspaceViewTarget('mcp', section); onOpen('mcp') }
  const openIdentity = (section: 'general' | 'models' | 'location') => { setWorkspaceViewTarget('identity', section); onOpen('identity') }
  const openAutomation = (section: string) => { setWorkspaceViewTarget('workshop', section); onOpen('schedules') }
  const bots = !enabledPanels || enabledPanels.has('bots')
  const status = useProjectStatus({ kind: 'crew', workspacePath, profileId, projectId })
  const webhooks = enabledPanels?.has('triggers') !== false
  return <WorkspaceContextCard>
    <Group title="Use & share">
      {allowed('dashboard') && <>
        <Row icon={LayoutDashboard} onClick={() => { selectReportDocument(workspacePath, defaultPath); onOpen('dashboard') }}>Dashboard</Row>
        {documents.filter(document => document.path !== defaultPath).map(document => <Row key={document.path} icon={LayoutDashboard} hint={document.path} onClick={() => { selectReportDocument(workspacePath, document.path); onOpen('dashboard') }}>{reportDocumentLabel(document, documents)}</Row>)}
        {allowed('mcp') && <ShareDashboardButton workspacePath={workspacePath} variant="row" onOpen={() => openConnection('share')} />}
      </>}
      {allowed('browser') && <WorkspaceBrowserRow workspacePath={workspacePath} profileId={profileId} onClick={() => onOpen('browser')} />}
      {allowed('schedules') && <WorkspaceAutomationRows onOpen={openAutomation} triggers={webhooks} counts={status} />}
      {allowed('mcp') && <WorkspaceChannelRows onOpen={openConnection} slack={bots} whatsapp={bots} slackBot={bots} slackAccountStatus={status.slackAccount} slackBotStatus={status.slackBot} whatsappStatus={status.whatsapp} emailStatus={status.email} onOpenEmail={allowed('schedules') && webhooks ? () => openAutomation('email') : undefined} />}
    </Group>
    {allowed('mcp') && <Group title="Connections">
      <WorkspaceConnectionRows onOpen={openConnection} workMode enabledPanels={enabledPanels} details={{ 'mcp:connected': status.mcps === undefined ? undefined : status.mcps ? String(status.mcps) : 'None' }} />
      {projectId && <Row icon={Laptop} detail={status.computerChats === undefined ? undefined : status.computerChats ? String(status.computerChats) : 'None'} onClick={() => openConnection('agents')}>Chats from your computer</Row>}
      {allowed('identity') && <Row icon={Laptop} onClick={() => { setWorkspaceViewTarget('identity', 'location'); onOpen('identity') }}>Local laptop</Row>}
      <WorkspaceAgentBridgeRows />
    </Group>}
    <Group title="Ops" collapsed>
      {allowed('plan') && <Row icon={Route} onClick={() => onOpen('plan')}>Plan</Row>}
      {allowed('files') && <Row icon={Files} onClick={() => onOpen('files')}>Files</Row>}
      {allowed('shell') && <Row icon={Terminal} onClick={() => onOpen('shell')}>Terminal</Row>}
      {allowed('database') && <Row icon={Database} onClick={() => onOpen('database')}>Database</Row>}
      {allowed('memory') && <Row icon={BookOpen} onClick={() => onOpen('memory')}>Memory &amp; skills</Row>}
      {allowed('history') && <Row icon={MessageSquareText} onClick={() => onOpen('history')}>Chat history</Row>}
      {showSuggestions && <Row icon={Hand} attention={Boolean(count)} detail={count ? `${count} waiting` : undefined} onClick={() => onOpen('suggestions')}>Suggestions</Row>}
      {allowed('costs') && <Row icon={DollarSign} onClick={() => onOpen('costs')}>Costs and usage</Row>}
      {allowed('identity') && isWorkIdentityTabEnabled('models', enabledPanels) && <Row icon={Cpu} onClick={() => openIdentity('models')}>Models</Row>}
      {allowed('identity') && <Row icon={Settings} onClick={() => openIdentity('general')}>Settings</Row>}
    </Group>
  </WorkspaceContextCard>
}
