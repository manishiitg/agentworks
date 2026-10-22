import { panelBackAction, useWorkspacePanelNavigationStore, useWorkspacePanelScope } from '../../stores/useWorkspacePanelNavigationStore'
import { WorkspaceContextButton } from './WorkspaceContextButton'
import { WorkspacePanelHelpButton } from './WorkspacePanelHelpButton'
import type { WorkspacePanelSurface } from '../workflow/workspacePanelGuides'

/** Page navigation stays inside the panel it controls, above its scroll area. */
export function WorkspacePanelHeader({ workspacePath, view, title, onBack, contextAllowed = true, surface = 'agentworks' }: {
  workspacePath?: string | null
  view: string
  title: string
  onBack: () => void
  contextAllowed?: boolean
  surface?: WorkspacePanelSurface
}) {
  const scope = useWorkspacePanelScope(workspacePath, view)
  const guide = useWorkspacePanelNavigationStore(state => panelBackAction(state.guides, scope))
  const pageTitle = guide?.topic ?? title
  return <header data-testid="workspace-panel-header" aria-label="Panel navigation" className="flex min-h-11 shrink-0 items-center justify-between gap-3 border-b border-border bg-background px-3 py-1.5">
    <h2 className="min-w-0 truncate text-sm font-semibold" title={pageTitle}>{pageTitle}</h2>
    <div className="flex shrink-0 items-center gap-1">
      {view !== 'context' && <WorkspacePanelHelpButton workspacePath={workspacePath} view={view} contained fallbackGuide={{ topic: title, surface }} />}
      <WorkspaceContextButton workspacePath={workspacePath} view={view} inContext={view === 'context'} contextAllowed={contextAllowed} onClick={onBack} />
    </div>
  </header>
}
