import { DollarSign, Files, FileText, LayoutDashboard, MessageSquareText, Plug, Route, Users, Webhook, Zap } from 'lucide-react'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { WorkspaceContextCard, WorkspaceContextRow as Row, WorkspaceContextSection as Section, WorkspaceContextShortcut as Shortcut } from '../workspace/WorkspaceContextCard'
import { selectReportDocument, useReportDocuments } from './reportDocuments'
import { reportDocumentLabel } from './reportDocumentLabels'
import { workflowContextViews } from './workspaceViews'
import { useAuthStore } from '../../stores/useAuthStore'
import { WorkspaceMoreTools, isProjectSettingsPanel } from '../workspace/WorkspaceMoreTools'

/** Navigation to the existing Flow panels. Each destination owns its permissions. */
export function RelayContextPanel({ workspacePath }: { workspacePath: string | null }) {
  const open = useWorkflowStore(state => state.openWorkspaceView)
  const multiUser = useAuthStore(state => state.isMultiUserMode)
  const { documents, defaultPath } = useReportDocuments(workspacePath || '')
  if (!workspacePath) return null
  return (
    <WorkspaceContextCard product="relays" consolidateConnections summary="Run this Flow and review its results." availableShortcuts={workflowContextViews(true, multiUser).filter(item => !isProjectSettingsPanel(item.id)).map(item => ({ ...item, onClick: () => open(item.id, item.id === 'mcp' ? 'overview' : undefined) }))} moreTools={<WorkspaceMoreTools items={workflowContextViews(true, multiUser).filter(item => item.id !== 'mcp' && !isProjectSettingsPanel(item.id)).map(item => ({ ...item, label: item.id === 'flow' ? 'Flow' : item.id === 'workshop' ? 'Triggers' : item.label, onClick: () => open(item.id) }))} />}>
      <Section title="Work & results">
        <Shortcut id="flow" label="Flow & runs" icon={Route}><Row icon={Route} onClick={() => open('flow')}>Flow &amp; runs</Row></Shortcut>
        <Shortcut id="report" label="Dashboard" icon={LayoutDashboard}>
        <Row icon={LayoutDashboard} onClick={() => { selectReportDocument(workspacePath, defaultPath); open('report') }}>Dashboard</Row>
        {documents.filter(document => document.path !== defaultPath).slice(0, 3).map(document => (
          <Row key={document.path} hint={document.path} icon={LayoutDashboard} onClick={() => { selectReportDocument(workspacePath, document.path); open('report') }}>{reportDocumentLabel(document, documents)}</Row>
        ))}
        {documents.filter(document => document.path !== defaultPath).length > 3 && (
          <button className="mt-1 text-[11px] text-muted-foreground hover:text-foreground" onClick={() => open('report')}>View all results</button>
        )}
        </Shortcut>
      </Section>
      <Section title="Start automatically">
        <Shortcut id="workshop:triggers" label="API triggers" icon={Webhook}><Row icon={Webhook} hint="Run when another app sends data (webhooks)" onClick={() => open('workshop', 'triggers')}>API triggers</Row></Shortcut>
        <Shortcut id="workshop:conditions" label="Condition triggers" icon={Zap}><Row icon={Zap} hint="Run when saved conditions match (watchers)" onClick={() => open('workshop', 'conditions')}>Condition triggers</Row></Shortcut>
      </Section>
      <Section title="Activity">
        <Shortcut id="costs" label="Costs" icon={DollarSign}><Row icon={DollarSign} onClick={() => open('costs')}>Costs</Row></Shortcut>
        <Shortcut id="execution-logs" label="Run logs" icon={FileText}><Row icon={FileText} hint="Inspect what happened in past runs" onClick={() => open('execution-logs')}>Run logs</Row></Shortcut>
      </Section>
      <Section title="Project tools">
        <Shortcut id="files" label="Files" icon={Files}><Row icon={Files} onClick={() => open('files')}>Files</Row></Shortcut>
        <Shortcut id="workshop:chats" label="Chat history" icon={MessageSquareText}><Row icon={MessageSquareText} onClick={() => open('workshop', 'chats')}>Chat history</Row></Shortcut>
      </Section>
      <Shortcut id="mcp" label="Connections & sharing" icon={Plug}><div className="border-t border-border pt-3"><Row icon={Plug} hint="What this Flow can use" onClick={() => open('mcp', 'overview')}>Connections</Row><Row icon={Users} hint="People, dashboard and API/MCP access" onClick={() => open('mcp', 'use-share')}>Use &amp; share</Row></div></Shortcut>
    </WorkspaceContextCard>
  )
}
