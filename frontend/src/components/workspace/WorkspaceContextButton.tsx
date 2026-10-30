import { ArrowLeft } from 'lucide-react'
import { panelBackAction, useWorkspacePanelNavigationStore, useWorkspacePanelScope } from '../../stores/useWorkspacePanelNavigationStore'

/** Expanded panels can return to the always-visible context summary. */
export function WorkspaceContextButton({ inContext, onClick, workspacePath, view, contextAllowed = true }: {
  inContext: boolean
  onClick: () => void
  workspacePath?: string | null
  view?: string
  contextAllowed?: boolean
}) {
  const scope = useWorkspacePanelScope(workspacePath, view)
  const action = useWorkspacePanelNavigationStore(state => panelBackAction(state.actions, scope))
  if (inContext || (!contextAllowed && !action)) return null
  const label = action?.label ?? 'Back to context'
  return (
    <button
      type="button"
      title={label}
      aria-label={label}
      onClick={action?.onBack ?? onClick}
      className="max-md:order-1 inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      <ArrowLeft className="h-4 w-4" />
    </button>
  )
}
