import { BookOpen, Cloud, DollarSign, Files, FileText, Globe, Hand, LayoutDashboard, MessageSquareText, Monitor, Route, Settings, BellRing, ShieldCheck, Sparkles } from 'lucide-react'
import { useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { WorkspaceContextCard, WorkspaceContextRow as Row, WorkspaceContextGroup as Group } from '../workspace/WorkspaceContextCard'
import { WorkspaceAutomationRows, WorkspaceChannelRows, WorkspaceConnectionRows } from '../workspace/WorkspaceProjectNavigation'
import { ShareDashboardButton } from './ShareDashboardButton'
import { selectReportDocument, useReportDocuments } from './reportDocuments'
import { reportDocumentLabel } from './reportDocumentLabels'
import { usePendingDecisionState } from './hooks/usePendingDecisionCount'
import { PulseIcon } from './pulseIcon'

/** Navigation projects the existing server access, manifest and real destinations. */
export function WorkflowContextPanel({ workspacePath, relayMode = false }: { workspacePath: string | null; relayMode?: boolean }) {
  const open = useWorkflowStore(state => state.openWorkspaceView)
  const canWrite = useCanWriteWorkflow(workspacePath)
  const multiUser = useAuthStore(state => state.isMultiUserMode)
  const { documents, defaultPath } = useReportDocuments(workspacePath || '')
  const decisions = usePendingDecisionState(relayMode ? null : workspacePath)
  const manifest = useWorkflowManifestStore(state => state.workflows.find(item => item.workspace_path === workspacePath)?.manifest)
  if (!workspacePath || !canWrite) return null
  const openConnection = (section: string) => open('mcp', section)
  const pulseOn = manifest?.pulse?.enabled === true
  const decisionsRow = (<Row icon={Hand} attention={decisions.loaded && decisions.count > 0} detail={decisions.loaded && decisions.count ? `${decisions.count} waiting` : undefined} onClick={() => open('human-actions')}>Decisions</Row>)
  return <WorkspaceContextCard>
    {!relayMode && pulseOn && <>
      <Row icon={PulseIcon} onClick={() => open('pulse')}>Pulse</Row>
      {decisionsRow}
    </>}
    <Group title="Use & share">
      <Row icon={LayoutDashboard} onClick={() => { selectReportDocument(workspacePath, defaultPath); open('report') }}>Dashboard</Row>
      {documents.filter(document => document.path !== defaultPath).map(document => <Row key={document.path} icon={LayoutDashboard} hint={document.path} onClick={() => { selectReportDocument(workspacePath, document.path); open('report') }}>{reportDocumentLabel(document, documents)}</Row>)}
      <ShareDashboardButton workspacePath={workspacePath} inline />
      {!relayMode && <Row icon={Monitor} onClick={() => open('browser')}>Browser</Row>}
      <WorkspaceAutomationRows relay={relayMode} onOpen={section => open('workshop', section)} />
      <WorkspaceChannelRows onOpen={openConnection} slack={true} whatsapp={!relayMode} />
    </Group>
    <Group title="Connections">
      <WorkspaceConnectionRows onOpen={openConnection} relayMode={relayMode} />
    </Group>
    <Group title="Ops" collapsed>
      {!relayMode && !pulseOn && <Row icon={PulseIcon} onClick={() => open('pulse')}>Turn Pulse on</Row>}
      <Row icon={Route} onClick={() => open('flow')}>{relayMode ? 'Flow & runs' : 'Plan'}</Row>
      <Row icon={Files} onClick={() => open('files')}>Files</Row>
      {!relayMode && <>
        <Row icon={BookOpen} onClick={() => open('knowledge')}>Knowledge base</Row>
      </>}
      <Row icon={Sparkles} onClick={() => open('mcp', 'skills')}>Skills</Row>
      <Row icon={MessageSquareText} onClick={() => open('workshop', 'chats')}>Chat history</Row>
      {!relayMode && !pulseOn && decisionsRow}
      <Row icon={DollarSign} onClick={() => open('costs')}>Costs and usage</Row>
      <Row icon={FileText} onClick={() => open('execution-logs')}>Run logs</Row>
      <Row icon={Settings} onClick={() => open('identity')}>Settings</Row>
      {multiUser && <Row icon={ShieldCheck} onClick={() => open('access')}>Team</Row>}
      {!relayMode && <>
        <Row icon={BookOpen} onClick={() => open('playbooks')}>Workflow playbooks</Row>
        <Row icon={Cloud} onClick={() => open('backup')}>Backup</Row>
        <Row icon={Globe} onClick={() => open('publish')}>Publish</Row>
        <Row icon={BellRing} onClick={() => open('notify')}>Notify</Row>
      </>}
    </Group>
  </WorkspaceContextCard>
}
