import { ChevronRight, CirclePlay, FileText, LayoutDashboard, type LucideIcon } from 'lucide-react'
import type { ReportDocument } from '../workflow/reportDocuments'
import { reportDocumentLabel } from '../workflow/reportDocumentLabels'
import { DashboardCreateDraftButton, type DashboardChatTarget } from '../workflow/DashboardEmptyState'
import { WorkspaceContextRow } from './WorkspaceContextCard'

/** One dashboard identity and link; the server's saved labels group playback pages and artifacts. */
export function WorkspaceDashboardMenu({ documents, onSelect, createTarget }: {
  documents: readonly ReportDocument[]
  onSelect: (path?: string) => void
  createTarget?: DashboardChatTarget
}) {
  const regular = documents.filter(document => !document.motion && !document.artifact)
  const motion = documents.filter(document => document.motion && !document.artifact)
  // Finished pages made in a chat to share one idea (PLAT-750).
  const artifacts = documents.filter(document => document.artifact)
  const group = (title: string, rows: readonly ReportDocument[], Icon: LucideIcon, help = false) => <details className="group/dashboards">
    <summary className="flex min-h-8 cursor-pointer list-none items-center gap-2.5 rounded-md px-1 py-1.5 text-xs text-foreground/85 hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
      <Icon aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      <span className="flex-1">{title}</span>
      <span className="text-[10px] text-muted-foreground">{rows.length}</span>
      <ChevronRight aria-hidden="true" className="h-3 w-3 text-muted-foreground transition-transform group-open/dashboards:rotate-90" />
    </summary>
    <div role="group" aria-label={title === 'Dashboards' ? 'Project dashboards' : title} className="ml-2.5 mt-1 space-y-0.5 border-l border-border pl-3">
      {rows.map(document => <WorkspaceContextRow key={document.path} icon={Icon} hint={document.title} onClick={() => onSelect(document.path)}>
        {reportDocumentLabel(document, documents)}
      </WorkspaceContextRow>)}
      {help && <div className="space-y-2 px-1 py-2 text-xs leading-5 text-muted-foreground">
        <p>Play and explore changes over time, or follow a visual explanation one step at a time.</p>
        <p>Try sales by month, spending over time, or a process walkthrough. Pause, replay or drag the timeline to explore.</p>
        <p>Need a video? Ask Builder chat to export the dashboard as an MP4.</p>
        {createTarget && <DashboardCreateDraftButton target={createTarget} motion compact />}
      </div>}
    </div>
  </details>
  return <div className="space-y-1">
    {regular.length ? group('Dashboards', regular, LayoutDashboard) : <WorkspaceContextRow icon={LayoutDashboard} onClick={() => onSelect()}>Dashboards</WorkspaceContextRow>}
    {(createTarget || motion.length > 0) && group('Motion dashboards', motion, CirclePlay, true)}
    {artifacts.length > 0 && group('Artifacts', artifacts, FileText)}
  </div>
}
