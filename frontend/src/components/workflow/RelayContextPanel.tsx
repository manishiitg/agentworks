import { DollarSign, Files, FileText, LayoutDashboard, MessageSquareText, Route, Webhook, Zap } from 'lucide-react'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { WorkspaceContextCard, WorkspaceContextRow as Row, WorkspaceContextSection as Section, WorkspaceContextShortcut as Shortcut } from '../workspace/WorkspaceContextCard'
import { selectReportDocument, useReportDocuments } from './reportDocuments'
import { reportDocumentLabel } from './reportDocumentLabels'
import { WORKSPACE_VIEWS, workflowContextViews } from './workspaceViews'
import { useAuthStore } from '../../stores/useAuthStore'
import { integrationShortcutContent, workspaceIntegrationShortcuts } from '../workspace/workspaceIntegrationShortcuts'

/** Navigation to the existing Flow panels. Each destination owns its permissions. */
export function RelayContextPanel({ workspacePath }: { workspacePath: string | null }) {
  const open = useWorkflowStore(state => state.openWorkspaceView)
  const multiUser = useAuthStore(state => state.isMultiUserMode)
  const allowedProducts = useAuthStore(state => state.user?.allowed_products)
  const connections = workspaceIntegrationShortcuts({ allowedProducts, relayMode: true, onOpen: section => open('mcp', section) })
  const { documents, defaultPath } = useReportDocuments(workspacePath || '')
  if (!workspacePath) return null
  return (
    <WorkspaceContextCard product="relays" summary="Run this Flow, inspect results and connect its tools." availableShortcuts={[...workflowContextViews(true, multiUser).map(item => ({ ...item, onClick: () => open(item.id) })), ...connections]}>
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
      {integrationShortcutContent(connections)}
      <div aria-label="Workspace setup" className="border-t border-border pt-3">
        <h3 className="mb-1 text-[11px] font-medium text-muted-foreground">Project setup</h3>
        {WORKSPACE_VIEWS.filter(item => item.id === 'identity' || item.id === 'mcp').map(item => (
          <Shortcut key={item.id} id={item.id} label={item.label} icon={item.icon}><Row icon={item.icon} onClick={() => open(item.id)}>{item.label}</Row></Shortcut>
        ))}
      </div>
    </WorkspaceContextCard>
  )
}
