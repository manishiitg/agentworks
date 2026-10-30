import { WorkspaceContextButton } from './WorkspaceContextButton'
import { useWorkspacePanelHeaderSlots } from './WorkspacePanelHeaderSlots'

/** Page navigation stays inside the panel it controls, above its scroll area. */
export function WorkspacePanelHeader({ workspacePath, view, title, onBack, contextAllowed = true }: {
  workspacePath?: string | null
  view: string
  title: string
  onBack: () => void
  contextAllowed?: boolean
}) {
  const slots = useWorkspacePanelHeaderSlots()
  // The landing menu is already the navigation; it has no page actions or title bar.
  if (view === 'context') return null
  const pageTitle = title
  return <header data-testid="workspace-panel-header" aria-label="Panel navigation" className="flex min-h-12 shrink-0 items-center max-md:gap-2 md:flex-wrap justify-between gap-x-3 gap-y-2 border-b border-border bg-background px-3 py-2">
    <div ref={slots?.setHeading} className="min-w-24 flex-1 [&>:not(:last-child)]:hidden max-md:order-2 max-md:min-w-0">
      <h2 className="truncate text-sm font-semibold" title={pageTitle}>{pageTitle}</h2>
    </div>
    <div className="ml-auto flex max-w-full items-center justify-end gap-1 max-md:contents">
      <div ref={slots?.setActions} data-testid="workspace-panel-actions" className="flex min-w-0 flex-wrap items-center justify-end gap-1.5 empty:hidden max-md:order-3 max-md:flex-nowrap [&>div]:flex-wrap max-md:[&>div]:flex-nowrap" />
      <WorkspaceContextButton workspacePath={workspacePath} view={view} inContext={view === 'context'} contextAllowed={contextAllowed} onClick={onBack} />
    </div>
  </header>
}
