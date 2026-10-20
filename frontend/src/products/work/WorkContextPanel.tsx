import { useEffect, useState } from 'react'
import { BookOpen, DollarSign, Files, Hand, Laptop, LayoutDashboard, MessageSquareText, Route, Settings, Terminal, Database } from 'lucide-react'
import { WorkspaceContextCard, WorkspaceContextRow as Row, WorkspaceContextGroup as Group } from '../../components/workspace/WorkspaceContextCard'
import { WorkspaceAutomationRows, WorkspaceChannelRows, WorkspaceConnectionRows } from '../../components/workspace/WorkspaceProjectNavigation'
import { WorkspaceBrowserRow } from '../../components/workspace/WorkspaceBrowserRow'
import { ShareDashboardButton } from '../../components/workflow/ShareDashboardButton'
import { selectReportDocument, useReportDocuments } from '../../components/workflow/reportDocuments'
import { reportDocumentLabel } from '../../components/workflow/reportDocumentLabels'
import { setWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
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
  const openAutomation = (section: string) => { setWorkspaceViewTarget('workshop', section); onOpen('schedules') }
  const bots = !enabledPanels || enabledPanels.has('bots')
  return <WorkspaceContextCard>
    <Group title="Use & share">
      {allowed('dashboard') && <>
        <Row icon={LayoutDashboard} onClick={() => { selectReportDocument(workspacePath, defaultPath); onOpen('dashboard') }}>Dashboard</Row>
        {documents.filter(document => document.path !== defaultPath).map(document => <Row key={document.path} icon={LayoutDashboard} hint={document.path} onClick={() => { selectReportDocument(workspacePath, document.path); onOpen('dashboard') }}>{reportDocumentLabel(document, documents)}</Row>)}
        <ShareDashboardButton workspacePath={workspacePath} inline />
      </>}
      {allowed('browser') && <WorkspaceBrowserRow workspacePath={workspacePath} profileId={profileId} onClick={() => onOpen('browser')} />}
      {allowed('schedules') && <WorkspaceAutomationRows onOpen={openAutomation} triggers={enabledPanels?.has('triggers') !== false} />}
      {allowed('mcp') && <WorkspaceChannelRows onOpen={openConnection} slack={bots} whatsapp={bots} />}
    </Group>
    {allowed('mcp') && <Group title="Connections">
      <WorkspaceConnectionRows onOpen={openConnection} workMode enabledPanels={enabledPanels} />
      {projectId && <Row icon={Laptop} onClick={() => openConnection('agents')}>Chats from your computer</Row>}
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
      {allowed('identity') && <Row icon={Settings} onClick={() => onOpen('identity')}>Settings</Row>}
    </Group>
  </WorkspaceContextCard>
}
