import { ChevronRight, LayoutDashboard } from 'lucide-react'
import type { ReportDocument } from '../workflow/reportDocuments'
import { reportDocumentLabel } from '../workflow/reportDocumentLabels'
import { WorkspaceContextRow } from './WorkspaceContextCard'

/** One project destination in Recent/Build, with every named dashboard inside. */
export function WorkspaceDashboardMenu({ documents, onSelect }: {
  documents: readonly ReportDocument[]
  onSelect: (path?: string) => void
}) {
  if (!documents.length) return <WorkspaceContextRow icon={LayoutDashboard} onClick={() => onSelect()}>Dashboards</WorkspaceContextRow>
  return <details className="group/dashboards">
    <summary className="flex min-h-8 cursor-pointer list-none items-center gap-2.5 rounded-md px-1 py-1.5 text-xs text-foreground/85 hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
      <LayoutDashboard aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      <span className="flex-1">Dashboards</span>
      <span className="text-[10px] text-muted-foreground">{documents.length}</span>
      <ChevronRight aria-hidden="true" className="h-3 w-3 text-muted-foreground transition-transform group-open/dashboards:rotate-90" />
    </summary>
    <div role="group" aria-label="Project dashboards" className="ml-2.5 mt-1 space-y-0.5 border-l border-border pl-3">
      {documents.map(document => <WorkspaceContextRow key={document.path} icon={LayoutDashboard} onClick={() => onSelect(document.path)}>
        {reportDocumentLabel(document, documents)}
      </WorkspaceContextRow>)}
    </div>
  </details>
}
