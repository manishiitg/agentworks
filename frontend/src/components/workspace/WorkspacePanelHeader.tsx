import { panelBackAction, useWorkspacePanelNavigationStore, useWorkspacePanelScope } from '../../stores/useWorkspacePanelNavigationStore'
import { WorkspaceContextButton } from './WorkspaceContextButton'
import { WorkspacePanelHelpButton } from './WorkspacePanelHelpButton'
import type { WorkspacePanelSurface } from '../workflow/workspacePanelGuides'
import { useWorkspacePanelHeaderSlots } from './WorkspacePanelHeaderSlots'

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
  const slots = useWorkspacePanelHeaderSlots()
  // The landing menu is already the navigation; it has no page actions or title bar.
  if (view === 'context') return null
  const pageTitle = guide?.topic ?? title
  return <header data-testid="workspace-panel-header" aria-label="Panel navigation" className="flex min-h-12 shrink-0 flex-wrap items-center justify-between gap-x-3 gap-y-2 border-b border-border bg-background px-3 py-2">
    <div ref={slots?.setHeading} className="min-w-24 flex-1 [&>:not(:last-child)]:hidden">
      <h2 className="truncate text-sm font-semibold" title={pageTitle}>{pageTitle}</h2>
    </div>
    <div className="ml-auto flex max-w-full items-center justify-end gap-1">
      <div ref={slots?.setActions} data-testid="workspace-panel-actions" className="flex min-w-0 flex-wrap items-center justify-end gap-1.5 empty:hidden [&>div]:flex-wrap" />
      {view !== 'context' && <WorkspacePanelHelpButton workspacePath={workspacePath} view={view} contained fallbackGuide={{ topic: title, surface }} />}
      <WorkspaceContextButton workspacePath={workspacePath} view={view} inContext={view === 'context'} contextAllowed={contextAllowed} onClick={onBack} />
    </div>
  </header>
}
