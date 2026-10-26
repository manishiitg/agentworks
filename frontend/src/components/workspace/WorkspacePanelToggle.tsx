import { createContext, useContext, type ReactNode } from 'react'
import { ChevronsLeft, ChevronsRight } from 'lucide-react'

/** The product owns visibility; shared chrome only calls its existing action. */
export const WorkspacePanelVisibilityContext = createContext<{
  open: boolean
  onToggle: () => void
} | null>(null)

export function WorkspacePanelToggle() {
  const panel = useContext(WorkspacePanelVisibilityContext)
  if (!panel) return null
  const Icon = panel.open ? ChevronsRight : ChevronsLeft
  const label = panel.open ? 'Close workspace panel' : 'Open workspace panel'
  return <button type="button" onClick={panel.onToggle} title={label} aria-label={label} aria-expanded={panel.open}
    className="hidden h-8 w-8 shrink-0 items-center justify-center rounded-lg text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:inline-flex">
    <Icon className="h-4 w-4" aria-hidden="true" />
  </button>
}

/** Keep the control beside the content, outside its heading and scroll area. */
export function WorkspacePanelFrame({ children }: { children: ReactNode }) {
  const panel = useContext(WorkspacePanelVisibilityContext)
  if (!panel) return <>{children}</>
  return <div className="flex h-full min-h-0 min-w-0">
    {panel?.open && <div data-workspace-panel-edge className="hidden shrink-0 pt-3 md:block"><WorkspacePanelToggle /></div>}
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">{children}</div>
  </div>
}

export function WorkspacePanelReopen() {
  const panel = useContext(WorkspacePanelVisibilityContext)
  if (!panel || panel.open) return null
  return <div data-workspace-panel-edge className="z-30 col-start-1 row-start-2 hidden self-start justify-self-end pt-3 md:block"><WorkspacePanelToggle /></div>
}
