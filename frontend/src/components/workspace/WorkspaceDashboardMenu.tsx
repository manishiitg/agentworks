import { CirclePlay, FileText, LayoutDashboard, type LucideIcon } from 'lucide-react'
import type { ReportDocument } from '../workflow/reportDocuments'
import { reportDocumentLabel } from '../workflow/reportDocumentLabels'
import { DashboardCreateDraftButton, type DashboardChatTarget } from '../workflow/DashboardEmptyState'
import { WorkspaceContextRow } from './WorkspaceContextCard'
import { WorkspaceCompactGroup } from './WorkspaceCompactGroup'

/** Published dashboards, motion dashboards and Artifacts share one compact entry. */
export function WorkspaceDashboardMenu({ documents, onSelect, createTarget }: {
  documents: readonly ReportDocument[]
  onSelect: (path?: string) => void
  createTarget?: DashboardChatTarget
}) {
  const groups: { title: string; label: string; rows: readonly ReportDocument[]; icon: LucideIcon }[] = [
    { title: 'Dashboards', label: 'Project dashboards', rows: documents.filter(document => !document.motion && !document.artifact), icon: LayoutDashboard },
    { title: 'Motion dashboards', label: 'Motion dashboards', rows: documents.filter(document => document.motion && !document.artifact), icon: CirclePlay },
    { title: 'Artifacts', label: 'Artifacts', rows: documents.filter(document => document.artifact), icon: FileText },
  ]
  return <WorkspaceCompactGroup title="Dashboards" icon={LayoutDashboard} detail={documents.length}>
    {groups.filter(group => group.rows.length).map(group => <div key={group.label} role="group" aria-label={group.label} className="space-y-0.5">
      {group.title !== 'Dashboards' && <h3 className="px-1 pb-1 pt-2 text-[10px] font-medium text-muted-foreground">{group.title}</h3>}
      {group.rows.map(document => <WorkspaceContextRow key={document.path} icon={group.icon} hint={document.title} onClick={() => onSelect(document.path)}>{reportDocumentLabel(document, documents)}</WorkspaceContextRow>)}
    </div>)}
    {!documents.length && <WorkspaceContextRow icon={LayoutDashboard} onClick={() => onSelect()}>Open dashboards</WorkspaceContextRow>}
    {createTarget && <div role="group" aria-label="Create dashboard content" className="flex items-center gap-1 px-1 pt-2">
      <DashboardCreateDraftButton target={createTarget} compact/>
      <DashboardCreateDraftButton target={createTarget} kind="motion" compact/>
      <DashboardCreateDraftButton target={createTarget} kind="artifact" compact/>
    </div>}
  </WorkspaceCompactGroup>
}
